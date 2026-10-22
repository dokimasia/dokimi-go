// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import "testing"

// Falsify applies each check of the suite to its planted defect. A generated
// suite runs it in the spec of its file, under the level of the function that
// returns the suite:
//
//	func TestStoreSuite(t *testing.T) {
//		t.Parallel()
//
//		t.Run("StoreSuite", func(t *testing.T) {
//			t.Parallel()
//
//			conform.Falsify(t, StoreSuite())
//		})
//	}
//
// Falsify reports every problem of the checks and the calls on t, and stops t
// when it finds one. Otherwise it runs each check as a parallel test of t,
// which has the identity of the check without the name of the declaration, as
// [Run] names it. The test of a check fails unless the planted defect fails
// the check at the assertion whose message is the statement of the check. The
// test also applies every other check of the suite to the defect, and fails
// when the defect fails one of them. Checks that share one defect accept the
// failures of each other.
//
// Falsify also runs a parallel test of each ceiling of bytes, p99 and mean of
// the budget of each call, such as Get/latency-budget. The test measures 10
// calls of the planted defect of the ceiling under a benchmark contract of the
// ceiling alone, and fails unless the contract fails. The test of a byte
// ceiling skips in a run that does not check allocation ceilings.
// [FalsifyAllocs] falsifies the allocation ceilings.
func Falsify[S any](t T, suite Suite[S]) {
	t.Helper()
	cfg := configure[S](nil)
	cfg.using = suite.Using
	name := suite.Declaration + "Suite"
	found := problems(name, suite.Checks, nil, ceilingIDs(suite.Calls, nil))
	stopAt(t, append(found, budgetProblems(name, suite.Calls, nil)...))
	for _, c := range suite.Checks {
		t.Run(c.ID.subtest(), func(t *testing.T) {
			t.Helper()
			t.Parallel()
			c.falsify(t, cfg, suite.Checks)
		})
	}
	for i := range suite.Calls {
		call := &suite.Calls[i]
		for _, l := range call.Budget.limits() {
			if !l.stated() || l.segment == allocationSegment {
				continue
			}
			t.Run(call.Callable.Check(l.segment).subtest(), func(t *testing.T) {
				t.Helper()
				t.Parallel()
				if l.budget.counted() {
					skipUncounted(t)
				}
				falsifyLimit(t, call, l, cfg)
			})
		}
	}
}
