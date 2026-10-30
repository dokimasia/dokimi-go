// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/spec"
)

// Check is one check of a suite over subjects of the type S. A check has a
// function Apply or a function Property, and not both.
//
// The decisive assertion of the check passes [Handle.Statement] as its
// message. Falsification proves the check when the first failure of the check
// on its planted defect is a failure of that assertion.
type Check[S any] struct {
	// Apply applies the check through its handle.
	Apply func(h *Handle[S])

	// Property applies the check to one case of the property engine through
	// the handle of the case. The engine runs it for each case of a run, with
	// the statement as the contract of the run, and shrinks a failure to its
	// smallest case. Each case has a handle of its own, with a fresh subject
	// and a fresh clock.
	Property func(h *Handle[S], c *prop.Case)

	// Defect returns the planted defect of the check. The defect is correct
	// for the calls of the check, except at the statement of the check.
	Defect func(h *Handle[S]) S

	// Doors are the doors of the planted defect of the check. Falsification
	// builds each handle on the defect with them: in the falsification of the
	// check, and where it applies another check to the defect. A door that
	// Doors lacks comes from the options of the falsification. An application
	// of the check to a subject reads the doors of its options alone.
	Doors Doors[S]

	// Options returns the options of the property run of the check in an
	// application, such as the shares of prop.Require. The application calls
	// it before the run, with a handle of its own whose subject is a fresh
	// subject of the factory, and passes the options after its own. The
	// application passes its own prop.OnResult after the options, and its own
	// prop.Around for a check with Around. An OnResult or an Around of Options
	// therefore does not apply. Falsification and fuzz targets leave the
	// options out. A check with Options has a function Property.
	Options func(h *Handle[S]) []prop.Option

	// Around runs each run of the check inside an environment, such as a
	// testing/synctest bubble through synctest.Test. It calls run once with
	// the test of the environment, and returns once run has returned.
	//
	// An application runs the check inside Around on the test of the check.
	// Falsification runs the check on its planted defect inside Around on the
	// test of the falsification, and so does the isolation of another planted
	// defect. A property check runs each of its cases inside Around. [Fuzz]
	// leaves a check with Around out.
	//
	// Around receives the first *testing.T of the chain of the seat: the seat
	// and each seat that its method Unwrap returns, or the test of an
	// [assert.Recorder] of WithTest. A panic of Around, such as the deadlock
	// of a bubble, is a failure of the environment. It fails an application.
	// It fails falsification when no assertion of the check failed before it.
	// The isolation of another planted defect counts it as a failure of the
	// check.
	Around func(t *testing.T, run func(*testing.T))

	// ID is the identity of the check.
	ID ID[S]

	// Statement is the sentence that the check proves of a subject.
	Statement string

	// Claim is the claim of the conformance spec that the check binds, and
	// empty for a check of the consumer. A check with a claim is a derived
	// check: an application falsifies it by the reason of its claim before it
	// applies it, and [Falsify] leaves it out.
	Claim spec.ClaimID

	// Probe is the probe of the claim, whose reasons [Handle.Expect] accepts,
	// and empty for a check of the consumer. A check with a probe engages its
	// claim through [Handle.Engage] alone.
	Probe spec.ProbeID

	// Reason is the reason code of the planted defect of the check, and empty
	// for a check of the consumer.
	Reason spec.ReasonID

	// Law is the law that the check witnesses, and empty for a check of the
	// consumer.
	Law spec.LawID

	// Machineries are the machineries of the claim, the deepest first, and nil
	// for a check of the consumer.
	Machineries []spec.MachineryID

	// Entails are the identities of the shallower checks whose laws the law
	// of the check implies. Each is an edge that the falsification of the
	// check proves in both directions.
	Entails []ID[S]

	// Counted reports that the check counts the allocations of the whole
	// process, as [assert.MaxAllocs] does. A count in a parallel test includes
	// the allocations of the tests that run concurrently. [RunBudgets] and
	// [FalsifyAllocs] therefore run the check in a test that does not run in
	// parallel, and [Run], [Falsify] and [Fuzz] leave it out.
	Counted bool

	// Shallow reports that the machineries of the claim do not include the
	// depth of its law.
	Shallow bool

	// ceiling reports that the check is a ceiling of a budget, which neither
	// the consumer nor a claim states.
	ceiling bool
}

