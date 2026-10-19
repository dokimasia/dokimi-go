// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

import (
	"fmt"
	"sync"
	"time"

	"go.dokimi.dev/assert"
)

// RecordedCall is one recorded value with the instant of its recording.
// [Recorder.Timestamped] returns them.
type RecordedCall[T any] struct {
	Value T
	At    time.Time
}

// Recorder is a log of the values of type T that a test records, such as the
// calls of a method of a double. It is safe for concurrent use.
//
//	rec := stub.NewRecorder[PutCall]()
//	rec.Record(PutCall{Key: "a", Value: "1"})
//	calls := rec.Calls()
type Recorder[T any] struct {
	clock   assert.Clock
	gate    *Gate
	entries []RecordedCall[T]
	hooks   []func(T)
	mu      sync.Mutex
	bench   bool
}

// NewRecorder returns an empty recorder.
func NewRecorder[T any]() *Recorder[T] {
	return &Recorder[T]{}
}

// WithClock sets the clock that stamps the instant of each recorded value,
// and returns the recorder. A nil clock stamps the instant of the system
// clock, which is the default.
func (r *Recorder[T]) WithClock(clk assert.Clock) *Recorder[T] {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clock = clk
	return r
}

// now returns the instant of the clock of the recorder. The caller holds the
// mutex.
func (r *Recorder[T]) now() time.Time {
	if r.clock == nil {
		return time.Now()
	}
	return r.clock.Now()
}

// BenchMode turns recording off. In bench mode, [Recorder.Record] keeps no
// value, stamps no instant, runs no hook and waits on no gate. The dispatch
// of a double through the recorder still works.
//
// A test sets bench mode while it builds the double, before any goroutine
// calls it, because Record reads the mode without the mutex.
func (r *Recorder[T]) BenchMode() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bench = true
}

// IsBenchMode reports whether [Recorder.BenchMode] turned recording off. It
// reads the mode without the mutex, as [Recorder.Record] does.
func (r *Recorder[T]) IsBenchMode() bool {
	return r.bench
}

// Record appends v to the log with the instant of the clock of the recorder,
// and runs each hook of [Recorder.OnRecord] with v. Where a [Gate] is
// active, Record first waits until the gate releases it. Record does nothing
// in bench mode.
func (r *Recorder[T]) Record(v T) {
	if r.bench {
		return
	}
	r.mu.Lock()
	if g := r.gate; g != nil {
		r.mu.Unlock()
		g.wait()
		r.mu.Lock()
	}
	r.entries = append(r.entries, RecordedCall[T]{Value: v, At: r.now()})
	for _, h := range r.hooks {
		h(v)
	}
	r.mu.Unlock()
}

// Calls returns a copy of the recorded values, in the order of recording.
func (r *Recorder[T]) Calls() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]T, len(r.entries))
	for i := range r.entries {
		out[i] = r.entries[i].Value
	}
	return out
}

// Timestamped returns a copy of the recorded values with the instant of each.
func (r *Recorder[T]) Timestamped() []RecordedCall[T] {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedCall[T], len(r.entries))
	copy(out, r.entries)
	return out
}

// CallCount returns the number of recorded values.
func (r *Recorder[T]) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// LastCall returns the value of the latest recording. It stops the test of tb
// where the recorder has no value.
func (r *Recorder[T]) LastCall(tb assert.TB) T {
	tb.Helper()
	calls := r.Calls()
	if len(calls) == 0 {
		tb.Fatalf("LastCall: the recorder has no call")
		var zero T
		return zero
	}
	return calls[len(calls)-1]
}

// Reset clears the log. It keeps the hooks, the gate and the clock.
func (r *Recorder[T]) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = nil
}

// AssertCalledOnce stops the test of tb unless the recorder has exactly one
// value, and returns that value. msg states the contract of the test.
func (r *Recorder[T]) AssertCalledOnce(tb assert.TB, msg string) T {
	tb.Helper()
	calls := r.Calls()
	if len(calls) != 1 {
		tb.Fatalf("%s: the recorder has %d calls, and the test expects 1", msg, len(calls))
		var zero T
		return zero
	}
	return calls[0]
}

