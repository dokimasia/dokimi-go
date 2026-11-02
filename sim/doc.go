// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package sim is a deterministic simulation of a system of sans-IO machines.
//
// A sans-IO machine applies one event at a time and returns the effects that
// a runtime performs, such as a commit to a store or a reply to a caller. A
// [World] is the runtime of the machines of one system. It runs every machine
// on the goroutine of one case of the property engine of the assert library,
// and every choice of a run is a choice of the case, so a failing run shrinks
// and replays from its seed.
//
// A world reads the events and the effects of its machines through an
// [Anatomy]: the events of the start, the stop and a cancelled call, the
// reply token of an inbound call, the reply to a call, and the token of an
// effect that starts a call or a wait. The world records each step of a
// machine in its log, and each call of a client in the history of the case.
//
// # Determinism
//
// The world hashes each entry of its log into a checksum, which a replay of
// the case compares. A machine or a performer that iterates a Go map, reads
// the wall clock, takes the global source of math/rand or starts a goroutine
// makes a replay differ.
//
// # Concurrency
//
// A world is not safe for concurrent use. Its machines, its performers and
// the doubles of its dependencies run on the goroutine of the case.
//
// # Dependency position
//
// The package imports the standard library, and the packages assert, history,
// prop and stateful of go.dokimi.dev/assert and the spec of go.dokimi.dev/spec.
package sim