// Run applies the check on tb to a subject that newSubject builds on the
// handle of the check. The doors of the handle and a property run read the
// options in opts. A derived check is first falsified on its planted defect,
// whose [Handle.Fresh] returns a subject of newSubject, and Run applies it to
// the subject once the defect fails it with the reason of its claim. Run fails
// tb for a check that passes without engaging its claim, as [Handle.Engage]
// states, and names the vacuous condition of the check. Under
// [Isolate] the checks that [With] adds are the other checks of the isolation
// and the edges of a derived check. Run ignores them otherwise, and ignores the
// excusals that the options add. Run ends tb with a fault that wraps
// [ErrConfig] for an invalid setting and for a check with Around on a seat
// without a test.
func (c Check[S]) Run(tb assert.TB, newSubject func(h *Handle[S]) S, opts ...Option[S]) {
	tb.Helper()
	cfg := configure(nil, opts)
	if invalid := cfg.invalid(); len(invalid) > 0 {
		assert.Fault(tb, fmt.Errorf("%w: %s", ErrConfig, invalid[0]))
		return
	}
	cfg.fresh = newSubject
	c.apply(tb, newSubject, cfg, cfg.checks, new(verdict), new(verdict))
}

// apply falsifies a derived check on its planted defect against others, with
// the result in falsified, and applies the check to a subject of source once
// the falsification proves it, with the result in judged.
func (c Check[S]) apply(
	tb assert.TB, source func(h *Handle[S]) S, cfg *config[S], others []Check[S], falsified, judged *verdict,
) {
	tb.Helper()
	if c.Claim != "" && !c.falsify(tb, cfg, others, falsified) {
		return
	}
	c.run(tb, source, cfg, judged)
}

// Falsify applies the check on tb to its planted defect. It fails tb unless
// the first failure of the check is a failure of the assertion whose message
// is the statement of the check. A check that skips before it fails skips tb.
// It also applies each check that [With] adds to the defect, and fails tb when
// the defect fails one of them, as [Falsify] applies the other checks of a
// suite. The doors of the handle read the options in opts. Falsify ends tb
// with a fault that wraps [ErrConfig] for a check with Around on a seat without
// a test.
func (c Check[S]) Falsify(tb assert.TB, opts ...Option[S]) {
	tb.Helper()
	cfg := configure(nil, opts)
	c.falsify(tb, cfg, cfg.checks, new(verdict))
}

// firsts is the state that the handles of one run of a check share: the
// message of the first failure outside an assertion, the first failed
// assertion with the reason that the probe set for it, the reason of the
// first skip, the result of a property run, and whether a handle engaged the
// claim of the check. The cases of a property check share it, so the check
// engages when one of its cases engages.
type firsts struct {
	message atomic.Pointer[string]
	failure atomic.Pointer[assert.Failure]
	reason  atomic.Pointer[spec.ReasonID]
	skip    atomic.Pointer[string]
	result  atomic.Pointer[prop.Result]
	engaged atomic.Bool
}

// run applies the check on tb to a subject of source, under the settings of
// an application. A property check with Options also takes the options of
// its run from a handle of its own on tb, after the options of the
// application. A check with Around runs inside its
// environment, and a failure of the environment fails tb. A check with Apply
// builds its handle on the test that Around hands run.
//
// A check that passes without engaging its claim is vacuous, and run fails tb
// with the vacuous condition of the check and both remedies. run sets the
// result vacuous in v, and the first failure and its reason of a check that
// failed. The test of the check sets every other result.
func (c Check[S]) run(tb assert.TB, source func(h *Handle[S]) S, cfg *config[S], v *verdict) {
	tb.Helper()
	env, ok := c.environment(tb)
	if !ok {
		return
	}
	opts := cfg.runOptions()
	if c.Property != nil && c.Options != nil {
		h := newHandle(tb, &c, source, cfg, new(firsts))
		opts = append(opts, c.Options(h)...)
	}
	if env != nil {
		opts = append(opts, prop.Around(env.wrap))
	}
	kept := new(firsts)
	defer func() {
		v.keep(kept)
		v.why = skipReason(kept)
	}()
	if env == nil || c.Property != nil {
		c.exercise(tb, source, cfg, opts, kept)
	} else if misuse := env.inside(func(t *testing.T) {
		t.Helper()
		c.exercise(t, source, cfg, opts, kept)
	}); misuse != "" {
		assert.Fault(tb, c.misused(misuse))
		return
	}
	if failure, failed := env.failure(); failed {
		tb.Errorf("the environment of %s fails: %s", c.ID, failure)
		return
	}
	if kept.engaged.Load() || kept.failed(tb) {
		return
	}
	v.result = resultVacuous
	tb.Errorf("%s never engaged: %s. Pass a subject that engages the check, or excuse the check with the "+
		"directive excuse or conform.Excuse and a reason.", c.ID, c.vacuity())
}

