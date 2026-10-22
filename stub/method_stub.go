// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

import (
	"math/rand/v2"
	"time"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/fault"
)

// MethodStub is the runtime of one method of a generated double. It records
// the calls of the method, injects faults, sleeps a latency, checks the
// expected count of calls and refuses an unconfigured call in strict mode. C
// is the type of the calls of the method.
//
// A generated double embeds one MethodStub in the stub of each method, which
// adds the dispatch of its signature:
//
//	type StoreGetStub struct {
//		*stub.MethodStub[StoreGetCall]
//
//		fn       func(context.Context, string) (Item, error)
//		fallback *StoreGetReturn
//	}
type MethodStub[C any] struct {
	*Recorder[C]

	fault   fault.Fault[C]
	clock   assert.Clock
	rand    rand.Source
	tb      assert.TB
	times   *int
	atLeast *int
	name    string
	latency time.Duration
	strict  bool
}

// NewMethodStub returns the stub of the method name, such as Store.Get, that
// reports to tb. A nil tb reports nothing, which suits a benchmark and a
// caller outside a test.
func NewMethodStub[C any](tb assert.TB, name string) *MethodStub[C] {
	return &MethodStub[C]{Recorder: NewRecorder[C](), tb: tb, name: name}
}

// Strict turns strict mode on. In strict mode, [MethodStub.FailUnexpectedCall]
// stops the test, and a double calls it for a call without a fault, a function
// or a fixed result.
func (s *MethodStub[C]) Strict() {
	s.strict = true
}

// IsStrict reports whether strict mode is on.
func (s *MethodStub[C]) IsStrict() bool {
	return s.strict
}

// Faults arms a fault that fires on every nth call with err, and returns the
// stub.
func (s *MethodStub[C]) Faults(err error, failEveryN int) *MethodStub[C] {
	s.fault = fault.NewCountedFault[C](err, failEveryN)
	return s
}

// FaultsOnce arms a fault that fails the next call with err, and returns the
// stub. The calls after it succeed. [MethodStub.Reset] arms the fault again.
func (s *MethodStub[C]) FaultsOnce(err error) *MethodStub[C] {
	s.fault = fault.NewRetryFault[C](err, 2)
	return s
}

// SetFault arms the fault f in place of the fault before it, and returns the
// stub.
func (s *MethodStub[C]) SetFault(f fault.Fault[C]) *MethodStub[C] {
	s.fault = f
	return s
}

// FaultsWhen arms a fault that fires on every nth call for which pred returns
// true, with err, and returns the stub. The count covers the calls that pred
// selects alone.
func (s *MethodStub[C]) FaultsWhen(pred func(C) bool, err error, n int) *MethodStub[C] {
	s.fault = fault.And[C](fault.NewPredicateFault[C](err, pred), fault.NewCountedFault[C](err, n))
	return s
}

// FaultsWithProbability arms a fault that fires with the probability p on each
// call with err, and returns the stub. The fault takes its random numbers from
// the source of [MethodStub.WithRandSource], or from the global source of
// math/rand/v2 without one.
func (s *MethodStub[C]) FaultsWithProbability(p float64, err error) *MethodStub[C] {
	s.fault = fault.NewProbabilityFault[C](err, p, s.rand)
	return s
}

// FaultsUntil arms a fault that fires with err while the clock of the stub is
// before deadline, and returns the stub.
func (s *MethodStub[C]) FaultsUntil(deadline time.Time, err error) *MethodStub[C] {
	s.fault = fault.NewWindowedFault[C](err, deadline)
	return s
}

// FaultsFor arms a fault that fires with err for the duration d from the
// current instant of the clock of the stub, and returns the stub.
func (s *MethodStub[C]) FaultsFor(d time.Duration, err error) *MethodStub[C] {
	s.fault = fault.NewWindowedFault[C](err, fault.ClockNow(s.clock).Add(d))
	return s
}

// WithClock sets the clock of the latency, of the windows of faults and of the
// instants of the recorded calls, and returns the stub. A nil clock reads the
// system clock, which is the default.
func (s *MethodStub[C]) WithClock(clk assert.Clock) *MethodStub[C] {
	s.clock = clk
	s.Recorder.WithClock(clk)
	return s
}

