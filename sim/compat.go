// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

// CompatV1 is the compatibility witness of major version 1 of the simulation.
// Every generated simulation refers to it once:
//
//	var _ = sim.CompatV1
//
// A major version of the module renames the witness. A simulation that a
// generator wrote for another major version then fails to compile, and the
// error contains the name of the witness of that version.
func CompatV1() {}
