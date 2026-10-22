// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"math/rand/v2"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/stub"
)

// instance returns the instance of a fake that reports to tb, with each
// option applied to the stub of its method as a generated constructor applies
// it through the group of the double.
func instance(tb assert.TB, options ...func(stub.Configurable)) stub.Instance[call] {
	tb.Helper()
	f := newFake(tb)
	for _, apply := range options {
		apply(f.on)
	}
	return stub.Instance[call]{Stub: f.on, Call: func() { _, _ = f.Get("k") }, Reset: f.on.Reset}
}

// onClock returns the instance of a fake on clk.
func onClock(tb assert.TB, clk assert.Clock) stub.Instance[call] {
	tb.Helper()
	return instance(tb, func(c stub.Configurable) { c.SetClock(clk) })
}

// delayed returns the instance of a fake whose calls sleep d on clk.
func delayed(tb assert.TB, d time.Duration, clk assert.Clock) stub.Instance[call] {
	tb.Helper()
	return instance(tb, func(c stub.Configurable) {
		c.SetClock(clk)
		c.SetLatency(d)
	})
}

// hasty returns i with a call that records at once instead of sleeping its
// latency.
func hasty(i stub.Instance[call]) stub.Instance[call] {
	i.Call = func() { i.Stub.Record(call{}) }
	return i
}

