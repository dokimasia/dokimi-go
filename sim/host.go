// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"slices"

	"go.dokimi.dev/assert/prop"
)

// Host is a machine of a world on which processes run. The processes of a
// host share its clock. A crash, a restart, a pause, a jump and a strobe strike
// each process of the host at once, and a fault of a medium strikes the media
// of each of them. A component runs alone on a host of its own.
type Host[E, F any] struct {
	w      *World[E, F]
	clock  *Clock
	name   string
	procs  []*Process[E, F]
	strobe strobe
	state  state
	paused bool
	full   bool
	// replaced marks the host of a component that a disk replacement
	// crashed, until its restart.
	replaced bool
}

// AddHost adds a host named name to the world and returns it. The case
// chooses the drift of its clock under the label drift.<name>. AddHost fails
// the case where the world already has a host named name.
func (w *World[E, F]) AddHost(name string) *Host[E, F] {
	w.vacant(name)
	drift := w.c.Draw(prop.Integer(minDrift, maxDrift), "drift."+name)
	return w.host(name, newClock(drift))
}

// host adds the host named name with the clock to the world. The world has no
// host of that name.
func (w *World[E, F]) host(name string, clock *Clock) *Host[E, F] {
	h := &Host[E, F]{w: w, name: name, clock: clock}
	w.hosts = append(w.hosts, h)
	return h
}

// vacant fails the case where the world has a host named name.
func (w *World[E, F]) vacant(name string) {
	if w.hostOf(name) != nil {
		w.c.Fatalf("sim: the world has a host named %q", name)
	}
}

// Name returns the name of the host.
func (h *Host[E, F]) Name() string { return h.name }

// Spawn adds a process named name to the host and queues its start, as
// [World.Spawn] does. newMachine builds the machine of the process from the
// clock of the host. A restart of the host starts the process again in a new
// epoch. deps are the dependencies of the process, such as the doubles of its
// stores, which a performer reads through Dependency. Spawn fails the case
// for a host that does not run, and where the world already has a process
// named name.
func (h *Host[E, F]) Spawn(name string, newMachine func(clock *Clock) Machine[E, F], deps ...any) {
	if h.state != running {
		h.w.c.Fatalf("sim: the host %q does not run", h.name)
	}
	h.w.start(h.add(name, newMachine, deps))
}

// add adds a process named name to the host, which builds its machine through
// newMachine and has the dependencies deps. It fails the case where the world
// already has a process named name.
func (h *Host[E, F]) add(name string, newMachine func(clock *Clock) Machine[E, F], deps []any) *Process[E, F] {
	w := h.w
	if w.Process(name) != nil {
		w.c.Fatalf("sim: the world has a process named %q", name)
	}
	p := &Process[E, F]{name: name, host: h, newMachine: newMachine, deps: deps}
	h.procs = append(h.procs, p)
	w.procs = append(w.procs, p)
	return p
}

// component returns the process of the component of the host, and nil for a
// host of machines.
func (h *Host[E, F]) component() *Process[E, F] {
	at := slices.IndexFunc(h.procs, func(p *Process[E, F]) bool { return p.comp != nil })
	if at < 0 {
		return nil
	}
	return h.procs[at]
}

// hostOf returns the host named name, and nil for a name that no host has.
func (w *World[E, F]) hostOf(name string) *Host[E, F] {
	at := slices.IndexFunc(w.hosts, func(h *Host[E, F]) bool { return h.name == name })
	if at < 0 {
		return nil
	}
	return w.hosts[at]
}

// hostNamed returns the host named name, and fails the case for a name that
// no host has.
func (w *World[E, F]) hostNamed(name string) *Host[E, F] {
	h := w.hostOf(name)
	if h == nil {
		w.c.Fatalf("sim: no host of the world is named %q", name)
	}
	return h
}
