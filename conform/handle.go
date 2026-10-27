// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
)

// origin is the instant at which the clock of each handle starts.
var origin = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// ErrOption is the fault of a door that a handle lacks: a door of an
// application without its option, such as [Handle.Reference] without the
// option [Reference], and a door of a planted defect whose check has no such
// door.
var ErrOption = errors.New("conform: the handle lacks the door")

// ErrClock is the fault of [Handle.Advance] where no subject of the check read
// the clock of its handle.
var ErrClock = errors.New("conform: no subject reads the clock of the handle")

// ErrTempDir is the fault of [Handle.TempDir] where it cannot make a directory.
var ErrTempDir = errors.New("conform: the handle cannot make a temporary directory")

// ErrSkip is the fault of [Handle.Skipf] on a seat without Skipf.
var ErrSkip = errors.New("conform: the handle cannot skip the check")

// seat is the seat of a handle. The handle embeds it, so the promoted Helper
// marks the frame of the assertion that calls it, and a failure on a test
// reports the line of the check.
type seat interface{ assert.TB }

// Doors are the doors of a handle: the subjects, the restart and the crash
// that only the code that builds a subject can supply. An application sets
// them through the options [Reference], [Second], [Restartable] and
// [Crashable], and a check sets the doors of its planted defect through
// [Check.Doors]. A nil door is not supplied.
type Doors[S any] struct {
	// Reference returns a fresh reference implementation, which
	// [Handle.Reference] returns.
	Reference func(h *Handle[S]) S

	// Second returns a fresh second subject, which [Handle.Second] returns.
	Second func(h *Handle[S]) S

	// Restart returns the subject s after a restart of its process, which
	// [Handle.Restart] returns.
	Restart func(h *Handle[S], s S) S

	// Crash runs call against s, crashes the medium of s while call runs, and
	// returns the subject over the medium as the crash left it. It draws the
	// point of the crash from h.Rand(), so a property check replays and
	// shrinks the point with its case. [Handle.Crash] returns its subject.
	Crash func(h *Handle[S], s S, call func()) S
}

// over returns d with each door that d lacks from fallback.
func (d Doors[S]) over(fallback Doors[S]) Doors[S] {
	if d.Reference == nil {
		d.Reference = fallback.Reference
	}
	if d.Second == nil {
		d.Second = fallback.Second
	}
	if d.Restart == nil {
		d.Restart = fallback.Restart
	}
	if d.Crash == nil {
		d.Crash = fallback.Crash
	}
	return d
}

// Handle is the handle of one check, or of one case of a property check. It
// is the seat of the assertions of the check, it builds the subjects of the
// check, and it supplies the machinery of the check. It implements
// [assert.TB], [assert.Reporter] and [assert.Clocked], and its method Unwrap
// returns the seat of the check.
//
// A failure on the handle fails the seat of the check. The seat is the test
// of the check under [Run] and [Falsify], and the case under a property
// check.
type Handle[S any] struct {
	seat

	check  *Check[S]
	source func(h *Handle[S]) S
	config *config[S]
	clock  *assert.Controlled
	random rand.Source
	kept   *atomic.Pointer[string]

	// subject is the subject of the check once built is true. mu guards
	// both. cleanups are the functions that Cleanup keeps on a seat without
	// Cleanup, in the order of their registration. cleaning guards them.
	subject  S
	cleanups []func()
	mu       sync.Mutex
	built    bool
	cleaning sync.Mutex

	building  atomic.Int32
	clockRead atomic.Bool
}

