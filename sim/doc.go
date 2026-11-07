// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package sim is a deterministic simulation of a system of sans-IO machines
// and of Go components on simulated hosts.
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
// A process persists through each medium.Memory among the dependencies that
// [World.Spawn] gives it. A crash of the process crashes each such medium, so
// the process loses each write that a sync did not cover. [World.Tear],
// [World.Corrupt] and [World.Fill] strike the faults of the media of a
// process: a torn write, a flipped bit at rest and a full disk.
//
// A component process runs a Go value on a host of go.dokimi.dev/host.
// [SpawnComponent] starts it, and [ComponentOf] returns it by its name to a
// function of the simulation. [World.Invoke] runs each call of a client on a
// goroutine of its incarnation. The faults of a process strike a
// component through its host, and the faults of the network strike the links
// between the hosts of the components.
//
// # Determinism
//
// The world hashes each entry of its log into a checksum, which a replay of
// the case compares. A machine or a performer that iterates a Go map, reads
// the wall clock, takes the global source of math/rand or starts a goroutine
// makes a replay differ. A world with a component in Tasks mode repeats the
// whole case. In native mode it repeats the choices of the case, and not the
// order of the goroutines of its components.
//
// A run with a component takes the bounds of each request from the choices of
// the case and from the state that its steps set, so a replay requests the
// bounds of the case in both modes. A call goes to the first client without an
// open call from the client that the case chooses, and a cancel, a fault of
// the datagrams and a corruption take no effect without a target. An action of
// the consumer in such a run takes its bounds from the case alone.
//
// # Concurrency
//
// A world is not safe for concurrent use. Its machines, its performers and
// the doubles of its dependencies run on the goroutine of the case. The
// goroutines of a component complete its calls under a lock of the world.
//
// # Dependency position
//
// The package imports the standard library, the packages assert, history,
// prop and stateful of go.dokimi.dev/assert, and the modules conform, host,
// medium and spec of go.dokimi.dev.
package sim
