// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"context"
	"errors"
	"slices"
	"time"

	"go.dokimi.dev/assert/prop"
)

// Fault is the kind of a fault of a world.
type Fault string

// The kinds of the faults of a world.
const (
	// FaultDrop drops a message in flight.
	FaultDrop Fault = "drop"
	// FaultDelay moves a message in flight behind a later event.
	FaultDelay Fault = "delay"
	// FaultDuplicate queues a message in flight once more.
	FaultDuplicate Fault = "duplicate"
	// FaultPartition cuts the processes into sides.
	FaultPartition Fault = "partition"
	// FaultRepair ends a partition.
	FaultRepair Fault = "repair"
	// FaultLoseBefore loses an effect before its dependency applies it.
	FaultLoseBefore Fault = "lose-before"
	// FaultLoseAfter loses an effect after its dependency applies it.
	FaultLoseAfter Fault = "lose-after"
	// FaultOutage loses each effect of a dependency.
	FaultOutage Fault = "outage"
	// FaultSlow keeps each effect of a dependency in flight.
	FaultSlow Fault = "slow"
	// FaultCrash crashes a process.
	FaultCrash Fault = "crash"
	// FaultPause pauses a process.
	FaultPause Fault = "pause"
	// FaultRestart restarts a crashed process.
	FaultRestart Fault = "restart"
	// FaultClockJump moves a clock.
	FaultClockJump Fault = "clock-jump"
	// FaultClockStrobe moves a clock between two offsets.
	FaultClockStrobe Fault = "clock-strobe"
)

// ErrLost is the error of an effect that a fault of its dependency loses,
// for a dependency whose directive fault does not declare an error.
var ErrLost = errors.New("sim: a fault of the dependency lost the call")

// dependency is the state of the faults of one dependency: its armed losses
// in the order of their arming, the effects that its slowness keeps in
// flight, and whether it has an outage or is slow.
type dependency[E, F any] struct {
	name   string
	armed  []Fault
	stalls []*stall[E, F]
	outage bool
	slow   bool
}

// stall is an effect that a slow dependency keeps in flight: the effect, its
// process, and whether it has its result.
type stall[E, F any] struct {
	f       F
	process *Process[E, F]
	done    bool
}

// Partition cuts the processes into sides. Two processes exchange messages
// while one side contains both, so the sides of majorities-ring and bridge
// overlap. A message across the cut does not arrive. It fails the case for a
// name that no process has, and while a partition is in force.
func (w *World[E, F]) Partition(sides ...[]string) {
	if w.sides != nil {
		w.c.Fatalf("sim: a partition is in force")
	}
	for _, side := range sides {
		for _, name := range side {
			w.named(name)
		}
	}
	w.sides = sides
	w.strike(FaultPartition)
}

// Repair ends the partition. It fails the case without a partition in force.
func (w *World[E, F]) Repair() {
	if w.sides == nil {
		w.c.Fatalf("sim: no partition is in force")
	}
	w.sides = nil
	w.strike(FaultRepair)
}

// Sides returns the sides of the partition in force, and nil without one.
func (w *World[E, F]) Sides() [][]string { return w.sides }

// cut reports whether a partition cuts the processes a and b apart.
func (w *World[E, F]) cut(a, b *Process[E, F]) bool {
	return w.sides != nil && !slices.ContainsFunc(w.sides, func(side []string) bool {
		return slices.Contains(side, a.name) && slices.Contains(side, b.name)
	})
}

// Drop drops the oldest message in flight to the process named to, and
// reports whether a message was in flight. It fails the case for a name that
// no process has.
func (w *World[E, F]) Drop(to string) bool {
	p := w.named(to)
	if !w.sent(p) {
		return false
	}
	w.drop(p)
	return true
}

// drop drops the oldest message in flight to p, which has one.
func (w *World[E, F]) drop(p *Process[E, F]) {
	at := w.message(p)
	delete(w.calls, p.queue[at].message)
	p.queue = slices.Delete(p.queue, at, at+1)
	w.strike(FaultDrop)
}

