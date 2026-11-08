// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package dump reads a dump of the stacks of goroutines in the form of the
// goroutine profile of runtime/pprof at debug level 2: the stacks of the
// goroutines of a testing/synctest bubble, the bubble of a goroutine, whether
// a goroutine waits durably, and whether the goroutines of a bubble have
// stalled.
//
// # Dependency position
//
// The package imports the Go stdlib alone.
package dump
