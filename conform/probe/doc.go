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
package probe
