// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/fault"
	"go.dokimi.dev/stub"
)

// A method stub records the calls of a method, arms faults, sleeps a latency,
// checks the expected counts and refuses an unconfigured call in strict mode.
func TestMethodStub(t *testing.T) {
	t.Parallel()

	t.Run("NewMethodStub", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a stub with the name and the TB", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			expect.Equal(t, s.Name(), getName, "the stub has the name of the method")
			expect.Equal(t, s.TB(), assert.TB(r), "the stub reports to the TB", assert.ByIdentity())
			expect.Equal(t, s.CallCount(), 0, "a new stub has no calls")
		})

		t.Run("returns a stub without a TB for a nil TB", func(t *testing.T) {
			t.Parallel()

			assert.Nil(t, stub.NewMethodStub[call](nil, getName).TB(), "the stub reports to nothing")
		})
	})

	t.Run("Strict", func(t *testing.T) {
		t.Parallel()

		t.Run("turns strict mode on", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			expect.False(t, s.IsStrict(), "a stub is lenient by default")
			s.Strict()
			expect.True(t, s.IsStrict(), "Strict turns strict mode on")
		})
	})

	t.Run("Faults", func(t *testing.T) {
		t.Parallel()

		t.Run("fires on every nth call", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			got := s.Faults(errTest, 2)
			assert.Equal(t, got, s, "Faults returns the stub", assert.ByIdentity())
			assert.Equal(t, fired(s, 4), []bool{false, true, false, true}, "a fault of every second call fires twice")
		})
	})

	t.Run("FaultsOnce", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the next call alone", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			assert.Equal(t, s.FaultsOnce(errTest), s, "FaultsOnce returns the stub", assert.ByIdentity())
			assert.Equal(t, fired(s, 3), []bool{true, false, false}, "the fault fails one call")
		})

		t.Run("arms the fault again after Reset", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			s.FaultsOnce(errTest)
			fired(s, 1)
			s.Reset()
			assert.Equal(t, fired(s, 1), []bool{true}, "the reset rewinds the fault")
		})
	})

	t.Run("SetFault", func(t *testing.T) {
		t.Parallel()

		t.Run("arms the fault in place of the fault before it", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			s.Faults(errTest, 1)
			got := s.SetFault(fault.NewPredicateFault(errTest, func(c call) bool { return c.Key == "poison" }))
			expect.Equal(t, got, s, "SetFault returns the stub", assert.ByIdentity())
			ok, _ := s.ShouldFaultFor(call{Key: "ok"})
			expect.False(t, ok, "the predicate passes the call of the key ok")
			poisoned, err := s.ShouldFaultFor(call{Key: "poison"})
			expect.True(t, poisoned, "the predicate fails the call of the key poison")
			expect.ErrorIs(t, err, errTest, "the fault returns the armed error")
		})
	})

	t.Run("FaultsWhen", func(t *testing.T) {
		t.Parallel()

		t.Run("fires on every nth call that the function selects", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			got := s.FaultsWhen(func(c call) bool { return c.Key == "a" }, errTest, 2)
			expect.Equal(t, got, s, "FaultsWhen returns the stub", assert.ByIdentity())
			keys := []string{"a", "b", "a"}
			out := make([]bool, 0, len(keys))
			for _, k := range keys {
				f, _ := s.ShouldFaultFor(call{Key: k})
				out = append(out, f)
			}
			expect.Equal(t, out, []bool{false, false, true}, "the second call of the key a fails")
		})
	})

	t.Run("FaultsWithProbability", func(t *testing.T) {
		t.Parallel()

		t.Run("draws from the source of WithRandSource", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			assert.Equal(t, s.WithRandSource(quarter{}), s, "WithRandSource returns the stub", assert.ByIdentity())
			got := s.FaultsWithProbability(0.5, errTest)
			expect.Equal(t, got, s, "FaultsWithProbability returns the stub", assert.ByIdentity())
			f, err := s.ShouldFaultFor(call{})
			expect.True(t, f, "the number 0.25 of the source is below 0.5")
			expect.ErrorIs(t, err, errTest, "the fault returns the armed error")
		})

		t.Run("never fires at a probability of 0 without a source", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			s.FaultsWithProbability(0, errTest)
			assert.Equal(t, fired(s, 3), []bool{false, false, false}, "no number is below 0")
		})
	})

	t.Run("FaultsUntil", func(t *testing.T) {
		t.Parallel()

		t.Run("fires until the clock reaches the deadline", func(t *testing.T) {
			t.Parallel()

			clk := assert.NewControlled(origin)
			s := stub.NewMethodStub[call](nil, getName).WithClock(clk)
			got := s.FaultsUntil(origin.Add(10*time.Second), errTest)
			expect.Equal(t, got, s, "FaultsUntil returns the stub", assert.ByIdentity())
			expect.Equal(t, fired(s, 1), []bool{true}, "the fault fires before the deadline")
			clk.Advance(11 * time.Second)
			expect.Equal(t, fired(s, 1), []bool{false}, "the fault does not fire after the deadline")
		})
	})

	t.Run("FaultsFor", func(t *testing.T) {
		t.Parallel()

		t.Run("fires for the duration from the instant of the clock", func(t *testing.T) {
			t.Parallel()

			clk := assert.NewControlled(origin)
			s := stub.NewMethodStub[call](nil, getName).WithClock(clk)
			clk.Advance(time.Minute)
			got := s.FaultsFor(5*time.Second, errTest)
			expect.Equal(t, got, s, "FaultsFor returns the stub", assert.ByIdentity())
			clk.Advance(4 * time.Second)
			expect.Equal(t, fired(s, 1), []bool{true}, "the fault fires inside its duration")
			clk.Advance(2 * time.Second)
			expect.Equal(t, fired(s, 1), []bool{false}, "the fault does not fire after its duration")
		})
	})

	t.Run("WithClock", func(t *testing.T) {
		t.Parallel()

		t.Run("sets the clock of the stub and of its recorder", func(t *testing.T) {
			t.Parallel()

			clk := assert.NewControlled(origin)
			s := stub.NewMethodStub[call](nil, getName)
			expect.Nil(t, s.Clock(), "a stub reads the system clock by default")
			got := s.WithClock(clk)
			expect.Equal(t, got, s, "WithClock returns the stub", assert.ByIdentity())
			expect.Equal(t, s.Clock(), assert.Clock(clk), "the stub has the clock", assert.ByIdentity())
			_ = get(s, "a")
			expect.Equal(t, s.Timestamped()[0].At, origin, "the recorder stamps the instant of the clock")
		})
	})

	t.Run("Latency", func(t *testing.T) {
		t.Parallel()

		t.Run("sleeps the latency on the clock of the stub", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				clk := assert.NewControlled(origin)
				s := stub.NewMethodStub[call](nil, getName).WithClock(clk)
				got := s.Latency(5 * time.Second)
				expect.Equal(t, got, s, "Latency returns the stub", assert.ByIdentity())
				start := time.Now()
				done := make(chan struct{})
				go func() {
					s.SleepLatency()
					close(done)
				}()
				synctest.Wait()
				clk.Advance(6 * time.Second)
				<-done
				expect.Equal(t, time.Since(start), time.Duration(0), "the sleep spends no time of the bubble")
			})
		})

		t.Run("sleeps the latency on the system clock without a clock", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				s := stub.NewMethodStub[call](nil, getName)
				s.Latency(5 * time.Millisecond)
				start := time.Now()
				s.SleepLatency()
				assert.Equal(t, time.Since(start), 5*time.Millisecond, "the sleep spends the latency")
			})
		})

		t.Run("sleeps not at all for a latency of zero", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				s := stub.NewMethodStub[call](nil, getName)
				s.Latency(5 * time.Second)
				s.SetLatency(0)
				start := time.Now()
				s.SleepLatency()
				assert.Equal(t, time.Since(start), time.Duration(0), "SetLatency of zero turns the latency off")
			})
		})
	})

	t.Run("ShouldFaultFor", func(t *testing.T) {
		t.Parallel()

		t.Run("fires not at all without a fault", func(t *testing.T) {
			t.Parallel()

			f, err := stub.NewMethodStub[call](nil, getName).ShouldFaultFor(call{})
			expect.False(t, f, "a stub without a fault fails no call")
			expect.NoError(t, err, "the stub returns no error")
		})
	})

	t.Run("FailUnexpectedCall", func(t *testing.T) {
		t.Parallel()

		t.Run("stops the test in strict mode with the method and the call", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			s.Strict()
			s.FailUnexpectedCall(call{Key: "the-key"})
			assert.Equal(t, r.Message(), "Store.Get: strict mode refuses the call, because the test configured no "+
				"answer\n  call: {Err:<nil> Key:the-key}", "the failure states the method and the call")
		})

		tests := []struct {
			name   string
			strict bool
			tb     bool
		}{
			{name: "does nothing in lenient mode", tb: true},
			{name: "does nothing without a TB", strict: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				r := assert.NewRecorder()
				var tb assert.TB
				if tt.tb {
					tb = r
				}
				s := stub.NewMethodStub[call](tb, getName)
				if tt.strict {
					s.Strict()
				}
				s.FailUnexpectedCall(call{})
				assert.False(t, r.Failed(), "the stub reports nothing")
			})
		}
	})

	t.Run("FailUnfaultable", func(t *testing.T) {
		t.Parallel()

		t.Run("stops the test with the method, the error and the call", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			stub.NewMethodStub[call](r, getName).FailUnfaultable(call{Key: "k"}, errTest)
			assert.Equal(
				t,
				r.Message(),
				"Store.Get: the test armed a fault, and the method returns no error to report "+
					"it in\n  fault: stub_test: the armed error\n  call: {Err:<nil> Key:k}",
				"the failure states the method, the fault and the call",
			)
		})

		t.Run("does nothing without a TB", func(t *testing.T) {
			t.Parallel()

			stub.NewMethodStub[call](nil, getName).FailUnfaultable(call{}, errTest)
		})
	})

	t.Run("Verify", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			times   int
			atLeast int
			calls   int
			want    string
		}{
			{name: "passes a count that Times expects", times: 2, calls: 2},
			{
				name: "reports a count that Times does not expect", times: 3, calls: 1,
				want: "Store.Get: the count of calls is 1, and the test expects 3",
			},
			{name: "passes a count at the minimum of TimesAtLeast", atLeast: 2, calls: 2},
			{
				name: "reports a count below the minimum of TimesAtLeast", atLeast: 5, calls: 1,
				want: "Store.Get: the count of calls is 1, and the test expects at least 5",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				r := assert.NewRecorder()
				s := stub.NewMethodStub[call](r, getName)
				if tt.times > 0 {
					expect.Equal(t, s.Times(tt.times), s, "Times returns the stub", assert.ByIdentity())
				}
				if tt.atLeast > 0 {
					expect.Equal(t, s.TimesAtLeast(tt.atLeast), s, "TimesAtLeast returns the stub", assert.ByIdentity())
				}
				for range tt.calls {
					_ = get(s, "a")
				}
				s.Verify()
				expect.Equal(t, r.Message(), tt.want, "the check reports the count where it differs")
			})
		}

		t.Run("does nothing without a TB", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			s.Times(99)
			s.Verify()
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("clears the calls, the fault counter and the expected counts", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			s.Faults(errTest, 2)
			s.Times(5)
			s.TimesAtLeast(5)
			_ = get(s, "a")
			s.Reset()
			expect.Equal(t, s.CallCount(), 0, "the reset clears the calls")
			expect.Equal(t, fired(s, 1), []bool{false}, "the reset rewinds the counter of the fault")
			s.Verify()
			expect.False(t, r.Failed(), "the reset clears the expected counts")
		})

		t.Run("keeps a stub without a fault", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](nil, getName)
			s.Reset()
			assert.Equal(t, s.CallCount(), 0, "the reset of a new stub keeps it empty")
		})
	})
}

// quarter is a random source whose Float64 returns 0.25 on every call.
type quarter struct{}

// Uint64 returns the bits of the number 0.25.
func (quarter) Uint64() uint64 { return 1 << 51 }

// fired asks the fault of s about n calls, and returns whether it fires for
// each.
func fired(s *stub.MethodStub[call], n int) []bool {
	out := make([]bool, 0, n)
	for range n {
		f, _ := s.ShouldFaultFor(call{})
		out = append(out, f)
	}
	return out
}