func TestDoubleBehaviour(t *testing.T) {
	t.Parallel()

	t.Run("BenchModeBehaviour", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a double that records no call in bench mode", func(t *testing.T) {
			t.Parallel()

			stub.BenchModeBehaviour(t, func(tb assert.TB) stub.Instance[call] {
				return instance(tb, stub.Configurable.BenchMode)
			})
		})

		t.Run("runs each check as a parallel subtest", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			stub.BenchModeBehaviour(t, func(tb assert.TB) stub.Instance[call] {
				built.Add(1)
				return instance(tb, stub.Configurable.BenchMode)
			})
			assert.Equal(t, built.Load(), int32(0), "no check starts before the test returns")
		})

		t.Run("fails the check of the log for a double that records in bench mode", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(suiteEnv) == t.Name() {
				stub.BenchModeBehaviour(t, func(tb assert.TB) stub.Instance[call] { return instance(tb) })
				return
			}
			assertFailedChecks(t, "stops_recording_the_calls")
		})
	})

	t.Run("StrictBehaviour", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a double that refuses an unconfigured call", func(t *testing.T) {
			t.Parallel()

			stub.StrictBehaviour(t, func(tb assert.TB) stub.Instance[call] {
				return instance(tb, stub.Configurable.Strict)
			})
		})

		t.Run("runs each check as a parallel subtest", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			stub.StrictBehaviour(t, func(tb assert.TB) stub.Instance[call] {
				built.Add(1)
				return instance(tb, stub.Configurable.Strict)
			})
			assert.Equal(t, built.Load(), int32(0), "no check starts before the test returns")
		})

		t.Run("fails the check of the refusal for a lenient double", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(suiteEnv) == t.Name() {
				stub.StrictBehaviour(t, func(tb assert.TB) stub.Instance[call] { return instance(tb) })
				return
			}
			assertFailedChecks(t, "refuses_an_unconfigured_call")
		})
	})

	t.Run("ResetBehaviour", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a double whose reset clears its log", func(t *testing.T) {
			t.Parallel()

			stub.ResetBehaviour(t, func(tb assert.TB) stub.Instance[call] { return instance(tb) })
		})

		t.Run("runs each check as a parallel subtest", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			stub.ResetBehaviour(t, func(tb assert.TB) stub.Instance[call] {
				built.Add(1)
				return instance(tb)
			})
			assert.Equal(t, built.Load(), int32(0), "no check starts before the test returns")
		})

		tests := []struct {
			name string
			give func(tb assert.TB) stub.Instance[call]
		}{
			{
				name: "fails the check of the instants for a double whose reset keeps the log",
				give: func(tb assert.TB) stub.Instance[call] {
					i := instance(tb)
					i.Reset = func() {}
					return i
				},
			},
			{
				name: "fails the check of the instants for a double that records no call",
				give: func(tb assert.TB) stub.Instance[call] {
					return instance(tb, stub.Configurable.BenchMode)
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if os.Getenv(suiteEnv) == t.Name() {
					stub.ResetBehaviour(t, tt.give)
					return
				}
				assertFailedChecks(t, "clears_the_instants_of_the_recorded_calls")
			})
		}
	})

	t.Run("RandSourceBehaviour", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a double whose faults read the source", func(t *testing.T) {
			t.Parallel()

			stub.RandSourceBehaviour(t, func(tb assert.TB, src rand.Source) stub.Instance[call] {
				return instance(tb, func(c stub.Configurable) { c.SetRandSource(src) })
			})
		})

		t.Run("runs each check as a parallel subtest", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			stub.RandSourceBehaviour(t, func(tb assert.TB, src rand.Source) stub.Instance[call] {
				built.Add(1)
				return instance(tb, func(c stub.Configurable) { c.SetRandSource(src) })
			})
			assert.Equal(t, built.Load(), int32(0), "no check starts before the test returns")
		})

		t.Run("fails the check of the source for a double that ignores the source", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(suiteEnv) == t.Name() {
				stub.RandSourceBehaviour(t, func(tb assert.TB, _ rand.Source) stub.Instance[call] {
					return instance(tb)
				})
				return
			}
			assertFailedChecks(t, "takes_the_number_of_a_probabilistic_fault_from_the_source")
		})
	})

	t.Run("ClockBehaviour", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a double that sleeps on the clock", func(t *testing.T) {
			t.Parallel()

			stub.ClockBehaviour(t, onClock)
		})

		t.Run("runs each check as a parallel subtest", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			stub.ClockBehaviour(t, func(tb assert.TB, clk assert.Clock) stub.Instance[call] {
				built.Add(1)
				return onClock(tb, clk)
			})
			assert.Equal(t, built.Load(), int32(0), "no check starts before the test returns")
		})

		tests := []struct {
			name string
			give func(tb assert.TB, clk assert.Clock) stub.Instance[call]
			want []string
		}{
			{
				name: "fails the checks of the clock for a double that ignores the clock",
				give: func(tb assert.TB, _ assert.Clock) stub.Instance[call] { return instance(tb) },
				want: []string{"sleeps_a_latency_on_the_clock", "ends_a_fault_at_the_end_of_its_window"},
			},
			{
				name: "fails the checks of the latency for a double that does not sleep",
				give: func(tb assert.TB, clk assert.Clock) stub.Instance[call] {
					return hasty(onClock(tb, clk))
				},
				want: []string{"sleeps_a_latency_on_the_clock", "records_a_call_after_its_latency"},
			},
			{
				name: "fails the check of the log for a double that records no call",
				give: func(tb assert.TB, clk assert.Clock) stub.Instance[call] {
					return instance(tb, stub.Configurable.BenchMode, func(c stub.Configurable) { c.SetClock(clk) })
				},
				want: []string{"records_a_call_after_its_latency"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if os.Getenv(suiteEnv) == t.Name() {
					stub.ClockBehaviour(t, tt.give)
					return
				}
				assertFailedChecks(t, tt.want...)
			})
		}
	})

	t.Run("LatencyBehaviour", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a double that sleeps its latency on the clock", func(t *testing.T) {
			t.Parallel()

			stub.LatencyBehaviour(t, delayed)
		})

		t.Run("runs each check as a parallel subtest", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			stub.LatencyBehaviour(t, func(tb assert.TB, d time.Duration, clk assert.Clock) stub.Instance[call] {
				built.Add(1)
				return delayed(tb, d, clk)
			})
			assert.Equal(t, built.Load(), int32(0), "no check starts before the test returns")
		})

		tests := []struct {
			name string
			give func(tb assert.TB, d time.Duration, clk assert.Clock) stub.Instance[call]
		}{
			{
				name: "fails the check of the latency for a double that ignores the clock",
				give: func(tb assert.TB, d time.Duration, _ assert.Clock) stub.Instance[call] {
					return instance(tb, func(c stub.Configurable) { c.SetLatency(d) })
				},
			},
			{
				name: "fails the check of the latency for a double that does not sleep",
				give: func(tb assert.TB, d time.Duration, clk assert.Clock) stub.Instance[call] {
					return hasty(delayed(tb, d, clk))
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if os.Getenv(suiteEnv) == t.Name() {
					stub.LatencyBehaviour(t, tt.give)
					return
				}
				assertFailedChecks(t, "sleeps_the_latency_on_the_clock")
			})
		}
	})
}
