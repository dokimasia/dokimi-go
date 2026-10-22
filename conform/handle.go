// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"go.dokimi.dev/assert"
)

// origin is the instant at which the clock of each handle starts.
var origin = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// seat is the seat of a handle. The handle embeds it, so the promoted Helper
// marks the frame of the assertion that calls it, and a failure on a test
// reports the line of the check.
type seat interface{ assert.TB }

// Handle is the handle of one check. It is the seat of the assertions of the
// check, it builds the subjects of the check, and it supplies the machinery
// of the check. It implements [assert.TB], [assert.Reporter] and
// [assert.Clocked], and its method Unwrap returns the seat of the check.
//
// A failure on the handle fails the seat of the check. The seat is the test
// of the check under [Run] and [Falsify].
type Handle[S any] struct {
	seat

	check   *Check[S]
	source  func(h *Handle[S]) S
	config  *config[S]
	clock   *assert.Controlled
	message atomic.Pointer[string]

	// subject is the subject of the check once built is true. mu guards
	// both.
	subject S
	mu      sync.Mutex
	built   bool

	building  atomic.Int32
	clockRead atomic.Bool
}

// newHandle returns the handle of the check c on tb. source builds the
// subjects of the check.
func newHandle[S any](tb assert.TB, c *Check[S], source func(h *Handle[S]) S, cfg *config[S]) *Handle[S] {
	return &Handle[S]{seat: tb, check: c, source: source, config: cfg, clock: assert.NewControlled(origin)}
}

// Subject returns the subject of the check. The first call builds the
// subject, and every later call returns the same subject. Under falsification
// the subject is the planted defect of the check.
//
// A factory of the subjects must not call Subject.
func (h *Handle[S]) Subject() S {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.built {
		h.subject, h.built = h.build(h.source), true
	}
	return h.subject
}

// New returns a fresh subject from the factory of the subject, for a check
// that needs more than one subject.
func (h *Handle[S]) New() S {
	return h.build(h.source)
}

// Reference returns a fresh reference implementation from the option
// [Reference]. Without the option it stops the check, and it returns the zero
// value to a seat that goes on after a fatal failure.
func (h *Handle[S]) Reference() S {
	h.Helper()
	if h.config.reference == nil {
		h.Fatalf("%[1]s calls Reference without the option conform.Reference. "+
			"Pass the option to the application:\n"+
			"\tconform.Reference(func(h *conform.Handle[%[2]v]) %[2]v { … })",
			h.check.ID, reflect.TypeFor[S]())
		var zero S
		return zero
	}
	return h.build(h.config.reference)
}

// Second returns a fresh second subject from the option [Second]. Without the
// option it stops the check, and it returns the zero value to a seat that
// goes on after a fatal failure.
func (h *Handle[S]) Second() S {
	h.Helper()
	if h.config.second == nil {
		h.Fatalf("%[1]s calls Second without the option conform.Second. "+
			"Pass the option to the application:\n"+
			"\tconform.Second(func(h *conform.Handle[%[2]v]) %[2]v { … })",
			h.check.ID, reflect.TypeFor[S]())
		var zero S
		return zero
	}
	return h.build(h.config.second)
}

// Restart returns the subject s after a restart of its process, from the
// option [Restartable]. Without the option it stops the check, and it returns
// s to a seat that goes on after a fatal failure.
func (h *Handle[S]) Restart(s S) S {
	h.Helper()
	if h.config.restart == nil {
		h.Fatalf("%[1]s calls Restart without the option conform.Restartable. "+
			"Pass the option to the application:\n"+
			"\tconform.Restartable(func(h *conform.Handle[%[2]v], s %[2]v) %[2]v { … })",
			h.check.ID, reflect.TypeFor[S]())
		return s
	}
	return h.build(func(h *Handle[S]) S { return h.config.restart(h, s) })
}

// Clock returns the controlled clock of the check. A factory builds a
// subject on it, and [Handle.Advance] moves it.
func (h *Handle[S]) Clock() assert.Clock {
	if h.building.Load() > 0 {
		h.clockRead.Store(true)
	}
	return h.clock
}

// Advance moves the clock of the check forward by d. It stops the check when
// no factory read the clock while it built a subject, because no subject then
// reads the clock that Advance moves. It leaves the clock at its instant for a
// seat that goes on after a fatal failure.
func (h *Handle[S]) Advance(d time.Duration) {
	h.Helper()
	if !h.clockRead.Load() {
		h.Fatalf("%[1]s advances a clock that the subject never read. "+
			"Build the subject on the clock of the handle:\n"+
			"\tRun%[2]sSuiteFromHandle(t, func(h *conform.Handle[%[3]v]) %[3]v { … h.Clock() … })",
			h.check.ID, h.check.ID.declaration(), reflect.TypeFor[S]())
		return
	}
	h.clock.Advance(d)
}

// Statement returns the statement of the check. The decisive assertion of the
// check passes it as its message.
func (h *Handle[S]) Statement() string {
	return h.check.Statement
}

// Context returns the context of the seat of the check.
func (h *Handle[S]) Context() context.Context {
	return assert.Context(h.seat)
}

// Unwrap returns the seat of the check. An assertion on the handle writes its
// call record to the seat. It calls Logf and each other method that the
// handle does not declare on the seat.
func (h *Handle[S]) Unwrap() assert.TB {
	return h.seat
}

// Report passes the record of a failure to a seat that implements
// [assert.Reporter]. It writes the text of the record to any other seat,
// through Fatalf when aborting is true and through Errorf otherwise.
func (h *Handle[S]) Report(f assert.Failure, aborting bool) {
	h.Helper()
	if reporter, ok := h.seat.(assert.Reporter); ok {
		reporter.Report(f, aborting)
		return
	}
	text := assert.NewRecorder()
	text.Report(f, false)
	if aborting {
		h.seat.Fatalf("%s", text.Message())
		return
	}
	h.seat.Errorf("%s", text.Message())
}

// Errorf reports a failure of the check outside an assertion, and returns.
func (h *Handle[S]) Errorf(format string, args ...any) {
	h.Helper()
	h.keep(format, args)
	h.seat.Errorf(format, args...)
}

// Fatalf reports a failure of the check outside an assertion, and stops the
// check.
func (h *Handle[S]) Fatalf(format string, args ...any) {
	h.Helper()
	h.keep(format, args)
	h.seat.Fatalf(format, args...)
}

// keep keeps the message of the first failure outside an assertion, which
// the falsification of the check reports.
func (h *Handle[S]) keep(format string, args []any) {
	message := fmt.Sprintf(format, args...)
	h.message.CompareAndSwap(nil, &message)
}

// build returns a subject of newSubject. A read of the clock while build runs
// is a read by a subject.
func (h *Handle[S]) build(newSubject func(h *Handle[S]) S) S {
	h.building.Add(1)
	defer h.building.Add(-1)
	return newSubject(h)
}
