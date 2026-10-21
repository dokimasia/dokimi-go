// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package fault

// CompatV1 is the compatibility witness of major version 1 of the fault
// strategies. Every generated double that arms a fault strategy refers to it
// once:
//
//	var _ = fault.CompatV1
//
// A major version of the module renames the witness. A double that a
// generator wrote for another major version then fails to compile, and the
// error contains the name of the witness of that version.
func CompatV1() {}
