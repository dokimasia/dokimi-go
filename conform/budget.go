// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/prop"
)

// The segments of the checks of the ceilings of a budget.
const (
	allocationSegment  = "allocation-budget"
	byteSegment        = "byte-budget"
	latencySegment     = "latency-budget"
	meanLatencySegment = "mean-latency-budget"
)

// sink keeps each allocation of the planted defect of an allocation ceiling or
// a byte ceiling on the heap.
var sink atomic.Pointer[byte]

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
			out = append(out, ceiling[S]{check: allocationCheck(c, budget.Allocs), added: added})
		}
	}
	return out
}

// allocationCheck returns the check that the callable of c makes at most
// *allocs heap allocations per call.
func allocationCheck[S any](c *Call[S], allocs *uint64) Check[S] {
	n := *allocs
	return Check[S]{
		ID:        c.Callable.Check(allocationSegment),
		Statement: allocationStatement(c.Callable.method(), n),
		Property: func(h *Handle[S], pc *prop.Case) {
			call := pc.Draw(c.Calls, "call")
			s := h.Subject()
			ctx := h.Context()
			run := func() { _ = call(ctx, s) }
			pc.Target("allocations", testing.AllocsPerRun(1, run))
			assert.MaxAllocs(h, run, n, h.Statement())
		},
		Defect: func(h *Handle[S]) S { return c.Over(h, Budget{Allocs: allocs}.over()) },
	}
}

// allocationStatement returns the statement that the callable method makes at
// most n heap allocations per call.
func allocationStatement(method string, n uint64) string {
	return fmt.Sprintf("%s allocates at most %d %s per call", method, n, plural(n, "time", "times"))
}

// plural returns one for a count of 1, and many for any other count.
func plural(n uint64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// limit is one ceiling of a budget: the segment of the identity of its check,
// and a budget of that ceiling alone.
type limit struct {
	segment string
	budget  Budget
}

// stated reports whether the budget of the limit states its ceiling.
func (l limit) stated() bool {
	return l.budget != Budget{}
}

// limits returns the four ceilings of the budget in the order of its fields:
// the allocations, the bytes, the p99 and the mean. The budget of a ceiling
// that the budget does not state is the zero Budget.
func (b Budget) limits() [4]limit {
	return [4]limit{
		{segment: allocationSegment, budget: Budget{Allocs: b.Allocs}},
		{segment: byteSegment, budget: Budget{Bytes: b.Bytes}},
		{segment: latencySegment, budget: Budget{Latency: b.Latency}},
		{segment: meanLatencySegment, budget: Budget{Mean: b.Mean}},
	}
}

// counted reports whether the budget states a ceiling of allocations or
// bytes, which only a run that counts allocations checks.
func (b Budget) counted() bool {
	return b.Allocs != nil || b.Bytes != nil
}

// state states each ceiling of the budget on c, and returns c.
func (b Budget) state(c *bench.Contract) *bench.Contract {
	if b.Allocs != nil {
		c.MaxAllocs(*b.Allocs)
	}
	if b.Bytes != nil {
		c.MaxBytes(*b.Bytes)
	}
	if b.Latency != 0 {
		c.MaxLatency(b.Latency)
	}
	if b.Mean != 0 {
		c.MaxMean(b.Mean)
	}
	return c
}

// over returns the spend of the planted defect of a budget of one ceiling: one
// heap allocation more than its allocation ceiling admits, more bytes than its
// byte ceiling admits, or twice the time of its p99 or its mean. A spend of
// bytes allocates 16 bytes at least, because the runtime packs smaller
// allocations without pointers into one block.
func (b Budget) over() func() {
	switch {
	case b.Allocs != nil:
		n := *b.Allocs + 1
		return func() {
			for range n {
				sink.Store(new(byte))
			}
		}
	case b.Bytes != nil:
		n := *b.Bytes
		return func() {
			spent := make([]byte, max(n+1, 16))
			sink.Store(&spent[0])
		}
	default:
		d := max(b.Latency, b.Mean)
		return func() { time.Sleep(2 * d) }
	}
}

// ceilingIDs returns the identity of the check of each ceiling of each call
// of calls, under the budgets of the declaration and of ceilings.
func ceilingIDs[S any](calls []Call[S], ceilings []stated[S]) []ID[S] {
	var out []ID[S]
	for i := range calls {
		budget, _ := budgetOf(&calls[i], ceilings)
		for _, l := range budget.limits() {
			if l.stated() {
				out = append(out, calls[i].Callable.Check(l.segment))
			}
		}
	}
	return out
}

// budgetProblems returns the problems of the calls and the budgets of the
// application name. A call needs a call generator and a planted defect, and
// no two calls have one callable. A budget with a mean has a p99 as well, and
// a warm-up of a budget is 0 iterations or more. The option Ceilings states
// one budget of a callable of a call, and only for a callable whose
// declaration states none.
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
		budget, _ := budgetOf(c, ceilings)
		if budget.Mean != 0 && budget.Latency == 0 {
			found = append(found, fmt.Sprintf("the budget of %s states a mean and no p99, and a mean alone passes "+
				"a tail that grows", c.Callable))
		}
		if budget.Warmup != nil && *budget.Warmup < 0 {
			found = append(found, fmt.Sprintf("the budget of %s states a warm-up of %d iterations, and a warm-up is "+
				"0 iterations or more", c.Callable, *budget.Warmup))
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
