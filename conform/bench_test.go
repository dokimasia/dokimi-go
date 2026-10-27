// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// errorsUnit is the unit under which a benchmark publishes the share of its
// calls that return an error.
const errorsUnit = "errors/op"

// benchtime is the flag of testing that states how long a benchmark runs.
const benchtime = "test.benchtime"

// errMissing is the error of each call of failingCalls.
var errMissing = errors.New("conform_test: the store has no value under the key")

// A benchmark of a callable measures the calls of its call generator on one
// subject, under the ceilings of its budget, and publishes the share of the
// calls that return an error.
func TestBench(t *testing.T) {
	t.Parallel()

	t.Run("Bench", func(t *testing.T) {
		t.Parallel()

		t.Run("reports no error for calls that succeed", func(t *testing.T) {
			t.Parallel()

			b := measured(t, 10, func(b conform.B) { conform.Bench(b, budgeted(conform.Budget{}), storeGet, newStore) })
			assert.Equal(t, b.metric(t, errorsUnit), 0.0, "no measured call returns an error")
		})

		t.Run("reports the share of the calls that return an error", func(t *testing.T) {
			t.Parallel()

			suite := budgeted(conform.Budget{})
			suite.Calls[0].Calls = failingCalls
			b := measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, newStore) })
			assert.Equal(t, b.metric(t, errorsUnit), 1.0, "each measured call returns an error")
		})

		t.Run("builds one subject", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			measured(t, 10, func(b conform.B) {
				conform.Bench(b, budgeted(conform.Budget{}), storeGet, counting(&built))
			})
			assert.Equal(t, built.Load(), int32(1), "the benchmark builds one subject")
		})

		warmups := []struct {
			name       string
			giveWarmup *int
			want       int32
		}{
			{name: "makes each call once before the measurement", want: 110},
			{name: "runs the warm-up of its budget before the measurement", giveWarmup: new(5), want: 15},
			{name: "measures from the first call under a warm-up of 0", giveWarmup: new(0), want: 10},
		}
		for _, tt := range warmups {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var calls atomic.Int32
				suite := budgeted(conform.Budget{Warmup: tt.giveWarmup})
				suite.Calls[0].Calls = counted(&calls)
				measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, newStore) })
				assert.Equal(t, calls.Load(), tt.want,
					"the benchmark makes the calls of its warm-up before 10 measured calls")
			})
		}

		t.Run("repeats the calls in their order", func(t *testing.T) {
			t.Parallel()

			var drawn, made []string
			suite := budgeted(conform.Budget{})
			suite.Calls[0].Calls = prop.Composite(func(c *prop.Case) func(context.Context, Store) error {
				key := c.Draw(prop.String(), "key")
				drawn = append(drawn, key)
				return func(context.Context, Store) error {
					made = append(made, key)
					return nil
				}
			})
			measured(t, 150, func(b conform.B) { conform.Bench(b, suite, storeGet, newStore) })
			assert.Length(t, made, 250, "the benchmark makes its 100 calls once, then 150 measured calls")
			assert.Equal(t, made[100:], slices.Concat(drawn, drawn[:50]),
				"the measured calls take the first call again after the last")
		})

		t.Run("takes the same calls in each run", func(t *testing.T) {
			t.Parallel()

			var first, second []string
			for _, keys := range []*[]string{&first, &second} {
				suite := budgeted(conform.Budget{})
				suite.Calls[0].Calls = keyed(keys)
				measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, newStore) })
			}
			assert.Equal(t, second, first, "the second run takes the same keys as the first")
		})

		t.Run("passes the overrides of the suite to the call generator", func(t *testing.T) {
			t.Parallel()

			var undeclared atomic.Bool
			suite := declaredBudget(&undeclared)
			suite.Calls[0].Budget = conform.Budget{}
			measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, newStore) })
			assert.False(t, undeclared.Load(), "each call draws a declared kind")
		})

		t.Run("passes the sinks of the suite to the call generator", func(t *testing.T) {
			t.Parallel()

			b := measured(t, 10, func(b conform.B) {
				conform.Bench(b, sinkBudget(conform.Budget{}), storeGet, newStore)
			})
			assert.Equal(t, b.metric(t, errorsUnit), 0.0, "each call returns after its sends")
		})

		t.Run("fails a subject over a ceiling of its budget", func(t *testing.T) {
			t.Parallel()

			suite := budgeted(conform.Budget{Latency: time.Nanosecond})
			b := measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, slow) })
			assert.Equal(t, b.verdicts(t), []string{"bench-max-latency fail"}, "the benchmark fails the ceiling of p99")
		})

		t.Run("leaves out an excused ceiling", func(t *testing.T) {
			t.Parallel()

			suite := budgeted(conform.Budget{Latency: time.Nanosecond})
			excuse := conform.Excuse(storeGet.Check("latency-budget"), "the store waits for its lock")
			b := measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, newStore, excuse) })
			assert.Empty(t, b.verdicts(t), "the benchmark states no ceiling")
		})

		t.Run("falsifies a ceiling of the option Ceilings before it measures the callable", func(t *testing.T) {
			t.Parallel()

			var defects atomic.Int32
			ceilings := conform.Ceilings(storeGet, conform.Budget{Latency: time.Millisecond})
			b := measured(t, 10, func(b conform.B) {
				conform.Bench(b, countingDefects(&defects), storeGet, newStore, ceilings)
			})
			expect.Equal(t, defects.Load(), int32(1), "the falsification builds the planted defect of the ceiling")
			want := []string{"rejects pass", "bench-max-latency fail", "bench-max-latency pass"}
			expect.Equal(t, b.verdicts(t), want, "the planted defect fails the ceiling that the subject passes")
		})

		t.Run("fails a ceiling of the option Ceilings that its planted defect does not exceed", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			ceilings := conform.Ceilings(storeGet, conform.Budget{Latency: time.Hour})
			b := measured(t, 10, func(b conform.B) {
				conform.Bench(b, unspending(), storeGet, counting(&built), ceilings)
			})
			failures := b.Failures()
			assert.Length(t, failures, 1, "the falsification fails once")
			expect.Equal(t, failures[0].Contract, "the planted defect of Store.Get/latency-budget fails the ceiling",
				"the failure states the rejection that did not happen")
			expect.Equal(t, b.verdicts(t), []string{"rejects fail", "bench-max-latency pass"},
				"the planted defect passes the ceiling")
			expect.Equal(t, built.Load(), int32(0), "the benchmark builds no subject")
		})

		t.Run("falsifies no ceiling of the declaration", func(t *testing.T) {
			t.Parallel()

			suite := unspending()
			suite.Calls[0].Budget = conform.Budget{Latency: time.Hour}
			b := measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, newStore) })
			assert.Equal(t, b.verdicts(t), []string{"bench-max-latency pass"},
				"the benchmark applies the ceiling of the declaration alone")
		})

		t.Run("falsifies no ceiling that the run does not check", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				budget := conform.Budget{Allocs: new(uint64(0)), Bytes: new(uint64(0))}
				ceilings := conform.Ceilings(storeGet, budget)
				b := measured(t, 10, func(b conform.B) { conform.Bench(b, unspending(), storeGet, newStore, ceilings) })
				assert.Equal(t, b.verdicts(t), []string{"bench-max-allocs pass", "bench-max-bytes pass"},
					"the benchmark applies both ceilings without falsifying them")
				return
			}
			expect.Contains(t, childOutputWith(t, "DOKIMI_MUTATE_INSTRUMENTED=1"), "--- PASS: "+t.Name(),
				"the benchmark passes in a run that does not count allocations")
		})

		tests := []struct {
			name         string
			giveCallable conform.Callable[Store]
			giveOpts     []conform.Option[Store]
			wantErrors   []string
		}{
			{
				name:         "reports a callable without a call in the suite",
				giveCallable: storePut,
				wantErrors:   []string{"BenchStoreSuite has no call of Store.Put"},
			},
			{
				name:         "reports an excusal that matches no check",
				giveCallable: storeGet,
				giveOpts: []conform.Option[Store]{
					conform.Excuse(storePut.Check("keeps-the-value"), "the store keeps no values"),
				},
				wantErrors: []string{"the excusal of Store.Put/keeps-the-value matches no check of BenchStoreSuite. " +
					"BenchStoreSuite has no checks."},
			},
			{
				name:         "reports a warm-up below 0",
				giveCallable: storeGet,
				giveOpts:     []conform.Option[Store]{conform.Ceilings(storeGet, conform.Budget{Warmup: new(-1)})},
				wantErrors: []string{"the budget of Store.Get states a warm-up of -1 iterations, and a warm-up is 0 " +
					"iterations or more"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var built atomic.Int32
				b := measured(t, 10, func(b conform.B) {
					conform.Bench(b, budgeted(conform.Budget{}), tt.giveCallable, counting(&built), tt.giveOpts...)
				})
				expect.Equal(t, b.Messages(), tt.wantErrors, "the benchmark reports each problem")
				expect.Equal(t, built.Load(), int32(0), "the benchmark builds no subject")
			})
		}
	})
}

