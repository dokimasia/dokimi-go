// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package conform is the runtime of the conformance suites that dokimi
// generates.
//
// A [Suite] contains the checks of one declaration. A [Check] has an
// identity, a statement, a function that applies the check through its
// [Handle], and a function that returns its planted defect. [Verify] applies
// every check of a suite to a subject of the consumer. Each check runs as a
// test of its own, named by its identity.
//
// A generated suite declares the application of its declaration, and a
// consumer adds checks of its own through [With]:
//
//	func TestInMemory(t *testing.T) {
//		t.Parallel()
//
//		storetest.RunStore(t, storetest.NewInMemory, conform.With(keepsTheValue()))
//	}
//
// The options of an application also excuse a check through [Excuse], and
// supply the machinery that only the consumer can build through [Reference],
// [Second] and [Restartable]. An application falsifies each check that [With]
// adds before it applies the check to the subject. The check must fail on its
// planted defect at the assertion whose message is its statement.
//
// # Dependency position
//
// The package imports go.dokimi.dev/assert and the Go stdlib, testing among
// it.
package conform
