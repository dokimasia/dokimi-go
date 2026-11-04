// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
)

// broker is the subject of the specs of the probes of deliveries. It delivers
// each message and each event to each subscription on a goroutine, and each
// update to each watch of its key at once. broken names the defect of a
// planted broker, which an operation shows on its fourth call or from its
// fourth call on. It is empty for a correct broker.
type broker struct {
	broken  string
	calls   map[string]int
	subs    []chan int
	closed  []bool
	watches []*watch
}

// brokers returns a factory of brokers with the defect broken.
func brokers(broken string) func(*conform.Handle[*broker]) *broker {
	return func(*conform.Handle[*broker]) *broker { return &broker{broken: broken, calls: map[string]int{}} }
}

// watch is the subscription of a key of a broker. A sticky defect of its broker
// keeps it after a stop.
type watch struct {
	broker  *broker
	key     string
	updates chan int
	stopped bool
}

// Next returns the next update of the watch, and false when none arrives
// within timeout.
func (w *watch) Next(timeout time.Duration) (int, bool) {
	select {
	case v := <-w.updates:
		return v, true
	case <-time.After(timeout):
		return 0, false
	}
}

// Stop ends the watch.
func (w *watch) Stop() { w.stopped = w.broker.broken != "sticky" }

// subscribe returns the channel of a new subscription of the broker. An
// unsubscribable defect fails with errOther.
func (b *broker) subscribe() (<-chan int, error) {
	if b.broken == "unsubscribable" {
		return nil, errOther
	}
	ch := make(chan int, 64)
	b.subs, b.closed = append(b.subs, ch), append(b.closed, false)
	return ch, nil
}

// deliver sends v to each open subscription on a goroutine. A closing defect
// closes each subscription in place of its first delivery.
func (b *broker) deliver(v int) {
	for i, ch := range b.subs {
		switch {
		case b.closed[i]:
		case b.broken == "closing":
			close(ch)
			b.closed[i] = true
		default:
			go func() { ch <- v }()
		}
	}
}

// do calls the operation op of the broker with key and value. A watch of the
// key c fails with errOther, and so does each other operation with the value
// 3.
func (b *broker) do(op, key string, value int) (any, error) {
	b.calls[op]++
	n := b.calls[op]
	if (op == "Watch" && key == "c") || (op != "Watch" && value == 3) {
		return nil, errOther
	}
	switch op {
	case "Append":
		if n != 4 || b.broken != "drop" {
			b.deliver(value)
		}
	case "Publish":
		b.publish(value, n > 3)
	case "Republish":
		if n < 4 || b.broken != "skip" {
			b.deliver(value)
		}
	case "Watch":
		w := &watch{broker: b, key: key, updates: make(chan int, 64)}
		b.watches = append(b.watches, w)
		return w, nil
	default:
		b.trigger(key, value, n)
	}
	return nil, nil
}

// publish delivers the event once. From its fourth call on, a silent defect
// delivers nothing, and a twice defect delivers the event twice.
func (b *broker) publish(v int, late bool) {
	switch {
	case late && b.broken == "silent":
	case late && b.broken == "twice":
		b.deliver(v)
		b.deliver(v)
	default:
		b.deliver(v)
	}
}

// trigger sends the update v to each watch of the key that is not stopped. A
// miss defect sends nothing on the fourth call.
func (b *broker) trigger(key string, v, n int) {
	if n == 4 && b.broken == "miss" {
		return
	}
	for _, w := range b.watches {
		if w.key == key && !w.stopped {
			w.updates <- v
		}
	}
}

// brokerActions returns the actions of the operations ops of a broker. Watch
// takes a key, Trigger a key and a value, and every other operation a value.
// The keys come from the keys a, b and c, and the values from 1 to 3.
func brokerActions(ops ...string) probe.Actions[*broker] {
	return func(c *prop.Case, subject func() *broker) []stateful.Action[struct{}] {
		actions := make([]stateful.Action[struct{}], len(ops))
		for i, op := range ops {
			actions[i] = stateful.Action[struct{}]{
				Name: op,
				Input: func(c *prop.Case, _ struct{}) any {
					key := c.Draw(prop.SampledFrom("a", "b", "c"), op+".key")
					value := c.Draw(prop.Integer(1, 3), op+".value")
					switch op {
					case "Watch":
						return []any{key}
					case "Trigger":
						return []any{key, value}
					default:
						return []any{value}
					}
				},
				Run: func(c *prop.Case, client int, x any) {
					args := x.([]any)
					var keys []any
					key, value := "", 0
					switch op {
					case "Watch":
						key, keys = args[0].(string), args[:1]
					case "Trigger":
						key, value, keys = args[0].(string), args[1].(int), args[:1]
					default:
						value = args[0].(int)
					}
					call := c.History().Invoke(client, op, args, keys...)
					v, err := subject().do(op, key, value)
					if v == nil {
						conform.Complete(c.Context(), call, err)
						return
					}
					conform.Complete(c.Context(), call, err, v)
				},
			}
		}
		return actions
	}
}

