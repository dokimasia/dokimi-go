// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"reflect"
	"slices"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// The probes of deliveries run inside a testing/synctest bubble, which the
// check sets through its function Around. A subject delivers a message on a
// goroutine of the bubble, and synctest.Wait returns once each goroutine of
// the bubble is blocked, so a probe waits exactly until the subject has
// delivered what it delivers. The wait of a read of an update elapses on the
// clock of the bubble.

// updateWait is the time that a probe of watches waits for the next update of
// a subscription.
const updateWait = time.Second

// AppendsArrive subscribes through subscribe, runs generated sequences of the
// actions, and takes each message that the channel of the subscription holds
// once the subject waits. It fails when a message that a call of appends that
// succeeded wrote did not arrive. written returns the message that a call of
// appends writes. A subscription that fails ends the probe.
func AppendsArrive[S, V any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], appends string, subscribe Call[<-chan V],
	written func(args []any) V,
) {
	h.Helper()
	calls, arrived := deliveries(h, c, actions, subscribe)
	sent := valuesOf(calls, appends, written)
	if len(sent) == 0 {
		return
	}
	h.Engage()
	h.Expect(spec.LostMessage)
	assert.Empty(h, without(sent, arrived), h.Statement())
}

// The delivery modes of a publisher.
const (
	atLeastOnce = "at-least-once"
	atMostOnce  = "at-most-once"
	exactlyOnce = "exactly-once"
)

// DeliveriesMatchTheMode subscribes through subscribe, runs generated
// sequences of the actions, and takes each event that the channel of the
// subscription holds once the subject waits. It fails when an event that the
// calls of publish that succeeded published arrived another number of times
// than mode allows: as often as it was published or more under at-least-once,
// as often or less under at-most-once, and as often under exactly-once.
// written returns the event that a call of publish publishes. A subscription
// that fails ends the probe, and the probe fails for any other mode.
func DeliveriesMatchTheMode[S, V any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], publish string, subscribe Call[<-chan V],
	written func(args []any) V, mode string,
) {
	h.Helper()
	if !slices.Contains([]string{atLeastOnce, atMostOnce, exactlyOnce}, mode) {
		h.Fatalf("%s: the delivery mode %q is none of %s, %s and %s", h.Statement(), mode, atLeastOnce, atMostOnce,
			exactlyOnce)
		return
	}
	calls, arrived := deliveries(h, c, actions, subscribe)
	sent := valuesOf(calls, publish, written)
	if len(sent) == 0 {
		return
	}
	h.Engage()
	h.Expect(spec.WrongDeliveryCount)
	if mode != atMostOnce {
		assert.Empty(h, without(sent, arrived), h.Statement())
	}
	if mode != atLeastOnce {
		assert.Empty(h, without(arrived, sent), h.Statement())
	}
}

// RedeliveriesArrive subscribes through subscribe, runs generated sequences of
// the actions, and takes each event that the channel of the subscription
// holds once the subject waits. It fails when an event arrived fewer times
// than the calls of publish and redeliver that succeeded passed it together.
// written and redelivered return the event that a call of publish and of
// redeliver passes. A subscription that fails ends the probe.
func RedeliveriesArrive[S, V any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], publish, redeliver string, subscribe Call[<-chan V],
	written, redelivered func(args []any) V,
) {
	h.Helper()
	calls, arrived := deliveries(h, c, actions, subscribe)
	again := valuesOf(calls, redeliver, redelivered)
	if len(again) == 0 {
		return
	}
	h.Engage()
	h.Expect(spec.NotRedelivered)
	assert.Empty(h, without(append(valuesOf(calls, publish, written), again...), arrived), h.Statement())
}

// WatchesSeeTheTriggers runs generated sequences of the actions, and takes the
// subscription of each call of watch that succeeded from its output. It reads
// next on each subscription until next reports no update, and fails when the
// updates differ from the updates of the calls of trigger of the key of the
// watch that succeeded after it, in their order. It then stops each
// subscription, applies retrigger to the arguments of the last of those calls
// of trigger, and fails when next returns an update. watchKey and triggerKey
// return the key of a call of watch and of trigger, and triggered the update
// that a call of trigger causes.
func WatchesSeeTheTriggers[S, H, V any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], watch, trigger string,
	next func(sub H, timeout time.Duration) (V, bool), stop func(sub H), retrigger Apply,
	watchKey, triggerKey func(args []any) any, triggered func(args []any) V,
) {
	h.Helper()
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: actions(c, h.Subject)})
	calls := completions(c.History().Events(), 0)
	var watched []subscription[H, V]
	for i, w := range calls {
		if w.operation != watch || w.out.Failed {
			continue
		}
		s := subscription[H, V]{sub: value[H](w.out)}
		for _, t := range calls[i+1:] {
			if t.operation == trigger && !t.out.Failed && reflect.DeepEqual(triggerKey(t.args), watchKey(w.args)) {
				s.updates, s.last = append(s.updates, triggered(t.args)), t.args
			}
		}
		watched = append(watched, s)
	}
	for _, s := range watched {
		if len(s.updates) == 0 {
			continue
		}
		got := make([]V, 0, len(s.updates))
		for range len(s.updates) + 1 {
			v, more := next(s.sub, updateWait)
			if !more {
				break
			}
			got = append(got, v)
		}
		h.Engage()
		h.Expect(spec.MissedUpdate)
		assert.Equal(h, got, s.updates, h.Statement())
	}
	for _, s := range watched {
		stop(s.sub)
	}
	for _, s := range watched {
		if len(s.updates) > 0 {
			retrigger(h.Context(), s.last)
		}
	}
	for _, s := range watched {
		_, late := next(s.sub, updateWait)
		assert.False(h, late, h.Statement())
	}
}

// subscription is a subscription of a call of watch, with the updates of the
// calls of trigger of its key after it and the arguments of the last of
// those calls.
type subscription[H, V any] struct {
	sub     H
	updates []V
	last    []any
}

// deliveries subscribes through subscribe, runs generated sequences of the
// actions, and returns the calls of the sequences and the values that arrived
// on the channel of the subscription once the subject waits. After a
// subscription that fails, it returns no call.
func deliveries[S, V any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], subscribe Call[<-chan V],
) ([]completed, []V) {
	ch, err := subscribe(h.Context())
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: actions(c, h.Subject)})
	if err != nil {
		return nil, nil
	}
	calls := completions(c.History().Events(), 0)
	var arrived []V
	for {
		synctest.Wait()
		select {
		case v, open := <-ch:
			if !open {
				return calls, arrived
			}
			arrived = append(arrived, v)
		default:
			return calls, arrived
		}
	}
}

// valuesOf returns the value that value returns for the arguments of each call
// of operation in calls that succeeded, in the order of the calls.
func valuesOf[V any](calls []completed, operation string, value func(args []any) V) []V {
	var values []V
	for _, call := range calls {
		if call.operation == operation && !call.out.Failed {
			values = append(values, value(call.args))
		}
	}
	return values
}
