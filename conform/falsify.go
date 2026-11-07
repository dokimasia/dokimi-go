// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"slices"
	"testing"
)

// Falsify applies each check of the suite, and each check that opts add, to
// its planted defect. A generated suite runs it in the spec of its file, under
// the level of the function that returns the suite:
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
// Falsify takes the options of an application, as [Run] takes them. Each
// planted defect takes the doors of its check first and each other door from
// opts, so a planted defect can wrap a reference or open a value that the
// caller of the suite builds, such as the reference of [Reference] or a value
// of [Supplies]. The test of a check or a ceiling that opts excuse skips with
// the reason of the excusal, and the generators of [Using] apply to each
// property run.
//
// Falsify reports every problem of the checks, the calls, the options and the
// seed on t, and stops t when it finds one. Otherwise it runs each check as a parallel test of t,
// which has the identity of the check without the name of the declaration, as
// [Run] names it. The test of a check fails unless the planted defect fails
// the check at the assertion whose message is the statement of the check. A
// check that skips before it fails skips its test. The test also applies every
// other check of the suite to the defect, and fails when the defect fails one
// of them. Checks that share one defect accept the failures of each other.
// The test also proves each edge of the check, as [Check.Run] states for a
// derived check under [Isolate]. Falsify leaves out each derived check, whose
// planted defect wraps a fresh subject of an application, which [Run]
// falsifies. Falsify leaves out each check that counts allocations, which
// [FalsifyAllocs] falsifies, and does not apply such a check to another
// planted defect.
//
// Falsify also runs a parallel test of each ceiling of bytes, p99 and mean of
// the budget of each call, such as Get/latency-budget. The test measures 10
// calls of the planted defect of the ceiling under a benchmark contract of the
// ceiling alone, and fails unless the contract fails. The test of a byte
// ceiling skips in a run that does not check allocation ceilings.
// [FalsifyAllocs] falsifies the allocation ceilings.
//
// Once every test has ended, Falsify writes the falsification record of the
// application into the output of a t that is an [assert.TB], such as
// *testing.T, as [Run] writes its records.
func Falsify[S any](t T, suite Suite[S], opts ...Option[S]) {
	t.Helper()
	cfg := configure(suite.overrides(t), suite.Checks, opts)
	name := suite.Declaration + "Suite"
	seed, faults := cfg.applicationSeed(name)
	app := newApplication(&suite, name, seed, falsification)
	app.begin(t, append(validate(name, suite, cfg), faults...))
	parallel := uncounted(append(slices.Clip(suite.Checks), cfg.checks...))
	for i, c := range parallel {
		if c.Claim != "" {
			continue
		}
		t.Run(c.ID.subtest(), func(t *testing.T) {
			t.Helper()
			t.Parallel()
			falsified := app.track(t, i, falsification, &c, cfg, seedOf(string(c.ID)))
			cfg.skipExcused(t, c.ID)
			c.falsify(t, cfg, parallel, falsified)
		})
	}
	at := len(parallel)
	for i := range suite.Calls {
		call := &suite.Calls[i]
		for _, l := range call.Budget.limits() {
			if !l.stated() || l.segment == allocationSegment {
				continue
			}
			ceiling := Check[S]{ID: call.Callable.Check(l.segment), ceiling: true}
			position := at
			at++
			t.Run(ceiling.ID.subtest(), func(t *testing.T) {
				t.Helper()
				t.Parallel()
				falsified := app.track(t, position, falsification, &ceiling, cfg, 0)
				cfg.skipExcused(t, ceiling.ID)
				if l.budget.counted() {
					skipUncounted(t, falsified)
				}
				falsifyLimit(t, call, l, cfg, falsified)
			})
		}
	}
}