// subscribes returns the call of a new subscription of the broker of h.
func subscribes(h *conform.Handle[*broker]) probe.Call[<-chan int] {
	return func(context.Context) (<-chan int, error) { return h.Subject().subscribe() }
}

// retriggers returns the Apply of a trigger of the broker of h.
func retriggers(h *conform.Handle[*broker]) probe.Apply {
	return func(_ context.Context, args []any) conform.Output {
		_, err := h.Subject().do("Trigger", args[0].(string), args[1].(int))
		return conform.OutputOf(err)
	}
}

// firstArg returns the first argument of a call, and secondArg its second.
func firstArg(args []any) int  { return args[0].(int) }
func secondArg(args []any) int { return args[1].(int) }

// keyArg returns the key of a call of Watch or Trigger.
func keyArg(args []any) any { return args[0] }

// watches applies WatchesSeeTheTriggers to the broker of h.
func watches(h *conform.Handle[*broker], c *prop.Case, actions probe.Actions[*broker]) {
	probe.WatchesSeeTheTriggers(h, c, actions, "Watch", "Trigger", (*watch).Next, (*watch).Stop, retriggers(h),
		keyArg, keyArg, secondArg)
}

// delivering is the property of a check over brokers.
type delivering = func(h *conform.Handle[*broker], c *prop.Case)

// silent applies a property check of the probe p inside a bubble to a broker
// with the defect broken, and fails t unless the check fails once for the
// vacuous condition of p.
func silent(t *testing.T, p spec.ProbeID, broken string, check delivering) {
	t.Helper()
	r := &onTest{Recorder: assert.NewRecorder(), t: t}
	conform.Check[*broker]{
		ID: "Broker.Call/claim", Statement: "the probe holds", Probe: p, Property: check, Around: synctest.Test,
	}.Run(r, brokers(broken), conform.Cases[*broker](10))
	entry, _ := spec.ProbeOf(p)
	condition := strings.ToLower(entry.Vacuous[:1]) + strings.TrimSuffix(entry.Vacuous[1:], ".")
	assert.Equal(t, r.Messages(), []string{"Broker.Call/claim never engaged: " + condition + ". Pass a subject " +
		"that engages the check, or excuse the check with the directive excuse or conform.Excuse and a reason."},
		"the check fails for the vacuous condition of its probe")
}