// Delay moves the oldest message in flight to the process named to behind
// the next event of the process, and reports whether such a message was in
// flight. It fails the case for a name that no process has.
func (w *World[E, F]) Delay(to string) bool {
	p := w.named(to)
	if !w.behind(p) {
		return false
	}
	w.delay(p)
	return true
}

// delay moves the oldest message in flight to p behind the next event of p,
// which has one.
func (w *World[E, F]) delay(p *Process[E, F]) {
	at := w.message(p)
	p.queue[at], p.queue[at+1] = p.queue[at+1], p.queue[at]
	w.strike(FaultDelay)
}

// Duplicate queues the oldest message in flight to the process named to once
// more, as a call with a new reply token. The sender takes the first reply of
// the two calls. Duplicate reports whether a message was in flight, and fails
// the case for a name that no process has.
func (w *World[E, F]) Duplicate(to string) bool {
	p := w.named(to)
	if !w.sent(p) {
		return false
	}
	w.duplicate(p)
	return true
}

// duplicate queues the oldest message in flight to p once more, as a call
// with a new reply token. p has a message in flight.
func (w *World[E, F]) duplicate(p *Process[E, F]) {
	in := w.calls[p.queue[w.message(p)].message]
	w.request(p, in.build, in.await)
	w.strike(FaultDuplicate)
}

// sent reports whether a message is in flight to p.
func (w *World[E, F]) sent(p *Process[E, F]) bool { return w.message(p) >= 0 }

// behind reports whether a message is in flight to p with an event of p
// behind it.
func (w *World[E, F]) behind(p *Process[E, F]) bool {
	at := w.message(p)
	return at >= 0 && at < len(p.queue)-1
}

// LoseBefore arms a loss of the next effect of the dependency named
// dependency, before the dependency applies it.
func (w *World[E, F]) LoseBefore(dependency string) {
	d := w.dependency(dependency)
	d.armed = append(d.armed, FaultLoseBefore)
}

// LoseAfter arms a loss of the next effect of the dependency named
// dependency, after the dependency applies it.
func (w *World[E, F]) LoseAfter(dependency string) {
	d := w.dependency(dependency)
	d.armed = append(d.armed, FaultLoseAfter)
}

// Outage loses each effect of the dependency named dependency before the
// dependency applies it, until Restore. It fails the case for a dependency
// with an outage.
func (w *World[E, F]) Outage(dependency string) {
	d := w.dependency(dependency)
	if d.outage {
		w.c.Fatalf("sim: the dependency %q has an outage", dependency)
	}
	d.outage = true
	w.strike(FaultOutage)
}

// Slow keeps each effect of the dependency named dependency in flight until
// Restore. An effect with a timeout fails with context.DeadlineExceeded once
// its timeout has passed on the clock of its process. It fails the case for a
// slow dependency.
func (w *World[E, F]) Slow(dependency string) {
	d := w.dependency(dependency)
	if d.slow {
		w.c.Fatalf("sim: the dependency %q is slow", dependency)
	}
	d.slow = true
	w.strike(FaultSlow)
}

// Restore ends the outage and the slowness of the dependency named
// dependency, and performs each effect that its slowness keeps in flight. It
// fails the case for a dependency without an outage that is not slow.
func (w *World[E, F]) Restore(dependency string) {
	d := w.dependency(dependency)
	if !d.outage && !d.slow {
		w.c.Fatalf("sim: the dependency %q has no fault to restore", dependency)
	}
	w.restore(d)
}

// restore ends the outage and the slowness of the dependency d, and performs
// each effect that its slowness keeps in flight.
func (w *World[E, F]) restore(d *dependency[E, F]) {
	d.outage, d.slow = false, false
	for _, s := range d.stalls {
		if !s.done {
			s.done = true
			w.apply(s.process, s.f)
		}
	}
}

// Jump moves the clock of the process named name by d, backwards for a
// negative d. It fails the case for a name that no process has.
func (w *World[E, F]) Jump(name string, d time.Duration) {
	w.named(name).clock.jumps += d
	w.strike(FaultClockJump)
}

