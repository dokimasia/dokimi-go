// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package host simulates the machine of a component: the incarnations of its
// process, its disk, its clocks, its network and its dependencies. The world
// of a subject in conform runs one host, and a world of sim runs a host for
// each component process.
//
// A [Host] keeps its disk, its clocks and its address across the incarnations
// of its process. [Host.Start] starts an [Incarnation] and returns the [Env]
// that the code of the component receives. Every resource of a component
// comes from its environment: its lifetime, its view of the disk, its
// [Clock], its source of choices, its dependencies, its goroutines and its
// [Network]. [Incarnation.Crash] ends an incarnation by a [Kill]: it fences
// every resource of the incarnation, ends its lifetime and applies the kind to
// the disk. [Incarnation.Pause] blocks every call of the environment until
// [Incarnation.Resume]. A world calls [Host.Broadcast] on each host after each
// of its steps, so each wait of [Env.Wait] calls its condition again.
//
// A [Network] connects the hosts of a world through streams and datagrams. A
// blocking network delays the bytes of each stream by the latency and the
// throughput of its link, which the case chooses from the ranges of [Latency]
// and [Throughput].
//
// # Modes
//
// A host runs in one of two modes. Without a scheduler, the goroutines of a
// component run in a testing/synctest bubble, and every wait of the host is
// durable in the bubble: a wait for a hold, for a pause, for the bytes of a
// stream and for a condition of [Env.Wait]. Under a scheduler of the stateful
// package, the goroutines of a component run as its tasks, and every wait of
// the host parks the task through Scheduler.Wait or sleeps on the clock of
// the scheduler. The scheduler replays the order of the tasks with the
// choices of the case.
//
// # Goroutines
//
// Each goroutine of an incarnation has the profiler labels dokimi.host,
// dokimi.incarnation and dokimi.run, which every goroutine that it starts
// inherits. dokimi.run is a number of the host that is unique in the process,
// so the incarnations of two hosts of one name in parallel tests differ.
// [Incarnation.Leaked] reads the goroutine profile of the process for the
// goroutines of the incarnation that remain alive after its crash. A timer of
// the host, such as the arrival of a datagram or the end of a deadline, can
// still run after the crash of the incarnation that started it. It takes the
// labels of the context of its wait, and that context carries no labels of an
// incarnation.
package host
