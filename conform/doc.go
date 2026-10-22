// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package conform is the runtime of the conformance suites that dokimi
// generates.
//
// A [Suite] contains the checks of one declaration. A [Check] has an
// identity, a statement, a function that returns its planted defect, and a
// function that applies the check through its [Handle]. A check with Apply
// runs once. A check with Property runs under the property engine of the
// assert library, with a handle and a fresh subject for each case. [Run]
// applies every check of a suite to a subject of the consumer. [Falsify]
// applies every check to its planted defect, and every other check of the
// suite to the same defect. Each check runs as a test of its own, named by
// its identity.
//
// A generated suite declares the application of its declaration, and a
// consumer adds checks of its own through [With]:
//
//	func TestInMemory(t *testing.T) {
//		t.Parallel()
//
//		storetest.RunStoreSuite(t, storetest.NewInMemory, conform.With(keepsTheValue()))
//	}
//
// The options of an application also excuse a check through [Excuse], set the
// number of cases of each property run through [Cases], and supply the
// machinery that only the consumer can build through [Reference], [Second]
// and [Restartable]. An application falsifies each check that [With] adds
// before it applies the check to the subject. The check must fail on its
// planted defect at the assertion whose message is its statement.
//
// The calls of a suite contain the call generator and the budget of each
// callable. [RunBudgets] applies the allocation ceilings of the budgets to a
// subject, and [FalsifyAllocs] applies each of them to its planted defect.
// The option [Ceilings] states a budget for one application. [Fuzz] registers
// the fuzz target of the property checks of one callable.
//
// # Dependency position
//
// The package imports go.dokimi.dev/assert, go.dokimi.dev/assert/prop and the
// Go stdlib, testing among it.
package conform
