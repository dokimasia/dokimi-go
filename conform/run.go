// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// T is the test of an application. *testing.T implements it. FailNow must
// not return.
type T interface {
	Helper()
	Errorf(format string, args ...any)
	FailNow()
	Run(name string, f func(t *testing.T)) bool
}

// Run applies each check of the suite, and each check that opts add, to a
// fresh subject of newSubject.
//
// Run reports every problem of the checks, the calls and the options on t, and
// stops t when it finds one. An excusal may name any check of the application
// or any ceiling of a budget, because each entry point of a suite takes the
// same options. Otherwise Run runs each check as a parallel test of t. The
// test has the identity of the check without the name of the declaration,
// such as Get/keeps-the-value, because the name of t contains the
// declaration. The test of an excused check skips with the reason of the
// excusal. The test of a check that opts add falsifies the check before it
// applies the check to the subject, as [Falsify] does, against every other
// check of the application that opts do not excuse. Run leaves out each check
// that counts allocations, which [RunBudgets] runs.
func Run[S any](t T, suite Suite[S], newSubject func(h *Handle[S]) S, opts ...Option[S]) {
	t.Helper()
	cfg := configure(suite.overrides(t), opts)
	stopAt(t, validate("Run"+suite.Declaration+"Suite", suite, cfg))
	checks := append(slices.Clip(suite.Checks), cfg.checks...)
	parallel := uncounted(checks)
	for i, c := range checks {
		if c.Counted {
			continue
		}
		t.Run(c.ID.subtest(), func(t *testing.T) {
			t.Helper()
			t.Parallel()
			cfg.skipExcused(t, c.ID)
			if i >= len(suite.Checks) {
				c.falsify(t, cfg, parallel)
			}
			c.run(t, newSubject, cfg)
		})
	}
}

// failer is the part of a test or a fuzz test that stopAt fails.
type failer interface {
	Helper()
	Errorf(format string, args ...any)
	FailNow()
}

// stopAt reports each problem on t, and stops t when it finds one.
func stopAt(t failer, found []string) {
	t.Helper()
	for _, problem := range found {
		t.Errorf("%s", problem)
	}
	if len(found) > 0 {
		t.FailNow()
	}
}

// validate returns the problems of the application name of the suite. They
// are the problems of the checks of the suite and of the checks that the
// option With adds, of the excusals, of the calls and their budgets, and of
// the settings. An excusal may name any check of the application, because
// each entry point of a suite takes the same options.
func validate[S any](name string, suite Suite[S], cfg *config[S]) []string {
	checks := append(slices.Clip(suite.Checks), cfg.checks...)
	found := problems(name, checks, cfg.excusals, ceilingIDs(suite.Calls, cfg.ceilings))
	return append(append(found, budgetProblems(name, suite.Calls, cfg.ceilings)...), cfg.invalid()...)
}

// problems returns the problems of the checks and the excusals of the
// application name. known are the identities of the ceilings of the budgets,
// which no check of checks may have. An excusal may name a check of checks or
// an identity of known.
func problems[S any](name string, checks []Check[S], excusals []excusal[S], known []ID[S]) []string {
	var found []string
	seen := make(map[ID[S]]bool, len(checks)+len(known))
	ids := make([]string, 0, len(checks)+len(known))
	for _, c := range checks {
		if !grammar.MatchString(string(c.ID)) {
			found = append(found, fmt.Sprintf("the identity %q of a check does not have the form "+
				"Declaration.Callable/segment, as in Store.Get/keeps-the-value", c.ID))
		}
		if c.Statement == "" {
			found = append(found, fmt.Sprintf("the check %s has no statement", c.ID))
		}
		switch {
		case c.Apply == nil && c.Property == nil:
			found = append(found, fmt.Sprintf("the check %s has no function Apply and no function Property", c.ID))
		case c.Apply != nil && c.Property != nil:
			found = append(found, fmt.Sprintf("the check %s has both a function Apply and a function Property", c.ID))
		}
		if c.Options != nil && c.Property == nil {
			found = append(found, fmt.Sprintf("the check %s has options and no function Property, and the options "+
				"of a check apply to its property run", c.ID))
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
	ceilings := make(map[ID[S]]bool, len(known))
	for _, id := range known {
		if seen[id] && !ceilings[id] {
			found = append(found, fmt.Sprintf("the check %s has the identity of a ceiling of the budget of its "+
				"callable", id))
		}
		ceilings[id] = true
		if !seen[id] {
			seen[id] = true
			ids = append(ids, string(id))
		}
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
