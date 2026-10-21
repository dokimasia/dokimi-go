// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import "go.dokimi.dev/assert"

// Check is one check of a suite over subjects of the type S.
//
// The decisive assertion of Apply passes [Handle.Statement] as its message.
// Falsification proves the check when the first failure of the check on its
// planted defect is a failure of that assertion.
type Check[S any] struct {
	// Apply applies the check through its handle.
	Apply func(h *Handle[S])

	// Defect returns the planted defect of the check. The defect is correct
	// for the calls of the check, except at the statement of the check.
	Defect func(h *Handle[S]) S

	// ID is the identity of the check.
	ID ID[S]

	// Statement is the sentence that the check proves of a subject.
	Statement string
}

// Run applies the check on tb to a subject that newSubject builds on the
// handle of the check. The doors of the handle read the options in opts.
// Run ignores the checks and the excusals that the options add.
func (c Check[S]) Run(tb assert.TB, newSubject func(h *Handle[S]) S, opts ...Option[S]) {
	tb.Helper()
	c.run(tb, newSubject, configure(opts), formRun)
}

// Falsify applies the check on tb to its planted defect. It fails tb unless
// the first failure of the check is a failure of the assertion whose message
// is the statement of the check. The doors of the handle read the options in
// opts.
func (c Check[S]) Falsify(tb assert.TB, opts ...Option[S]) {
	tb.Helper()
	c.falsify(tb, configure(opts), formRun)
}

// run applies the check on tb to a subject of source, for an application of
// the form form.
func (c Check[S]) run(tb assert.TB, source func(h *Handle[S]) S, cfg *config[S], form string) {
	tb.Helper()
	c.Apply(newHandle(tb, &c, source, cfg, form))
}

// falsify applies the check on tb to its planted defect, for an application
// of the form form.
func (c Check[S]) falsify(tb assert.TB, cfg *config[S], form string) {
	tb.Helper()
	var h *Handle[S]
	failures := assert.Rejects(tb, "the planted defect of "+string(c.ID)+" fails the check", func(seat assert.TB) {
		h = newHandle(seat, &c, c.Defect, cfg, form)
		c.Apply(h)
	})
	if len(failures) > 0 {
		if first := failures[0]; first.Contract != c.Statement {
			tb.Fatalf("the planted defect of %s fails the assertion %s with the message %q. "+
				"Falsification needs a failure of the assertion whose message is the statement %q. "+
				"Pass h.Statement() as the message of the decisive assertion, "+
				"or change the defect so that it passes the other assertions of the check.",
				c.ID, first.Assertion, first.Contract, c.Statement)
		}
		return
	}
	if message := h.message.Load(); message != nil {
		tb.Fatalf("the planted defect of %s fails the check before an assertion fails. "+
			"Falsification needs a failure of the assertion whose message is the statement %q. "+
			"The check failed with this message:\n%s",
			c.ID, c.Statement, *message)
	}
}