// AssertCalledN stops the test of tb unless the recorder has exactly n
// values, and returns them. msg states the contract of the test.
func (r *Recorder[T]) AssertCalledN(tb assert.TB, n int, msg string) []T {
	tb.Helper()
	calls := r.Calls()
	if len(calls) != n {
		tb.Fatalf("%s: the recorder has %d calls, and the test expects %d", msg, len(calls), n)
		return nil
	}
	return calls
}

// AssertNotCalled stops the test of tb where the recorder has a value. msg
// states the contract of the test.
func (r *Recorder[T]) AssertNotCalled(tb assert.TB, msg string) {
	tb.Helper()
	if n := r.CallCount(); n != 0 {
		tb.Fatalf("%s: the recorder has %d calls, and the test expects none", msg, n)
	}
}

// Filter returns the recorded values for which pred returns true, in the
// order of recording.
func (r *Recorder[T]) Filter(pred func(T) bool) []T {
	var out []T
	for _, v := range r.Calls() {
		if pred(v) {
			out = append(out, v)
		}
	}
	return out
}

// First returns the first recorded value for which pred returns true, and
// reports false where no value matches.
func (r *Recorder[T]) First(pred func(T) bool) (T, bool) {
	for _, v := range r.Calls() {
		if pred(v) {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// Any reports whether pred returns true for a recorded value.
func (r *Recorder[T]) Any(pred func(T) bool) bool {
	_, found := r.First(pred)
	return found
}

// All reports whether pred returns true for each recorded value. It reports
// true for an empty log.
func (r *Recorder[T]) All(pred func(T) bool) bool {
	for _, v := range r.Calls() {
		if !pred(v) {
			return false
		}
	}
	return true
}

// WaitForN waits until the recorder has at least n values, and stops the test
// of tb where timeout passes first. It waits on the clock of tb, as
// [assert.EventuallyTrue] does.
func (r *Recorder[T]) WaitForN(tb assert.TB, n int, timeout time.Duration) {
	tb.Helper()
	assert.EventuallyTrue(tb, timeout, func() bool { return r.CallCount() >= n },
		fmt.Sprintf("WaitForN: the recorder has %d calls before the timeout", n))
}

// WaitFor waits until pred returns true for a recorded value, and stops the
// test of tb where timeout passes first. It waits on the clock of tb, as
// [assert.EventuallyTrue] does. msg states the contract of the test.
func (r *Recorder[T]) WaitFor(tb assert.TB, pred func(T) bool, timeout time.Duration, msg string) {
	tb.Helper()
	assert.EventuallyTrue(tb, timeout, func() bool { return r.Any(pred) }, msg)
}

// OnRecord adds a hook that [Recorder.Record] runs with each value it
// records, after the values of the hooks added before it. A hook runs under
// the mutex of the recorder, so it must not call the recorder.
func (r *Recorder[T]) OnRecord(fn func(T)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, fn)
}

// Gate holds each [Recorder.Record] of its recorder until a test releases
// it, so a test can order the calls of goroutines.
type Gate struct {
	ch chan struct{}
}

// NewGate activates a new gate on the recorder, in place of the gate before
// it, and returns it.
func (r *Recorder[T]) NewGate() *Gate {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := &Gate{ch: make(chan struct{})}
	r.gate = g
	return g
}

// Release releases every recording that waits on the gate, and each later
// one.
func (g *Gate) Release() {
	close(g.ch)
}

// ReleaseOne releases exactly one recording that waits on the gate. It waits
// until a recording arrives.
func (g *Gate) ReleaseOne() {
	g.ch <- struct{}{}
}

// wait waits until the gate releases the caller.
func (g *Gate) wait() {
	<-g.ch
}
