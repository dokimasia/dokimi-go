// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

// CompatV1 is the compatibility witness of major version 1 of the runtime of
// doubles. Every generated double refers to it once:
//
//	var _ = stub.CompatV1
//
// A major version of the runtime renames the witness. A double that a
// generator wrote for another major version then fails to compile, and the
// error names the witness of that version.
func CompatV1() {}