// The allocation ceilings of a benchmark count the allocations of the whole
// process, so neither the test nor its subtests run in parallel.
func TestBenchAllocs(t *testing.T) {
	t.Run("Bench", func(t *testing.T) {
		t.Run("fails a subject over an allocation ceiling of its budget", func(t *testing.T) {
			skipUnchecked(t)
			suite := budgeted(conform.Budget{Allocs: new(uint64(0))})
			b := measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, squander) })
			assert.Equal(t, b.verdicts(t), []string{"bench-max-allocs fail"},
				"the benchmark fails the allocation ceiling")
		})

		t.Run("falsifies an allocation ceiling of the option Ceilings before it measures the callable",
			func(t *testing.T) {
				skipUnchecked(t)
				var defects atomic.Int32
				ceilings := conform.Ceilings(storeGet, conform.Budget{Allocs: new(uint64(0))})
				b := measured(t, 10, func(b conform.B) {
					conform.Bench(b, countingDefects(&defects), storeGet, newStore, ceilings)
				})
				expect.Equal(t, defects.Load(), int32(1), "the falsification builds the planted defect of the ceiling")
				want := []string{"rejects pass", "bench-max-allocs fail", "bench-max-allocs pass"}
				expect.Equal(t, b.verdicts(t), want, "the planted defect fails the ceiling that the subject passes")
			})
	})
}