// newHandle returns the handle of the check c on tb. source builds the
// subjects of the check, and kept keeps the message of the first failure of
// the check outside an assertion. A handle on a case of the property engine
// draws its random values from the case. Any other handle draws them from the
// seed of cfg, or from the identity of c without a seed.
func newHandle[S any](
	tb assert.TB, c *Check[S], source func(h *Handle[S]) S, cfg *config[S], kept *atomic.Pointer[string],
) *Handle[S] {
	h := &Handle[S]{seat: tb, check: c, source: source, config: cfg, clock: assert.NewControlled(origin), kept: kept}
	if pc, isCase := tb.(*prop.Case); isCase {
		h.random = pc.Rand()
	} else {
		seed := seedOf(string(c.ID))
		if cfg.seeded {
			seed = cfg.seed
		}
		h.random = rand.NewPCG(seed, seed)
	}
	return h
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
// [Reference], or from the door Reference of a planted defect. Without the
// door it ends the check with a fault that wraps [ErrOption], and it returns
// the zero value to a seat that takes the fault and goes on.
func (h *Handle[S]) Reference() S {
	h.Helper()
	if h.config.doors.Reference == nil {
		h.lacks("Reference", "conform.Reference", "func(h *conform.Handle[%[1]v]) %[1]v { … }")
		var zero S
		return zero
	}
	return h.build(h.config.doors.Reference)
}

// Second returns a fresh second subject from the option [Second], or from the
// door Second of a planted defect. Without the door it ends the check with a
// fault that wraps [ErrOption], and it returns the zero value to a seat that
// takes the fault and goes on.
func (h *Handle[S]) Second() S {
	h.Helper()
	if h.config.doors.Second == nil {
		h.lacks("Second", "conform.Second", "func(h *conform.Handle[%[1]v]) %[1]v { … }")
		var zero S
		return zero
	}
	return h.build(h.config.doors.Second)
}

// Restart returns the subject s after a restart of its process, from the
// option [Restartable] or the door Restart of a planted defect. Without the
// door it ends the check with a fault that wraps [ErrOption], and it returns
// s to a seat that takes the fault and goes on.
func (h *Handle[S]) Restart(s S) S {
	h.Helper()
	if h.config.doors.Restart == nil {
		h.lacks("Restart", "conform.Restartable", "func(h *conform.Handle[%[1]v], s %[1]v) %[1]v { … }")
		return s
	}
	return h.build(func(h *Handle[S]) S { return h.config.doors.Restart(h, s) })
}

// Crash runs call against s and crashes the medium of s while call runs,
// through the option [Crashable] or the door Crash of a planted defect. It
// returns the subject over the medium as the crash left it. Without the door
// it ends the check with a fault that wraps [ErrOption]. A seat that takes the
// fault and goes on gets s after call runs without a crash.
func (h *Handle[S]) Crash(s S, call func()) S {
	h.Helper()
	if h.config.doors.Crash == nil {
		h.lacks("Crash", "conform.Crashable", "func(h *conform.Handle[%[1]v], s %[1]v, call func()) %[1]v { … }")
		call()
		return s
	}
	return h.build(func(h *Handle[S]) S { return h.config.doors.Crash(h, s, call) })
}

// lacks ends the check with the fault of the door that the handle lacks. The
// fault of a handle on a planted defect contains the door of the check, and
// the fault of any other handle contains the option of the application.
// signature is the signature of the door, where %[1]v is the type of the
// subjects.
func (h *Handle[S]) lacks(door, option, signature string) {
	h.Helper()
	subjects := reflect.TypeFor[S]()
	fn := fmt.Sprintf(signature, subjects)
	if h.config.defect {
		assert.Fault(h, fmt.Errorf("%w: %s calls %s on its planted defect, and the check has no door %s. "+
			"Give the check the door:\n\tconform.Doors[%v]{%s: %s}",
			ErrOption, h.check.ID, door, door, subjects, door, fn))
		return
	}
	assert.Fault(h, fmt.Errorf("%w: %s calls %s without the option %s. Pass the option to the application:\n\t%s(%s)",
		ErrOption, h.check.ID, door, option, option, fn))
}

// Clock returns the controlled clock of the check. A factory builds a
// subject on it, and [Handle.Advance] moves it.
func (h *Handle[S]) Clock() assert.Clock {
	if h.building.Load() > 0 {
		h.clockRead.Store(true)
	}
	return h.clock
}

// Advance moves the clock of the check forward by d. It ends the check with a
// fault that wraps [ErrClock] when no factory read the clock while it built a
// subject, because no subject then reads the clock that Advance moves. It
// leaves the clock at its instant for a seat that takes the fault and goes on.
func (h *Handle[S]) Advance(d time.Duration) {
	h.Helper()
	if !h.clockRead.Load() {
		assert.Fault(h, fmt.Errorf("%[1]w: %[2]s advances a clock that the subject never read. "+
			"Build the subject on the clock of the handle:\n"+
			"\tRun%[3]sSuiteFromHandle(t, func(h *conform.Handle[%[4]v]) %[4]v { … h.Clock() … })",
			ErrClock, h.check.ID, h.check.ID.declaration(), reflect.TypeFor[S]()))
		return
	}
	h.clock.Advance(d)
}

// Rand returns the random source of the check. In a property check it is the
// source of the case, whose every value is a choice of the case, so a double
// that draws from it replays and shrinks with the case. In any other check it
// is a source of the seed of DOKIMI_SEED, or of the identity of the check
// where the variable is not set, so each run of the check draws the same
// values.
func (h *Handle[S]) Rand() rand.Source {
	return h.random
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

// Cleanup registers f to run when the check ends, or when the case ends under
// a property check. A factory registers the stop of a subject with it, such
// as the end of the goroutines of the subject. On a seat with Cleanup, such
// as the test of the check or a case, Cleanup registers f with the seat. On
// any other seat the handle keeps f, and runs the functions that it keeps
// when the check ends, the last registered first. A function that stops the
// check, such as through Fatalf, or that panics leaves the earlier functions
// to run.
func (h *Handle[S]) Cleanup(f func()) {
	if seat, has := h.seat.(interface{ Cleanup(f func()) }); has {
		seat.Cleanup(f)
		return
	}
	h.cleaning.Lock()
	defer h.cleaning.Unlock()
	h.cleanups = append(h.cleanups, f)
}

// Skipf skips the check with the reason that format and args state, through
// Skipf of the seat. On the test of a check it skips the test. Under a
// property check it ends the property run as skipped at the case. Under
// falsification it skips the test of the falsification. The isolation of
// another planted defect gives a check that skips no verdict. A factory skips
// a check where the environment of the run lacks a capability, such as a file
// system without direct I/O. Skipf ends the check with a fault that wraps
// [ErrSkip] on a seat without Skipf, and it returns to a seat that takes the
// fault and goes on.
func (h *Handle[S]) Skipf(format string, args ...any) {
	h.Helper()
	seat, has := h.seat.(interface {
		Skipf(format string, args ...any)
	})
	if !has {
		assert.Fault(h, fmt.Errorf("%w: the seat of %s has no Skipf", ErrSkip, h.check.ID))
		return
	}
	seat.Skipf(format, args...)
}

// TempDir returns a new directory of the check, which the run removes when
// the check ends, or when the case ends under a property check. Each call
// returns another directory, so a factory builds each subject on a directory
// of its own.
//
// TempDir makes the directory in the directory of temporary files, and
// registers its removal through [Handle.Cleanup]. It ends the check with a
// fault that wraps [ErrTempDir] where it cannot make the directory, and it
// returns an empty name to a seat that takes the fault and goes on.
func (h *Handle[S]) TempDir() string {
	h.Helper()
	dir, err := os.MkdirTemp("", "conform-")
	if err != nil {
		assert.Fault(h, fmt.Errorf("%w for %s: %w", ErrTempDir, h.check.ID, err))
		return ""
	}
	h.Cleanup(func() {
		assert.NoError(h, os.RemoveAll(dir), "TempDir removes the directory of the check")
	})
	return dir
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

// keep keeps the message of the first failure outside an assertion. The
// falsification of the check reports that message.
func (h *Handle[S]) keep(format string, args []any) {
	message := fmt.Sprintf(format, args...)
	h.kept.CompareAndSwap(nil, &message)
}

// build returns a subject of newSubject. A read of the clock while build runs
// is a read by a subject.
func (h *Handle[S]) build(newSubject func(h *Handle[S]) S) S {
	h.building.Add(1)
	defer h.building.Add(-1)
	return newSubject(h)
}

// end ends the check of the handle: it runs the functions that Cleanup kept,
// the last registered first. Each function runs in a frame of its own, so a
// function that stops the goroutine of the check or panics leaves the earlier
// functions to run. A function that a function registers runs next. The code
// that makes a handle on a seat without Cleanup calls end when the check
// ends.
func (h *Handle[S]) end() {
	h.cleaning.Lock()
	last := len(h.cleanups) - 1
	kept := last >= 0
	var f func()
	if kept {
		f, h.cleanups = h.cleanups[last], h.cleanups[:last]
	}
	h.cleaning.Unlock()
	if !kept {
		return
	}
	defer h.end()
	f()
}
