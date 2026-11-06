// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import (
	"context"
	"sync"
	"time"
)

// Clock is the clock of a host, as an incarnation reads it: a wall clock that
// the faults of the clock move, and a monotonic clock that they do not move.
// The monotonic clock advances at the rate of the clock of the run times one
// plus the rate error of the host. The clock of the run is the clock of the
// bubble, or the clock of the scheduler of the host.
//
// # Concurrency
//
// Its methods are safe for concurrent use.
type Clock struct {
	h *Host
	i *Incarnation
}

// Wall returns the wall time of the host in UTC, without a monotonic reading,
// so a comparison or a difference of two values is a computation of wall
// time. The wall time is the start of the host, plus its monotonic time, plus
// the offset of its bumps and its strobe.
func (c *Clock) Wall() time.Time {
	h := c.h
	mono := h.monotonic()
	h.mu.Lock()
	h.wall.read = true
	offset := h.wall.offset(mono)
	h.mu.Unlock()
	return h.start.Add(mono + offset).Round(0).UTC()
}

// Monotonic returns the time since the start of the host on its monotonic
// clock.
func (c *Clock) Monotonic() time.Duration {
	return c.h.monotonic()
}

// Sleep blocks until the monotonic clock has advanced by d, and returns at
// once for a d of zero or less. It returns early once the lifetime of the
// incarnation ends, so a goroutine of a crashed incarnation that sleeps sees
// the end of its lifetime. A sleep that ends while the incarnation is paused
// returns at Resume.
func (c *Clock) Sleep(d time.Duration) {
	if c.h.w.sleep(c.i.ctx, c.h.runTime(d)) {
		c.i.gate.enter()
	}
}

// AfterFunc calls f on a goroutine of the incarnation, or as a task under the
// scheduler, once the monotonic clock has advanced by d. A pause of the
// incarnation defers the call until Resume, and the end of the lifetime of the
// incarnation cancels it. stop cancels the call, and reports whether it did so
// before f started.
func (c *Clock) AfterFunc(d time.Duration, f func()) (stop func() bool) {
	ctx, cancel := context.WithCancel(c.i.ctx)
	var mu sync.Mutex
	pending := true
	// take ends the pending call, and reports whether it was pending.
	take := func() bool {
		mu.Lock()
		defer mu.Unlock()
		was := pending
		pending = false
		return was
	}
	c.i.Go(func() {
		//dokimi:mutate-skip sbr-delete: the cancel releases the context of the call before the lifetime ends, which only the memory of a long incarnation observes
		defer cancel()
		if c.h.w.sleep(ctx, c.h.runTime(d)) && c.i.gate.enter() && take() {
			f()
		}
	})
	return func() bool {
		if !take() {
			return false
		}
		cancel()
		return true
	}
}

// Bump moves the wall clock of the host by d.
func (h *Host) Bump(d time.Duration) {
	h.mu.Lock()
	h.wall.bumps += d
	h.mu.Unlock()
	h.w.notify()
}

// Strobe moves the wall clock of the host back and forth by delta every period
// of its monotonic clock, until ResetClock. The wall clock is ahead by delta
// in the first period from the call, and in every second period after it. It
// fails the case for a period of zero or less.
func (h *Host) Strobe(delta, period time.Duration) {
	if period <= 0 {
		h.c.Fatalf("host: Strobe(%v, %v) of %s states no period", delta, period, h.name)
	}
	mono := h.monotonic()
	h.mu.Lock()
	h.wall.delta, h.wall.period, h.wall.since = delta, period, mono
	h.mu.Unlock()
	h.w.notify()
}

// ResetClock ends each bump and each strobe of the wall clock of the host.
func (h *Host) ResetClock() {
	h.mu.Lock()
	h.wall = wall{read: h.wall.read}
	h.mu.Unlock()
	h.w.notify()
}

// WallRead reports whether an incarnation of the host has read its wall clock.
func (h *Host) WallRead() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.wall.read
}

// monotonic returns the time since the start of the host on its monotonic
// clock: the time of the clock of the run since the start, at the rate of the
// host.
func (h *Host) monotonic() time.Duration {
	return scaled(h.w.now().Sub(h.start), h.rate)
}

// runTime returns the shortest time of the clock of the run after which the
// monotonic clock has advanced by at least d from now, and 0 or less for a d
// of zero or less.
func (h *Host) runTime(d time.Duration) time.Duration {
	elapsed := h.w.now().Sub(h.start)
	target := int64(scaled(elapsed, h.rate) + d)
	run := time.Duration(target/h.rate*perMille + (target%h.rate*perMille+h.rate-1)/h.rate)
	return max(run-elapsed, 0)
}

// scaled returns the time d of the clock of the run at the rate per mille.
func scaled(d time.Duration, rate int64) time.Duration {
	n := int64(d)
	return time.Duration(n/perMille*rate + n%perMille*rate/perMille)
}

// wall contains the faults of the wall clock of a host, and whether an
// incarnation has read it.
type wall struct {
	// bumps is the sum of the bumps.
	bumps time.Duration

	// delta and period are the offset and the period of the strobe, and
	// period is 0 without one. since is the monotonic time at its start.
	delta, period, since time.Duration

	read bool
}

// offset returns the offset of the wall clock at the monotonic time mono.
func (w wall) offset(mono time.Duration) time.Duration {
	offset := w.bumps
	if w.period > 0 && (mono-w.since)/w.period%2 == 0 {
		offset += w.delta
	}
	return offset
}
