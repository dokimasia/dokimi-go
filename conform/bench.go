// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/bench"
	"go.dokimi.dev/assert/prop"
)

// benchCalls is the number of calls that a benchmark takes from the call
// generator of its callable. falsifyCalls is the number of calls of a planted
// defect that the falsification of a ceiling measures.
const (
	benchCalls   = 100
	falsifyCalls = 10
)

// errorsUnit is the unit of the share of the calls of a benchmark that return
// an error.
const errorsUnit = "errors/op"

// benchSegment is the segment of the identity of the handle of a benchmark.
const benchSegment = "benchmark"

// B is the benchmark of an application. *testing.B implements it. FailNow
// must not return.
type B interface {
	bench.B
	FailNow()
}

// Bench measures the callable of the suite on a subject of newSubject, under
// the ceilings of its budget. A generated suite calls it on the level of each
// callable of Benchmark<Subject>, and in Benchmark<Function> for the suite of
// a function.
//
// Bench builds one subject and takes 100 calls from the call generator of the
// callable, with the overrides of the suite. The functions that the factory
// registers through [Handle.Cleanup] run when the benchmark ends. The calls
// take the seed of DOKIMI_SEED, or the seed of the identity of the callable
// where the variable is not set. Each run of one seed measures the same
// calls. Bench repeats the calls in their order under a contract of
// [bench.Start] with the ceilings of the budget. The contract first runs the
// Warmup of the budget, or each call once for a budget without one, as the
// iterations of [bench.Contract.Warmup]. Bench publishes the share of the
// calls that return an error as errors/op, the calls of the warm-up included.
//
// Bench reports every problem of the application on b, as [Run] does, and
// stops b when it finds one. A callable without a call in the suite is a
// problem. The contract leaves out each ceiling that the options excuse. Bench
// falsifies each ceiling that the option [Ceilings] adds before it measures the
// callable, as [Falsify] falsifies the ceilings of the declaration. In a run
// that does not check allocation ceilings, it falsifies no ceiling of
// allocations or bytes, because the contract checks neither.
func Bench[S any](b B, suite Suite[S], callable Callable[S], newSubject func(h *Handle[S]) S, opts ...Option[S]) {
	b.Helper()
	cfg, call, found := benchmarked("Bench"+suite.Declaration+"Suite", suite, callable, opts)
	stopAt(b, found)
	h, draw, contract := measure(b, call, newSubject, cfg)
	defer h.end()
	defer contract.End()
	made, failed := repeat(h.Context(), h.Subject(), draw(), contract.Loop)
	b.ReportMetric(float64(failed)/float64(made), errorsUnit)
}

// BenchParallel measures the callable of the suite as [Bench] does, with the
// calls on GOMAXPROCS goroutines at once through [bench.Contract.RunParallel].
// A generated suite calls it on the level of each callable of
// Benchmark<Subject>Parallel.
//
// Before the measurement, BenchParallel draws 100 calls for each goroutine
// from the seed of Bench, for the GOMAXPROCS of that moment. A GOMAXPROCS
// that grows before the measurement panics the run. Each goroutine makes its own calls in the order of
// Bench, so no two goroutines pass one input to the subject, also an input
// that a call writes into. The goroutines run the iterations of the warm-up
// between them before the measurement, and share no counter while they
// measure. BenchParallel reports every problem as Bench does. A call whose
// Concurrent is false is a problem. The testing package calls the benchmark
// once for each b.N that it tries, as it does for testing.B.RunParallel, and
// each call builds a subject of its own.
func BenchParallel[S any](
	b *testing.B, suite Suite[S], callable Callable[S], newSubject func(h *Handle[S]) S, opts ...Option[S],
) {
	b.Helper()
	cfg, call, found := benchmarked("Bench"+suite.Declaration+"SuiteParallel", suite, callable, opts)
	if call != nil && !call.Concurrent {
		found = append(found, fmt.Sprintf("the call of %s admits no concurrent calls", callable))
	}
	stopAt(b, found)
	h, draw, contract := measure(b, call, newSubject, cfg)
	defer contract.End()
	s, ctx := h.Subject(), h.Context()
	own := make([][]func(ctx context.Context, s S) error, runtime.GOMAXPROCS(0))
	for i := range own {
		own[i] = draw()
	}
	var started, made, failed atomic.Int64
	contract.RunParallel(func(pb *bench.PB) {
		n, errs := repeat(ctx, s, own[started.Add(1)-1], pb.Next)
		made.Add(int64(n))
		failed.Add(int64(errs))
	})
	b.ReportMetric(float64(failed.Load())/float64(made.Load()), errorsUnit)
}

