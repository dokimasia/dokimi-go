// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/medium"
)

// The bounds of the rate error of the monotonic clock of a host, per mille,
// which are the bounds of the drift of the clock of a process of sim: a clock
// runs between half and twice as fast as the clock of the run.
const (
	minDrift = -500
	maxDrift = 1000
)

// perMille is the rate of a clock without a rate error.
const perMille = 1000

// hosts counts the hosts of the process, so the label of the goroutines of a
// host differs from the labels of every other host of the process.
var hosts atomic.Uint64

// Host is a simulated machine. It keeps its disk, its clocks and its address
// across the incarnations of its process. Only the world has it: the code of
// a component receives the [Env] of an incarnation.
//
// # Concurrency
//
// Its methods are safe for concurrent use.
type Host struct {
	// start is the instant of the clock of the run at which the host started.
	start time.Time

	c        *prop.Case
	n        *Network
	w        *waiter
	injector *Injector

	// disk is the disk that the next incarnation opens, and current the
	// incarnation that runs, nil while none runs.
	disk    *medium.Memory
	current *Incarnation

	name string

	// id is the label of the goroutines of the host, unique in the process.
	id string

	// wall contains the faults of the wall clock.
	wall wall

	mu sync.Mutex

	// rate is the rate of the monotonic clock per mille of the clock of the
	// run, and incarnations counts the incarnations that started.
	rate         int64
	incarnations int
}

// New returns the host named name on the network n, whose monotonic clock runs
// at a rate error that the case c chooses, as the drift of the clock of a
// process of sim: from half to twice the rate of the clock of the run, which
// shrinks to the rate of the run. With the scheduler s, the host runs its
// goroutines, its sleeps and its timers as tasks of s. With a nil s, it runs
// them on the goroutines of the bubble. A nil n gives the host no network.
// New fails the case for a host whose scheduler differs from the scheduler of
// the other hosts of n, because the hosts of a network wait for one another.
func New(c *prop.Case, name string, n *Network, s *stateful.Scheduler) *Host {
	h := &Host{
		c: c, n: n, name: name, disk: medium.NewMemory(), id: strconv.FormatUint(hosts.Add(1), 10),
		rate: perMille + int64(c.Draw(prop.Integer(minDrift, maxDrift), "drift."+name)),
	}
	h.w = newWaiter(s)
	if n != nil {
		h.w = n.join(h, s)
	}
	h.injector = &Injector{w: h.w}
	h.start = h.w.now()
	return h
}

// Name returns the name of the host.
func (h *Host) Name() string {
	return h.name
}

// Disk returns the disk of the host, which the next incarnation of its process
// opens. A crash by DiskReplacement gives the host a new empty disk.
func (h *Host) Disk() *medium.Memory {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.disk
}

// Injector returns the injector of the dependencies of the host, which fails,
// blocks and releases the calls of the dependencies of each incarnation.
func (h *Host) Injector() *Injector {
	return h.injector
}

// Start starts a new incarnation of the process of the host over its disk,
// and returns it with the environment that its code receives. The
// incarnations are numbered from 1. Start fails the case while an
// incarnation runs, because one host runs one process.
func (h *Host) Start() (*Incarnation, *Env) {
	h.mu.Lock()
	running := h.current
	//dokimi:mutate-skip ror-true: Start fails the case after the lock, so the incarnation that it starts beside a running one serves no call
	if running == nil {
		h.incarnations++
		h.current = newIncarnation(h, h.incarnations, h.disk)
	}
	i := h.current
	h.mu.Unlock()
	if running != nil {
		h.c.Fatalf("host: Start of %s while its incarnation %d runs", h.name, running.number)
	}
	return i, i.env
}

// Broadcast wakes each wait of an incarnation of the host on [Env.Wait], which
// calls its condition again. A world calls it on each host after each of its
// steps. Under the scheduler it does nothing, because the scheduler checks its
// parked tasks at the start of each Run and each Advance.
func (h *Host) Broadcast() {
	h.w.notify()
}

// crashed marks the end of the incarnation that runs, so the host can start
// another, and gives the host a new empty disk for k DiskReplacement.
func (h *Host) crashed(k Kill) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if k == DiskReplacement {
		h.disk = medium.NewMemory()
	}
	h.current = nil
}
