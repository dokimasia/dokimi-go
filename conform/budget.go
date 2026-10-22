// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
)

// The segments of the checks of the ceilings of a budget.
const (
	allocationSegment  = "allocation-budget"
	byteSegment        = "byte-budget"
	latencySegment     = "latency-budget"
	meanLatencySegment = "mean-latency-budget"
)

// allocated keeps each allocation of the planted defect of an allocation
// ceiling on the heap.
var allocated atomic.Pointer[[8]byte]

// ceiling is the check of one ceiling of a call. added reports that the
// option Ceilings states the budget of the ceiling, so an application
// falsifies the check before it applies it.
type ceiling[S any] struct {
	check Check[S]
	added bool
}

// RunBudgets applies the allocation ceiling of each call of the suite, and of
// each budget that the option [Ceilings] adds, to subjects of newSubject. A
// generated suite runs it in Test<Subject>Allocs.
//
// Each allocation ceiling is a property check. Each case draws a call from
// the call generator of the callable and builds a fresh subject outside the
// count. It asserts [assert.MaxAllocs] on the call with the statement of the
// check, such as "Get allocates at most 1 time per call". The case records
// the allocations of one call as its score with [prop.Case.Target], so the
// campaign profile of the property engine searches for the input with the
// most allocations.
//
// RunBudgets reports every problem of the checks, the calls and the options
// on t, as [Run] does, and stops t when it finds one. Otherwise it runs each
// ceiling as a test of t, such as Get/allocation-budget. The tests do not run
// in parallel, because a count of allocations covers the whole process. The
// test of an excused ceiling skips with the reason of the excusal. In a run
// that does not check allocation ceilings, each test skips with the reason of
// [assert.AllocsChecked]. The test of a ceiling that the option Ceilings adds
// falsifies the ceiling before it applies it.
func RunBudgets[S any](t T, suite Suite[S], newSubject func(h *Handle[S]) S, opts ...Option[S]) {
	t.Helper()
	cfg := configure(opts)
	cfg.using = suite.Using
	stopAt(t, validate("Run"+suite.Declaration+"Budgets", suite, cfg))
	for _, c := range allocationCeilings(suite.Calls, cfg.ceilings) {
		t.Run(c.check.ID.subtest(), func(t *testing.T) {
			t.Helper()
			cfg.skipExcused(t, c.check.ID)
			skipUncounted(t)
			if c.added {
				c.check.falsify(t, cfg, nil)
			}
			c.check.run(t, newSubject, cfg)
		})
	}
}

// FalsifyAllocs applies the allocation ceiling of each call of the suite to
// its planted defect. The defect is the subject that Over of the call returns,
// whose callable allocates one time more per call than the ceiling admits. A
// generated suite runs it in Test<Declaration>SuiteAllocs.
//
// FalsifyAllocs reports every problem of the calls on t, and stops t when it
// finds one. Otherwise it runs each ceiling as a test of t. The tests do not
// run in parallel, because a count of allocations covers the whole process.
// The test of a ceiling fails unless the defect fails the ceiling at
// [assert.MaxAllocs]. In a run that does not check allocation ceilings, each
// test skips with the reason of [assert.AllocsChecked].
func FalsifyAllocs[S any](t T, suite Suite[S]) {
	t.Helper()
	cfg := configure[S](nil)
	cfg.using = suite.Using
	stopAt(t, budgetProblems(suite.Declaration+"Suite", suite.Calls, nil))
	for _, c := range allocationCeilings(suite.Calls, nil) {
		t.Run(c.check.ID.subtest(), func(t *testing.T) {
			t.Helper()
			skipUncounted(t)
			c.check.falsify(t, cfg, nil)
		})
	}
}

// skipUncounted skips tb with the reason of [assert.AllocsChecked] in a run
// that does not check allocation ceilings.
func skipUncounted(tb testing.TB) {
	tb.Helper()
	if checked, reason := assert.AllocsChecked(); !checked {
		tb.Skipf("the run does not check allocation ceilings: %s", reason)
	}
}

// budgetOf returns the budget of the call c: the budget of its declaration,
// or the budget that ceilings state for its callable where the declaration
// states none. It reports whether ceilings state the budget.
func budgetOf[S any](c *Call[S], ceilings []stated[S]) (Budget, bool) {
	if c.Budget != (Budget{}) {
		return c.Budget, false
	}
	for _, s := range ceilings {
		if s.callable == c.Callable {
			return s.budget, true
		}
	}
	return Budget{}, false
}