// The variable DOKIMI_SEED of the process seeds the calls of each benchmark.
// The test sets the variable, so neither it nor its subtests run in parallel.
func TestBenchSeed(t *testing.T) {
	t.Run("takes the calls of the seed of DOKIMI_SEED", func(t *testing.T) {
		t.Setenv("DOKIMI_SEED", "7")
		var keys []string
		suite := budgeted(conform.Budget{})
		suite.Calls[0].Calls = keyed(&keys)
		measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, newStore) })
		drawn := prop.Composite(func(c *prop.Case) string { return c.Draw(prop.String(), "key") })
		assert.Equal(t, keys, prop.Values(drawn, 7, 100), "the calls draw the values of seed 7")
	})
}

// A parallel benchmark measures the calls of a callable that admits concurrent
// calls on GOMAXPROCS goroutines at once. BenchParallel needs a *testing.B,
// which testing.Benchmark runs in the test process under the flag
// -test.benchtime that the test sets. So neither the test nor its subtests run
// in parallel.
func TestBenchProcess(t *testing.T) {
	t.Run("BenchParallel", func(t *testing.T) {
		t.Run("reports no error for concurrent calls that succeed", func(t *testing.T) {
			result, failed := parallel(t, func(b *testing.B) {
				b.Helper()
				conform.BenchParallel(b, concurrent(getCalls), storeGet, newStore)
			})
			errs, published := result.Extra[errorsUnit]
			expect.False(t, failed, "the benchmark passes")
			expect.True(t, published, "the benchmark publishes errors/op")
			expect.Equal(t, errs, 0.0, "no measured call returns an error")
		})

		t.Run("reports the share of the calls that return an error", func(t *testing.T) {
			result, _ := parallel(t, func(b *testing.B) {
				b.Helper()
				conform.BenchParallel(b, concurrent(failingCalls), storeGet, newStore)
			})
			assert.Equal(t, result.Extra[errorsUnit], 1.0, "each measured call returns an error")
		})

		t.Run("runs the warm-up of its budget before the measurement", func(t *testing.T) {
			var runs []*atomic.Int32
			suite := concurrent(getCalls)
			suite.Calls[0].Budget = conform.Budget{Warmup: new(7)}
			parallel(t, func(b *testing.B) {
				b.Helper()
				conform.BenchParallel(b, suite, storeGet, func(*conform.Handle[Store]) Store {
					calls := new(atomic.Int32)
					runs = append(runs, calls)
					return tallied{memory: newMemory(), calls: calls}
				})
			})
			assert.NotEmpty(t, runs, "the benchmark builds a subject for each run")
			assert.Equal(t, runs[len(runs)-1].Load(), int32(17),
				"the last run makes 7 calls of its warm-up before 10 measured calls")
		})

		t.Run("draws the calls of each goroutine from the seed of the benchmark", func(t *testing.T) {
			var keys []string
			parallel(t, func(b *testing.B) {
				b.Helper()
				keys = nil
				conform.BenchParallel(b, concurrent(keyed(&keys)), storeGet, newStore)
			})
			assert.Length(t, keys, 100*runtime.GOMAXPROCS(0), "the last run draws 100 calls for each goroutine")
			assert.Equal(t, keys, slices.Repeat(keys[:100], runtime.GOMAXPROCS(0)),
				"each goroutine takes the calls of the seed in the order of Bench")
		})

		t.Run("passes no input to two goroutines at once", func(t *testing.T) {
			_, failed := parallel(t, func(b *testing.B) {
				b.Helper()
				conform.BenchParallel(b, concurrent(buffered), storeGet, newStore)
			})
			assert.False(t, failed, "each goroutine writes the buffers of its own calls")
		})

		t.Run("passes the overrides of the suite to the call generator", func(t *testing.T) {
			var undeclared atomic.Bool
			suite := declaredBudget(&undeclared)
			suite.Calls[0].Budget, suite.Calls[0].Concurrent = conform.Budget{}, true
			parallel(t, func(b *testing.B) {
				b.Helper()
				conform.BenchParallel(b, suite, storeGet, newStore)
			})
			assert.False(t, undeclared.Load(), "each call draws a declared kind")
		})

		t.Run("passes the sinks of the suite to the call generator", func(t *testing.T) {
			suite := sinkBudget(conform.Budget{})
			suite.Calls[0].Concurrent = true
			_, failed := parallel(t, func(b *testing.B) {
				b.Helper()
				conform.BenchParallel(b, suite, storeGet, newStore)
			})
			assert.False(t, failed, "each goroutine sends to the sink of the benchmark")
		})

		t.Run("fails a subject over a ceiling of its budget", func(t *testing.T) {
			suite := concurrent(getCalls)
			suite.Calls[0].Budget = conform.Budget{Latency: time.Nanosecond}
			_, failed := parallel(t, func(b *testing.B) {
				b.Helper()
				conform.BenchParallel(b, suite, storeGet, slow)
			})
			assert.True(t, failed, "the benchmark fails the ceiling of p99")
		})

		t.Run("reports a callable without a call in the suite", func(t *testing.T) {
			var built atomic.Int32
			_, failed := parallel(t, func(b *testing.B) {
				b.Helper()
				conform.BenchParallel(b, budgeted(conform.Budget{}), storePut, counting(&built))
			})
			expect.True(t, failed, "the benchmark fails")
			expect.Equal(t, built.Load(), int32(0), "the benchmark builds no subject")
		})

		t.Run("reports a call that admits no concurrent calls", func(t *testing.T) {
			var built atomic.Int32
			_, failed := parallel(t, func(b *testing.B) {
				b.Helper()
				conform.BenchParallel(b, concurrent(getCalls), storePut, counting(&built))
			})
			expect.True(t, failed, "the benchmark fails")
			expect.Equal(t, built.Load(), int32(0), "the benchmark builds no subject")
		})
	})
}

