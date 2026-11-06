// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import (
	"context"
	"math/rand/v2"
	"net"
	"runtime"
	"sync"

	"go.dokimi.dev/medium"
)

// Env is the environment of one incarnation: what the code of a component
// takes from its machine. Each method returns the same resource for the whole
// incarnation.
//
// # Concurrency
//
// Its methods are safe for concurrent use.
type Env struct {
	i     *Incarnation
	clock *Clock
	disk  *view

	// deps contains the dependency of each name.
	deps map[string]*Dependency
	mu   sync.Mutex
}

// Name returns the name of the host.
func (e *Env) Name() string {
	return e.i.h.name
}

// Incarnation returns the number of the incarnation, from 1.
func (e *Env) Incarnation() int {
	return e.i.number
}

// Lifetime returns the context of the incarnation, which a crash and the end
// of the case end.
func (e *Env) Lifetime() context.Context {
	return e.i.ctx
}

// Disk returns the view of the incarnation on the disk of the host. Each call
// of the view and of its files waits while the incarnation is paused, and
// fails with an error that wraps medium.ErrCrashed once it has crashed.
func (e *Env) Disk() medium.FS {
	return e.disk
}

// Clock returns the clock of the host, as the incarnation reads it.
func (e *Env) Clock() *Clock {
	return e.clock
}

// Rand returns a source whose every value is a choice of the case.
func (e *Env) Rand() rand.Source {
	return e.i.h.c.Rand()
}

// Dependency returns the dependency name of the incarnation, which the
// injector of the host fails, blocks and releases. A pause of the incarnation
// blocks its calls, and once the incarnation crashes each call fails with an
// error that wraps medium.ErrCrashed.
func (e *Env) Dependency(name string) *Dependency {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, found := e.deps[name]
	if !found {
		d = e.i.h.injector.dependency(e.i.gate)
		e.deps[name] = d
	}
	return d
}

// Go runs f on a goroutine of the incarnation, or as a task under the
// scheduler of the host, as [Incarnation.Go] does.
func (e *Env) Go(f func()) {
	e.i.Go(f)
}

// Yield is a scheduling point under the scheduler of the host, and calls
// runtime.Gosched without one. It wakes each wait of [Env.Wait].
func (e *Env) Yield() {
	if s := e.i.h.w.s; s != nil {
		s.Yield()
	} else {
		//dokimi:mutate-skip sbr-delete: Gosched lets another goroutine run sooner, which no wait observes
		runtime.Gosched()
	}
	e.i.h.w.notify()
}

// Wait blocks until cond returns true. Under the scheduler it parks the task
// through Scheduler.Wait. Without one it waits on a condition variable that
// each Yield, each return of a function that [Incarnation.Go] runs and each
// [Host.Broadcast] signal, and it calls cond again after each signal. The wait is durable in a testing/synctest bubble.
func (e *Env) Wait(cond func() bool) {
	e.i.h.w.wait(cond)
}

// Listen returns a listener of streams on the port of the host. It fails with
// an error that wraps ErrNoNetwork for a host without a network.
func (e *Env) Listen(port int) (net.Listener, error) {
	return e.i.h.n.listen(e.i, port)
}

// Dial connects to the listener at addr, the name of a host and a port, such
// as node-2:7000, as net.Dialer.DialContext does. It fails with an error that
// wraps ErrNoNetwork for a host without a network.
func (e *Env) Dial(ctx context.Context, addr string) (net.Conn, error) {
	return e.i.h.n.dial(ctx, e.i, addr)
}

// ListenPacket returns a connection of datagrams on the port of the host. It
// fails with an error that wraps ErrNoNetwork for a host without a network.
func (e *Env) ListenPacket(port int) (net.PacketConn, error) {
	return e.i.h.n.listenPacket(e.i, port)
}
