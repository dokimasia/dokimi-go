// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/stub"
)

// A recorder keeps the values that a test records with the instant of each,
// reads them back and waits for them.
func TestRecorder(t *testing.T) {
	t.Parallel()

	t.Run("NewRecorder", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty recorder", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, stub.NewRecorder[int]().CallCount(), 0, "a new recorder has no values")
		})
	})

	t.Run("Record", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the values in the order of recording", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[string]()
			rec.Record("a")
			rec.Record("b")
			assert.Equal(t, rec.Calls(), []string{"a", "b"}, "the log has the values in order")
		})

		t.Run("keeps the values of concurrent goroutines", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[int]()
			var wg sync.WaitGroup
			for i := range 100 {
				wg.Go(func() { rec.Record(i) })
			}
			wg.Wait()
			assert.Equal(t, rec.CallCount(), 100, "the log has each value of the goroutines")
		})

		t.Run("keeps nothing in bench mode", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[string]()
			var seen []string
			rec.OnRecord(func(v string) { seen = append(seen, v) })
			rec.NewGate()
			rec.BenchMode()
			rec.Record("a")
			expect.Equal(t, rec.CallCount(), 0, "bench mode keeps no value and waits on no gate")
			expect.Length(t, rec.Timestamped(), 0, "bench mode stamps no instant")
			expect.Length(t, seen, 0, "bench mode runs no hook")
		})
	})

	t.Run("Calls", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a copy of the values", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[string]()
			rec.Record("a")
			calls := rec.Calls()
			calls[0] = "changed"
			assert.Equal(t, rec.Calls(), []string{"a"}, "a change of the copy leaves the log alone")
		})
	})

	t.Run("Timestamped", func(t *testing.T) {
		t.Parallel()

		t.Run("returns each value with the instant of the clock", func(t *testing.T) {
			t.Parallel()

			clk := assert.NewControlled(origin)
			rec := stub.NewRecorder[string]().WithClock(clk)
			rec.Record("a")
			clk.Advance(5 * time.Second)
			rec.Record("b")
			assert.Equal(t, rec.Timestamped(), []stub.RecordedCall[string]{
				{Value: "a", At: origin}, {Value: "b", At: origin.Add(5 * time.Second)},
			}, "each value has the instant of its recording")
		})

		t.Run("returns the instant of the system clock without a clock", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				rec := stub.NewRecorder[string]()
				rec.Record("a")
				assert.Equal(t, rec.Timestamped()[0].At, time.Now(), "the instant is the time of the bubble")
			})
		})

		t.Run("returns a copy of the entries", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[string]()
			rec.Record("a")
			entries := rec.Timestamped()
			entries[0].Value = "changed"
			assert.Equal(t, rec.Timestamped()[0].Value, "a", "a change of the copy leaves the log alone")
		})
	})

	t.Run("LastCall", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the latest value", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[string]()
			rec.Record("first")
			rec.Record("last")
			assert.Equal(t, rec.LastCall(t), "last", "the latest value is the last")
		})

		t.Run("stops the test of an empty recorder", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			got := stub.NewRecorder[string]().LastCall(r)
			expect.True(t, r.Failed(), "an empty recorder has no latest value")
			expect.Equal(t, got, "", "the call returns the zero value")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("clears the log and keeps the hooks", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[int]()
			seen := 0
			rec.OnRecord(func(int) { seen++ })
			rec.Record(1)
			rec.Reset()
			rec.Record(2)
			expect.Equal(t, rec.Calls(), []int{2}, "the log has the value after the reset alone")
			expect.Equal(t, seen, 2, "the hook runs before and after the reset")
		})
	})

	t.Run("AssertCalledOnce", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the one value", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[string]()
			rec.Record("only")
			assert.Equal(t, rec.AssertCalledOnce(t, "the recorder has one value"), "only", "the value is the one")
		})

		tests := []struct {
			name string
			give []string
			want string
		}{
			{
				name: "stops the test of an empty recorder",
				want: "once: the recorder has 0 calls, and the test expects 1",
			},
			{
				name: "stops the test of a recorder with two values", give: []string{"a", "b"},
				want: "once: the recorder has 2 calls, and the test expects 1",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				rec := stub.NewRecorder[string]()
				for _, v := range tt.give {
					rec.Record(v)
				}
				r := assert.NewRecorder()
				got := rec.AssertCalledOnce(r, "once")
				expect.Equal(t, r.Message(), tt.want, "the failure states the count")
				expect.Equal(t, got, "", "the call returns the zero value")
			})
		}
	})

	t.Run("AssertCalledN", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the values", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[int]()
			rec.Record(1)
			rec.Record(2)
			assert.Equal(
				t,
				rec.AssertCalledN(t, 2, "the recorder has two values"),
				[]int{1, 2},
				"the values are in order",
			)
		})

		t.Run("stops the test of another count", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[int]()
			rec.Record(1)
			r := assert.NewRecorder()
			got := rec.AssertCalledN(r, 5, "five")
			expect.Equal(t, r.Message(), "five: the recorder has 1 calls, and the test expects 5",
				"the failure states both counts")
			expect.Nil(t, got, "the call returns no values")
		})
	})

	t.Run("AssertNotCalled", func(t *testing.T) {
		t.Parallel()

		t.Run("passes for an empty recorder", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			stub.NewRecorder[string]().AssertNotCalled(r, "none")
			assert.False(t, r.Failed(), "an empty recorder has no calls")
		})

		t.Run("stops the test of a recorder with a value", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[string]()
			rec.Record("a")
			r := assert.NewRecorder()
			rec.AssertNotCalled(r, "none")
			assert.Equal(t, r.Message(), "none: the recorder has 1 calls, and the test expects none",
				"the failure states the count")
		})
	})

	t.Run("Filter", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the values that the function accepts", func(t *testing.T) {
			t.Parallel()

			rec := recordOneToThree()
			assert.Equal(t, rec.Filter(func(v int) bool { return v != 2 }), []int{1, 3}, "the odd values pass")
		})
	})

	t.Run("First", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the first value that the function accepts", func(t *testing.T) {
			t.Parallel()

			v, found := recordOneToThree().First(func(v int) bool { return v > 1 })
			expect.True(t, found, "a value above 1 exists")
			expect.Equal(t, v, 2, "2 is the first value above 1")
		})

		t.Run("reports false where the function accepts no value", func(t *testing.T) {
			t.Parallel()

			v, found := recordOneToThree().First(func(v int) bool { return v > 100 })
			expect.False(t, found, "no value is above 100")
			expect.Equal(t, v, 0, "the call returns the zero value")
		})
	})

	t.Run("Any", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give int
			want bool
		}{
			{name: "reports true where the function accepts a value", give: 2, want: true},
			{name: "reports false where the function accepts no value", give: 99},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got := recordOneToThree().Any(func(v int) bool { return v == tt.give })
				assert.Equal(t, got, tt.want, "the values are 1, 2 and 3")
			})
		}
	})

	t.Run("All", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give int
			want bool
		}{
			{name: "reports true where the function accepts each value", give: 4, want: true},
			{name: "reports false where the function rejects a value", give: 2},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got := recordOneToThree().All(func(v int) bool { return v < tt.give })
				assert.Equal(t, got, tt.want, "the values are 1, 2 and 3")
			})
		}

		t.Run("reports true for an empty recorder", func(t *testing.T) {
			t.Parallel()

			assert.True(t, stub.NewRecorder[int]().All(func(int) bool { return false }),
				"an empty log has no value to reject")
		})
	})

	t.Run("WaitForN", func(t *testing.T) {
		t.Parallel()

		t.Run("returns once the recorder has the values", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				rec := stub.NewRecorder[int]()
				go func() {
					time.Sleep(10 * time.Millisecond)
					rec.Record(1)
					rec.Record(2)
				}()
				rec.WaitForN(t, 2, time.Second)
				assert.Equal(t, rec.CallCount(), 2, "the recorder has the two values")
			})
		})

		t.Run("stops the test where the values do not arrive in time", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder().WithClock(assert.NewControlled(origin))
			stub.NewRecorder[int]().WaitForN(r, 5, 50*time.Millisecond)
			assert.True(t, r.Failed(), "the wait runs out without the five values")
		})
	})

	t.Run("WaitFor", func(t *testing.T) {
		t.Parallel()

		t.Run("returns once the function accepts a value", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				rec := stub.NewRecorder[string]()
				go func() {
					time.Sleep(10 * time.Millisecond)
					rec.Record("other")
					rec.Record("target")
				}()
				rec.WaitFor(t, func(v string) bool { return v == "target" }, time.Second, "the target arrives")
				assert.Equal(t, rec.CallCount(), 2, "the recorder has the target")
			})
		})

		t.Run("stops the test where no accepted value arrives in time", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder().WithClock(assert.NewControlled(origin))
			stub.NewRecorder[string]().WaitFor(r, func(v string) bool { return v == "never" }, 50*time.Millisecond,
				"the target arrives")
			assert.True(t, r.Failed(), "the wait runs out without the target")
		})
	})

	t.Run("OnRecord", func(t *testing.T) {
		t.Parallel()

		t.Run("runs each hook with each value in the order of the hooks", func(t *testing.T) {
			t.Parallel()

			rec := stub.NewRecorder[string]()
			var seen []string
			rec.OnRecord(func(v string) { seen = append(seen, "a:"+v) })
			rec.OnRecord(func(v string) { seen = append(seen, "b:"+v) })
			rec.Record("x")
			rec.Record("y")
			assert.Equal(t, seen, []string{"a:x", "b:x", "a:y", "b:y"}, "the hooks see each value in order")
		})
	})

	t.Run("IsBenchMode", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			bench bool
		}{
			{name: "reports false by default"},
			{name: "reports true after BenchMode", bench: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				rec := stub.NewRecorder[string]()
				if tt.bench {
					rec.BenchMode()
				}
				assert.Equal(t, rec.IsBenchMode(), tt.bench, "the mode is the mode that the test set")
			})
		}
	})

	t.Run("NewGate", func(t *testing.T) {
		t.Parallel()

		t.Run("holds each recording until Release", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				rec := stub.NewRecorder[string]()
				gate := rec.NewGate()
				var wg sync.WaitGroup
				wg.Go(func() { rec.Record("held") })
				synctest.Wait()
				expect.Equal(t, rec.CallCount(), 0, "the gate holds the recording")
				gate.Release()
				wg.Wait()
				expect.Equal(t, rec.CallCount(), 1, "the release lets the recording through")
			})
		})

		t.Run("lets one recording through for each ReleaseOne", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				rec := stub.NewRecorder[int]()
				gate := rec.NewGate()
				var wg sync.WaitGroup
				wg.Go(func() { rec.Record(1) })
				wg.Go(func() { rec.Record(2) })
				synctest.Wait()
				expect.Equal(t, rec.CallCount(), 0, "the gate holds both recordings")
				gate.ReleaseOne()
				synctest.Wait()
				expect.Equal(t, rec.CallCount(), 1, "one release lets one recording through")
				gate.ReleaseOne()
				wg.Wait()
				expect.Equal(t, rec.CallCount(), 2, "the second release lets the second through")
			})
		})
	})
}

// recordOneToThree returns a recorder with the values 1, 2 and 3.
func recordOneToThree() *stub.Recorder[int] {
	rec := stub.NewRecorder[int]()
	for v := 1; v <= 3; v++ {
		rec.Record(v)
	}
	return rec
}
