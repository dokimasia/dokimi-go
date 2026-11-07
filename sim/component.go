// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"context"
	"errors"
	"slices"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
)

// The bounds of the components of a world: the time of the clock of a host
// within which the open of an incarnation returns, and the longest advance of
// a world without a timer of a machine, in microseconds.
const (
	componentBound = time.Second
	longestAdvance = 10_000_000
)

// None is the type of the events and the effects of a world without machine
// processes. No type implements it.
type None interface{ none() }

// Component is a process of a world that runs a Go value of the type T on a
// host. Each incarnation of the process is the value that open returns over a
// new environment of the host.
type Component[T any] struct {
	c    *component
	name string
}

// component is the host of a component process, the incarnation of the
// process that runs with its environment and its value, the open of each
// incarnation, and the *Component[T] that SpawnComponent returned for it.
type component struct {
	c      *prop.Case
	host   *host.Host
	inc    *host.Incarnation
	env    *host.Env
	value  any
	open   func(env *host.Env) any
	handle any
}

// invocation is an open call of a client on a component: its call of the
// history, the end of its context, its client and its process.
type invocation struct {
	call    history.Call
	cancel  context.CancelFunc
	process string
	client  int
}

// unknown are the errors with which a call of a client on a component may
// have taken effect or not.
var unknown = []error{context.Canceled, context.DeadlineExceeded, medium.ErrCrashed, host.ErrReset}

// SpawnComponent adds a component process named name to w on a new host of
// the same name, and starts its first incarnation. The hosts of the
// components of w share one network, which the case builds from the options
// of the simulation. The open of an incarnation runs on a goroutine of the
// incarnation, and the world waits for it as the world of a subject waits for
// its open. SpawnComponent fails the case where w has a host or a process
// named name.
func SpawnComponent[T, E, F any](w *World[E, F], name string, open func(env *host.Env) T) *Component[T] {
	w.vacant(name)
	if w.network == nil {
		w.network = host.NewNetwork(w.c, w.blocking(), w.netOpts...)
	}
	c := &component{
		c: w.c, host: host.New(w.c, name, w.network, w.sched),
		open: func(env *host.Env) any { return open(env) },
	}
	handle := &Component[T]{c: c, name: name}
	c.handle = handle
	p := w.host(name, newClock(0)).add(name, nil, nil)
	p.comp = c
	w.startComponent(p)
	return handle
}

// ComponentOf returns the component process named name of w, whose values
// have the type T, as SpawnComponent returned it. A function of a
// simulation, such as an action or an invariant, gets the components of its
// world from it. ComponentOf fails the case of w for a name that no process
// has, a process that is no component and a component whose values have
// another type.
func ComponentOf[T, E, F any](w *World[E, F], name string) *Component[T] {
	p := w.named(name)
	if p.comp == nil {
		w.c.Fatalf("sim: the process %q is no component", name)
	}
	c, typed := p.comp.handle.(*Component[T])
	if !typed {
		w.c.Fatalf("sim: the component %q is a %T, not a %T", name, p.comp.handle, c)
	}
	return c
}

// Running reports whether an incarnation of the process runs, so that Value
// returns its value. It reports false while the process has crashed and after
// the stop of the world, such as for the check of an invariant that follows
// the stop.
func (c *Component[T]) Running() bool {
	return c.c.inc != nil
}

// Value returns the value of the incarnation that runs. It fails the case of
// the world while the process has crashed or stopped.
func (c *Component[T]) Value() T {
	if c.c.inc == nil {
		c.c.c.Fatalf("sim: the component %q does not run", c.name)
	}
	v, _ := c.c.value.(T)
	return v
}

// Invoke records the invocation of operation by client in the history of the
// case with args and keys, and runs call on a goroutine of the incarnation of
// the process. It completes the call as OK with its output for a nil error,
// as unknown for an error that wraps context.Canceled,
// context.DeadlineExceeded, medium.ErrCrashed or host.ErrReset, and as failed
// for any other error. A call of a crashed process ends as unknown with
// medium.ErrCrashed, and a call of a stopped process as unknown with
// ErrRunEnded. The context of call derives from the lifetime of the
// incarnation. Invoke fails the case for a name that no component has.
func (w *World[E, F]) Invoke(
	client int, process, operation string, args, keys []any, call func(ctx context.Context) (any, error),
) {
	p := w.named(process)
	if p.comp == nil {
		w.c.Fatalf("sim: the process %q is no component", process)
	}
	invoked := w.c.History().Invoke(client, operation, args, keys...)
	switch p.state {
	case crashed:
		invoked.Unknown(medium.ErrCrashed)
		return
	case stopped:
		invoked.Unknown(ErrRunEnded)
		return
	case running:
	}
	ctx, cancel := context.WithCancel(p.comp.env.Lifetime())
	in := &invocation{call: invoked, cancel: cancel, process: process, client: client}
	w.mu.Lock()
	w.invoked = append(w.invoked, in)
	w.mu.Unlock()
	p.comp.inc.Go(func() {
		out, err := call(ctx)
		w.mu.Lock()
		defer w.mu.Unlock()
		if !w.end(in) {
			return
		}
		switch {
		case err == nil:
			in.call.OK(out)
		case slices.ContainsFunc(unknown, func(cause error) bool { return errors.Is(err, cause) }):
			in.call.Unknown(err)
		default:
			in.call.Fail(err)
		}
	})
}