// failed reports whether the run of a check on tb failed: tb failed, or a
// handle kept a failure.
func (f *firsts) failed(tb assert.TB) bool {
	seat, has := tb.(interface{ Failed() bool })
	return has && seat.Failed() || f.reason.Load() != nil || f.message.Load() != nil
}

// vacuity returns the condition under which the check does not exercise its
// claim. It is the vacuous condition of the probe of the check. A check
// without a probe is vacuous when no assertion passes its statement.
func (c *Check[S]) vacuity() string {
	if probe, _ := spec.ProbeOf(c.Probe); probe.Vacuous != "" {
		return strings.ToLower(probe.Vacuous[:1]) + strings.TrimSuffix(probe.Vacuous[1:], ".")
	}
	return "no assertion ran with the statement of the check as its message"
}

// exercise applies the check on seat to subjects of source. A check with
// Apply runs once on one handle, which registers its cleanups with seat. A
// check with Property runs the property engine on seat with opts, and each
// case has a handle of its own, which registers its cleanups with the case.
// Each handle keeps the message of the first failure outside an assertion in
// kept, and the run keeps its result there.
func (c *Check[S]) exercise(
	seat assert.TB, source func(h *Handle[S]) S, cfg *config[S], opts []prop.Option, kept *firsts,
) {
	seat.Helper()
	if c.Property == nil {
		c.Apply(newHandle(seat, c, source, cfg, kept))
		return
	}
	prop.ForAll(seat, c.Statement, func(pc *prop.Case) {
		c.Property(newHandle(pc, c, source, cfg, kept), pc)
	}, append(slices.Clip(opts), prop.OnResult(func(r prop.Result) { kept.result.Store(&r) }))...)
}

// falsify applies the check on tb to its planted defect, and reports whether
// the defect proves the check. A property check runs hermetically, as
// [config.hermetic] states. Each handle on the defect takes the doors of the
// check first, as [config.onDefect] states.
//
// The defect proves a check of the consumer when it fails the check at its
// statement. It proves a derived check when it fails the check at an
// assertion for which the probe set the reason of the claim. Once the defect
// proves a check of the consumer, or a derived check under [Isolate], falsify
// applies each check of others to the defect, as [Check.isolate] states, and
// proves each edge of the check, as [Check.edges] states.
//
// A check with Around runs inside its environment. A check with Apply runs
// Rejects on the test that Around hands run, and a skip of that test skips
// the test of the environment. A failure of the environment fails tb when no
// assertion of the check failed before it.
//
// falsify sets the result of falsification in v before it fails tb, with the
// reason that the probe set and the other check of an isolation or an edge.
func (c Check[S]) falsify(tb assert.TB, cfg *config[S], others []Check[S], v *verdict) bool {
	tb.Helper()
	env, ok := c.environment(tb)
	if !ok {
		return false
	}
	cfg = cfg.onDefect(c.Doors)
	kept := new(firsts)
	defer func() { v.why = skipReason(kept) }()
	opts := cfg.hermetic(c.ID)
	if env != nil {
		opts = append(opts, prop.Around(env.wrap))
	}
	contract := "the planted defect of " + string(c.ID) + " fails the check"
	if c.Claim != "" {
		contract += " with the reason " + string(c.Reason) + " of the claim " + string(c.Claim)
	}
	rejects := func(seat assert.TB) []assert.Failure {
		seat.Helper()
		return assert.Rejects(seat, contract, func(defect assert.TB) {
			c.exercise(defect, c.Defect, cfg, opts, kept)
			switch {
			case kept.failed(defect):
			case kept.engaged.Load():
				v.set(resultPassed, kept)
			default:
				v.set(resultVacuous, kept)
				seat.Errorf("the planted defect of %s never engaged the check: %s.", c.ID, c.vacuity())
			}
		})
	}
	var failures []assert.Failure
	if env == nil || c.Property != nil {
		failures = rejects(tb)
	} else {
		var skipped bool
		misuse := env.inside(func(t *testing.T) {
			t.Helper()
			defer func() { skipped = t.Skipped() }()
			failures = rejects(t)
		})
		switch {
		case misuse != "":
			assert.Fault(tb, c.misused(misuse))
			return false
		case skipped:
			env.t.SkipNow()
			return false
		}
	}
	if len(failures) == 0 {
		v.set(resultWrongReason, kept)
		if message := kept.message.Load(); message != nil {
			c.outside(tb, *message)
			return false
		}
		if failure, failed := env.failure(); failed {
			c.outside(tb, failure)
		}
		return false
	}
	first := failures[0]
	if inner, isCase := first.CaseFailure(); isCase {
		first = inner
	}
	switch {
	case first.Assertion == "":
		v.set(resultWrongReason, kept)
		c.outside(tb, first.Contract)
		return false
	case c.Claim != "":
		var reason spec.ReasonID
		if set := kept.reason.Load(); set != nil {
			reason = *set
		}
		if reason != c.Reason {
			v.set(resultWrongReason, kept)
			c.wrongReason(tb, reason)
			return false
		}
		// The planted defect of a check of the reason panicked panics on
		// every call, and the defect of a check of the reason overran-budget
		// waits past the budget on every call, so a check that calls its
		// callable fails on it.
		if !cfg.isolate || c.Reason == spec.Panicked || c.Reason == spec.OverranBudget {
			v.set(resultProven, kept)
			return true
		}
	case first.Contract != c.Statement:
		v.set(resultWrongReason, kept)
		tb.Fatalf("the planted defect of %s fails the assertion %s with the message %q. "+
			"Falsification needs a failure of the assertion whose message is the statement %q. "+
			"Pass h.Statement() as the message of the decisive assertion, "+
			"or change the defect so that it passes the other assertions of the check.",
			c.ID, first.Assertion, first.Contract, c.Statement)
		return false
	}
	if !c.isolate(tb, cfg, others, v, kept) || !c.edges(tb, cfg, others, env, v, kept) {
		return false
	}
	v.set(resultProven, kept)
	return true
}