// Strobe moves the clock of the process named name between the offsets a
// and b at each of its next steps steps, from a. It fails the case for a name
// that no process has.
func (w *World[E, F]) Strobe(name string, a, b time.Duration, steps int) {
	w.named(name).strobe = strobe{next: a, then: b, left: steps}
	w.strike(FaultClockStrobe)
}

// Recover ends each fault in force and restarts each crashed process.
func (w *World[E, F]) Recover() {
	for _, p := range w.procs {
		p.paused = false
		p.strobe = strobe{}
		if p.state == crashed {
			w.start(p)
		}
	}
	w.sides = nil
	for _, d := range w.deps {
		d.armed = nil
		w.restore(d)
	}
}

// Faults returns the faults in force: the crash, the pause and the strobe of
// each process in the order of their spawns, the partition, and the outage,
// the slowness and the armed losses of each dependency in the order in which
// the world first saw it. Each fault is its kind and its target, such as
// pause:store, and the partition is its kind alone.
func (w *World[E, F]) Faults() []string {
	var out []string
	for _, p := range w.procs {
		if p.state == crashed {
			out = append(out, string(FaultCrash)+":"+p.name)
		}
		if p.paused {
			out = append(out, string(FaultPause)+":"+p.name)
		}
		if p.strobe.left > 0 {
			out = append(out, string(FaultClockStrobe)+":"+p.name)
		}
	}
	if w.sides != nil {
		out = append(out, string(FaultPartition))
	}
	for _, d := range w.deps {
		if d.outage {
			out = append(out, string(FaultOutage)+":"+d.name)
		}
		if d.slow {
			out = append(out, string(FaultSlow)+":"+d.name)
		}
		for _, kind := range d.armed {
			out = append(out, string(kind)+":"+d.name)
		}
	}
	return out
}

// strike passes the kind of a fault that strikes to the case.
func (w *World[E, F]) strike(kind Fault) { w.c.Classify(string(kind)) }

// dependency returns the state of the faults of the dependency named name.
// It fails the case for an empty name.
func (w *World[E, F]) dependency(name string) *dependency[E, F] {
	if name == "" {
		w.c.Fatalf("sim: a dependency has no name")
	}
	at := slices.IndexFunc(w.deps, func(d *dependency[E, F]) bool { return d.name == name })
	if at < 0 {
		w.deps = append(w.deps, &dependency[E, F]{name: name})
		return w.deps[len(w.deps)-1]
	}
	return w.deps[at]
}

// faulted performs the effect f of the process p under the faults of its
// dependency, and reports whether a fault of the dependency took the effect.
func (w *World[E, F]) faulted(p *Process[E, F], f F) bool {
	name, ok := w.a.Dependency(f)
	if !ok {
		return false
	}
	d := w.dependency(name)
	switch {
	case d.outage:
		w.lose(p, f, w.lost(f))
	case len(d.armed) > 0:
		kind := d.armed[0]
		d.armed = d.armed[1:]
		if kind == FaultLoseAfter {
			w.a.Perform(w, p, f)
		}
		w.lose(p, f, w.lost(f))
		w.strike(kind)
	case d.slow:
		s := &stall[E, F]{f: f, process: p}
		d.stalls = append(d.stalls, s)
		if timeout := w.a.Timeout(f); timeout > 0 {
			w.at(p, timeout, func() {
				if !s.done {
					s.done = true
					w.lose(p, f, context.DeadlineExceeded)
				}
			})
		}
	default:
		return false
	}
	return true
}

// lost returns the error of a loss of the effect f: an error that the case
// chooses among the errors of its dependency, and ErrLost for a dependency
// without errors.
func (w *World[E, F]) lost(f F) error {
	errs := w.a.Errors(f)
	if len(errs) == 0 {
		return ErrLost
	}
	return errs[w.c.Draw(prop.Integer(0, len(errs)-1), "error")]
}

// lose queues the event of the failure of the effect f of the process p
// with err, for an effect with a result.
func (w *World[E, F]) lose(p *Process[E, F], f F, err error) {
	if ev, ok := w.a.Fail(f, err); ok {
		p.queue = append(p.queue, queued[E]{ev: ev, epoch: p.epoch})
	}
}
