// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import "testing"

// Falsify applies each check of the suite to its planted defect. A generated
// suite runs it in the test of its proofs:
//
//	func TestStoreSuite(t *testing.T) {
//		t.Parallel()
//
//		conform.Falsify(t, StoreSuite())
//	}
//
// Falsify reports every problem of the checks on t, and stops t when it finds
// one. Otherwise it runs each check as a parallel test of t, named by the
// identity of the check. The test of a check fails unless the planted defect
// fails the check at the assertion whose message is the statement of the
// check.
func Falsify[S any](t T, suite Suite[S]) {
	t.Helper()
	cfg := configure[S](nil)
	stopAt(t, problems(suite.Declaration+"Suite", suite.Checks, nil))
	for _, c := range suite.Checks {
		t.Run(string(c.ID), func(t *testing.T) {
			t.Helper()
			t.Parallel()
			c.falsify(t, cfg)
		})
	}
}
