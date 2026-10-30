// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/spec"
)

// seedEnv is the variable of the environment that states the seed of the
// property runs of an application, in decimal or in hexadecimal after the
// prefix 0x.
const seedEnv = "DOKIMI_SEED"

// ErrConfig is the fault of an application with an invalid setting, such as
// a seed in DOKIMI_SEED that is no number.
var ErrConfig = errors.New("conform: the application has an invalid setting")

// ErrGenerated is the error that a generated function or a generated double
// returns where its declaration states no error that it may return.
var ErrGenerated = errors.New("conform: a generated error")

// Suite contains the checks of one declaration whose subjects have the type
// S.
type Suite[S any] struct {
	// Declaration is the name of the declaration, such as Store.
	Declaration string

	// Package is the import path of the package of the declaration, such as
	// example.com/store.
	Package string

	// Generator is the generator that wrote the suite.
	Generator Generator

	// Checks are the checks of the declaration.
	Checks []Check[S]

	// Refusals are the licensed claims that the generator refused.
	Refusals []Refusal

	// Unclaimed are the classifications of the callables of the declaration
	// without a check.
	Unclaimed []Unclaimed

	// Calls are the call generators of the callables of the declaration,
	// with their budgets.
	Calls []Call[S]

	// Using are the overrides of the generators of the property runs of the
	// suite, such as the members of an enumeration. Each run of a property
	// check of the suite takes them, in an application and in falsification.
	// An application passes the generators of the option [Using] after them.
	Using []prop.Option

	// Sinks are the send channel types that the inputs of the calls of the
	// suite contain. [Run], [RunBudgets], [Falsify], [FalsifyAllocs], [Fuzz],
	// [Bench] and [BenchParallel] each open every sink on the context of
	// their test. They pass the channel of each sink after the overrides of
	// Using and before the generators of the option [Using], so the option
	// replaces a sink.
	Sinks []Sink
}

// Generator is the generator that wrote a suite, with its version, such as
// dokimi v0.1.0.
type Generator struct {
	Name    string
	Version string
}

// Refusal is a licensed claim whose gate fails for a callable.
type Refusal struct {
	// Claim is the claim of the spec.
	Claim spec.ClaimID

	// Callable is the declaration and the callable, such as Store.Scan.
	Callable string

	// Why is the reason of the gate that fails.
	Why string

	// Remedy is the change that lets the gate pass.
	Remedy string
}

// Unclaimed is a classification of a callable without a check, with its
// stance.
type Unclaimed struct {
	// Classification is shape:<name>, mixin:<name> or
	// contract:<name>/<role>, with the names of the shape catalog.
	Classification string

	// Callable is the declaration and the callable, such as Store.Stale.
	Callable string

	// Stance is the stance of the spec for the classification.
	Stance spec.StanceID

	// Note is the reason of the stance for this classification.
	Note string
}

// Call is the call generator of one callable of a suite, with the budget that
// the declaration states for the callable.
type Call[S any] struct {
	// Calls generates the calls of the callable, each with the inputs that
	// its case draws. A call returns the error of the callable, and nil for a
	// callable that returns none.
	Calls prop.Generator[func(ctx context.Context, s S) error]

	// Over returns the planted defect of a ceiling of the budget: a subject
	// whose callable calls spend and returns its zero results. The runtime
	// passes a spend that exceeds the ceiling, such as one allocation more
	// than the ceiling admits.
	Over func(h *Handle[S], spend func()) S

	// Callable is the callable, such as Store.Get.
	Callable Callable[S]

	// Budget is the budget of the callable.
	Budget Budget

	// Concurrent reports that the callable admits concurrent calls, so a
	// parallel benchmark measures it.
	Concurrent bool

	// Waits reports that a call can wait until another call changes the state
	// of the subject, as the mixin waits states. A benchmark and the check of
	// a ceiling repeat the calls of a callable on one subject, so a benchmark
	// of the call and a ceiling of the call are problems of the application.
	Waits bool
}

// Budget is the budget of one call of a callable. A nil Allocs or Bytes, and a
// zero Latency or Mean, state no ceiling. Warmup states no ceiling either.
type Budget struct {
	// Allocs is the most heap allocations of one call.
	Allocs *uint64

	// Bytes is the most heap bytes that one call allocates.
	Bytes *uint64

	// Warmup is the number of iterations that a benchmark of the callable
	// runs before it measures, through [bench.Contract.Warmup]. A nil Warmup
	// runs each call of the benchmark once.
	Warmup *int

	// Latency is the highest p99 latency of one call.
	Latency time.Duration

	// Mean is the highest mean latency of one call. A budget with a Mean has
	// a Latency as well, because a mean alone passes a tail that grows.
	Mean time.Duration
}