// end ends the open call in, and reports false for a call that has ended. The
// caller holds mu.
func (w *World[E, F]) end(in *invocation) bool {
	at := slices.Index(w.invoked, in)
	if at < 0 {
		return false
	}
	w.invoked = slices.Delete(w.invoked, at, at+1)
	in.cancel()
	return true
}

// invokers returns the clients with an open call of a component, in the order
// of their numbers.
func (w *World[E, F]) invokers() []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]int, 0, len(w.invoked))
	for _, in := range w.invoked {
		out = append(out, in.client)
	}
	slices.Sort(out)
	return out
}

// endInvocations completes each open call that matches as unknown with err.
func (w *World[E, F]) endInvocations(match func(in *invocation) bool, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, in := range slices.Clone(w.invoked) {
		if match(in) {
			w.end(in)
			in.call.Unknown(err)
		}
	}
}

// startComponent starts a new incarnation of the component process p in a
// new epoch, and opens its value on a goroutine of the incarnation. It fails
// the case for an open that does not return within componentBound of the
// clock of its host.
func (w *World[E, F]) startComponent(p *Process[E, F]) {
	c := p.comp
	c.inc, c.env = c.host.Start()
	p.epoch++
	p.state = running
	env := c.env
	returned := make(chan struct{})
	var value any
	c.inc.Go(func() {
		value = c.open(env)
		close(returned)
	})
	w.quiesce()
	if !closed(returned) {
		w.Advance(componentBound)
	}
	if !closed(returned) {
		w.c.Fatalf("sim: the open of the incarnation %d of %s did not return within %v of its host",
			env.Incarnation(), p.name, componentBound)
	}
	c.value = value
}

// endComponent ends the incarnation of the component process p by the kind
// k, waits for quiescence, and fails the case for a goroutine of the
// incarnation that is alive after quiescence, because it ignored its
// lifetime.
func (w *World[E, F]) endComponent(p *Process[E, F], k host.Kill) {
	c := p.comp
	inc, number := c.inc, c.env.Incarnation()
	inc.Crash(k)
	c.inc, c.env, c.value = nil, nil, nil
	w.quiesce()
	if leaked := inc.Leaked(); len(leaked) > 0 {
		w.c.Fatalf("sim: a goroutine of the incarnation %d of %s is alive after its crash, so it ignored its "+
			"lifetime:\n\n%s", number, p.name, leaked[0])
	}
}

// shutdown crashes the incarnation of each component process that runs by a
// process crash, so each goroutine of the incarnation sees the end of its
// lifetime. A run calls it at the end of each case.
func (w *World[E, F]) shutdown() {
	for _, p := range w.procs {
		if p.comp != nil && p.state == running {
			p.comp.inc.Crash(host.ProcessCrash)
		}
	}
}

// crashComponent crashes the component process p by the kind k. Its open
// calls end as unknown with medium.ErrCrashed.
func (w *World[E, F]) crashComponent(p *Process[E, F], k host.Kill) {
	w.endInvocations(func(in *invocation) bool { return in.process == p.name }, medium.ErrCrashed)
	w.endComponent(p, k)
}

// blocking reports whether the world strikes the blocking faults on its
// components: in Tasks mode, and with the option Blocking.
func (w *World[E, F]) blocking() bool {
	return w.sched != nil || w.blockingOpt
}

// settle ends a step of a world with a component: it wakes each wait of the
// components on Env.Wait, records the step for the watchdog of
// conform.Bubble, and waits for quiescence.
func (w *World[E, F]) settle() {
	if w.network == nil {
		return
	}
	for _, p := range w.procs {
		if p.comp != nil {
			p.comp.host.Broadcast()
		}
	}
	//dokimi:mutate-skip sbr-delete: the count of the steps decides the watchdog alone, for two samples a second apart with the same stacks and a wait for a mutex, which no deterministic case makes while its world steps
	conform.RecordStep()
	w.quiesce()
}

// quiesce waits until no goroutine or task of a component can run: the return
// of Scheduler.Run in Tasks mode, and of synctest.Wait in native mode. The
// world has a component.
func (w *World[E, F]) quiesce() {
	if w.sched != nil {
		w.sched.Run()
		return
	}
	synctest.Wait()
}

// advanceHosts advances the clock of the bubble, or of the scheduler, by d,
// so the monotonic clock of each host advances by d times its rate error, and
// waits for quiescence.
func (w *World[E, F]) advanceHosts(d time.Duration) {
	switch {
	case w.network == nil:
	case w.sched != nil:
		w.sched.Advance(d)
	default:
		time.Sleep(d)
		synctest.Wait()
	}
}

// disks returns the disks of the process p: each medium.Memory among the
// dependencies of a machine, and the disk of the host of a component.
func disks[E, F any](p *Process[E, F]) []*medium.Memory {
	if p.comp != nil {
		return []*medium.Memory{p.comp.host.Disk()}
	}
	return media(p)
}

// closed reports whether ch is closed.
func closed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
