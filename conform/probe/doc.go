// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package probe implements the probes of the conformance spec of dokimi.
// Each function is one probe. It takes the handle of its check first, and
// one argument for each input of its entry in the spec, in the order of the
// entry.
//
// A probe calls the subject through the calls that the generated check
// passes, in one of the shapes [Call], [Act] and [Lookup]. It sets the
// reason of a failure through Handle.Expect before each decisive assertion,
// and passes the statement of the check as the message of each assertion.
// A guard assertion, such as the success of an establishing write, sets no
// reason.
//
// A probe calls Handle.Engage where the vacuous condition of its entry stops
// being true, such as after an establishing write that succeeded. A probe
// that ends before that call, such as after a write that failed, leaves its
// check vacuous on the subject.
//
// A probe of generated sequences runs the [Actions] of a declaration on the
// subject through the machine of the package stateful of the assert library.
// It judges the calls that the history of the case records after each step.
// It calls a partner through an [Apply] and a reference through a
// [Reference].
//
// A probe of a world runs the operations of a [World] on a subject that a
// host of the package host runs. The case chooses concurrent calls, crashes
// and restarts, faults of the disk, the dependencies and the clock, and
// advances of the clock. The operations pick their keys from a [Pool]. The
// check of a world runs each case inside a bubble of conform.Bubble, under
// the options that [WorldOptions] returns.
package probe
