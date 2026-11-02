// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"sync"
	"sync/atomic"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// outcomeField is the field of the record of a check of history.Linearizable
// that states how the check ended, as a history.Verdict.
const outcomeField = "outcome"

// LinearizableHistory runs generated sequences of the actions on the subject
// of h, with a concurrent section on the clients 0 to clients that the
// schedule of h runs. Each case keeps every action, so each case can show a
// race between two actions. After the last step it checks the history of c
// once with history.Linearizable and opts, against the spec that
// history.SpecFrom builds over fresh references of h through reference. The
// check splits the history by the keys of its calls. It fails when the calls
// have no order in which the reference returns the same results. A history
// without a call, and a history that the checker cannot decide within its
// budget, do not engage h. A fault of the checker, such as a reference that
// panics, ends the check with that fault.
func LinearizableHistory[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], reference Reference[S], clients int,
	opts ...history.Option,
) {
	h.Helper()
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: actions(c, h.Subject)}, stateful.Clients(clients),
		h.Schedule(c), stateful.Swarm(false))
	sequential := history.SpecFrom(func() history.Subject {
		apply := reference(h.Reference())
		return func(operation string, args []any) any { return apply(h.Context(), operation, args) }
	})
	r := assert.NewRecorder().WithContext(h.Context())
	history.Linearizable(r, c.History(), sequential, h.Statement(), opts...)
	if faults := r.Faults(); len(faults) > 0 {
		assert.Fault(h, faults[0])
		return
	}
	failures := r.Failures()
	if len(c.History().Events()) == 0 || len(failures) > 0 && failures[0].Detail[outcomeField] == history.Undecided {
		return
	}
	h.Engage()
	h.Expect(spec.NotLinearizable)
	for _, f := range failures {
		h.Report(f, true)
	}
}

// ConcurrentReadsAgree runs generated sequences of the actions on the subject
// of h on client 0, so the subject has state, and then a concurrent section of
// the action read alone on the clients 0 to clients, which the schedule of h
// runs. Each case keeps every action. After the section it calls again with
// the arguments of each read that a client other than 0 completed, and fails
// when the output differs. Each other action runs on client 0 alone, because
// Steps leaves an action with Enabled out of a concurrent section. A case
// without a completed read on a client other than 0 does not engage h.
func ConcurrentReadsAgree[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], read string, again Apply, clients int,
) {
	h.Helper()
	steps := actions(c, h.Subject)
	for i := range steps {
		if steps[i].Name != read && steps[i].Enabled == nil {
			steps[i].Enabled = func(struct{}) bool { return true }
		}
	}
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: steps}, stateful.Clients(clients), h.Schedule(c),
		stateful.Swarm(false))
	events := c.History().Events()
	for i := range events {
		e := &events[i]
		if e.Kind != history.OK || e.Client == 0 || events[e.Call].Operation != read {
			continue
		}
		h.Engage()
		h.Expect(spec.InconsistentConcurrentRead)
		got, _ := e.Output.(conform.Output)
		assert.Equal(h, got, again(h.Context(), events[e.Call].Args), h.Statement())
	}
}

// ExclusiveUnderConcurrency runs generated sequences of the call on the
// subject of h, with a concurrent section on the clients 0 to clients that
// the schedule of h runs. The sequences run the call alone, so the lease that
// the first call to succeed takes stays taken, and each other call fails. It
// fails when more than one call succeeds, and when a call fails with an error
// that does not match sentinel. A case without a call that succeeds does not
// engage h.
func ExclusiveUnderConcurrency[S any](h *conform.Handle[S], c *prop.Case, call Act, sentinel error, clients int) {
	h.Helper()
	var mu sync.Mutex
	var errs []error
	acquire := stateful.Action[struct{}]{
		Name: acquireOperation,
		Run: func(c *prop.Case, client int, _ any) {
			invoked := c.History().Invoke(client, acquireOperation, nil)
			err := call(c.Context())
			conform.Complete(c.Context(), invoked, err)
			mu.Lock()
			defer mu.Unlock()
			errs = append(errs, err)
		},
	}
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: []stateful.Action[struct{}]{acquire}},
		stateful.Clients(clients), h.Schedule(c), stateful.Swarm(false))
	granted := 0
	for _, err := range errs {
		if err == nil {
			granted++
		}
	}
	if granted == 0 {
		return
	}
	h.Engage()
	h.Expect(spec.UnguardedWrite)
	assert.Equal(h, granted, 1, h.Statement())
	for _, err := range errs {
		if err != nil {
			assert.ErrorIs(h, err, sentinel, h.Statement())
		}
	}
}

// acquireOperation is the operation of each call of ExclusiveUnderConcurrency,
// as the history records it.
const acquireOperation = "acquire"

// SharesOneComputation holds the dependencies of h, and the clients 0 to
// clients make the call at once, each on a goroutine of its own. Once every
// goroutine of the bubble of the check is blocked, it counts the calls that
// the dependencies recorded since the hold, releases the dependencies and
// waits for each call. It fails when the dependencies recorded more than one
// call, each call one computation. A check whose dependencies do not record a
// call does not engage h. The probe waits through testing/synctest, so its
// check runs inside a bubble that the field Around of the check sets. A call
// that panics fails the check with the value of the first panic once each
// call has returned.
func SharesOneComputation[S, V any](h *conform.Handle[S], call Call[V], clients int) {
	h.Helper()
	injector := h.Injector()
	before := len(injector.Calls())
	injector.Hold()
	var calls sync.WaitGroup
	var panicked atomic.Pointer[any]
	for range clients + 1 {
		calls.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					panicked.CompareAndSwap(nil, &r)
				}
			}()
			_, _ = call(h.Context())
		})
	}
	synctest.Wait()
	computed := len(injector.Calls()) - before
	injector.Release()
	calls.Wait()
	if r := panicked.Load(); r != nil {
		h.Fatalf("%s: a call panics with %v", h.Statement(), *r)
		return
	}
	if computed == 0 {
		return
	}
	h.Engage()
	h.Expect(spec.RepeatedComputation)
	assert.Equal(h, computed, 1, h.Statement())
}
