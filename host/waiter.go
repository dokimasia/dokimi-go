// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import (
	"context"
	"runtime/pprof"
	"sync"
	"time"

	"go.dokimi.dev/assert/stateful"
)

// waiter waits for a condition in the mode of the hosts that share it: as a
// task of their scheduler, or on a condition variable of the bubble. A wait on
// the condition variable is durable in a testing/synctest bubble.
type waiter struct {
	// s is the scheduler of the hosts, and nil for hosts without one.
	s *stateful.Scheduler

	// cond is the condition variable of the waits, which notify signals. gen
	// counts the signals, so a wait that checked its condition before a signal
	// checks it again.
	cond sync.Cond
	mu   sync.Mutex
	gen  uint64
}

// newWaiter returns a waiter of the hosts under the scheduler s, or without
// one for a nil s.
func newWaiter(s *stateful.Scheduler) *waiter {
	w := &waiter{s: s}
	w.cond.L = &w.mu
	return w
}

// wait returns once cond returns true. Under a scheduler it parks the running
// task through Scheduler.Wait, which calls cond between two turns. Without one
// it waits on the condition variable, and calls cond again after each signal
// of notify. It calls cond without a lock of the waiter, so cond may take the
// locks of the state that it reads.
func (w *waiter) wait(cond func() bool) {
	if w.s != nil {
		w.s.Wait(cond)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for {
		gen := w.gen
		w.mu.Unlock()
		done := cond()
		w.mu.Lock()
		if done {
			return
		}
		for w.gen == gen {
			w.cond.Wait()
		}
	}
}

// notify makes each wait on the condition variable check its condition again.
func (w *waiter) notify() {
	w.mu.Lock()
	//dokimi:mutate-skip uoi-incdec: a wait checks that the count changed, which a decrement changes as well
	w.gen++
	w.mu.Unlock()
	w.cond.Broadcast()
}

// sleep waits until the clock of the waiter has advanced by d, and reports
// true, or until ctx ends, and reports false. It reports true at once for a d
// of zero or less. The clock is the clock of the scheduler, or the clock of
// the bubble without one. Under a scheduler, a timer task of the waiter sleeps
// until the end of d in place of the running task, which waits.
func (w *waiter) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	if w.s == nil {
		t := time.NewTimer(d)
		select {
		case <-t.C:
			return true
		case <-ctx.Done():
			return false
		}
	}
	elapsed := false
	w.s.Spawn(labelledBy(ctx, func() {
		w.s.Clock().Sleep(d)
		elapsed = true
	}))
	w.s.Wait(func() bool { return elapsed || ctx.Err() != nil })
	return elapsed
}

// wake makes each wait check its condition at the instant at of the clock of
// the waiter, unless ctx ends first. A timer task of the waiter sleeps until at
// under a scheduler, and the end of its turn is a point at which the scheduler
// checks the condition of each waiting task. Without a scheduler a timer
// goroutine of the waiter sleeps until at.
func (w *waiter) wake(ctx context.Context, at time.Time) {
	d := at.Sub(w.now())
	if w.s != nil {
		w.s.Spawn(labelledBy(ctx, func() { w.s.Clock().Sleep(d) }))
		return
	}
	go labelledBy(ctx, func() {
		if w.sleep(ctx, d) {
			w.notify()
		}
	})()
}

// labelledBy returns f, which runs with the profiler labels of ctx in place of
// the labels of the goroutine that starts it. A timer of the waiter serves the
// network and the clocks of the hosts. The context of its wait carries no
// labels of an incarnation, because [Incarnation.Go] sets them on its
// goroutines alone. The timer can still run after the crash of the incarnation
// that starts it, and [Incarnation.Leaked] does not count it.
func labelledBy(ctx context.Context, f func()) func() {
	return func() {
		pprof.SetGoroutineLabels(ctx)
		f()
	}
}

// now returns the instant of the clock of the waiter: the clock of the
// scheduler, or the clock of the bubble without one.
func (w *waiter) now() time.Time {
	if w.s != nil {
		return w.s.Clock().Now()
	}
	return time.Now()
}

// spawn runs f on a goroutine, or as a task of the scheduler.
func (w *waiter) spawn(f func()) {
	if w.s != nil {
		w.s.Spawn(f)
		return
	}
	go f()
}