func TestDelivery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		giveProbe    spec.ProbeID
		giveBreak    string
		giveProperty delivering
		want         spec.ReasonID
	}{
		{
			name: "AppendsArrive", giveProbe: spec.AppendsArrive, giveBreak: "drop", want: spec.LostMessage,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.AppendsArrive(h, c, brokerActions("Append"), "Append", subscribes(h), firstArg)
			},
		},
		{
			name: "AppendsArrive fails a subscription that closes", giveProbe: spec.AppendsArrive,
			giveBreak: "closing", want: spec.LostMessage,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.AppendsArrive(h, c, brokerActions("Append", "Watch"), "Append", subscribes(h), firstArg)
			},
		},
		{
			name: "DeliveriesMatchTheMode under at-least-once", giveProbe: spec.DeliveriesMatchTheMode,
			giveBreak: "silent", want: spec.WrongDeliveryCount,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.DeliveriesMatchTheMode(h, c, brokerActions("Publish"), "Publish", subscribes(h), firstArg,
					"at-least-once")
			},
		},
		{
			name: "DeliveriesMatchTheMode under at-most-once", giveProbe: spec.DeliveriesMatchTheMode,
			giveBreak: "twice", want: spec.WrongDeliveryCount,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.DeliveriesMatchTheMode(h, c, brokerActions("Publish"), "Publish", subscribes(h), firstArg,
					"at-most-once")
			},
		},
		{
			name: "DeliveriesMatchTheMode under exactly-once", giveProbe: spec.DeliveriesMatchTheMode,
			giveBreak: "silent", want: spec.WrongDeliveryCount,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.DeliveriesMatchTheMode(h, c, brokerActions("Publish"), "Publish", subscribes(h), firstArg,
					"exactly-once")
			},
		},
		{
			name:      "DeliveriesMatchTheMode fails a double delivery under exactly-once",
			giveProbe: spec.DeliveriesMatchTheMode, giveBreak: "twice", want: spec.WrongDeliveryCount,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.DeliveriesMatchTheMode(h, c, brokerActions("Publish"), "Publish", subscribes(h), firstArg,
					"exactly-once")
			},
		},
		{
			name: "RedeliveriesArrive", giveProbe: spec.RedeliveriesArrive, giveBreak: "skip",
			want: spec.NotRedelivered,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.RedeliveriesArrive(h, c, brokerActions("Publish", "Republish"), "Publish", "Republish",
					subscribes(h), firstArg, firstArg)
			},
		},
		{
			name: "WatchesSeeTheTriggers", giveProbe: spec.WatchesSeeTheTriggers, giveBreak: "miss",
			want: spec.MissedUpdate,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				watches(h, c, brokerActions("Watch", "Trigger"))
			},
		},
		{
			name: "WatchesSeeTheTriggers fails an update after a stop", giveProbe: spec.WatchesSeeTheTriggers,
			giveBreak: "sticky", want: spec.MissedUpdate,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				watches(h, c, brokerActions("Watch", "Trigger", "Append"))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conform.Check[*broker]{
				ID: "Broker.Call/claim", Statement: "the probe holds", Claim: "claim", Probe: tt.giveProbe,
				Reason: tt.want, Property: tt.giveProperty, Defect: brokers(tt.giveBreak), Around: synctest.Test,
			}.Run(t, brokers(""))
		})
	}

	allowed := []struct {
		name      string
		giveBreak string
		giveMode  string
	}{
		{
			name:      "DeliveriesMatchTheMode passes an event that arrives twice under at-least-once",
			giveBreak: "twice", giveMode: "at-least-once",
		},
		{
			name:      "DeliveriesMatchTheMode passes an event that never arrives under at-most-once",
			giveBreak: "silent", giveMode: "at-most-once",
		},
	}
	for _, tt := range allowed {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conform.Check[*broker]{
				ID: "Broker.Call/claim", Statement: "the probe holds", Probe: spec.DeliveriesMatchTheMode,
				Property: func(h *conform.Handle[*broker], c *prop.Case) {
					probe.DeliveriesMatchTheMode(h, c, brokerActions("Publish"), "Publish", subscribes(h), firstArg,
						tt.giveMode)
				},
				Around: synctest.Test,
			}.Run(t, brokers(tt.giveBreak))
		})
	}

	t.Run("DeliveriesMatchTheMode fails a mode that it does not know", func(t *testing.T) {
		t.Parallel()
		r := &onTest{Recorder: assert.NewRecorder(), t: t}

		conform.Check[*broker]{
			ID: "Broker.Call/claim", Statement: "the probe holds", Probe: spec.DeliveriesMatchTheMode,
			Property: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.DeliveriesMatchTheMode(h, c, brokerActions("Publish"), "Publish", subscribes(h), firstArg,
					"twice-a-day")
			},
			Around: synctest.Test,
		}.Run(r, brokers(""), conform.Cases[*broker](1))

		assert.Contains(t, r.Message(), `the probe holds: the delivery mode "twice-a-day" is none of `+
			"at-least-once, at-most-once and exactly-once", "the check names the mode")
	})

	ends := []struct {
		name         string
		giveProbe    spec.ProbeID
		giveBreak    string
		giveProperty delivering
	}{
		{
			name: "AppendsArrive is vacuous on sequences without an append", giveProbe: spec.AppendsArrive,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.AppendsArrive(h, c, brokerActions("Publish"), "Append", subscribes(h), firstArg)
			},
		},
		{
			name: "AppendsArrive is vacuous on a subscription that fails", giveProbe: spec.AppendsArrive,
			giveBreak: "unsubscribable",
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.AppendsArrive(h, c, brokerActions("Append"), "Append", subscribes(h), firstArg)
			},
		},
		{
			name:      "DeliveriesMatchTheMode is vacuous on sequences without a publish",
			giveProbe: spec.DeliveriesMatchTheMode,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.DeliveriesMatchTheMode(h, c, brokerActions("Append"), "Publish", subscribes(h), firstArg,
					"exactly-once")
			},
		},
		{
			name:      "RedeliveriesArrive is vacuous on sequences without a redelivery",
			giveProbe: spec.RedeliveriesArrive,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				probe.RedeliveriesArrive(h, c, brokerActions("Publish"), "Publish", "Republish", subscribes(h),
					firstArg, firstArg)
			},
		},
		{
			name:      "WatchesSeeTheTriggers is vacuous on sequences without a trigger",
			giveProbe: spec.WatchesSeeTheTriggers,
			giveProperty: func(h *conform.Handle[*broker], c *prop.Case) {
				watches(h, c, brokerActions("Watch"))
			},
		},
	}
	for _, tt := range ends {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			silent(t, tt.giveProbe, tt.giveBreak, tt.giveProperty)
		})
	}
}
