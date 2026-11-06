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

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
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

	// Gate is the gate of the spec that fails for the callable.
	Gate spec.GateID

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

	// RunsUntilCancelled reports that a call runs until its context ends, as
	// the mixin runs-until-cancelled states. A benchmark and the check of a
	// ceiling call the callable with a context that does not end, so a
	// benchmark of the call and a ceiling of the call are problems of the
	// application.
	RunsUntilCancelled bool
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
// matches no check of the application, and when the reason is empty. The
// excusal of a check also excuses the check that generates its inputs, such as
// Store.Get/keeps-the-value#generated for Store.Get/keeps-the-value. The
// excusal of a generated check excuses that check alone.
func Excuse[S any](id ID[S], reason string) Option[S] {
	return Option[S]{set: func(c *config[S]) {
		c.excusals = append(c.excusals, excusal[S]{id: id, generated: id.Generated(), reason: reason})
	}}
}

// ExcuseSuite excuses each check of the field Checks of the suite from an
// application. The application skips each such check with the reason. It does
// not excuse a check that [With] adds or a ceiling of a budget. An application
// fails when the reason is empty, and when the suite has no check. A later
// ExcuseSuite replaces the reason of an earlier one.
func ExcuseSuite[S any](reason string) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.suiteReason = &reason }}
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
	return Option[S]{set: func(c *config[S]) { c.doors.Restart, c.restartable = restart, true }}
}

// Crashable supplies the crash of the subjects of an application.
// [Handle.Crash] returns the subject that crash returns.
func Crashable[S any](crash func(h *Handle[S], s S, call func()) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.doors.Crash, c.crashable = crash, true }}
}

// Durable sets the door Open of an application to open, which opens a subject
// over a medium and recovers a subject from a medium after a crash.
// [Handle.Open] returns the subject that open returns. The handles of the
// application open each subject over a new medium.Memory, and keep the medium
// of each subject that they open over a medium in memory. [Handle.Restart]
// opens a subject again over its medium. [Handle.Crash] runs its call,
// crashes the medium of the subject, and opens the subject again over what
// survived. Durable sets the doors Restart and Crash this way, so an
// application with Durable and with Restartable or Crashable has an invalid
// setting.
func Durable[S any](open func(h *Handle[S], m medium.FS) S) Option[S] {
	return Option[S]{set: func(c *config[S]) {
		c.doors.Open, c.open = open, open
		c.doors.Restart = func(h *Handle[S], s S) S { return h.Open(h.mediumOf(s)) }
		c.doors.Crash = func(h *Handle[S], s S, call func()) S {
			call()
			m := h.mediumOf(s)
			m.Crash()
			return h.Open(m)
		}
	}}
}

// Hosted sets the door Host of an application to open, which opens a subject
// over the environment of an incarnation of a host: its lifetime, its disk,
// its clock, its network and its dependencies. [Handle.Host] returns the
// subject that open returns, and [Handle.FreshHost] returns it also on a
// planted defect. The world of a check opens each incarnation of the subject
// through the door, and opens a subject without it over the disk of the
// incarnation through the door Open of [Durable].
func Hosted[S any](open func(h *Handle[S], env *host.Env) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.doors.Host, c.host = open, open }}
}

// Tasks states that each subject of an application blocks only through its
// host: its goroutines, its sleeps, its timers, its waits, its disk, its
// network and its dependencies. The world of a check then runs the host under
// a task scheduler of the case, and a replay of a case repeats the whole case.
func Tasks[S any]() Option[S] {
	return Option[S]{set: func(c *config[S]) { c.mode = spec.ModeTasks }}
}

// Blocking states that no goroutine of a subject of an application waits on a
// channel, on a sleep, on the disk or on the network while it has locked a
// mutex that another goroutine waits for. The world of a check then strikes the
// faults that make a goroutine wait for a later step of the world, such as a
// hold of the disk, without a mode of tasks.
func Blocking[S any]() Option[S] {
	return Option[S]{set: func(c *config[S]) { c.blocking = true }}
}

// Faultable supplies the subjects of an application whose dependencies fail
// on demand. A handle builds each subject of a check of the machinery
// call-that-fails-on-demand from newFaulty, with the injector of the handle.
func Faultable[S any](newFaulty func(h *Handle[S], inj *Injector) S) Option[S] {
	return Option[S]{set: func(c *config[S]) { c.doors.Faults = newFaulty }}
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

	// open is the door Open of the option Durable, which [Handle.FreshOpen]
	// calls also on a planted defect, and nil without the option.
	open func(h *Handle[S], m medium.FS) S

	// host is the door Host of the option Hosted, which [Handle.FreshHost]
	// calls also on a planted defect, and nil without the option.
	host func(h *Handle[S], env *host.Env) S

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

	// suiteReason is the reason of the option ExcuseSuite, and nil without the
	// option. suite contains the identity of each check of the suite under the
	// option.
	suiteReason *string
	suite       map[ID[S]]bool

	// ceilings are the budgets of the option Ceilings in the order of the
	// options.
	ceilings []stated[S]

	// mode is the execution mode of the worlds of the application: tasks
	// under the option Tasks, and native otherwise.
	mode spec.Mode

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

	// restartable and crashable report that the options Restartable and
	// Crashable set the doors Restart and Crash.
	restartable, crashable bool

	// isolate reports the option Isolate, and blocking the option Blocking.
	isolate, blocking bool
}

// onDefect returns a copy of c for the handles on a planted defect with the
// doors d of its check. Each door that d lacks comes from c.
func (c *config[S]) onDefect(d Doors[S]) *config[S] {
	defect := *c
	defect.doors, defect.defect = d.over(c.doors), true
	return &defect
}

// excusal is the excusal of the check with the identity id. It also excuses
// the check with the identity generated, which generates the inputs of the
// check.
type excusal[S any] struct {
	id        ID[S]
	generated ID[S]
	reason    string
}

// excused returns the reason of the excusal of the check with the identity
// id, and whether the options excuse the check.
func (c *config[S]) excused(id ID[S]) (string, bool) {
	for _, e := range c.excusals {
		if e.id == id || e.generated == id {
			return e.reason, true
		}
	}
	if c.suite[id] {
		return *c.suiteReason, true
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
// checks are the checks of the suite, which the option ExcuseSuite excuses.
func configure[S any](using []prop.Option, checks []Check[S], opts []Option[S]) *config[S] {
	c := &config[S]{using: slices.Clip(using)}
	for _, o := range opts {
		if o.set != nil {
			o.set(c)
		}
	}
	if c.suiteReason != nil {
		c.suite = make(map[ID[S]]bool, len(checks))
		for i := range checks {
			c.suite[checks[i].ID] = true
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
	if c.open != nil && c.restartable {
		found = append(found, "the application passes conform.Durable and conform.Restartable, and Durable "+
			"restarts each subject over its medium")
	}
	if c.open != nil && c.crashable {
		found = append(found, "the application passes conform.Durable and conform.Crashable, and Durable "+
			"crashes each subject over its medium")
	}
	return found
}

// factory returns the factory of the subjects of an application: newSubject,
// or under the option Durable a factory that opens each subject over a new
// medium.Memory through the door Open of the option.
func (c *config[S]) factory(newSubject func(h *Handle[S]) S) func(h *Handle[S]) S {
	if c.open == nil {
		return newSubject
	}
	return func(h *Handle[S]) S { return h.FreshOpen(medium.NewMemory()) }
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
