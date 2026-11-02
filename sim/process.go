// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"reflect"
	"slices"
	"time"
)

// Process is one process of a world: its name, its epoch, its clock, its
// machine and its dependencies.
type Process[E, F any] struct {
	machine    Machine[E, F]
	clock      *Clock
	newMachine func(clock *Clock) Machine[E, F]
	name       string
	queue      []queued[E]
	deps       []any
	strobe     strobe
	epoch      uint64
	state      state
	paused     bool
}

// queued is an event that waits for its process, with the epoch of the
// process in which the world queued it. message is the reply token of a
// message of another process, and 0 for any other event.
type queued[E any] struct {
	ev      E
	epoch   uint64
	message uint64
}

// strobe is the strobe of a clock: the offset of the next step, the offset of
// the step after it, and the steps that remain.
type strobe struct {
	next, then time.Duration
	left       int
}

// state is the state of a process.
type state uint8

const (
	// running is a process that takes events, while it is not paused.
	running state = iota
	// stopped is a process that took the stop event, or whose machine stopped
	// itself.
	stopped
	// crashed is a process whose machine is gone until a restart.
	crashed
)

// Name returns the name of the process.
func (p *Process[E, F]) Name() string { return p.name }

// Epoch returns the epoch of the process, from 1, which each restart raises.
func (p *Process[E, F]) Epoch() uint64 { return p.epoch }

// Clock returns the clock of the process.
func (p *Process[E, F]) Clock() *Clock { return p.clock }

// runs reports whether the process takes events: it runs and is not paused.
func (p *Process[E, F]) runs() bool { return p.state == running && !p.paused }

// Dependency returns the first dependency of the process p of the type T,
// such as the double of a store that the performer of an effect calls. Spawn
// gives a process its dependencies, and a restart keeps them. Dependency
// fails the case of w for a process without a dependency of the type T.
func Dependency[T, E, F any](w *World[E, F], p *Process[E, F]) T {
	at := slices.IndexFunc(p.deps, func(d any) bool {
		_, typed := d.(T)
		return typed
	})
	if at < 0 {
		w.c.Fatalf("sim: the process %q has no dependency of the type %s", p.name, reflect.TypeFor[T]())
	}
	dep, _ := p.deps[at].(T)
	return dep
}