// WithRandSource sets the source of the random numbers of
// [MethodStub.FaultsWithProbability], and returns the stub. A seeded source
// repeats the faults of a run.
func (s *MethodStub[C]) WithRandSource(src rand.Source) *MethodStub[C] {
	s.rand = src
	return s
}

// Latency sets the time that each call sleeps on the clock of the stub before
// the double returns its results, and returns the stub. A latency of zero
// sleeps not at all, which is the default.
func (s *MethodStub[C]) Latency(d time.Duration) *MethodStub[C] {
	s.latency = d
	return s
}

// SetLatency sets the latency as [MethodStub.Latency] does, without the
// return value, so a [Group] applies it to every method of a double.
func (s *MethodStub[C]) SetLatency(d time.Duration) {
	s.latency = d
}

// SleepLatency sleeps the latency of the stub on its clock. [Answer] calls it
// first for each call.
func (s *MethodStub[C]) SleepLatency() {
	if s.clock == nil {
		time.Sleep(s.latency)
		return
	}
	s.clock.Sleep(s.latency)
}

// Clock returns the clock of the stub, and nil for the system clock.
func (s *MethodStub[C]) Clock() assert.Clock {
	return s.clock
}

// ShouldFaultFor asks the armed fault whether it fires for call, and returns
// the error of the fault where it fires.
func (s *MethodStub[C]) ShouldFaultFor(call C) (bool, error) {
	if s.fault == nil {
		return false, nil
	}
	//nolint:wrapcheck // the code under test matches the armed error with errors.Is as the test armed it
	return s.fault.ShouldFire(call, s.clock)
}

// FailUnexpectedCall stops the test in strict mode, with the name of the
// method and the call. A double calls it for a call without a fault, a
// function or a fixed result.
func (s *MethodStub[C]) FailUnexpectedCall(call C) {
	if s.strict && s.tb != nil {
		s.tb.Fatalf("%s: strict mode refuses the call, because the test configured no answer\n  call: %+v",
			s.name, call)
	}
}

// FailUnfaultable stops the test for a fault that fired on a method without
// an error result, with the name of the method, the error and the call. The
// method has no place for the error, so a double that dropped it would let a
// test of a fault pass without the fault.
func (s *MethodStub[C]) FailUnfaultable(call C, err error) {
	if s.tb == nil {
		return
	}
	s.tb.Fatalf("%s: the test armed a fault, and the method returns no error to report it in\n"+
		"  fault: %v\n  call: %+v", s.name, err, call)
}

// Times sets the exact count of calls that the test expects, and returns the
// stub. [MethodStub.Verify] checks it.
func (s *MethodStub[C]) Times(n int) *MethodStub[C] {
	s.times = &n
	return s
}

// TimesAtLeast sets the lowest count of calls that the test expects, and
// returns the stub. [MethodStub.Verify] checks it.
func (s *MethodStub[C]) TimesAtLeast(n int) *MethodStub[C] {
	s.atLeast = &n
	return s
}

// Verify fails the test where the count of calls differs from the count of
// [MethodStub.Times], or is below the count of [MethodStub.TimesAtLeast]. A
// generated double registers it for the end of the test.
func (s *MethodStub[C]) Verify() {
	if s.tb == nil {
		return
	}
	count := s.CallCount()
	if s.times != nil && count != *s.times {
		s.tb.Errorf("%s: the count of calls is %d, and the test expects %d", s.name, count, *s.times)
	}
	if s.atLeast != nil && count < *s.atLeast {
		s.tb.Errorf("%s: the count of calls is %d, and the test expects at least %d", s.name, count, *s.atLeast)
	}
}

// Reset clears the recorded calls, rewinds the counters of the fault and
// clears the expected counts. It keeps the configuration of the results: the
// function, the fixed result and the fault itself.
func (s *MethodStub[C]) Reset() {
	s.Recorder.Reset()
	if s.fault != nil {
		s.fault.Reset()
	}
	s.times = nil
	s.atLeast = nil
}

// Name returns the name of the method, such as Store.Get.
func (s *MethodStub[C]) Name() string {
	return s.name
}

// TB returns the TB that the stub reports to, and nil for none.
func (s *MethodStub[C]) TB() assert.TB {
	return s.tb
}
