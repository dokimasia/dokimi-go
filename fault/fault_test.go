// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package fault_test

import (
	"errors"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/fault"
)

// call is the call of the cases, which a predicate reads.
type call struct {
	ID string
}

// fixed is a random source whose Float64 returns the same number on every
// call: its 53 low bits over 2^53.
type fixed uint64

// The cases read the smallest, the middle and the largest number that
// Float64 returns from the sources low, half and high.
const (
	low  fixed = 0
	half fixed = 1 << 52
	high fixed = 1<<53 - 1
)

// Uint64 returns the bits of the source.
func (f fixed) Uint64() uint64 { return uint64(f) }

// at is a clock that reads one instant.
type at time.Time

// Now returns the instant of the clock.
func (a at) Now() time.Time { return time.Time(a) }

// errArmed is the error that the cases arm.
var errArmed = errors.New("fault_test: the armed error")

// draws is the number of calls of a case that counts the numbers of a source.
const draws = 100

// The instants of the cases around a deadline.
var (
	deadline = time.Date(2026, 1, 1, 0, 0, 5, 0, time.UTC)
	before   = at(deadline.Add(-time.Second))
	after    = at(deadline.Add(time.Second))
)

// A fault fires for the calls that its strategy selects and returns the
// armed error as it is. Reset rewinds the counters of the fault.
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

		t.Run("fires on every nth call", func(t *testing.T) {
			t.Parallel()

			f := fault.NewCountedFault[call](errArmed, 3)
			assert.Equal(t, firesFor(f, 6), []bool{false, false, true, false, false, true},
				"a fault of every third call fires on the third and the sixth call")
		})

		t.Run("returns the armed error", func(t *testing.T) {
			t.Parallel()

			fired, err := fault.NewCountedFault[call](errArmed, 1).ShouldFire(call{}, before)
			assert.True(t, fired, "a fault of every call fires on the first call")
			expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
		})

		tests := []struct {
			name string
			give int
		}{
			{name: "never fires for an n of zero", give: 0},
			{name: "never fires for a negative n", give: -1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				f := fault.NewCountedFault[call](errArmed, tt.give)
				assert.Equal(
					t,
					firesFor(f, 3),
					[]bool{false, false, false},
					"a fault with an n below 1 fires on no call",
				)
			})
		}

		t.Run("counts from the first call again after Reset", func(t *testing.T) {
			t.Parallel()

			f := fault.NewCountedFault[call](errArmed, 3)
			firesFor(f, 2)
			f.Reset()
			assert.Equal(
				t,
				firesFor(f, 3),
				[]bool{false, false, true},
				"the fault fires on the third call after the reset",
			)
		})
	})

	t.Run("NewRetryFault", func(t *testing.T) {
		t.Parallel()

		t.Run("fires on the calls before the nth and on no call from it", func(t *testing.T) {
			t.Parallel()

			f := fault.NewRetryFault[call](errArmed, 3)
			assert.Equal(t, firesFor(f, 5), []bool{true, true, false, false, false},
				"a dependency that recovers on the third attempt fails the first two")
		})

		t.Run("returns the armed error", func(t *testing.T) {
			t.Parallel()

			_, err := fault.NewRetryFault[call](errArmed, 2).ShouldFire(call{}, before)
			expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
		})

		t.Run("fires on the first call again after Reset", func(t *testing.T) {
			t.Parallel()

			f := fault.NewRetryFault[call](errArmed, 2)
			firesFor(f, 2)
			f.Reset()
			assert.Equal(t, firesFor(f, 2), []bool{true, false}, "the fault starts the attempts again after the reset")
		})
	})

	t.Run("NewProbabilityFault", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			p    float64
			give fixed
			want bool
		}{
			{name: "fires for a call whose random number is below the probability", p: 0.75, give: half, want: true},
			{name: "fires for no call whose random number is the probability", p: 0.5, give: half},
			{name: "fires for every call at a probability of 1", p: 1, give: high, want: true},
			{name: "fires for no call at a probability of 0", p: 0, give: low},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				fired, err := fault.NewProbabilityFault[call](errArmed, tt.p, tt.give).ShouldFire(call{}, before)
				assert.Equal(t, fired, tt.want, "the fault fires where the random number is below the probability")
				if tt.want {
					expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
				}
			})
		}

		t.Run("takes a new random number for each call", func(t *testing.T) {
			t.Parallel()

			f := fault.NewProbabilityFault[call](errArmed, 1, high)
			assert.Equal(t, firesFor(f, 2), []bool{true, true}, "a fault of every call fires for each of two calls")
		})

		t.Run("takes each random number from the source", func(t *testing.T) {
			t.Parallel()

			// The source reads 0.5 on each call, so a fault at 0.75 fires for
			// each of the calls, which a fault that drew its numbers elsewhere
			// would not do a hundred times in a row.
			fired := firesFor(fault.NewProbabilityFault[call](errArmed, 0.75, half), draws)
			assert.NotContains(t, fired, false, "each number of the source is below the probability")
		})

		t.Run("takes random numbers on both sides of the probability without a source", func(t *testing.T) {
			t.Parallel()

			// The global source draws below 0.5 and from 0.5 up alike, so a
			// hundred calls fire for some calls and for no others with a
			// probability of 2 in 2^100.
			fired := firesFor(fault.NewProbabilityFault[call](errArmed, 0.5, nil), draws)
			expect.Contains(t, fired, true, "the global source draws a number below 0.5")
			expect.Contains(t, fired, false, "the global source draws a number from 0.5 up")
		})

		sources := []struct {
			name string
			p    float64
			want bool
		}{
			{name: "fires for every call at a probability of 1 without a source", p: 1, want: true},
			{name: "fires for no call at a probability of 0 without a source", p: 0},
		}
		for _, tt := range sources {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				fired, _ := fault.NewProbabilityFault[call](errArmed, tt.p, nil).ShouldFire(call{}, before)
				assert.Equal(t, fired, tt.want, "the global source draws a number in [0, 1)")
			})
		}

		t.Run("keeps its probability after Reset", func(t *testing.T) {
			t.Parallel()

			f := fault.NewProbabilityFault[call](errArmed, 1, high)
			f.Reset()
			assert.Equal(t, firesFor(f, 1), []bool{true}, "a fault of every call fires after the reset")
		})
	})

	t.Run("NewWindowedFault", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give at
			want bool
		}{
			{name: "fires for a call before the deadline", give: before, want: true},
			{name: "fires for no call at the deadline", give: at(deadline)},
			{name: "fires for no call after the deadline", give: after},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				fired, err := fault.NewWindowedFault[call](errArmed, deadline).ShouldFire(call{}, tt.give)
				assert.Equal(t, fired, tt.want, "the window fires while the clock is before the deadline")
				if tt.want {
					expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
				}
			})
		}

		t.Run("reads the system clock without a clock", func(t *testing.T) {
			t.Parallel()

			fired, _ := fault.NewWindowedFault[call](errArmed, time.Now().Add(time.Hour)).ShouldFire(call{}, nil)
			assert.True(t, fired, "the system clock is before a deadline an hour away")
		})

		t.Run("keeps its deadline after Reset", func(t *testing.T) {
			t.Parallel()

			f := fault.NewWindowedFault[call](errArmed, deadline)
			f.Reset()
			fired, _ := f.ShouldFire(call{}, after)
			assert.False(t, fired, "the reset does not open a window that has closed")
		})
	})

	t.Run("NewPredicateFault", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give call
			want bool
		}{
			{name: "fires for a call for which the function returns true", give: call{ID: "a"}, want: true},
			{name: "fires for no call for which the function returns false", give: call{ID: "b"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				fired, err := idA().ShouldFire(tt.give, before)
				assert.Equal(t, fired, tt.want, "the predicate selects the calls of the ID a")
				if tt.want {
					expect.ErrorIs(t, err, errArmed, "the fault returns the error that the test armed")
				}
			})
		}

		t.Run("keeps its function after Reset", func(t *testing.T) {
			t.Parallel()

			f := idA()
			f.Reset()
			fired, _ := f.ShouldFire(call{ID: "a"}, before)
			assert.True(t, fired, "the predicate selects the call of the ID a after the reset")
		})
	})

	t.Run("And", func(t *testing.T) {
		t.Parallel()

		t.Run("fires with the error of the first fault where each fault fires", func(t *testing.T) {
			t.Parallel()

			errSecond := errors.New("fault_test: the second error")
			fired, err := fault.And(idA(), fault.Fault[call](fault.NewCountedFault[call](errSecond, 1))).
				ShouldFire(call{ID: "a"}, before)
			assert.True(t, fired, "both faults fire for the call of the ID a")
			expect.ErrorIs(t, err, errArmed, "the fault returns the error of the first fault")
		})

		t.Run("fires for no call that one fault rejects", func(t *testing.T) {
			t.Parallel()

			fired, _ := fault.And(idA(), fault.Fault[call](fault.NewCountedFault[call](errArmed, 1))).
				ShouldFire(call{ID: "b"}, before)
			assert.False(t, fired, "the predicate rejects the call of the ID b")
		})

		t.Run("counts the calls that the faults before a counter pass", func(t *testing.T) {
			t.Parallel()

			f := fault.And(idA(), fault.Fault[call](fault.NewCountedFault[call](errArmed, 2)))
			ids := []string{"a", "b", "b", "a"}
			got := make([]bool, 0, len(ids))
			for _, id := range ids {
				fired, _ := f.ShouldFire(call{ID: id}, before)
				got = append(got, fired)
			}
			assert.Equal(t, got, []bool{false, false, false, true},
				"the counter counts the two calls of the ID a, and fires on the second")
		})

		t.Run("resets each of its faults", func(t *testing.T) {
			t.Parallel()

			f := fault.And(fault.Fault[call](fault.NewCountedFault[call](errArmed, 2)))
			firesFor(f, 1)
			f.Reset()
			assert.Equal(
				t,
				firesFor(f, 2),
				[]bool{false, true},
				"the counter counts from the first call after the reset",
			)
		})

		t.Run("never fires without faults", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, firesFor(fault.And[call](), 1), []bool{false}, "And of no faults fires for no call")
		})
	})

	t.Run("Or", func(t *testing.T) {
		t.Parallel()

		t.Run("fires with the error of the first fault that fires", func(t *testing.T) {
			t.Parallel()

			errSecond := errors.New("fault_test: the second error")
			f := fault.Or(idA(), fault.Fault[call](fault.NewPredicateFault(errSecond, func(c call) bool {
				return c.ID == "b"
			})))
			fired, err := f.ShouldFire(call{ID: "b"}, before)
			assert.True(t, fired, "the second fault fires for the call of the ID b")
			expect.ErrorIs(t, err, errSecond, "the fault returns the error of the fault that fires")
		})

		t.Run("fires for no call that each fault rejects", func(t *testing.T) {
			t.Parallel()

			fired, _ := fault.Or(idA()).ShouldFire(call{ID: "c"}, before)
			assert.False(t, fired, "the predicate rejects the call of the ID c")
		})

		t.Run("asks no fault after the first that fires", func(t *testing.T) {
			t.Parallel()

			counter := fault.NewCountedFault[call](errArmed, 2)
			f := fault.Or(idA(), fault.Fault[call](counter))
			for range 3 {
				_, _ = f.ShouldFire(call{ID: "a"}, before)
			}
			assert.Equal(t, firesFor(counter, 1), []bool{false}, "the counter counts no call that the predicate took")
		})

		t.Run("resets each of its faults", func(t *testing.T) {
			t.Parallel()

			f := fault.Or(fault.Fault[call](fault.NewCountedFault[call](errArmed, 2)))
			firesFor(f, 1)
			f.Reset()
			assert.Equal(
				t,
				firesFor(f, 2),
				[]bool{false, true},
				"the counter counts from the first call after the reset",
			)
		})

		t.Run("never fires without faults", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, firesFor(fault.Or[call](), 1), []bool{false}, "Or of no faults fires for no call")
		})
	})
}

// firesFor asks f about n calls before the deadline, and returns whether f
// fires for each.
func firesFor(f fault.Fault[call], n int) []bool {
	out := make([]bool, 0, n)
	for range n {
		fired, _ := f.ShouldFire(call{}, before)
		out = append(out, fired)
	}
	return out
}

// idA returns a predicate that fires for the calls of the ID a with the armed
// error.
func idA() fault.Fault[call] {
	return fault.NewPredicateFault(errArmed, func(c call) bool { return c.ID == "a" })
}