// benchmark is a benchmark of a fixed number of iterations. It records what
// an application reports and keeps each metric that the application
// publishes. Its FailNow and its Fatalf end the goroutine of the application.
type benchmark struct {
	*assert.Recorder

	left    int
	metrics map[string]float64
}

// measured runs run on a benchmark of the given number of iterations, on a
// goroutine of its own, and returns the benchmark once run has ended.
func measured(t *testing.T, iterations int, run func(b conform.B)) *benchmark {
	t.Helper()
	b := &benchmark{
		Recorder: assert.NewRecorder().WithGoexit().WithContext(t.Context()),
		left:     iterations,
		metrics:  map[string]float64{},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(b)
	}()
	<-done
	return b
}

// Loop reports whether an iteration is left, and takes it.
func (b *benchmark) Loop() bool {
	if b.left == 0 {
		return false
	}
	b.left--
	return true
}

// ReportMetric keeps n under unit.
func (b *benchmark) ReportMetric(n float64, unit string) {
	b.metrics[unit] = n
}

// FailNow ends the goroutine of the application.
func (*benchmark) FailNow() {
	runtime.Goexit()
}

// verdicts returns the assertion and the verdict of each call record that the
// benchmark kept, in order, as "assertion verdict".
func (b *benchmark) verdicts(t *testing.T) []string {
	t.Helper()
	records := b.Records()
	out := make([]string, 0, len(records))
	for _, line := range records {
		var record struct {
			Assertion string `json:"assertion"`
			Verdict   string `json:"verdict"`
		}
		assert.NoError(t, json.Unmarshal([]byte(line), &record), "the call record is JSON")
		out = append(out, record.Assertion+" "+record.Verdict)
	}
	return out
}

// metric returns the metric that the application published under unit.
func (b *benchmark) metric(t *testing.T, unit string) float64 {
	t.Helper()
	n, published := b.metrics[unit]
	assert.True(t, published, "the benchmark publishes "+unit)
	return n
}