// Option configures an application of a suite. The zero Option configures
// nothing.
type Option[S any] struct {
	set func(c *config[S])
}

// With adds the checks to an application. The application falsifies each
// added check before it applies the check to the subject.
func With[S any](checks ...Check[S]) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.checks = append(c.checks, checks...) }}
}

// Excuse excuses the check with the identity id from an application. The
// application skips the check with the reason. It fails when the identity
// matches no check of the application, and when the reason is empty.
func Excuse[S any](id ID[S], reason string) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.excusals = append(c.excusals, excusal[S]{id: id, reason: reason}) }}
}

// Reference supplies the reference implementations of an application.
// [Handle.Reference] returns a fresh one from newReference.
func Reference[S any](newReference func(h *Handle[S]) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.doors.Reference = newReference }}
}

// Second supplies the second subjects of an application. [Handle.Second]
// returns a fresh one from newSecond.
func Second[S any](newSecond func(h *Handle[S]) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.doors.Second = newSecond }}
}

// Restartable supplies the restart of the subjects of an application.
// [Handle.Restart] returns the subject that restart returns.
func Restartable[S any](restart func(h *Handle[S], s S) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.doors.Restart = restart }}
}

// Crashable supplies the crash of the subjects of an application.
// [Handle.Crash] returns the subject that crash returns.
func Crashable[S any](crash func(h *Handle[S], s S, call func()) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.doors.Crash = crash }}
}

// Cases sets the number of valid cases that each property run of an
// application aims for, 100 by default. An application reports fewer than one
// case as a problem.
func Cases[S any](n int) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.cases = &n }}
}

// Using states g as the generator of T in the property runs, the fuzz targets
// and the benchmarks of an application, as [prop.Using] states it for a run.
// The application passes g after the overrides of the suite, so g replaces the
// override of the suite for T. A later option of one type replaces an earlier
// one. A consumer states the values of a type that the suite cannot build,
// such as a struct whose fields only a constructor of its package sets:
//
//	storetest.RunStoreSuite(t, storetest.NewInMemory,
//		conform.Using[store.Store](prop.Just(store.DefaultLimits())))
func Using[S, T any](g prop.Generator[T]) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.using = append(c.using, prop.Using(g)) }}
}

// Isolate makes an application run every other check on the planted defect of
// each derived check, and prove each edge of a derived check in both
// directions. The corpus passes it for the subject of each fixture, which is a
// correct implementation. Without it an application falsifies a derived check
// by the reason of its claim alone, because a defect of the subject of a
// consumer would fail every planted defect that wraps the subject.
func Isolate[S any]() Option[S] {
	return Option[S]{set: func(c *config[S]) { c.isolate = true }}
}

// Ceilings states the budget of the callable for one application, for a
// callable whose declaration states no budget. Each ceiling of the budget is a
// check of that application alone, which the application falsifies before it
// applies it.
func Ceilings[S any](callable Callable[S], budget Budget) Option[S] {
	return Option[S]{set: func(c *config[S]) {
		c.ceilings = append(c.ceilings, stated[S]{callable: callable, budget: budget})
	}}
}

// config is the configuration that the options of an application and the
// environment set.
type config[S any] struct {
	// doors are the doors of the handle. A door without its option is nil.
	doors Doors[S]

	// fresh is the factory of the application, whose subjects
	// [Handle.Fresh] returns, and nil outside an application.
	fresh func(h *Handle[S]) S

	// cases is the number of valid cases of the option Cases, and nil
	// without the option.
	cases *int

	// seedText is the value of DOKIMI_SEED where the value is no seed, and
	// nil otherwise.
	seedText *string

	// checks are the checks that With adds.
	checks []Check[S]

	// excusals are the excusals of Excuse in the order of the options.
	excusals []excusal[S]

	// ceilings are the budgets of the option Ceilings in the order of the
	// options.
	ceilings []stated[S]

	// using are the overrides of the suite, followed by the generators of the
	// option Using in the order of the options.
	using []prop.Option

	// seed is the seed of DOKIMI_SEED where seeded is true, and runSeed the
	// seed of the property runs of an application where runSeeded is true.
	seed      uint64
	runSeed   uint64
	seeded    bool
	runSeeded bool

	// defect reports that the handles of the configuration build on a planted
	// defect, whose doors come from its check first.
	defect bool

	// isolate reports the option Isolate.
	isolate bool
}

