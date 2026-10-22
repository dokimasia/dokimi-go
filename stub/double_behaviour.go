// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

import (
	"errors"
	"math/rand/v2"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
)

// latencyWindow is the latency and the window of a fault that the checks of
// the clock configure. A virtual clock passes it at once. A double that sleeps
// on the system clock spends it in the time of the bubble.
const latencyWindow = 5 * time.Second

// readSource is a random source that records whether a fault read it.
type readSource struct {
	read bool
}

// Uint64 records the read and returns 0.
func (s *readSource) Uint64() uint64 {
	s.read = true
	return 0
}

// errDouble is the error of the faults that the checks of the options arm.
var errDouble = errors.New("stub: the fault of the checks of the options")

// Instance is one double, as the checks of its options drive it: the stub of
// one method, a call of that method, and the reset of the double.
type Instance[C any] struct {
	// Stub is the stub of the method that the checks configure and read.
	Stub *MethodStub[C]
	// Call calls the method with zero arguments.
	Call func()
	// Reset resets the double, as its method ResetCalls does.
	Reset func()
}

// BenchModeBehaviour checks the option of a generated double that turns the
// recording of calls off. newInstance returns a new double with that option
// that reports to tb. Each check is a parallel subtest of t.
func BenchModeBehaviour[C any](t *testing.T, newInstance func(tb assert.TB) Instance[C]) {
	t.Helper()

	t.Run("stops recording the calls", func(t *testing.T) {
		t.Parallel()
		i := newInstance(t)
		i.Call()
		assert.Equal(t, i.Stub.CallCount(), 0, "bench mode keeps the log of calls empty")
	})
}

// StrictBehaviour checks the option of a generated double that refuses each
// unconfigured call. newInstance returns a new double with that option that
// reports to tb, and the check passes an [assert.Recorder] as tb. Each check
// is a parallel subtest of t.
func StrictBehaviour[C any](t *testing.T, newInstance func(tb assert.TB) Instance[C]) {
	t.Helper()

	t.Run("refuses an unconfigured call", func(t *testing.T) {
		t.Parallel()
		r := assert.NewRecorder()
		i := newInstance(r)
		i.Call()
		assert.True(t, r.Failed(), "a strict double refuses an unconfigured call")
	})
}

// ResetBehaviour checks the reset of a generated double, which clears the
// instants of the recorded calls with the log. newInstance returns a new
// double that reports to tb. Each check is a parallel subtest of t.
func ResetBehaviour[C any](t *testing.T, newInstance func(tb assert.TB) Instance[C]) {
	t.Helper()

	t.Run("clears the instants of the recorded calls", func(t *testing.T) {
		t.Parallel()
		i := newInstance(t)
		i.Call()
		assert.Length(t, i.Stub.Timestamped(), 1, "the double stamps the instant of a call")
		i.Reset()
		assert.Length(t, i.Stub.Timestamped(), 0, "a reset clears the instants with the log")
	})
}

// RandSourceBehaviour checks the option of a generated double that sets the
// random source of its probabilistic faults. newInstance returns a new double
// with that option on src that reports to tb. Each check is a parallel
// subtest of t.
func RandSourceBehaviour[C any](t *testing.T, newInstance func(tb assert.TB, src rand.Source) Instance[C]) {
	t.Helper()

	t.Run("takes the number of a probabilistic fault from the source", func(t *testing.T) {
		t.Parallel()
		src := &readSource{}
		i := newInstance(t, src)
		i.Stub.FaultsWithProbability(0.5, errDouble)
		_, _ = i.Stub.ShouldFaultFor(*new(C))
		assert.True(t, src.read, "a fault with a probability takes its number from the source")
	})
}

// ClockBehaviour checks the option of a generated double that sets its clock.
// newInstance returns a new double with that option on clk that reports to
// tb. Each check is a parallel subtest of t.
//
// The checks run in a bubble of testing/synctest with a [assert.Controlled]
// clock. A double that sleeps on the system clock moves the time of the
// bubble, and the checks compare that time with zero.
func ClockBehaviour[C any](t *testing.T, newInstance func(tb assert.TB, clk assert.Clock) Instance[C]) {
	t.Helper()

	t.Run("sleeps a latency on the clock", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			clk := assert.NewControlled(time.Unix(0, 0))
			i := newInstance(t, clk)
			i.Stub.Latency(latencyWindow)
			waited, spent := sleptOn(clk, i.Call)
			assert.True(t, waited, "the call waits for its latency")
			assert.Equal(t, spent, time.Duration(0),
				"the latency sleeps on the clock of the option and not on the system clock")
		})
	})

	t.Run("records a call after its latency", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			clk := assert.NewControlled(time.Unix(0, 0))
			i := newInstance(t, clk)
			i.Stub.Latency(latencyWindow)
			done := make(chan struct{})
			go func() {
				i.Call()
				close(done)
			}()
			synctest.Wait()
			assert.Equal(t, i.Stub.CallCount(), 0, "a call inside its latency is not recorded yet")
			clk.Advance(latencyWindow + time.Second)
			<-done
			assert.Equal(t, i.Stub.CallCount(), 1, "the call is recorded after its latency")
		})
	})

	t.Run("ends a fault at the end of its window", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			clk := assert.NewControlled(time.Unix(0, 0))
			i := newInstance(t, clk)
			i.Stub.FaultsFor(latencyWindow, errDouble)
			clk.Advance(latencyWindow + time.Second)
			faulted, _ := i.Stub.ShouldFaultFor(*new(C))
			assert.False(t, faulted, "a fault after its window does not fail the call")
		})
	})
}

// LatencyBehaviour checks a constructor or an option of a generated double
// that makes each method sleep a latency. newInstance returns a new double
// whose methods sleep d on clk and that reports to tb. Each check is a
// parallel subtest of t, and runs in a bubble of testing/synctest as the
// checks of [ClockBehaviour] do.
func LatencyBehaviour[C any](
	t *testing.T,
	newInstance func(tb assert.TB, d time.Duration, clk assert.Clock) Instance[C],
) {
	t.Helper()

	t.Run("sleeps the latency on the clock", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			clk := assert.NewControlled(time.Unix(0, 0))
			i := newInstance(t, latencyWindow, clk)
			waited, spent := sleptOn(clk, i.Call)
			assert.True(t, waited, "the call waits for the latency")
			assert.Equal(t, spent, time.Duration(0), "the latency sleeps on the clock and not on the system clock")
		})
	})
}

// sleptOn calls call on a goroutine of the bubble, and advances clk past
// latencyWindow once the goroutine blocks. It reports whether the call still
// ran when the goroutine blocked, and returns the time of the bubble that
// passed until the call returned. A call that sleeps on clk waits and spends
// no time. A call that sleeps on the system clock spends latencyWindow.
func sleptOn(clk *assert.Controlled, call func()) (waited bool, spent time.Duration) {
	start := time.Now()
	done := make(chan struct{})
	go func() {
		call()
		close(done)
	}()
	synctest.Wait()
	select {
	case <-done:
	default:
		waited = true
	}
	clk.Advance(latencyWindow + time.Second)
	<-done
	return waited, time.Since(start)
}
