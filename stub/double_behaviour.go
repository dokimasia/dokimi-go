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

// errDouble is the error of the faults that [DoubleBehaviour] arms.
var errDouble = errors.New("stub: the fault of the double behaviour")

// Instance is one double, as the checks of a whole double drive it: the stub
// of one method, a call of that method, and the reset of the double.
type Instance[C any] struct {
	// Stub is the stub of the method that the checks configure and read.
	Stub *MethodStub[C]
	// Call calls the method with zero arguments.
	Call func()
	// Reset resets the double, as its method ResetCalls does.
	Reset func()
}

// Double builds a generated double under each option whose effect does not
// depend on the signature of a method. Each field returns a new double that
// reports to tb.
type Double[C any] struct {
	// New builds a double without options.
	New func(tb assert.TB) Instance[C]
	// WithClock builds a double whose latency and windows of faults read clk.
	WithClock func(tb assert.TB, clk assert.Clock) Instance[C]
	// WithLatency builds a double whose every method sleeps d on clk before
	// it answers.
	WithLatency func(tb assert.TB, d time.Duration, clk assert.Clock) Instance[C]
	// WithRandSource builds a double whose faults take their random numbers
	// from src.
	WithRandSource func(tb assert.TB, src rand.Source) Instance[C]
	// BenchMode builds a double that records no call.
	BenchMode func(tb assert.TB) Instance[C]
	// Strict builds a double that refuses each unconfigured call.
	Strict func(tb assert.TB) Instance[C]
}

// DoubleBehaviour checks the contract that a generated double keeps as a
// whole: that each option of d reaches the double, and that a reset clears
// the instants of the recorded calls. Each check is a parallel subtest of t.
//
// The checks of the clock run in a bubble of testing/synctest with a
// [assert.Controlled] clock. A double that sleeps on the system clock moves
// the time of the bubble. The checks compare that time with zero.
func DoubleBehaviour[C any](t *testing.T, d Double[C]) {
	t.Helper()

	t.Run("stops recording in bench mode", func(t *testing.T) {
		t.Parallel()
		i := d.BenchMode(t)
		i.Call()
		assert.Equal(t, i.Stub.CallCount(), 0, "bench mode keeps the log of calls empty")
	})

	t.Run("refuses an unconfigured call when built strict", func(t *testing.T) {
		t.Parallel()
		r := assert.NewRecorder()
		i := d.Strict(r)
		i.Call()
		assert.True(t, r.Failed(), "a strict double refuses an unconfigured call")
	})

	t.Run("clears timestamps on reset", func(t *testing.T) {
		t.Parallel()
		i := d.New(t)
		i.Call()
		assert.Length(t, i.Stub.Timestamped(), 1, "the double stamps the instant of a call")
		i.Reset()
		assert.Length(t, i.Stub.Timestamped(), 0, "a reset clears the instants with the log")
	})

	t.Run("draws probabilistic faults from the injected source", func(t *testing.T) {
		t.Parallel()
		src := &readSource{}
		i := d.WithRandSource(t, src)
		i.Stub.FaultsWithProbability(0.5, errDouble)
		_, _ = i.Stub.ShouldFaultFor(*new(C))
		assert.True(t, src.read, "a fault with a probability takes its number from the injected source")
	})

	doubleLatency(t, d)
}

// doubleLatency runs the checks of the clock of a double.
func doubleLatency[C any](t *testing.T, d Double[C]) {
	t.Helper()

	t.Run("sleeps latency against the injected clock", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			clk := assert.NewControlled(time.Unix(0, 0))
			i := d.WithClock(t, clk)
			i.Stub.Latency(latencyWindow)
			waited, spent := sleptOn(clk, i.Call)
			assert.True(t, waited, "the call waits for its latency")
			assert.Equal(t, spent, time.Duration(0),
				"the latency sleeps on the injected clock and not on the system clock")
		})
	})

	t.Run("records a call only once its latency has elapsed", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			clk := assert.NewControlled(time.Unix(0, 0))
			i := d.WithClock(t, clk)
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
			assert.Equal(t, i.Stub.CallCount(), 1, "the call is recorded once its latency has passed")
		})
	})

	t.Run("holds the WithLatency preset to the same clock", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			clk := assert.NewControlled(time.Unix(0, 0))
			i := d.WithLatency(t, latencyWindow, clk)
			waited, spent := sleptOn(clk, i.Call)
			assert.True(t, waited, "the call waits for the latency of the preset")
			assert.Equal(t, spent, time.Duration(0),
				"the latency of the preset sleeps on the injected clock and not on the system clock")
		})
	})

	t.Run("stops a time-windowed fault once its window closes", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			clk := assert.NewControlled(time.Unix(0, 0))
			i := d.WithClock(t, clk)
			i.Stub.FaultsFor(latencyWindow, errDouble)
			clk.Advance(latencyWindow + time.Second)
			fired, _ := i.Stub.ShouldFaultFor(*new(C))
			assert.False(t, fired, "a fault after its window does not fire")
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