// skipReason returns the reason of the first skip of a run, and the empty
// string for a run that did not skip through a handle.
func skipReason(kept *firsts) string {
	if why := kept.skip.Load(); why != nil {
		return *why
	}
	return ""
}

// wrongReason fails tb for a planted defect of a derived check that fails the
// check with the reason that the probe set, which is not the reason of its
// claim.
func (c Check[S]) wrongReason(tb assert.TB, reason spec.ReasonID) {
	tb.Helper()
	with := "without a reason"
	if reason != "" {
		with = "with the reason " + string(reason)
	}
	tb.Fatalf("the planted defect of %s fails the check %s, and the claim %s needs the reason %s. "+
		"Call h.Expect with the reason before the decisive assertion of the probe.",
		c.ID, with, c.Claim, c.Reason)
}

// outside fails tb for a planted defect that fails the check with message,
// outside an assertion, before an assertion fails.
func (c Check[S]) outside(tb assert.TB, message string) {
	tb.Helper()
	tb.Fatalf("the planted defect of %s fails the check before an assertion fails. "+
		"Falsification needs a failure of the assertion whose message is the statement %q. "+
		"The check failed with this message:\n%s",
		c.ID, c.Statement, message)
}

// isolate applies each check of others to the planted defect of c, and
// reports whether the defect passes each of them. It leaves out each check
// that shares the defect, c among them, each check that the options excuse,
// each check of an edge of c, which [Check.edges] proves, and each check of
// the consumer where c is a derived check. It fails tb when
// the defect fails one of them, because a planted defect proves its check
// only when no other check fails on it. The failure lists each check that
// fails, with its first assertion or its panic. A check that skips or ends
// with a fault on the defect has no verdict on it. A check with Around runs
// inside its environment on the test of tb. isolate sets the result
// not-isolated in v with the first check that fails, before it fails tb.
func (c Check[S]) isolate(tb assert.TB, cfg *config[S], others []Check[S], v *verdict, kept *firsts) bool {
	tb.Helper()
	defect := reflect.ValueOf(c.Defect).Pointer()
	var failed []string
	var first ID[S]
	for i := range others {
		o := &others[i]
		if _, excused := cfg.excused(o.ID); excused || reflect.ValueOf(o.Defect).Pointer() == defect ||
			slices.Contains(c.Entails, o.ID) || slices.Contains(o.Entails, c.ID) || c.Claim != "" && o.Claim == "" {

			continue
		}
		env, ok := o.environment(tb)
		if !ok {
			return false
		}
		if verdict, fails := o.against(c.Defect, cfg, env); fails {
			if failed == nil {
				first = o.ID
			}
			failed = append(failed, verdict)
		}
	}
	if len(failed) > 0 {
		v.set(resultNotIsolated, kept)
		v.other = string(first)
		tb.Fatalf("the planted defect %s of %s also fails these checks:\n\t%s\n"+
			"A planted defect fails the check that it proves and no other. Change the defect so that it "+
			"passes these checks, or give each check that it fails the same defect.",
			funcName(c.Defect), c.ID, strings.Join(failed, "\n\t"))
		return false
	}
	return true
}

