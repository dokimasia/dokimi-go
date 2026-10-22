// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package fault contains the strategies that select the calls of a generated
// double that fail.
//
// A test arms a [Fault] on a method of a double. The double calls the method
// ShouldTrigger of the fault for each call before it answers the call. The
// strategies compose:
//
//	f := fault.And(
//		fault.Fault[StoreGetCall](fault.NewPredicateFault(errUnavailable, func(c StoreGetCall) bool {
//			return c.ID == "a"
//		})),
//		fault.NewCountedFault[StoreGetCall](errUnavailable, 2),
//	)
//
// fails every second call of Get for the ID a. A fault returns the error that
// the test armed as it is, so errors.Is in the code under test matches it.
//
// # Determinism
//
// [NewProbabilityFault] takes its random numbers from a source that the test
// passes, such as a seeded PCG, so a seeded run repeats its faults.
// [NewWindowedFault] compares its deadline with the instant of the clock of
// the double, so a test that controls the clock controls the window.
//
// # Concurrency
//
// A fault is safe for concurrent use, so the goroutines that call one double
// can share the fault of the double.
//
// # Dependency position
//
// The package imports the Go stdlib alone.
package fault