// parallel runs body as a benchmark of testing for 10 measured iterations,
// and returns the result of the benchmark and whether a call of body failed.
// It sets the flag -test.benchtime of the test binary for the run.
func parallel(t *testing.T, body func(b *testing.B)) (testing.BenchmarkResult, bool) {
	t.Helper()
	before := flag.Lookup(benchtime).Value.String()
	assert.NoError(t, flag.Set(benchtime, "10x"), "-benchtime takes 10x")
	defer func() { assert.NoError(t, flag.Set(benchtime, before), "-benchtime takes its value back") }()
	var failed bool
	result := testing.Benchmark(func(b *testing.B) {
		b.Helper()
		defer func() { failed = failed || b.Failed() }()
		body(b)
	})
	return result, failed
}

// counting returns a factory of stores in memory, which counts each store that
// it builds in built.
func counting(built *atomic.Int32) func(*conform.Handle[Store]) Store {
	return func(*conform.Handle[Store]) Store {
		built.Add(1)
		return newMemory()
	}
}

// ticking is a store in memory whose Get returns once the clock of the
// process has advanced by more than a nanosecond. A call then exceeds a
// ceiling of 1ns on a clock of any resolution, also on a clock that advances
// in ticks longer than the call.
type ticking struct{ *memory }

// Get waits until the clock has advanced, and returns the value of the store
// under key.
func (s ticking) Get(key string) (string, bool) {
	start := time.Now()
	for time.Since(start) <= time.Nanosecond {
		runtime.Gosched()
	}
	return s.memory.Get(key)
}

// slow is a factory of ticking stores.
func slow(*conform.Handle[Store]) Store { return ticking{memory: newMemory()} }

// tallied is a store in memory whose Get counts its calls in calls.
type tallied struct {
	*memory

	calls *atomic.Int32
}

// Get counts the call, and returns the value of the store under key.
func (s tallied) Get(key string) (string, bool) {
	s.calls.Add(1)
	return s.memory.Get(key)
}

// countingDefects returns a suite of Store with the call of Get, which counts
// each planted defect of a ceiling that it builds in built.
func countingDefects(built *atomic.Int32) conform.Suite[Store] {
	suite := budgeted(conform.Budget{})
	over := suite.Calls[0].Over
	suite.Calls[0].Over = func(h *conform.Handle[Store], spend func()) Store {
		built.Add(1)
		return over(h, spend)
	}
	return suite
}

// keyed returns a call generator of Get, which appends the key of each call
// that it generates to keys.
func keyed(keys *[]string) prop.Generator[func(context.Context, Store) error] {
	return prop.Composite(func(c *prop.Case) func(context.Context, Store) error {
		key := c.Draw(prop.String(), "key")
		*keys = append(*keys, key)
		return func(_ context.Context, s Store) error {
			_, _ = s.Get(key)
			return nil
		}
	})
}

// buffered generates calls of Get that each write their key into a buffer of
// the call before the read, as a callable that appends its output to the
// buffer of its request does. Two goroutines that make one call write one
// buffer, which the race detector reports.
var buffered = prop.Composite(func(c *prop.Case) func(context.Context, Store) error {
	key := c.Draw(prop.String(), "key")
	buf := make([]byte, 0, len(key))
	return func(_ context.Context, s Store) error {
		buf = append(buf[:0], key...)
		_, _ = s.Get(string(buf))
		return nil
	}
})

// failingCalls generates calls of Get that each return errMissing.
var failingCalls = prop.Composite(func(c *prop.Case) func(context.Context, Store) error {
	key := c.Draw(prop.String(), "key")
	return func(_ context.Context, s Store) error {
		_, _ = s.Get(key)
		return errMissing
	}
})

// unspending returns a suite of Store with the call of Get, whose planted
// defect of a ceiling does not spend.
func unspending() conform.Suite[Store] {
	suite := budgeted(conform.Budget{})
	suite.Calls[0].Over = func(*conform.Handle[Store], func()) Store { return newMemory() }
	return suite
}

// concurrent returns a suite of Store with a call of Get from calls that
// admits concurrent calls, and a call of Put that admits none.
func concurrent(calls prop.Generator[func(context.Context, Store) error]) conform.Suite[Store] {
	suite := budgeted(conform.Budget{})
	suite.Calls[0].Calls, suite.Calls[0].Concurrent = calls, true
	put := suite.Calls[0]
	put.Callable, put.Concurrent = storePut, false
	suite.Calls = append(suite.Calls, put)
	return suite
}