// edges proves each edge of c to a check of others, and reports whether each
// edge holds. Down, c fails on the planted defect of the shallower check,
// because the law of c implies its law. Up, the shallower check passes on the
// planted defect of c, because the two checks are equivalent otherwise. edges
// fails tb with each edge that does not hold. It ends tb with a fault that
// wraps [ErrConfig] for an edge to a check that others lack. env is the
// environment of c on tb. edges sets the result not-deeper or equivalent of
// the first edge that does not hold in v, with its shallower check, before it
// fails tb.
func (c Check[S]) edges(
	tb assert.TB, cfg *config[S], others []Check[S], env *environment, v *verdict, kept *firsts,
) bool {
	tb.Helper()
	var failed []string
	fail := func(result string, other ID[S], message string) {
		if failed == nil {
			v.set(result, kept)
			v.other = string(other)
		}
		failed = append(failed, message)
	}
	for _, id := range c.Entails {
		at := slices.IndexFunc(others, func(o Check[S]) bool { return o.ID == id })
		if at < 0 {
			assert.Fault(tb, fmt.Errorf("%w: %s entails %s, which the application lacks", ErrConfig, c.ID, id))
			return false
		}
		s := &others[at]
		if _, fails := c.against(s.Defect, cfg, env); !fails {
			fail(resultNotDeeper, s.ID, fmt.Sprintf("%s passes on the planted defect of %s, which it entails, so it "+
				"is not deeper", c.ID, s.ID))
		}
		shallower, ok := s.environment(tb)
		if !ok {
			return false
		}
		if _, fails := s.against(c.Defect, cfg, shallower); fails {
			fail(resultEquivalent, s.ID, fmt.Sprintf("%s fails on the planted defect of %s, which entails it, so "+
				"the two checks are equivalent", s.ID, c.ID))
		}
	}
	if len(failed) > 0 {
		tb.Fatalf("the edges of %s fail:\n\t%s\nA deeper check fails on the planted defect of each check that it "+
			"entails, and each of those checks passes on the planted defect of the deeper check.",
			c.ID, strings.Join(failed, "\n\t"))
		return false
	}
	return true
}

// against applies the check to subjects of defect on a seat of its own, as
// falsification applies a check. A check with Around runs inside env, its
// environment, on a seat with the test of env. A check without Around has a
// nil env. A check with Apply runs on a seat with the context of the test
// that Around hands run. against returns the verdict
// of the check and whether the check fails. A check that panics fails with
// the value of the panic, and so does a check whose environment fails. A
// check that ends with a fault does not fail, and neither does a check whose
// Around misuses its run. A failure of a property check names the assertion
// of its smallest case. A failure outside an assertion names its message
// alone.
func (c *Check[S]) against(defect func(h *Handle[S]) S, cfg *config[S], env *environment) (string, bool) {
	seat := assert.NewRecorder().WithGoexit()
	opts := cfg.hermetic(c.ID)
	if env != nil {
		seat.WithTest(env.t)
		opts = append(opts, prop.Around(env.wrap))
	}
	var panicked any
	exercise := func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() { panicked = recover() }()
			defer seat.RunCleanups()
			c.exercise(seat, defect, cfg, opts, new(firsts))
		}()
		<-done
	}
	if env == nil || c.Property != nil {
		exercise()
	} else if misuse := env.inside(func(t *testing.T) {
		t.Helper()
		seat.WithContext(t.Context())
		exercise()
	}); misuse != "" {
		return "", false
	}
	failure, failed := env.failure()
	switch {
	case panicked != nil:
		return fmt.Sprintf("%s panics with %v", c.ID, panicked), true
	case failed:
		return fmt.Sprintf("%s fails its environment with %q", c.ID, failure), true
	case len(seat.Faults()) > 0:
		return "", false
	case len(seat.Failures()) > 0:
		first := seat.Failures()[0]
		if inner, isCase := first.CaseFailure(); isCase {
			first = inner
		}
		if first.Assertion == "" {
			return fmt.Sprintf("%s fails with the message %q", c.ID, first.Contract), true
		}
		return fmt.Sprintf("%s fails at the assertion %s with the message %q", c.ID, first.Assertion,
			first.Contract), true
	case seat.Failed():
		return fmt.Sprintf("%s fails with the message %q", c.ID, seat.Message()), true
	}
	return "", false
}

