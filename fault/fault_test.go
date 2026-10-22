// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package fault_test

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/fault"
)

// The cases read the smallest, the middle and the largest number that
// Float64 returns from the sources low, half and high.
const (
	low  fixed = 0
	half fixed = 1 << 52
	high fixed = 1<<53 - 1
)

// errArmed is the error that the cases arm.
var errArmed = errors.New("fault_test: the armed error")

// samples is the number of calls of a case that counts the numbers of a
// source.
const samples = 100

// The instants of the cases around a deadline.
var (
	deadline = time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC)
	before   = at(deadline.Add(-time.Second))
	after    = at(deadline.Add(time.Second))
)

// A fault fails the calls that its strategy selects and returns the armed
// error as it is. Reset rewinds the counters of the fault.
func TestFault(t *testing.T) {
	t.Parallel()

	t.Run("ClockNow", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the instant of the clock", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, fault.ClockNow(before), time.Time(before), "the clock reads its own instant")
		})

		t.Run("returns the instant of the system clock for a nil clock", func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			got := fault.ClockNow(nil)
			assert.False(t, got.Before(start), "the system clock does not run backwards")
		})
	})

	t.Run("NewCountedFault", func(t *testing.T) {
		t.Parallel()

		t.Run("fails every nth call", func(t *testing.T) {
			t.Parallel()

			f := fault.NewCountedFault[call](errArmed, 3)
			assert.Equal(t, failsFor(f, 6), []bool{false, false, true, false, false, true},
				"a fault of every third call fails the third and the sixth call")
		})

		t.Run("returns the armed error", func(t *testing.T) {
			t.Parallel()

			failed, err := fault.NewCountedFault[call](errArmed, 1).ShouldTrigger(call{}, before)
			assert.True(t, failed, "a fault of every call fails the first call")
			expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
		})

		tests := []struct {
			name string
			give int
		}{
			{name: "fails no call for an n of zero", give: 0},
			{name: "fails no call for a negative n", give: -1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				f := fault.NewCountedFault[call](errArmed, tt.give)
				assert.Equal(
					t,
					failsFor(f, 3),
					[]bool{false, false, false},
					"a fault with an n below 1 fails no call",
				)
			})
		}

		t.Run("counts from the first call again after Reset", func(t *testing.T) {
			t.Parallel()

			f := fault.NewCountedFault[call](errArmed, 3)
			failsFor(f, 2)
			f.Reset()
			assert.Equal(
				t,
				failsFor(f, 3),
				[]bool{false, false, true},
				"the fault fails the third call after the reset",
			)
		})
	})

	t.Run("NewRetryFault", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the calls before the nth call", func(t *testing.T) {
			t.Parallel()

			f := fault.NewRetryFault[call](errArmed, 3)
			assert.Equal(t, failsFor(f, 5), []bool{true, true, false, false, false},
				"a dependency that recovers on the third attempt fails the first two")
		})

		t.Run("returns the armed error", func(t *testing.T) {
			t.Parallel()

			_, err := fault.NewRetryFault[call](errArmed, 2).ShouldTrigger(call{}, before)
			expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
		})

		t.Run("fails the first call again after Reset", func(t *testing.T) {
			t.Parallel()

			f := fault.NewRetryFault[call](errArmed, 2)
			failsFor(f, 2)
			f.Reset()
			assert.Equal(t, failsFor(f, 2), []bool{true, false}, "the fault starts the attempts again after the reset")
		})
	})

	t.Run("NewProbabilityFault", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name            string
			giveProbability float64
			giveSource      rand.Source
			want            bool
		}{
			{
				name: "fails a call whose random number is below the probability", giveProbability: 0.75,
				giveSource: half, want: true,
			},
			{
				name: "fails no call whose random number is the probability", giveProbability: 0.5,
				giveSource: half,
			},
			{name: "fails every call at a probability of 1", giveProbability: 1, giveSource: high, want: true},
			{name: "fails no call at a probability of 0", giveProbability: 0, giveSource: low},
			{name: "fails every call at a probability of 1 without a source", giveProbability: 1, want: true},
			{name: "fails no call at a probability of 0 without a source", giveProbability: 0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				f := fault.NewProbabilityFault[call](errArmed, tt.giveProbability, tt.giveSource)
				failed, err := f.ShouldTrigger(call{}, before)
				assert.Equal(t, failed, tt.want, "the fault fails a call whose random number is below the probability")
				if tt.want {
					expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
				}
			})
		}

		t.Run("takes a new random number for each call", func(t *testing.T) {
			t.Parallel()

			f := fault.NewProbabilityFault[call](errArmed, 1, high)
			assert.Equal(t, failsFor(f, 2), []bool{true, true}, "a fault of every call fails each of two calls")
		})

		t.Run("takes each random number from the source", func(t *testing.T) {
			t.Parallel()

			// The source returns 0.5 on each call. A fault at 0.75 fails each
			// of the calls. A fault that took its numbers elsewhere would not
			// do that a hundred times in a row.
			failed := failsFor(fault.NewProbabilityFault[call](errArmed, 0.75, half), samples)
			assert.NotContains(t, failed, false, "each number of the source is below the probability")
		})

		t.Run("takes random numbers on both sides of the probability without a source", func(t *testing.T) {
			t.Parallel()

			// The global source returns numbers below 0.5 and from 0.5 up alike.
			// A hundred calls all fail or all succeed with a probability of 2 in
			// 2^100.
			failed := failsFor(fault.NewProbabilityFault[call](errArmed, 0.5, nil), samples)
			expect.Contains(t, failed, true, "the global source returns a number below 0.5")
			expect.Contains(t, failed, false, "the global source returns a number from 0.5 up")
		})

		t.Run("keeps its probability after Reset", func(t *testing.T) {
			t.Parallel()

			f := fault.NewProbabilityFault[call](errArmed, 1, high)
			f.Reset()
			assert.Equal(t, failsFor(f, 1), []bool{true}, "a fault of every call fails a call after the reset")
		})
	})

	t.Run("NewWindowedFault", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give at
			want bool
		}{
			{name: "fails a call before the deadline", give: before, want: true},
			{name: "fails no call at the deadline", give: at(deadline)},
			{name: "fails no call after the deadline", give: after},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				failed, err := fault.NewWindowedFault[call](errArmed, deadline).ShouldTrigger(call{}, tt.give)
				assert.Equal(t, failed, tt.want, "the window fails each call while the clock is before the deadline")
				if tt.want {
					expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
				}
			})
		}

		t.Run("reads the system clock without a clock", func(t *testing.T) {
			t.Parallel()

			failed, _ := fault.NewWindowedFault[call](errArmed, time.Now().Add(time.Hour)).ShouldTrigger(call{}, nil)
			assert.True(t, failed, "the system clock is before a deadline an hour away")
		})

		t.Run("keeps its deadline after Reset", func(t *testing.T) {
			t.Parallel()

			f := fault.NewWindowedFault[call](errArmed, deadline)
			f.Reset()
			failed, _ := f.ShouldTrigger(call{}, after)
			assert.False(t, failed, "the reset does not open a window that has closed")
		})
	})

	t.Run("NewPredicateFault", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give call
			want bool
		}{
			{name: "fails a call for which the function returns true", give: call{ID: "a"}, want: true},
			{name: "fails no call for which the function returns false", give: call{ID: "b"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				failed, err := idA().ShouldTrigger(tt.give, before)
				assert.Equal(t, failed, tt.want, "the predicate selects the calls of the ID a")
				if tt.want {
					expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
				}
			})
		}

		t.Run("keeps its function after Reset", func(t *testing.T) {
			t.Parallel()

			f := idA()
			f.Reset()
			failed, _ := f.ShouldTrigger(call{ID: "a"}, before)
			assert.True(t, failed, "the predicate selects the call of the ID a after the reset")
		})
	})

	t.Run("And", func(t *testing.T) {
		t.Parallel()

		t.Run("fails a call that each fault fails with the error of the first fault", func(t *testing.T) {
			t.Parallel()

			errSecond := errors.New("fault_test: the second error")
			failed, err := fault.And(idA(), fault.Fault[call](fault.NewCountedFault[call](errSecond, 1))).
				ShouldTrigger(call{ID: "a"}, before)
			assert.True(t, failed, "both faults fail the call of the ID a")
			expect.ErrorIs(t, err, errArmed, "the fault returns the error of the first fault")
		})

		t.Run("fails no call that one fault rejects", func(t *testing.T) {
			t.Parallel()

			failed, _ := fault.And(idA(), fault.Fault[call](fault.NewCountedFault[call](errArmed, 1))).
				ShouldTrigger(call{ID: "b"}, before)
			assert.False(t, failed, "the predicate rejects the call of the ID b")
		})

		t.Run("counts the calls that the faults before a counter pass", func(t *testing.T) {
			t.Parallel()

			f := fault.And(idA(), fault.Fault[call](fault.NewCountedFault[call](errArmed, 2)))
			ids := []string{"a", "b", "b", "a"}
			got := make([]bool, 0, len(ids))
			for _, id := range ids {
				failed, _ := f.ShouldTrigger(call{ID: id}, before)
				got = append(got, failed)
			}
			assert.Equal(t, got, []bool{false, false, false, true},
				"the counter counts the two calls of the ID a and fails the second")
		})

		t.Run("resets each of its faults", func(t *testing.T) {
			t.Parallel()

			f := fault.And(fault.Fault[call](fault.NewCountedFault[call](errArmed, 2)))
			failsFor(f, 1)
			f.Reset()
			assert.Equal(
				t,
				failsFor(f, 2),
				[]bool{false, true},
				"the counter counts from the first call after the reset",
			)
		})

		t.Run("fails no call without faults", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, failsFor(fault.And[call](), 1), []bool{false}, "And of no faults fails no call")
		})
	})

	t.Run("Or", func(t *testing.T) {
		t.Parallel()

		t.Run("fails a call with the error of the first fault that fails it", func(t *testing.T) {
			t.Parallel()

			errSecond := errors.New("fault_test: the second error")
			f := fault.Or(idA(), fault.Fault[call](fault.NewPredicateFault(errSecond, func(c call) bool {
				return c.ID == "b"
			})))
			failed, err := f.ShouldTrigger(call{ID: "b"}, before)
			assert.True(t, failed, "the second fault fails the call of the ID b")
			expect.ErrorIs(t, err, errSecond, "the fault returns the error of the fault that fails the call")
		})

		t.Run("fails no call that each fault rejects", func(t *testing.T) {
			t.Parallel()

			failed, _ := fault.Or(idA()).ShouldTrigger(call{ID: "c"}, before)
			assert.False(t, failed, "the predicate rejects the call of the ID c")
		})

		t.Run("asks no fault after the first that fails the call", func(t *testing.T) {
			t.Parallel()

			counter := fault.NewCountedFault[call](errArmed, 2)
			f := fault.Or(idA(), fault.Fault[call](counter))
			for range 3 {
				_, _ = f.ShouldTrigger(call{ID: "a"}, before)
			}
			assert.Equal(t, failsFor(counter, 1), []bool{false}, "the counter counts no call that the predicate took")
		})

		t.Run("resets each of its faults", func(t *testing.T) {
			t.Parallel()

			f := fault.Or(fault.Fault[call](fault.NewCountedFault[call](errArmed, 2)))
			failsFor(f, 1)
			f.Reset()
			assert.Equal(
				t,
				failsFor(f, 2),
				[]bool{false, true},
				"the counter counts from the first call after the reset",
			)
		})

		t.Run("fails no call without faults", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, failsFor(fault.Or[call](), 1), []bool{false}, "Or of no faults fails no call")
		})
	})
}

// call is the call of the cases, which a predicate reads.
type call struct {
	ID string
}

// fixed is a random source whose Float64 returns the same number on every
// call: its 53 low bits over 2^53.
type fixed uint64

// Uint64 returns the bits of the source.
func (f fixed) Uint64() uint64 { return uint64(f) }

// at is a clock that reads one instant.
type at time.Time

// Now returns the instant of the clock.
func (a at) Now() time.Time { return time.Time(a) }

// failsFor asks f about n calls before the deadline, and returns whether f
// fails each.
func failsFor(f fault.Fault[call], n int) []bool {
	out := make([]bool, 0, n)
	for range n {
		failed, _ := f.ShouldTrigger(call{}, before)
		out = append(out, failed)
	}
	return out
}

// idA returns a predicate that fails the calls of the ID a with the armed
// error.
func idA() fault.Fault[call] {
	return fault.NewPredicateFault(errArmed, func(c call) bool { return c.ID == "a" })
}