// allocationCeilings returns the check of the allocation ceiling of each call
// of calls whose budget states one.
func allocationCeilings[S any](calls []Call[S], ceilings []stated[S]) []ceiling[S] {
	var out []ceiling[S]
	for i := range calls {
		c := &calls[i]
		if budget, added := budgetOf(c, ceilings); budget.Allocs != nil {
			out = append(out, ceiling[S]{check: allocationCheck(c, *budget.Allocs), added: added})
		}
	}
	return out
}

// allocationCheck returns the check that the callable of c makes at most n
// heap allocations per call.
func allocationCheck[S any](c *Call[S], n uint64) Check[S] {
	times := "times"
	if n == 1 {
		times = "time"
	}
	return Check[S]{
		ID:        c.Callable.Check(allocationSegment),
		Statement: fmt.Sprintf("%s allocates at most %d %s per call", c.Callable.method(), n, times),
		Property: func(h *Handle[S], pc *prop.Case) {
			call := pc.Draw(c.Calls, "call")
			s := h.Subject()
			ctx := h.Context()
			run := func() { _ = call(ctx, s) }
			pc.Target("allocations", testing.AllocsPerRun(1, run))
			assert.MaxAllocs(h, run, n, h.Statement())
		},
		Defect: func(h *Handle[S]) S {
			return c.Over(h, func() {
				for range n + 1 {
					allocated.Store(new([8]byte))
				}
			})
		},
	}
}

// ceilingIDs returns the identity of the check of each ceiling of each call
// of calls, under the budgets of the declaration and of ceilings.
func ceilingIDs[S any](calls []Call[S], ceilings []stated[S]) []ID[S] {
	var out []ID[S]
	for i := range calls {
		c := &calls[i]
		budget, _ := budgetOf(c, ceilings)
		if budget.Allocs != nil {
			out = append(out, c.Callable.Check(allocationSegment))
		}
		if budget.Bytes != nil {
			out = append(out, c.Callable.Check(byteSegment))
		}
		if budget.Latency != 0 {
			out = append(out, c.Callable.Check(latencySegment))
		}
		if budget.Mean != 0 {
			out = append(out, c.Callable.Check(meanLatencySegment))
		}
	}
	return out
}

// budgetProblems returns the problems of the calls and the budgets of the
// application name. A call needs a call generator and a planted defect, and
// no two calls have one callable. A budget with a mean has a p99 as well. The
// option Ceilings states one budget of a callable of a call, and only for a
// callable whose declaration states none.
func budgetProblems[S any](name string, calls []Call[S], ceilings []stated[S]) []string {
	var found []string
	byCallable := make(map[Callable[S]]*Call[S], len(calls))
	for i := range calls {
		c := &calls[i]
		if reflect.ValueOf(c.Calls).IsZero() {
			found = append(found, fmt.Sprintf("the call of %s has no call generator", c.Callable))
		}
		if c.Over == nil {
			found = append(found, fmt.Sprintf("the call of %s has no planted defect", c.Callable))
		}
		if _, seen := byCallable[c.Callable]; seen {
			found = append(found, fmt.Sprintf("two calls of %s have the callable %s", name, c.Callable))
		}
		byCallable[c.Callable] = c
		if budget, _ := budgetOf(c, ceilings); budget.Mean != 0 && budget.Latency == 0 {
			found = append(found, fmt.Sprintf("the budget of %s states a mean and no p99, and a mean alone passes "+
				"a tail that grows", c.Callable))
		}
	}
	seen := make(map[Callable[S]]bool, len(ceilings))
	for _, s := range ceilings {
		c, called := byCallable[s.callable]
		switch {
		case seen[s.callable]:
			found = append(found, fmt.Sprintf("the option Ceilings states two budgets of %s", s.callable))
		case !called:
			found = append(found, fmt.Sprintf("the option Ceilings states a budget of %s, and %s has no call of it",
				s.callable, name))
		case c.Budget != (Budget{}):
			found = append(found, fmt.Sprintf("the option Ceilings states a budget of %s, whose declaration states "+
				"a budget. Excuse the checks of the declaration instead", s.callable))
		}
		seen[s.callable] = true
	}
	return found
}
