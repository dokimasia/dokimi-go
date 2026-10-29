// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"cmp"
	"reflect"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// The probes of the clock advance the clock of the handle, which the factory
// of the subject reads. Each probe that waits for the subject runs inside a
// testing/synctest bubble, which the check sets through its function Around,
// and calls synctest.Wait before it advances the clock and before it reads
// the subject after an advance.

// ReturnsWithin calls call on a goroutine, advances the clock past budget,
// and fails when the call has not returned. The call runs on the context of
// the check, which the seat cancels once the check ends.
func ReturnsWithin[S any](h *conform.Handle[S], call Act, budget time.Duration) {
	h.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = call(h.Context())
	}()
	synctest.Wait()
	h.Advance(budget + time.Nanosecond)
	synctest.Wait()
	returned := false
	select {
	case <-done:
		returned = true
	default:
	}
	h.Expect(spec.OverranBudget)
	assert.True(h, returned, h.Statement())
}

// CountsWithinWindow calls call, reads count halfway through window and
// once more a nanosecond past window, and fails unless the second count is
// below the first. A call that fails ends the probe, and the guard of the
// probe fails a count that fails.
func CountsWithinWindow[S any, N cmp.Ordered](h *conform.Handle[S], call Act, count Call[N], window time.Duration) {
	h.Helper()
	if call(h.Context()) != nil {
		return
	}
	inside := countAfter(h, count, window/2)
	after := countAfter(h, count, window-window/2+time.Nanosecond)
	h.Expect(spec.MisreadWindow)
	assert.Pairwise(h, []N{inside, after}, func(a, b N) bool { return b < a }, h.Statement())
}

// RunsOnSchedule registers the task with call, which returns the delay of
// its instant, reads count halfway to the instant and once more a nanosecond
// past it, and fails unless count is unchanged halfway and higher past the
// instant. A registration that fails ends the probe, and the guard of the
// probe fails a count that fails.
func RunsOnSchedule[S any, N cmp.Ordered](h *conform.Handle[S], call Call[time.Duration], count Call[N]) {
	h.Helper()
	before, err := count(h.Context())
	assert.NoError(h, err, h.Statement())
	delay, err := call(h.Context())
	if err != nil {
		return
	}
	half := countAfter(h, count, delay/2)
	past := countAfter(h, count, delay-delay/2+time.Nanosecond)
	h.Expect(spec.OffSchedule)
	assert.Equal(h, half, before, h.Statement())
	assert.Pairwise(h, []N{half, past}, func(a, b N) bool { return b > a }, h.Statement())
}

// countAfter advances the clock by d once the subject waits, and returns the
// count once the subject waits again. The guard of the probe fails a count
// that fails.
func countAfter[S any, N cmp.Ordered](h *conform.Handle[S], count Call[N], d time.Duration) N {
	h.Helper()
	synctest.Wait()
	h.Advance(d)
	synctest.Wait()
	n, err := count(h.Context())
	assert.NoError(h, err, h.Statement())
	return n
}

// MovesWithTheClock calls put and call, advances the clock in doubling steps
// from a nanosecond up to 2^52 nanoseconds, calls call after each step, and
// fails when no call returns another value. A put or a first call that fails
// ends the probe.
func MovesWithTheClock[S, V any](h *conform.Handle[S], call Call[V], put Act) {
	h.Helper()
	if put(h.Context()) != nil {
		return
	}
	first, err := call(h.Context())
	if err != nil {
		return
	}
	last := first
	for step := time.Duration(1); step <= 1<<52 && reflect.DeepEqual(last, first); step *= 2 {
		synctest.Wait()
		h.Advance(step)
		synctest.Wait()
		last, _ = call(h.Context())
	}
	h.Expect(spec.UnmovedByTheClock)
	assert.NotEqual(h, last, first, h.Statement())
}

// SteadyWhileTheClockStops calls put, then call twice without an advance,
// and fails when the second value differs from the first. A put or a first
// call that fails ends the probe, and the guard of the probe fails a second
// call that fails.
func SteadyWhileTheClockStops[S, V any](h *conform.Handle[S], call Call[V], put Act) {
	h.Helper()
	if put(h.Context()) != nil {
		return
	}
	first, err := call(h.Context())
	if err != nil {
		return
	}
	second, err := call(h.Context())
	assert.NoError(h, err, h.Statement())
	h.Expect(spec.DriftedWithoutClock)
	assert.Equal(h, second, first, h.Statement())
}

// ExpiresAfter writes, reads, advances the clock past lifetime, reads again,
// and fails unless the error of the second read matches sentinel. A write or
// a first read that fails ends the probe.
func ExpiresAfter[S, V any](h *conform.Handle[S], read Call[V], write Act, lifetime time.Duration, sentinel error) {
	h.Helper()
	if write(h.Context()) != nil {
		return
	}
	if _, err := read(h.Context()); err != nil {
		return
	}
	synctest.Wait()
	h.Advance(lifetime + time.Nanosecond)
	synctest.Wait()
	_, err := read(h.Context())
	h.Expect(spec.NotExpired)
	assert.ErrorIs(h, err, sentinel, h.Statement())
}