// repeat makes the calls on s in their order while next reports true, and takes
// the first call again after the last. It returns the number of calls that it
// made and the number that returned an error.
func repeat[S any](
	ctx context.Context, s S, calls []func(ctx context.Context, s S) error, next func() bool,
) (made, failed int) {
	i := 0
	for next() {
		if calls[i](ctx, s) != nil {
			failed++
		}
		made++
		i++
		if i == len(calls) {
			i = 0
		}
	}
	return made, failed
}

// benchmarked returns the configuration of opts and the call of the callable
// for the benchmark of the application name, with the problems of the
// application. A suite without a call of the callable has that problem, and
// the call is nil.
func benchmarked[S any](
	name string, suite Suite[S], callable Callable[S], opts []Option[S],
) (*config[S], *Call[S], []string) {
	cfg := configure(opts)
	cfg.using = suite.Using
	found := validate(name, suite, cfg)
	for i := range suite.Calls {
		if suite.Calls[i].Callable == callable {
			return cfg, &suite.Calls[i], found
		}
	}
	return cfg, nil, append(found, fmt.Sprintf("%s has no call of %s", name, callable))
}

// measure returns the handle and the contract of the benchmark of the call on
// b, and the function that draws its 100 calls from the seed of the callable.
// Each draw returns new calls with equal inputs. measure falsifies each
// ceiling that the option Ceilings adds, and states on the contract each
// ceiling that the options do not excuse. It also states the Warmup of the
// budget, or one iteration of each call for a budget without one.
func measure[S any](
	b B, call *Call[S], newSubject func(h *Handle[S]) S, cfg *config[S],
) (*Handle[S], func() []func(ctx context.Context, s S) error, *bench.Contract) {
	b.Helper()
	budget, added := budgetOf(call, cfg.ceilings)
	checked, _ := assert.AllocsChecked()
	contract := bench.Start(b)
	for _, l := range budget.limits() {
		if !l.stated() {
			continue
		}
		if _, excused := cfg.excused(call.Callable.Check(l.segment)); excused {
			continue
		}
		if added && (checked || !l.budget.counted()) {
			falsifyLimit(b, call, l, cfg)
		}
		l.budget.state(contract)
	}
	seed := seedOf(string(call.Callable))
	if cfg.seeded {
		seed = cfg.seed
	}
	draw := func() []func(ctx context.Context, s S) error {
		return prop.Values(call.Calls, seed, benchCalls, cfg.using...)
	}
	warmup := benchCalls
	if budget.Warmup != nil {
		warmup = *budget.Warmup
	}
	contract.Warmup(warmup)
	h := newHandle(b, &Check[S]{ID: call.Callable.Check(benchSegment)}, newSubject, cfg, new(atomic.Pointer[string]))
	return h, draw, contract
}

// falsifyLimit applies the ceiling l of the call to its planted defect, the
// subject that Over of the call returns with the spend of the ceiling. It
// measures 10 calls of the defect under a contract of the ceiling alone, and
// fails tb unless the contract fails.
func falsifyLimit[S any](tb assert.TB, call *Call[S], l limit, cfg *config[S]) {
	tb.Helper()
	id := call.Callable.Check(l.segment)
	defect := func(h *Handle[S]) S { return call.Over(h, l.budget.over()) }
	h := newHandle(tb, &Check[S]{ID: id}, defect, cfg, new(atomic.Pointer[string]))
	defer h.end()
	s, ctx := h.Subject(), h.Context()
	calls := prop.Values(call.Calls, seedOf(string(id)), falsifyCalls, cfg.using...)
	assert.Rejects(tb, "the planted defect of "+string(id)+" fails the ceiling", func(seat assert.TB) {
		contract := l.budget.state(bench.Start(&iterations{TB: seat, left: len(calls)}))
		defer contract.End()
		for i := 0; contract.Loop(); i++ {
			_ = calls[i](ctx, s)
		}
	})
}

// iterations is a benchmark of a fixed number of iterations on a seat. It
// drops what a contract publishes, because falsification reads the verdicts of
// the contract alone.
type iterations struct {
	assert.TB
	left int
}

// Loop reports whether an iteration is left, and takes it.
func (r *iterations) Loop() bool {
	if r.left == 0 {
		return false
	}
	r.left--
	return true
}

// ReportMetric drops the measurement.
func (*iterations) ReportMetric(float64, string) {}

// Unwrap returns the seat, which receives the records of the contract.
func (r *iterations) Unwrap() assert.TB {
	return r.TB
}