// environment is the environment of the runs of a check with Around on the
// test t. It keeps the message of the first failure of the environment.
//
// # Concurrency
//
// Its methods are safe for concurrent use.
type environment struct {
	// around is the function Around of the check.
	around func(t *testing.T, run func(*testing.T))

	// t is the test that around receives.
	t *testing.T

	// failed is the message of the first failure of the environment, and nil
	// before one.
	failed atomic.Pointer[string]
}

// environment returns the environment of the runs of the check on tb, and nil
// for a check without Around. It ends tb with a fault that wraps [ErrConfig]
// and reports false where the chain of tb has no *testing.T.
func (c *Check[S]) environment(tb assert.TB) (*environment, bool) {
	tb.Helper()
	if c.Around == nil {
		return nil, true
	}
	t, found := testOf(tb)
	if !found {
		assert.Fault(tb, fmt.Errorf("%w: the check %s runs inside Around, and the seat is no *testing.T and wraps "+
			"none", ErrConfig, c.ID))
		return nil, false
	}
	return &environment{around: c.Around, t: t}, true
}

// misused returns the fault of a function Around of the check that misused
// its run, with the reason of the misuse.
func (c *Check[S]) misused(reason string) error {
	return fmt.Errorf("conform: Around of %s: %s", c.ID, reason)
}

// wrap calls Around with t and run. A panic of Around is a failure of the
// environment. wrap keeps the value of the panic as the message of the
// failure, and returns.
func (e *environment) wrap(t *testing.T, run func(*testing.T)) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			message := fmt.Sprint(r)
			e.failed.CompareAndSwap(nil, &message)
		}
	}()
	e.around(t, run)
}

// inside calls run once inside the environment, with the test that Around
// hands it. A second call of run and a call after Around has returned run
// nothing. inside returns the reason of a misuse of run when Around returned
// without calling run or called it a second time. It returns the empty string
// where Around called run once, and where the environment failed.
func (e *environment) inside(run func(*testing.T)) string {
	var mu sync.Mutex
	calls, open := 0, true
	e.wrap(e.t, func(t *testing.T) {
		t.Helper()
		mu.Lock()
		calls++
		first := calls == 1 && open
		mu.Unlock()
		if first {
			run(t)
		}
	})
	mu.Lock()
	defer mu.Unlock()
	open = false
	switch {
	case e.failed.Load() != nil:
		return ""
	case calls == 0:
		return "the wrapper returned without calling run"
	case calls > 1:
		return "the wrapper called run a second time"
	}
	return ""
}

// failure returns the message of the first failure of the environment, and
// whether the environment failed. A nil environment has not failed.
func (e *environment) failure() (string, bool) {
	if e == nil {
		return "", false
	}
	message := e.failed.Load()
	if message == nil {
		return "", false
	}
	return *message, true
}

// testOf returns the test that Around receives on tb: the first *testing.T of
// the chain of tb, which is tb and each seat that its method Unwrap returns. A
// seat with a method Test, such as an [assert.Recorder] of WithTest, leads to
// the test that Test returns. testOf reports false for a chain without a
// test.
func testOf(tb assert.TB) (*testing.T, bool) {
	switch s := tb.(type) {
	case *testing.T:
		return s, true
	case interface{ Test() assert.TB }:
		if t := s.Test(); t != nil {
			return testOf(t)
		}
	}
	if wrapper, wraps := tb.(interface{ Unwrap() assert.TB }); wraps {
		return testOf(wrapper.Unwrap())
	}
	return nil, false
}

// funcName returns the name of the function fn after the path of its
// package, such as vfstest.createsFailedLinks.
func funcName(fn any) string {
	name := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	return name[strings.LastIndex(name, "/")+1:]
}

// uncounted returns the checks of checks that do not count allocations, in
// their order.
func uncounted[S any](checks []Check[S]) []Check[S] {
	return slices.DeleteFunc(slices.Clone(checks), func(c Check[S]) bool { return c.Counted })
}
