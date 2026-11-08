// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"reflect"
	"slices"
	"time"
)

// Process is one process of a world: its name, its host, its epoch, its
// machine and its dependencies, or the component that it runs. The clock of a
// process is the clock of its host. The clock of the host of a component is
// the clock of the run without a drift, and the code of the component reads
// the clock of its host of go.dokimi.dev/host.
type Process[E, F any] struct {
	host       *Host[E, F]
	machine    Machine[E, F]
	newMachine func(clock *Clock) Machine[E, F]
	comp       *component
	name       string
	queue      []queued[E]
	deps       []any
	epoch      uint64
	state      state
	// started marks a process that an effect started, which a restart of its
	// host does not start again.
	started bool
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

// state is the state of a process or of a host.
type state uint8

const (
	// running is a process that takes events, while its host is not paused,
	// and a host that runs.
	running state = iota
	// stopped is a process that took the stop event, at the stop of the world,
	// at the exit of its host or at the stop by an effect, and a host after
	// the stop of the world.
	stopped
	// crashed is a process whose machine is gone until a restart, and a host
	// that crashed.
	crashed
	// exited is a host whose processes the error of the machine of one of
	// them stopped, until a restart.
	exited
)

// Name returns the name of the process.
func (p *Process[E, F]) Name() string { return p.name }

// Epoch returns the epoch of the process, from 1, which each restart raises.
func (p *Process[E, F]) Epoch() uint64 { return p.epoch }

// Clock returns the clock of the process, which is the clock of its host.
func (p *Process[E, F]) Clock() *Clock { return p.host.clock }

// runs reports whether the process takes events: it runs, and its host is not
// paused.
func (p *Process[E, F]) runs() bool { return p.state == running && !p.host.paused }

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
