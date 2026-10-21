// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The forms of an application. The name of a generated function of a suite
// starts with its form, such as RunStore and ControlStoreOn.
const (
	formRun     = "Run"
	formControl = "Control"
)

// T is the test of an application. *testing.T implements it. FailNow must
// not return.
type T interface {
	Helper()
	Errorf(format string, args ...any)
	FailNow()
	Run(name string, f func(t *testing.T)) bool
}

// Verify applies each check of the suite, and each check that opts add, to a
// fresh subject of newSubject.
//
// Verify reports every problem of the checks and the options on t, and stops
// t when it finds one. Otherwise it runs each check as a parallel test of t,
// named by the identity of the check. The test of an excused check skips with
// the reason of the excusal. The test of a check that opts add falsifies the
// check before it applies the check to the subject.
func Verify[S any](t T, suite Suite[S], newSubject func(h *Handle[S]) S, opts ...Option[S]) {
	t.Helper()
	apply(t, formRun, suite, newSubject, opts)
}

// Control applies the same checks as [Verify] to a fresh control of
// newControl. A control is a correct implementation that differs from the
// subject.
func Control[S any](t T, suite Suite[S], newControl func(h *Handle[S]) S, opts ...Option[S]) {
	t.Helper()
	apply(t, formControl, suite, newControl, opts)
}

// apply applies the checks of the suite and of opts to the subjects of
// source, for an application of the form form.
func apply[S any](t T, form string, suite Suite[S], source func(h *Handle[S]) S, opts []Option[S]) {
	t.Helper()
	cfg := configure(opts)
	checks := append(slices.Clip(suite.Checks), cfg.checks...)
	if found := problems(form+suite.Declaration, checks, cfg.excusals); len(found) > 0 {
		for _, problem := range found {
			t.Errorf("%s", problem)
		}
		t.FailNow()
	}
	excused := make(map[ID[S]]string, len(cfg.excusals))
	for _, e := range cfg.excusals {
		excused[e.id] = e.reason
	}
	for i, c := range checks {
		t.Run(string(c.ID), func(t *testing.T) {
			t.Helper()
			t.Parallel()
			if reason, ok := excused[c.ID]; ok {
				t.Skipf("%s is excused: %s", c.ID, reason)
			}
			if i >= len(suite.Checks) {
				c.falsify(t, cfg, form)
			}
			c.run(t, source, cfg, form)
		})
	}
}

// problems returns the problems of the checks and the excusals of the
// application name.
func problems[S any](name string, checks []Check[S], excusals []excusal[S]) []string {
	var found []string
	seen := make(map[ID[S]]bool, len(checks))
	ids := make([]string, 0, len(checks))
	for _, c := range checks {
		if !grammar.MatchString(string(c.ID)) {
			found = append(found, fmt.Sprintf("the identity %q of a check does not have the form "+
				"Declaration.Callable/segment, as in Store.Get/keeps-the-value", c.ID))
		}
		if c.Statement == "" {
			found = append(found, fmt.Sprintf("the check %s has no statement", c.ID))
		}
		if c.Apply == nil {
			found = append(found, fmt.Sprintf("the check %s has no function Apply", c.ID))
		}
		if c.Defect == nil {
			found = append(found, fmt.Sprintf("the check %s has no planted defect", c.ID))
		}
		if seen[c.ID] {
			found = append(found, fmt.Sprintf("two checks of %s have the identity %s", name, c.ID))
		}
		seen[c.ID] = true
		ids = append(ids, string(c.ID))
	}
	for _, e := range excusals {
		if !seen[e.id] {
			checksOf := name + " has no checks."
			if len(ids) > 0 {
				checksOf = "The checks of " + name + " are:\n\t" + strings.Join(ids, "\n\t")
			}
			found = append(found, fmt.Sprintf("the excusal of %s matches no check of %s. %s", e.id, name, checksOf))
		}
		if e.reason == "" {
			found = append(found, fmt.Sprintf("the excusal of %s has no reason", e.id))
		}
	}
	return found
}
