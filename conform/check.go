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
	// subject of the factory, and passes the options after its own.
	// Falsification and fuzz targets leave the options out. A check with
	// Options has a function Property.
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

	// Counted reports that the check counts the allocations of the whole
	// process, as [assert.MaxAllocs] does. A count in a parallel test includes
	// the allocations of the tests that run concurrently. [RunBudgets] and
	// [FalsifyAllocs] therefore run the check in a test that does not run in
	// parallel, and [Run], [Falsify] and [Fuzz] leave it out.
	Counted bool
}

// Run applies the check on tb to a subject that newSubject builds on the
// handle of the check. The doors of the handle and a property run read the
// options in opts. Run ignores the checks and the excusals that the options
// add. Run ends tb with a fault that wraps [ErrConfig] for an invalid
// setting and for a check with Around on a seat without a test.
func (c Check[S]) Run(tb assert.TB, newSubject func(h *Handle[S]) S, opts ...Option[S]) {
	tb.Helper()
	cfg := configure(nil, opts)
	if invalid := cfg.invalid(); len(invalid) > 0 {
		assert.Fault(tb, fmt.Errorf("%w: %s", ErrConfig, invalid[0]))
		return
	}
	c.run(tb, newSubject, cfg)
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
	c.falsify(tb, cfg, cfg.checks)
}

// run applies the check on tb to a subject of source, under the settings of
// an application. A property check with Options also takes the options of
// its run from a handle of its own, after the options of the application. The
// handle ends once Options returns. A check with Around runs inside its
// environment, and a failure of the environment fails tb. A check with Apply
// builds its handle on the test that Around hands run.
func (c Check[S]) run(tb assert.TB, source func(h *Handle[S]) S, cfg *config[S]) {
	tb.Helper()
	env, ok := c.environment(tb)
	if !ok {
		return
	}
	opts := cfg.runOptions()
	if c.Property != nil && c.Options != nil {
		h := newHandle(tb, &c, source, cfg, new(atomic.Pointer[string]))
		opts = append(opts, c.Options(h)...)
		h.end()
	}
	if env != nil {
		opts = append(opts, prop.Around(env.wrap))
	}
	kept := new(atomic.Pointer[string])
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
	}
}

// exercise applies the check on seat to subjects of source. A check with
// Apply runs once on one handle, which ends when Apply returns or stops. A
// check with Property runs the property engine on seat with opts, and each
// case has a handle of its own, which registers its cleanups with the case.
// Each handle keeps the message of the first failure outside an assertion in
// kept.
func (c *Check[S]) exercise(
	seat assert.TB, source func(h *Handle[S]) S, cfg *config[S], opts []prop.Option, kept *atomic.Pointer[string],
) {
	seat.Helper()
	if c.Property == nil {
		h := newHandle(seat, c, source, cfg, kept)
		defer h.end()
		c.Apply(h)
		return
	}
	prop.ForAll(seat, c.Statement, func(pc *prop.Case) {
		c.Property(newHandle(pc, c, source, cfg, kept), pc)
	}, opts...)
}

// falsify applies the check on tb to its planted defect. A property check
// runs hermetically, as [config.hermetic] states. Each handle on the defect
// takes the doors of the check first, as [config.onDefect] states. Once the
// defect fails the check at its statement, falsify applies each check of
// others to the defect, as [Check.isolate] states.
//
// A check with Around runs inside its environment. A check with Apply runs
// Rejects on the test that Around hands run, and a skip of that test skips
// the test of the environment. A failure of the environment fails tb when no
// assertion of the check failed before it.
func (c Check[S]) falsify(tb assert.TB, cfg *config[S], others []Check[S]) {
	tb.Helper()
	env, ok := c.environment(tb)
	if !ok {
		return
	}
	cfg = cfg.onDefect(c.Doors)
	kept := new(atomic.Pointer[string])
	opts := cfg.hermetic(c.ID)
	if env != nil {
		opts = append(opts, prop.Around(env.wrap))
	}
	rejects := func(seat assert.TB) []assert.Failure {
		seat.Helper()
		return assert.Rejects(seat, "the planted defect of "+string(c.ID)+" fails the check", func(seat assert.TB) {
			c.exercise(seat, c.Defect, cfg, opts, kept)
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
			return
		case skipped:
			env.t.SkipNow()
			return
		}
	}
	if len(failures) > 0 {
		first := failures[0]
		if inner, isCase := first.CaseFailure(); isCase {
			first = inner
		}
		switch {
		case first.Assertion == "":
			c.outside(tb, first.Contract)
		case first.Contract != c.Statement:
			tb.Fatalf("the planted defect of %s fails the assertion %s with the message %q. "+
				"Falsification needs a failure of the assertion whose message is the statement %q. "+
				"Pass h.Statement() as the message of the decisive assertion, "+
				"or change the defect so that it passes the other assertions of the check.",
				c.ID, first.Assertion, first.Contract, c.Statement)
		default:
			c.isolate(tb, cfg, others)
		}
		return
	}
	if message := kept.Load(); message != nil {
		c.outside(tb, *message)
		return
	}
	if failure, failed := env.failure(); failed {
		c.outside(tb, failure)
	}
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

// isolate applies each check of others to the planted defect of c, except
// each check that shares the defect, c among them, and each check that the
// options excuse. It fails tb when the defect fails one of them, because a
// planted defect proves its check only when no other check fails on it. The
// failure lists each check that fails, with its first assertion or its panic.
// A check that skips or ends with a fault on the defect has no verdict on it.
// A check with Around runs inside its environment on the test of tb.
func (c Check[S]) isolate(tb assert.TB, cfg *config[S], others []Check[S]) {
	tb.Helper()
	defect := reflect.ValueOf(c.Defect).Pointer()
	var failed []string
	for i := range others {
		o := &others[i]
		if _, excused := cfg.excused(o.ID); excused || reflect.ValueOf(o.Defect).Pointer() == defect {
			continue
		}
		env, ok := o.environment(tb)
		if !ok {
			return
		}
		if verdict, fails := o.against(c.Defect, cfg, env); fails {
			failed = append(failed, verdict)
		}
	}
	if len(failed) > 0 {
		tb.Fatalf("the planted defect %s of %s also fails these checks:\n\t%s\n"+
			"A planted defect fails the check that it proves and no other. Change the defect so that it "+
			"passes these checks, or give each check that it fails the same defect.",
			funcName(c.Defect), c.ID, strings.Join(failed, "\n\t"))
	}
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
			c.exercise(seat, defect, cfg, opts, new(atomic.Pointer[string]))
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
