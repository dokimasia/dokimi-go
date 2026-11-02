// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/spec"
)

// guarded is a store of one integer under each key that a mutex guards, the
// subject of the concurrent specs. A torn store writes -1 under the key of a
// put, yields, and then writes the value, so a get between the two steps
// returns a value that no put wrote. A racy store keeps the result of a get in
// its fields last and lastErr, yields, and returns the fields, so a get that
// overlaps another returns the result of the other.
type guarded struct {
	lastErr error
	values  map[string]int
	yield   func()
	last    int
	mu      sync.Mutex
	torn    bool
	racy    bool
}

// newGuarded returns an empty guarded store.
func newGuarded(*conform.Handle[*guarded]) *guarded { return &guarded{values: map[string]int{}} }

// tornGuarded returns an empty torn store that yields to the scheduler of h.
func tornGuarded(h *conform.Handle[*guarded]) *guarded {
	return &guarded{values: map[string]int{}, yield: h.Yield, torn: true}
}

// racyGuarded returns an empty racy store that yields to the scheduler of h.
func racyGuarded(h *conform.Handle[*guarded]) *guarded {
	return &guarded{values: map[string]int{}, yield: h.Yield, racy: true}
}

// guardedGet returns the apply that calls Get on the subject of h with the
// arguments of a call.
func guardedGet(h *conform.Handle[*guarded]) probe.Apply {
	return func(_ context.Context, args []any) conform.Output {
		v, err := h.Subject().call("Get", args)
		return conform.OutputOf(err, v)
	}
}

// putsOnly are the actions of guardedActions without Get.
func putsOnly(c *prop.Case, subject func() *guarded) []stateful.Action[struct{}] {
	return guardedActions(c, subject)[:1]
}

// withNever are the actions of guardedActions and the action Never, which is
// never enabled and fails the case when it runs.
func withNever(c *prop.Case, subject func() *guarded) []stateful.Action[struct{}] {
	return append(guardedActions(c, subject), stateful.Action[struct{}]{
		Name:    "Never",
		Enabled: func(struct{}) bool { return false },
		Run:     func(c *prop.Case, _ int, _ any) { c.Fatalf("the probe ran an action that is never enabled") },
	})
}

// flight shares one computation of each key among the calls that run at once,
// as golang.org/x/sync/singleflight does. A call after the computation ended
// computes again. A fresh flight computes on a flight of its own for each
// call, the planted defect of the specs of one computation.
type flight struct {
	calls map[string]*flightCall
	mu    sync.Mutex
	fresh bool
}

// flightCall is one computation in flight, which the calls of its key wait
// for until done closes.
type flightCall struct {
	done  chan struct{}
	value int
}

// newFlight returns a flight without a computation in flight.
func newFlight(*conform.Handle[*flight]) *flight { return &flight{calls: map[string]*flightCall{}} }

// freshFlight returns a flight that computes for each call.
func freshFlight(*conform.Handle[*flight]) *flight {
	return &flight{calls: map[string]*flightCall{}, fresh: true}
}

// run returns the value that compute returns for key. The calls of key that
// run at once share one call of compute.
func (f *flight) run(key string, compute func() int) int {
	if f.fresh {
		return newFlight(nil).run(key, compute)
	}
	f.mu.Lock()
	if c, found := f.calls[key]; found {
		f.mu.Unlock()
		<-c.done
		return c.value
	}
	c := &flightCall{done: make(chan struct{})}
	f.calls[key] = c
	f.mu.Unlock()
	c.value = compute()
	f.mu.Lock()
	delete(f.calls, key)
	f.mu.Unlock()
	close(c.done)
	return c.value
}

// computing returns the call that runs the key k on the subject of h with a
// computation that calls a dependency of the injector of h.
func computing(h *conform.Handle[*flight]) probe.Call[int] {
	return func(context.Context) (int, error) {
		return h.Subject().run("k", func() int {
			_ = h.Injector().Dependency().Call("run")
			return 1
		}), nil
	}
}

// errHeld is the error of an acquire of a lease that another call holds.
var errHeld = errors.New("probe_test: the lease is held")

