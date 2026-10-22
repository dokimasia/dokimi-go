// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package fault

import (
	"math/rand/v2"
	"sync"
	"time"
)

// Fault decides for each call of a double whether the call fails. C is the
// type of the call that the double records.
type Fault[C any] interface {
	// ShouldTrigger reports whether the fault fails call, and returns the error
	// of the fault for a call that fails. clock is the clock of the double, and
	// nil reads the system clock.
	ShouldTrigger(call C, clock Clock) (bool, error)
	// Reset rewinds the counters of the fault and keeps its configuration.
	Reset()
}

// Clock is where a fault reads the current instant, such as the clock of a
// double that a test advances.
type Clock interface {
	// Now returns the current instant.
	Now() time.Time
}

// ClockNow returns the instant of clock, and the instant of the system clock
// for a nil clock.
func ClockNow(clock Clock) time.Time {
	if clock == nil {
		return time.Now()
	}
	return clock.Now()
}

// stateless is the Reset of a strategy without a counter, whose decision
// comes from its configuration alone.
type stateless struct{}

// Reset keeps the configuration, because the strategy has no counter.
func (stateless) Reset() {}

// CountedFault fails every nth call, whatever the call and the clock.
type CountedFault[C any] struct {
	err   error
	n     int
	mu    sync.Mutex
	count int
}

// NewCountedFault returns a fault that fails every nth call. A fault with an n
// below 1 fails no call.
func NewCountedFault[C any](err error, n int) *CountedFault[C] {
	return &CountedFault[C]{err: err, n: n}
}

// ShouldTrigger counts the call and fails the nth call. It then starts the
// count again.
func (f *CountedFault[C]) ShouldTrigger(C, Clock) (bool, error) {
	if f.n < 1 {
		return false, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	if f.count < f.n {
		return false, nil
	}
	f.count = 0
	return true, f.err
}

// Reset sets the count of calls to zero.
func (f *CountedFault[C]) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count = 0
}

// RetryFault fails calls 1 to n − 1 and no call from call n on. It models a
// dependency that recovers on the nth attempt.
type RetryFault[C any] struct {
	err   error
	n     int
	mu    sync.Mutex
	count int
}

// NewRetryFault returns a fault that fails each call before the nth call. The
// nth call and every call after it succeed.
func NewRetryFault[C any](err error, n int) *RetryFault[C] {
	return &RetryFault[C]{err: err, n: n}
}

// ShouldTrigger counts the call and fails the calls before the nth.
func (f *RetryFault[C]) ShouldTrigger(C, Clock) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	if f.count >= f.n {
		return false, nil
	}
	return true, f.err
}

// Reset sets the count of calls to zero, so the next call is the first
// attempt again.
func (f *RetryFault[C]) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count = 0
}

// ProbabilityFault fails each call with a probability.
type ProbabilityFault[C any] struct {
	stateless
	err error
	r   *rand.Rand
	p   float64
	mu  sync.Mutex
}

// NewProbabilityFault returns a fault that fails each call with the
// probability p. It takes its random numbers from src, such as a seeded source
// of the test, so a seeded run repeats its faults. A nil src takes them from
// the global source of math/rand/v2. A fault with p of 0 fails no call, and a
// fault with p of 1 fails every call.
func NewProbabilityFault[C any](err error, p float64, src rand.Source) *ProbabilityFault[C] {
	f := &ProbabilityFault[C]{err: err, p: p}
	if src != nil {
		f.r = rand.New(src)
	}
	return f
}

// ShouldTrigger takes a random number in [0, 1) and fails the call when the
// number is below the probability.
func (f *ProbabilityFault[C]) ShouldTrigger(C, Clock) (bool, error) {
	if f.draw() >= f.p {
		return false, nil
	}
	return true, f.err
}

// draw returns the next random number in [0, 1) of the source of the fault.
func (f *ProbabilityFault[C]) draw() float64 {
	if f.r == nil {
		return rand.Float64()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.r.Float64()
}

// WindowedFault fails each call while the clock of the double is before a
// deadline.
type WindowedFault[C any] struct {
	stateless
	err      error
	deadline time.Time
}

// NewWindowedFault returns a fault that fails each call while the clock of the
// double is before deadline.
func NewWindowedFault[C any](err error, deadline time.Time) *WindowedFault[C] {
	return &WindowedFault[C]{err: err, deadline: deadline}
}

// ShouldTrigger fails the call while the instant of clock is before the
// deadline.
func (f *WindowedFault[C]) ShouldTrigger(_ C, clock Clock) (bool, error) {
	if !ClockNow(clock).Before(f.deadline) {
		return false, nil
	}
	return true, f.err
}

// PredicateFault fails each call for which its function returns true.
type PredicateFault[C any] struct {
	stateless
	err  error
	pred func(C) bool
}

// NewPredicateFault returns a fault that fails each call for which pred
// returns true.
func NewPredicateFault[C any](err error, pred func(C) bool) *PredicateFault[C] {
	return &PredicateFault[C]{err: err, pred: pred}
}

// ShouldTrigger fails the call when the function of the fault returns true for
// call.
func (f *PredicateFault[C]) ShouldTrigger(call C, _ Clock) (bool, error) {
	if !f.pred(call) {
		return false, nil
	}
	return true, f.err
}

// AndFault fails a call that each of its faults fails.
type AndFault[C any] struct {
	inner []Fault[C]
}

// And returns a fault that fails a call that each of faults fails, with the
// error of the first. It asks the faults in order and stops at the first that
// does not fail the call. So a counter after a predicate counts only the calls
// that the predicate passes. And of no faults fails no call.
func And[C any](faults ...Fault[C]) *AndFault[C] {
	return &AndFault[C]{inner: faults}
}

// ShouldTrigger asks the faults in order until one does not fail the call.
func (f *AndFault[C]) ShouldTrigger(call C, clock Clock) (bool, error) {
	var first error
	for i, inner := range f.inner {
		triggered, err := inner.ShouldTrigger(call, clock)
		if !triggered {
			return false, nil
		}
		if i == 0 {
			first = err
		}
	}
	return len(f.inner) > 0, first
}

// Reset resets each of the faults.
func (f *AndFault[C]) Reset() {
	for _, inner := range f.inner {
		inner.Reset()
	}
}

// OrFault fails a call that one of its faults fails.
type OrFault[C any] struct {
	inner []Fault[C]
}

// Or returns a fault that fails a call that one of faults fails, with the
// error of the first fault that fails it. It asks the faults in order and
// stops at that fault. Or of no faults fails no call.
func Or[C any](faults ...Fault[C]) *OrFault[C] {
	return &OrFault[C]{inner: faults}
}

// ShouldTrigger asks the faults in order until one fails the call.
func (f *OrFault[C]) ShouldTrigger(call C, clock Clock) (bool, error) {
	for _, inner := range f.inner {
		if triggered, err := inner.ShouldTrigger(call, clock); triggered {
			//nolint:wrapcheck // the code under test matches the armed error with errors.Is as the test armed it
			return true, err
		}
	}
	return false, nil
}

// Reset resets each of the faults.
func (f *OrFault[C]) Reset() {
	for _, inner := range f.inner {
		inner.Reset()
	}
}
