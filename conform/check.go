// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"

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
// setting.
func (c Check[S]) Run(tb assert.TB, newSubject func(h *Handle[S]) S, opts ...Option[S]) {
	tb.Helper()
	cfg := configure(opts)
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
// suite. The doors of the handle read the options in opts.
func (c Check[S]) Falsify(tb assert.TB, opts ...Option[S]) {
	tb.Helper()
	cfg := configure(opts)
	c.falsify(tb, cfg, cfg.checks)
}

// run applies the check on tb to a subject of source, under the settings of
// an application.
func (c Check[S]) run(tb assert.TB, source func(h *Handle[S]) S, cfg *config[S]) {
	tb.Helper()
	c.exercise(tb, source, cfg, cfg.runOptions(), new(atomic.Pointer[string]))
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
// runs hermetically, as [config.hermetic] states. Once the defect fails the
// check at its statement, falsify applies each check of others to the
// defect, as [Check.isolate] states.
func (c Check[S]) falsify(tb assert.TB, cfg *config[S], others []Check[S]) {
	tb.Helper()
	kept := new(atomic.Pointer[string])
	failures := assert.Rejects(tb, "the planted defect of "+string(c.ID)+" fails the check", func(seat assert.TB) {
		c.exercise(seat, c.Defect, cfg, cfg.hermetic(c.ID), kept)
	})
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
func (c Check[S]) isolate(tb assert.TB, cfg *config[S], others []Check[S]) {
	tb.Helper()
	defect := reflect.ValueOf(c.Defect).Pointer()
	var failed []string
	for i := range others {
		o := &others[i]
		if _, excused := cfg.excused(o.ID); excused || reflect.ValueOf(o.Defect).Pointer() == defect {
			continue
		}
		if verdict, fails := o.against(c.Defect, cfg); fails {
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
// falsification applies a check. It returns the verdict of the check and
// whether the check fails. A check that panics fails with the value of the
// panic, and a check that ends with a fault does not fail. A failure of a
// property check names the assertion of its smallest case.
func (c *Check[S]) against(defect func(h *Handle[S]) S, cfg *config[S]) (string, bool) {
	seat := assert.NewRecorder().WithGoexit()
	var panicked any
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { panicked = recover() }()
		c.exercise(seat, defect, cfg, cfg.hermetic(c.ID), new(atomic.Pointer[string]))
	}()
	<-done
	switch {
	case panicked != nil:
		return fmt.Sprintf("%s panics with %v", c.ID, panicked), true
	case len(seat.Faults()) > 0:
		return "", false
	case len(seat.Failures()) > 0:
		first := seat.Failures()[0]
		if inner, isCase := first.CaseFailure(); isCase {
			first = inner
		}
		return fmt.Sprintf("%s fails at the assertion %s with the message %q", c.ID, first.Assertion,
			first.Contract), true
	case seat.Failed():
		return fmt.Sprintf("%s fails with the message %q", c.ID, seat.Message()), true
	}
	return "", false
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
