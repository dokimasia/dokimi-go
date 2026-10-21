// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

// CompatV1 is the compatibility witness of major version 1 of the runtime of
// suites. Every generated suite refers to it once:
//
//	var _ = conform.CompatV1
//
// A major version of the runtime renames the witness. A suite that a
// generator wrote for another major version then fails to compile, and the
// error contains the name of the witness of that version.
func CompatV1() {}
