// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import (
	"context"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"

	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/medium"
)

// The labels of the goroutines of an incarnation, which the goroutine profile
// of the process states with each stack.
const (
	// labelHost is the name of the host.
	labelHost = "dokimi.host"
	// labelIncarnation is the number of the incarnation.
	labelIncarnation = "dokimi.incarnation"
	// labelRun tells the hosts of one name in the tests of one process apart.
	labelRun = "dokimi.run"
)

// Incarnation is one run of the process of a host, from its start to its
// crash. Only the world has it.
//
// # Concurrency
//
// Its methods are safe for concurrent use.
type Incarnation struct {
	h    *Host
	gate *gate
	env  *Env

	// disk is the disk of the host at the start of the incarnation.
	disk *medium.Memory

	// ctx is the lifetime of the incarnation, which Env.Lifetime returns for
	// the whole incarnation, and cancel ends it.
	ctx    context.Context //nolint:containedctx // Env.Lifetime returns this one context for the whole incarnation
	cancel context.CancelFunc

	// labels are the profiler labels of the goroutines of the incarnation.
	labels pprof.LabelSet

	number int
}

// newIncarnation returns the incarnation number of the host h over the disk.
// Its lifetime contains the waiter of h, which [Park] reads.
func newIncarnation(h *Host, number int, disk *medium.Memory) *Incarnation {
	ctx, cancel := context.WithCancel(context.WithValue(h.c.Context(), waiterKey{}, h.w))
	i := &Incarnation{
		h: h, gate: &gate{w: h.w, disk: disk, done: make(chan struct{})}, disk: disk, ctx: ctx, cancel: cancel,
		number: number,
		labels: pprof.Labels(labelHost, h.name, labelIncarnation, strconv.Itoa(number), labelRun, h.id),
	}
	i.env = &Env{i: i, clock: &Clock{h: h, i: i}, disk: &view{fs: disk, gate: i.gate}, deps: map[string]*Dependency{}}
	return i
}

// Go runs f on a goroutine of the incarnation, or as a task under the
// scheduler of the host. The goroutine and every goroutine that it starts
// have the labels of the incarnation. The return of f wakes each wait of
// [Env.Wait] without a scheduler.
func (i *Incarnation) Go(f func()) {
	i.h.w.spawn(func() {
		defer i.h.w.notify()
		pprof.Do(context.Background(), i.labels, func(context.Context) { f() })
	})
}

// Crash ends the incarnation by the kind k. It fences every resource of the
// incarnation: each later call on its view of the disk, on a dependency of
// [Env.Dependency], on a connection and on a listener fails with an error that
// wraps medium.ErrCrashed. It then ends the lifetime of the incarnation and
// applies k to the disk:
//
//   - ProcessCrash calls medium.Memory.Kill, which keeps every write.
//   - PowerLoss calls medium.Memory.CrashKeeping, with a count of the pending
//     calls of each file and of each directory that the case chooses and
//     that shrinks to 0, the crash state of a crash without a torn write.
//   - DiskReplacement gives the host a new empty disk.
//
// Crash then resets each connection of the incarnation and closes each of its
// listeners. Each call that a hold of the disk or of a dependency blocks fails
// with an error that wraps medium.ErrCrashed.
//
// Crash fails the case for a kind outside the three, and for an incarnation
// that crashed already.
func (i *Incarnation) Crash(k Kill) {
	h := i.h
	if k < ProcessCrash || k > DiskReplacement {
		h.c.Fatalf("host: Crash of %s by %v, which is no kind of crash", h.name, k)
	}
	if !i.gate.end() {
		h.c.Fatalf("host: a second Crash of the incarnation %d of %s", i.number, h.name)
	}
	i.cancel()
	switch k {
	case ProcessCrash, DiskReplacement:
		i.disk.Kill()
	case PowerLoss:
		i.disk.CrashKeeping(func(name string, pending int) int {
			return h.c.Draw(prop.Integer(0, pending), "keep."+name)
		})
	}
	h.crashed(k)
	if h.n != nil {
		h.n.fence(i)
	}
	h.w.notify()
}

// Pause blocks every call of the environment of the incarnation, and defers
// its timers, until Resume. The clocks of the host keep running.
func (i *Incarnation) Pause() {
	i.gate.pause(true)
}

// Resume ends a pause.
func (i *Incarnation) Resume() {
	i.gate.pause(false)
	i.h.w.notify()
}

// Leaked returns the stacks of the goroutines of the incarnation that are
// alive, as the goroutine profile of the process states them, with the count
// of the goroutines of each stack. After the crash and the quiescence of the
// world, each of them ignored the end of its lifetime.
func (i *Incarnation) Leaked() []string {
	var profile strings.Builder
	// A write to a strings.Builder does not fail.
	_ = pprof.Lookup("goroutine").WriteTo(&profile, 1)
	run := strconv.Quote(labelRun) + ":" + strconv.Quote(i.h.id)
	number := strconv.Quote(labelIncarnation) + ":" + strconv.Quote(strconv.Itoa(i.number))
	var out []string
	// The text of a label appears in the line of the labels of a stack alone.
	for block := range strings.SplitSeq(profile.String(), "\n\n") {
		for line := range strings.Lines(block) {
			if strings.Contains(line, run) && strings.Contains(line, number) {
				out = append(out, block)
				break
			}
		}
	}
	return out
}

// gate admits the calls of the environment of an incarnation. It blocks them
// while the incarnation is paused, and refuses them once it has crashed. A nil
// gate admits every call.
type gate struct {
	w *waiter

	// disk is the disk of the incarnation, whose hold blocks the calls of its
	// view.
	disk *medium.Memory

	// done is closed once the incarnation has crashed.
	done chan struct{}

	mu     sync.Mutex
	paused bool
}

// enter waits while the incarnation is paused, and reports false once it has
// crashed.
func (g *gate) enter() bool {
	if g == nil {
		return true
	}
	g.w.wait(func() bool { return !g.blocks() || closed(g.done) })
	return !closed(g.done)
}

// enterDisk passes the gate as enter does, for a call of the view on the
// disk. Under a scheduler it also parks the task of the call while the disk
// is held, because a hold blocks a call on a channel that the scheduler does
// not see. Without one, the hold of the disk blocks the call durably. The
// view of an incarnation always has the gate of the incarnation.
func (g *gate) enterDisk() bool {
	if g.w.s == nil {
		return g.enter()
	}
	g.w.wait(func() bool { return !g.blocks() && !g.disk.Held() || closed(g.done) })
	return !closed(g.done)
}

// blocks reports whether the gate blocks the calls of the incarnation, which
// it does while the incarnation is paused.
func (g *gate) blocks() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused
}

// pause pauses the incarnation, or ends its pause.
func (g *gate) pause(paused bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paused = paused
}

// end marks the crash of the incarnation, and reports false for an
// incarnation that crashed already.
func (g *gate) end() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if closed(g.done) {
		return false
	}
	close(g.done)
	return true
}

// ended reports whether the incarnation of a gate has crashed, and false for
// a nil gate.
func (g *gate) ended() bool {
	return g != nil && closed(g.done)
}