// lessor grants the lease of a key to the first acquire of the key, and fails
// each later acquire with refusal. A racy lessor marks each acquire, yields,
// and grants an acquire that finds another acquire marked without a look at
// the leases, the planted defect of the specs of one holder.
type lessor struct {
	refusal error
	held    map[string]bool
	yield   func()
	marked  atomic.Int32
	mu      sync.Mutex
	racy    bool
}

// newLessor returns a lessor without a lease that refuses with errHeld.
func newLessor(*conform.Handle[*lessor]) *lessor {
	return &lessor{refusal: errHeld, held: map[string]bool{}}
}

// racyLessor returns a racy lessor that yields to the scheduler of h.
func racyLessor(h *conform.Handle[*lessor]) *lessor {
	return &lessor{refusal: errHeld, held: map[string]bool{}, yield: h.Yield, racy: true}
}

// acquire takes the lease of key, and fails with the refusal of l where a
// call holds the lease.
func (l *lessor) acquire(key string) error {
	if l.racy {
		l.marked.Add(1)
		defer l.marked.Add(-1)
		l.yield()
		if l.marked.Load() > 1 {
			return nil
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] {
		return l.refusal
	}
	l.held[key] = true
	return nil
}

// acquiring returns the call that acquires the key k on the subject of h.
func acquiring(h *conform.Handle[*lessor]) probe.Act {
	return func(context.Context) error { return h.Subject().acquire("k") }
}

// peeks are the actions of guardedActions whose action Get records each call
// in the history as a call of Peek.
func peeks(c *prop.Case, subject func() *guarded) []stateful.Action[struct{}] {
	actions := guardedActions(c, subject)
	actions[1].Run = func(c *prop.Case, client int, input any) {
		args, _ := input.([]any)
		call := c.History().Invoke(client, "Peek", args, args[0])
		v, err := subject().call("Get", args)
		conform.Complete(c.Context(), call, err, v)
	}
	return actions
}

// set writes the value under the key.
func (g *guarded) set(key string, value int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.values[key] = value
}

// call applies the operation Put or Get to g with args. Put returns no value,
// and Get fails with errMiss for a key without a value.
func (g *guarded) call(op string, args []any) (any, error) {
	key := args[0].(string)
	if op == "Put" {
		if g.torn {
			g.set(key, -1)
			g.yield()
		}
		g.set(key, args[1].(int))
		return nil, nil
	}
	g.mu.Lock()
	v, found := g.values[key]
	g.mu.Unlock()
	var err error
	if !found {
		v, err = 0, errMiss
	}
	if g.racy {
		g.last, g.lastErr = v, err
		g.yield()
		return g.last, g.lastErr
	}
	return v, err
}

// guardedActions are the actions Put and Get of a guarded store. Each action
// takes its key from a pool of one or two keys of the case.
func guardedActions(c *prop.Case, subject func() *guarded) []stateful.Action[struct{}] {
	pool := c.Draw(prop.List(prop.SampledFrom("a", "b"), prop.Unique(), prop.MinSize(1), prop.MaxSize(2)), "keys")
	action := func(op string) stateful.Action[struct{}] {
		return stateful.Action[struct{}]{
			Name: op,
			Input: func(c *prop.Case, _ struct{}) any {
				args := []any{c.Draw(prop.SampledFrom(pool...), op+".key")}
				if op == "Put" {
					args = append(args, c.Draw(prop.Integer(0, 9), op+".value"))
				}
				return args
			},
			Run: func(c *prop.Case, client int, input any) {
				args, _ := input.([]any)
				call := c.History().Invoke(client, op, args, args[0])
				v, err := subject().call(op, args)
				if v == nil {
					conform.Complete(c.Context(), call, err)
					return
				}
				conform.Complete(c.Context(), call, err, v)
			},
		}
	}
	return []stateful.Action[struct{}]{action("Put"), action("Get")}
}

// doubledActions are the actions of guardedActions, each of which makes its
// call twice, so each step adds two calls to the history.
func doubledActions(c *prop.Case, subject func() *guarded) []stateful.Action[struct{}] {
	actions := guardedActions(c, subject)
	for i := range actions {
		run := actions[i].Run
		actions[i].Run = func(c *prop.Case, client int, input any) {
			run(c, client, input)
			run(c, client, input)
		}
	}
	return actions
}

// guardedReference applies an operation to the guarded store r by its name.
func guardedReference(r *guarded) func(ctx context.Context, op string, args []any) conform.Output {
	return func(_ context.Context, op string, args []any) conform.Output {
		v, err := r.call(op, args)
		if v == nil {
			return conform.OutputOf(err)
		}
		return conform.OutputOf(err, v)
	}
}

func TestConcurrent(t *testing.T) {
	t.Parallel()

	t.Run("LinearizableHistory", func(t *testing.T) {
		t.Parallel()

		t.Run("proves the check on a store that a mutex guards against a torn store", func(t *testing.T) {
			t.Parallel()

			conform.Check[*guarded]{
				ID: "Guarded/linearizable", Statement: "the probe holds", Claim: "claim",
				Probe: spec.LinearizableHistory, Reason: spec.NotLinearizable,
				Property: func(h *conform.Handle[*guarded], c *prop.Case) {
					probe.LinearizableHistory(h, c, guardedActions, guardedReference, 2)
				},
				Defect: tornGuarded,
			}.Run(t, newGuarded, conform.Reference(newGuarded))
		})

		t.Run("engages no case whose history the checker cannot decide within its budget", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			conform.Check[*guarded]{
				ID: "Guarded/linearizable", Statement: "the probe holds", Probe: spec.LinearizableHistory,
				Property: func(h *conform.Handle[*guarded], c *prop.Case) {
					probe.LinearizableHistory(h, c, doubledActions, guardedReference, 2, history.Budget(1),
						history.Whole())
				},
			}.Run(r, newGuarded, conform.Cases[*guarded](10), conform.Reference(newGuarded))
			entry, _ := spec.ProbeOf(spec.LinearizableHistory)
			condition := strings.ToLower(entry.Vacuous[:1]) + strings.TrimSuffix(entry.Vacuous[1:], ".")
			assert.Equal(t, r.Messages(), []string{"Guarded/linearizable never engaged: " + condition + ". Pass a " +
				"subject that engages the check, or excuse the check with the directive excuse or conform.Excuse " +
				"and a reason."}, "the check fails for the vacuous condition of its probe")
		})

		t.Run("ends the check with the fault of a reference that panics", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			conform.Check[*guarded]{
				ID: "Guarded/linearizable", Statement: "the probe holds", Probe: spec.LinearizableHistory,
				Property: func(h *conform.Handle[*guarded], c *prop.Case) {
					probe.LinearizableHistory(h, c, guardedActions,
						func(*guarded) func(context.Context, string, []any) conform.Output {
							var gone *guarded
							return guardedReference(gone)
						}, 2)
				},
			}.Run(r, newGuarded, conform.Reference(newGuarded))
			faults := r.Faults()
			assert.NotEmpty(t, faults, "the check ends with a fault")
			expect.ErrorIs(t, faults[0], history.ErrSpec, "the fault is the fault of the spec of the reference")
		})
	})

	t.Run("ConcurrentReadsAgree", func(t *testing.T) {
		t.Parallel()

		t.Run("proves the check on a store that a mutex guards against a racy store", func(t *testing.T) {
			t.Parallel()

			conform.Check[*guarded]{
				ID: "Guarded.Get/reads-agree-under-concurrency", Statement: "the probe holds", Claim: "claim",
				Probe: spec.ConcurrentReadsAgree, Reason: spec.InconsistentConcurrentRead,
				Property: func(h *conform.Handle[*guarded], c *prop.Case) {
					probe.ConcurrentReadsAgree(h, c, guardedActions, "Get", guardedGet(h), 2)
				},
				Defect: racyGuarded,
			}.Run(t, newGuarded)
		})

		t.Run("engages no case without a read beside another read", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			conform.Check[*guarded]{
				ID: "Guarded.Get/reads-agree-under-concurrency", Statement: "the probe holds",
				Probe: spec.ConcurrentReadsAgree,
				Property: func(h *conform.Handle[*guarded], c *prop.Case) {
					probe.ConcurrentReadsAgree(h, c, putsOnly, "Get", guardedGet(h), 2)
				},
			}.Run(r, newGuarded, conform.Cases[*guarded](10))
			entry, _ := spec.ProbeOf(spec.ConcurrentReadsAgree)
			condition := strings.ToLower(entry.Vacuous[:1]) + strings.TrimSuffix(entry.Vacuous[1:], ".")
			assert.Equal(t, r.Messages(), []string{"Guarded.Get/reads-agree-under-concurrency never engaged: " +
				condition + ". Pass a subject that engages the check, or excuse the check with the directive " +
				"excuse or conform.Excuse and a reason."}, "the check fails for the vacuous condition of its probe")
		})

		t.Run("compares only the calls that the history records as calls of the read", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			conform.Check[*guarded]{
				ID: "Guarded.Get/reads-agree-under-concurrency", Statement: "the probe holds",
				Probe: spec.ConcurrentReadsAgree,
				Property: func(h *conform.Handle[*guarded], c *prop.Case) {
					probe.ConcurrentReadsAgree(h, c, peeks, "Get", guardedGet(h), 2)
				},
			}.Run(r, newGuarded, conform.Cases[*guarded](10))
			assert.Length(t, r.Messages(), 1, "a history without a call of Get leaves the check vacuous")
			expect.Contains(t, r.Messages()[0], "never engaged", "the check never compares a call of Peek")
		})

		t.Run("keeps an action that is never enabled out of the steps", func(t *testing.T) {
			t.Parallel()

			conform.Check[*guarded]{
				ID: "Guarded.Get/reads-agree-under-concurrency", Statement: "the probe holds",
				Probe: spec.ConcurrentReadsAgree,
				Property: func(h *conform.Handle[*guarded], c *prop.Case) {
					probe.ConcurrentReadsAgree(h, c, withNever, "Get", guardedGet(h), 2)
				},
			}.Run(t, newGuarded)
		})
	})

	t.Run("SharesOneComputation", func(t *testing.T) {
		t.Parallel()

		t.Run("proves the check on a single flight against a flight for each call", func(t *testing.T) {
			t.Parallel()

			conform.Check[*flight]{
				ID: "Flight.Run/computes-once", Statement: "the probe holds", Claim: "claim",
				Probe: spec.SharesOneComputation, Reason: spec.RepeatedComputation,
				Apply:  func(h *conform.Handle[*flight]) { probe.SharesOneComputation(h, computing(h), 2) },
				Defect: freshFlight, Around: synctest.Test,
			}.Run(t, newFlight)
		})

		t.Run("counts the computations of its own calls alone", func(t *testing.T) {
			t.Parallel()

			conform.Check[*flight]{
				ID: "Flight.Run/computes-once", Statement: "the probe holds", Probe: spec.SharesOneComputation,
				Apply: func(h *conform.Handle[*flight]) {
					_ = h.Injector().Dependency().Call("warm")
					probe.SharesOneComputation(h, computing(h), 2)
				},
				Around: synctest.Test,
			}.Run(t, newFlight)
		})

		t.Run("returns once each call has returned", func(t *testing.T) {
			t.Parallel()

			conform.Check[*flight]{
				ID: "Flight.Run/computes-once", Statement: "the probe holds", Probe: spec.SharesOneComputation,
				Apply: func(h *conform.Handle[*flight]) {
					var returned atomic.Int32
					call := computing(h)
					probe.SharesOneComputation(h, func(ctx context.Context) (int, error) {
						v, err := call(ctx)
						time.Sleep(time.Second)
						returned.Add(1)
						return v, err
					}, 2)
					expect.Equal(h, returned.Load(), int32(3), "the probe returns once the three calls have returned")
				},
				Around: synctest.Test,
			}.Run(t, newFlight)
		})

		t.Run("fails the check with the value of a call that panics", func(t *testing.T) {
			t.Parallel()

			r := &onTest{Recorder: assert.NewRecorder(), t: t}
			var calls, returned atomic.Int32
			conform.Check[*flight]{
				ID: "Flight.Run/computes-once", Statement: "the probe holds", Claim: "claim",
				Probe: spec.SharesOneComputation, Reason: spec.RepeatedComputation,
				Apply: func(h *conform.Handle[*flight]) {
					probe.SharesOneComputation(h, func(context.Context) (int, error) {
						none := make([]int, 0, 1)
						if n := calls.Add(1); n == 1 {
							return none[n-1], nil
						}
						time.Sleep(time.Second)
						returned.Add(1)
						return 0, nil
					}, 2)
				},
				Defect: newFlight, Around: synctest.Test,
			}.Run(r, newFlight)
			assert.Contains(t, r.Message(), "the probe holds: a call panics with runtime error: index out of range "+
				"[0] with length 0", "the check fails with the value of the panic")
			expect.Equal(t, returned.Load(), int32(2), "the other two calls return before the check fails")
		})

		t.Run("engages no check whose dependencies record no computation", func(t *testing.T) {
			t.Parallel()

			r := &onTest{Recorder: assert.NewRecorder(), t: t}
			conform.Check[*flight]{
				ID: "Flight.Run/computes-once", Statement: "the probe holds", Probe: spec.SharesOneComputation,
				Apply: func(h *conform.Handle[*flight]) {
					probe.SharesOneComputation(h, func(context.Context) (int, error) {
						return h.Subject().run("k", func() int { return 1 }), nil
					}, 2)
				},
				Around: synctest.Test,
			}.Run(r, newFlight)
			entry, _ := spec.ProbeOf(spec.SharesOneComputation)
			condition := strings.ToLower(entry.Vacuous[:1]) + strings.TrimSuffix(entry.Vacuous[1:], ".")
			assert.Equal(t, r.Messages(), []string{"Flight.Run/computes-once never engaged: " + condition + ". Pass " +
				"a subject that engages the check, or excuse the check with the directive excuse or " +
				"conform.Excuse and a reason."}, "the check fails for the vacuous condition of its probe")
		})
	})

	t.Run("ExclusiveUnderConcurrency", func(t *testing.T) {
		t.Parallel()

		t.Run("proves the check on a lessor against a racy lessor", func(t *testing.T) {
			t.Parallel()

			conform.Check[*lessor]{
				ID: "Lessor.Acquire/one-holder-at-a-time", Statement: "the probe holds", Claim: "claim",
				Probe: spec.ExclusiveUnderConcurrency, Reason: spec.UnguardedWrite,
				Property: func(h *conform.Handle[*lessor], c *prop.Case) {
					probe.ExclusiveUnderConcurrency(h, c, acquiring(h), errHeld, 2)
				},
				Defect: racyLessor,
			}.Run(t, newLessor)
		})

		t.Run("fails a call that fails with an error other than the sentinel", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			conform.Check[*lessor]{
				ID: "Lessor.Acquire/one-holder-at-a-time", Statement: "the probe holds",
				Probe: spec.ExclusiveUnderConcurrency,
				Property: func(h *conform.Handle[*lessor], c *prop.Case) {
					probe.ExclusiveUnderConcurrency(h, c, acquiring(h), errHeld, 2)
				},
			}.Run(r, func(*conform.Handle[*lessor]) *lessor {
				return &lessor{refusal: errOther, held: map[string]bool{}}
			}, conform.Cases[*lessor](10))
			failures := r.Failures()
			assert.NotEmpty(t, failures, "the check fails")
			failure, _ := failures[0].CaseFailure()
			expect.Equal(t, []any{failure.Assertion, failure.Contract}, []any{"err-is", "the probe holds"},
				"the check fails at the match of the sentinel")
		})

		t.Run("engages no case without a call that takes the lease", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			conform.Check[*lessor]{
				ID: "Lessor.Acquire/one-holder-at-a-time", Statement: "the probe holds",
				Probe: spec.ExclusiveUnderConcurrency,
				Property: func(h *conform.Handle[*lessor], c *prop.Case) {
					probe.ExclusiveUnderConcurrency(h, c, func(context.Context) error { return errHeld }, errHeld, 2)
				},
			}.Run(r, newLessor, conform.Cases[*lessor](10))
			entry, _ := spec.ProbeOf(spec.ExclusiveUnderConcurrency)
			condition := strings.ToLower(entry.Vacuous[:1]) + strings.TrimSuffix(entry.Vacuous[1:], ".")
			assert.Equal(t, r.Messages(), []string{"Lessor.Acquire/one-holder-at-a-time never engaged: " + condition +
				". Pass a subject that engages the check, or excuse the check with the directive excuse or " +
				"conform.Excuse and a reason."}, "the check fails for the vacuous condition of its probe")
		})
	})
}