// onDefect returns a copy of c for the handles on a planted defect with the
// doors d of its check. Each door that d lacks comes from c.
func (c *config[S]) onDefect(d Doors[S]) *config[S] {
	defect := *c
	defect.doors, defect.defect = d.over(c.doors), true
	return &defect
}

// excusal is the excusal of the check with the identity id.
type excusal[S any] struct {
	id     ID[S]
	reason string
}

// excused returns the reason of the excusal of the check with the identity
// id, and whether the options excuse the check.
func (c *config[S]) excused(id ID[S]) (string, bool) {
	for _, e := range c.excusals {
		if e.id == id {
			return e.reason, true
		}
	}
	return "", false
}

// skipExcused skips tb with the reason of the excusal when the options excuse
// the check with the identity id.
func (c *config[S]) skipExcused(tb testing.TB, id ID[S]) {
	tb.Helper()
	if reason, excused := c.excused(id); excused {
		tb.Skipf("%s is excused: %s", id, reason)
	}
}

// stated is the budget that the option Ceilings states for a callable.
type stated[S any] struct {
	callable Callable[S]
	budget   Budget
}

// configure returns the configuration that opts and the variable DOKIMI_SEED
// set, with the overrides using of the suite before the generators of opts.
func configure[S any](using []prop.Option, opts []Option[S]) *config[S] {
	c := &config[S]{using: slices.Clip(using)}
	for _, o := range opts {
		if o.set != nil {
			o.set(c)
		}
	}
	if text, set := os.LookupEnv(seedEnv); set {
		digits, base := text, 10
		if hex, isHex := strings.CutPrefix(text, "0x"); isHex {
			digits, base = hex, 16
		}
		seed, err := strconv.ParseUint(digits, base, 64)
		if err != nil {
			c.seedText = &text
		} else {
			c.seed, c.seeded = seed, true
		}
	}
	return c
}

// invalid returns the invalid settings of the configuration.
func (c *config[S]) invalid() []string {
	var found []string
	if c.seedText != nil {
		found = append(found, fmt.Sprintf("%s is %q, and a seed is a decimal or a 0x hexadecimal number below 2^64",
			seedEnv, *c.seedText))
	}
	if c.cases != nil && *c.cases < 1 {
		found = append(found, fmt.Sprintf("the option Cases asks for %d cases, and a property run needs at least 1",
			*c.cases))
	}
	return found
}

// runOptions returns the options of a property run of an application: the
// overrides of the suite, the generators of the option Using, the cases of the
// option Cases, and the seed of the application or of DOKIMI_SEED. A run
// without a seed takes the seed of the profile of the property engine.
func (c *config[S]) runOptions() []prop.Option {
	opts := slices.Clip(c.using)
	if c.cases != nil {
		opts = append(opts, prop.Cases(*c.cases))
	}
	switch {
	case c.runSeeded:
		opts = append(opts, prop.Seed(c.runSeed))
	case c.seeded:
		opts = append(opts, prop.Seed(c.seed))
	}
	return opts
}

// applicationSeed returns the seed of the property runs of the application
// name, and passes it to each of them: the seed of DOKIMI_SEED, and otherwise
// the seed that prop.RunSeed returns for name. It also returns the text of
// each fault of prop.RunSeed, such as a malformed DOKIMI_ASSERT_PROP_SEED, as
// a problem of the application.
func (c *config[S]) applicationSeed(name string) (uint64, []string) {
	c.runSeed, c.runSeeded = c.seed, true
	var found []string
	if !c.seeded {
		r := assert.NewRecorder()
		c.runSeed = prop.RunSeed(r, name)
		for _, err := range r.Faults() {
			found = append(found, err.Error())
		}
	}
	return c.runSeed, found
}

// hermetic returns the options of a property run of falsification for the
// check with the identity id: the overrides of the suite, the generators of the
// option Using, a hermetic run with a seed of the identity, no store and no
// shrinking. So each falsification of
// one check runs the same cases, writes no stored case, and ends at the first
// failing case.
func (c *config[S]) hermetic(id ID[S]) []prop.Option {
	return append(slices.Clip(c.using), prop.Hermetic(), prop.Seed(seedOf(string(id))), prop.Store(""), prop.Shrink(0))
}

// seedOf returns the seed of the text, its 64-bit FNV-1a hash.
func seedOf(text string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return h.Sum64()
}
