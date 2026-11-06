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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
	"go.dokimi.dev/spec"
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

// ErrReason is the fault of [Handle.Expect] for a reason that the entry of the
// probe of the check does not list.
var ErrReason = errors.New("conform: the probe sets a reason that its entry does not list")

// seat is the seat of a handle. The handle embeds it, so the promoted Helper
// marks the frame of the assertion that calls it, and a failure on a test
// reports the line of the check.
type seat interface{ assert.TB }

// Doors are the doors of a handle: the subjects, the restart, the crash and
// the values that only the code that builds a subject can supply. An
// application sets them through the options [Reference], [Second],
// [Restartable], [Crashable], [Faultable] and [Supplies], and a check sets
// the doors of its planted defect through [Check.Doors]. A nil door is not
// supplied.
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

	// Faults returns a fresh subject whose dependencies ask inj, which
	// [Handle.Subject], [Handle.New] and [Handle.Fresh] return for a check of
	// the machinery call-that-fails-on-demand.
	Faults func(h *Handle[S], inj *Injector) S

	// Open returns a subject over the medium m, which [Handle.Open] returns.
	Open func(h *Handle[S], m medium.FS) S

	// Host returns a subject over the environment env of an incarnation,
	// which [Handle.Host] returns.
	Host func(h *Handle[S], env *host.Env) S

	// Values are the doors of values, one for each type, each from [Supply].
	// [Open] returns the value of the door of its type.
	Values []Supplied[S]
}

// over returns d with each door that d lacks from fallback, a door of a value
// included.
func (d Doors[S]) over(fallback Doors[S]) Doors[S] {
	values := slices.Clone(d.Values)
	for _, v := range fallback.Values {
		if !slices.ContainsFunc(values, v.opens) {
			values = append(values, v)
		}
	}
	d.Values = values
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
	if d.Faults == nil {
		d.Faults = fallback.Faults
	}
	if d.Open == nil {
		d.Open = fallback.Open
	}
	if d.Host == nil {
		d.Host = fallback.Host
	}
	return d
}

// Handle is the handle of one check, or of one case of a property check. It
// is the seat of the assertions of the check, it builds the subjects of the
// check, and it supplies the machinery of the check. It implements
// [assert.TB], [assert.Reporter], [assert.Clocked] and [assert.Observer], and
// its method Unwrap returns the seat of the check.
//
// A failure on the handle fails the seat of the check. The seat is the test
// of the check under [Run] and [Falsify], and the case under a property
// check.
type Handle[S any] struct {
	seat

	check    *Check[S]
	source   func(h *Handle[S]) S
	config   *config[S]
	clock    *assert.Controlled
	random   rand.Source
	kept     *firsts
	injector *Injector

	// expected is the reason that the probe set last, and nil before the
	// first call of Expect.
	expected atomic.Pointer[spec.ReasonID]

	// scheduler runs the clients of a planted defect as tasks, and is nil
	// before the first call of Schedule on a handle on a planted defect.
	scheduler atomic.Pointer[stateful.Scheduler]

	// subject is the subject of the check once built is true. media are the
	// media in memory of the subjects that the handle opened, in the order of
	// their opens. mu guards subject and built, and mediaMu guards media.
	subject S
	media   []mediumOfSubject
	mu      sync.Mutex
	mediaMu sync.Mutex
	built   bool

	building  atomic.Int32
	clockRead atomic.Bool
}

// newHandle returns the handle of the check c on tb. source builds the
// subjects of the check, and kept keeps the message of the first failure of
// the check outside an assertion. A handle on a case of the property engine
// draws its random values from the case. Any other handle draws them from the
// seed of cfg, or from the identity of c without a seed.
func newHandle[S any](
	tb assert.TB, c *Check[S], source func(h *Handle[S]) S, cfg *config[S], kept *firsts,
) *Handle[S] {
	h := &Handle[S]{
		seat: tb, check: c, source: source, config: cfg, clock: assert.NewControlled(origin), kept: kept,
		injector: new(Injector),
	}
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

// Fresh returns a fresh subject from the factory of the application, also
// under falsification, where Subject and New return the planted defect. The
// planted defect of a derived check wraps it. Fresh builds the subject of a
// check of the machinery call-that-fails-on-demand through the door Faults.
// Without a factory, as under [Falsify], Fresh ends the check with a fault that
// wraps [ErrOption], and it returns the zero value to a seat that takes the
// fault and goes on.
func (h *Handle[S]) Fresh() S {
	h.Helper()
	if h.check.faults() {
		return h.build((*Handle[S]).faulty)
	}
	if h.config.fresh == nil {
		assert.Fault(h, fmt.Errorf("%w: %s calls Fresh without a factory of subjects. A planted defect over a "+
			"fresh subject proves its check in an application, which has the factory", ErrOption, h.check.ID))
		var zero S
		return zero
	}
	return h.build(h.config.fresh)
}

// Expect sets the reason code of the assertions that the probe calls next.
// The handle keeps the reason of the first assertion that fails, and the
// falsification of a derived check compares it with the reason of the claim.
// Expect ends the check with a fault that wraps [ErrReason] where the entry of
// the probe of the check does not list the reason.
func (h *Handle[S]) Expect(reason spec.ReasonID) {
	h.Helper()
	probe, found := spec.ProbeOf(h.check.probe())
	if !slices.Contains(probe.Reasons, reason) {
		lists := "the check has no probe"
		if found {
			reasons := make([]string, len(probe.Reasons))
			for i, r := range probe.Reasons {
				reasons[i] = string(r)
			}
			lists = fmt.Sprintf("the probe %s reports %s", probe.ID, strings.Join(reasons, ", "))
		}
		assert.Fault(h, fmt.Errorf("%w: %s sets the reason %s, and %s", ErrReason, h.check.ID, reason, lists))
		return
	}
	h.expected.Store(&reason)
}

// Engage records that the probe put the subject into the state of the claim
// of the check, such as a call that failed for a check of the zero value
// beside an error. An application fails a check whose run ends without a call
// of Engage as vacuous, unless an excusal covers it. A property check engages
// when one of its cases engages.
func (h *Handle[S]) Engage() {
	h.kept.engaged.Store(true)
}

// ObserveCall receives each assertion call on the handle, a passing one
// included, as [assert.Observer] states. A call whose message is the
// statement engages a check without a probe. A check with a probe engages
// through [Handle.Engage] alone, because the probe passes the statement to
// the assertions of its guards too.
func (h *Handle[S]) ObserveCall(call assert.Call) {
	if h.check.probe() == "" && call.Contract == h.check.Statement {
		h.kept.engaged.Store(true)
	}
}

// Reference returns a fresh reference implementation from the option
// [Reference], or from the door Reference of a planted defect. Without the
// door it ends the check with a fault that wraps [ErrOption], and it returns
// the zero value to a seat that takes the fault and goes on.
func (h *Handle[S]) Reference() S {
	h.Helper()
	if h.config.doors.Reference == nil {
		h.lacks("calls Reference", "Reference", "conform.Reference", "func(h *conform.Handle[%[1]v]) %[1]v { … }")
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
		h.lacks("calls Second", "Second", "conform.Second", "func(h *conform.Handle[%[1]v]) %[1]v { … }")
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
		h.lacks("calls Restart", "Restart", "conform.Restartable",
			"func(h *conform.Handle[%[1]v], s %[1]v) %[1]v { … }")
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
		h.lacks("calls Crash", "Crash", "conform.Crashable",
			"func(h *conform.Handle[%[1]v], s %[1]v, call func()) %[1]v { … }")
		call()
		return s
	}
	return h.build(func(h *Handle[S]) S { return h.config.doors.Crash(h, s, call) })
}

// Open returns a subject over the medium m through the option [Durable] or the
// door Open of a planted defect. The handle keeps the medium of the subject
// where m is a medium.Memory, so [Handle.Restart] and [Handle.Crash] open the
// subject again over it. Without the door it ends the check with a fault that
// wraps [ErrOption], and it returns the zero value to a seat that takes the
// fault and goes on.
func (h *Handle[S]) Open(m medium.FS) S {
	h.Helper()
	if h.config.doors.Open == nil {
		h.lacks("calls Open", "Open", "conform.Durable", "func(h *conform.Handle[%[1]v], m medium.FS) %[1]v { … }")
		var zero S
		return zero
	}
	return h.withMedium(m, h.build(func(h *Handle[S]) S { return h.config.doors.Open(h, m) }))
}

// FreshOpen returns a subject over the medium m through the door Open of the
// option [Durable], also on a planted defect, as [Handle.Fresh] returns a
// fresh subject of the application. A planted defect over a subject of a
// medium wraps it. Without the option it ends the check with a fault that
// wraps [ErrOption], and it returns the zero value to a seat that takes the
// fault and goes on.
func (h *Handle[S]) FreshOpen(m medium.FS) S {
	h.Helper()
	if h.config.open == nil {
		assert.Fault(h, fmt.Errorf("%w: %s calls FreshOpen without the option conform.Durable. A planted defect "+
			"over a subject of a medium proves its check in an application, which has the option", ErrOption,
			h.check.ID))
		var zero S
		return zero
	}
	return h.withMedium(m, h.build(func(h *Handle[S]) S { return h.config.open(h, m) }))
}

// FreshHost returns a subject over the environment env of an incarnation of a
// host through the door Host of the option [Hosted], also on a planted
// defect, as [Handle.FreshOpen] returns a subject over a medium. Without the
// option it opens the subject over the disk of env through the door Open of
// the option [Durable], as the world of a check opens such a subject. Without
// either option it ends the check with a fault that wraps [ErrOption], and it
// returns the zero value to a seat that takes the fault and goes on.
func (h *Handle[S]) FreshHost(env *host.Env) S {
	h.Helper()
	switch {
	case h.config.host != nil:
		return h.build(func(h *Handle[S]) S { return h.config.host(h, env) })
	case h.config.open != nil:
		return h.FreshOpen(env.Disk())
	}
	assert.Fault(h, fmt.Errorf("%w: %s calls FreshHost without the option conform.Hosted or conform.Durable. A "+
		"planted defect over a subject of a host proves its check in an application, which has one of the options",
		ErrOption, h.check.ID))
	var zero S
	return zero
}

// Host returns a subject over the environment env of an incarnation of a
// host, through the option [Hosted] or the door Host of a planted defect.
// Without the door it ends the check with a fault that wraps [ErrOption], and
// it returns the zero value to a seat that takes the fault and goes on.
func (h *Handle[S]) Host(env *host.Env) S {
	h.Helper()
	if h.config.doors.Host == nil {
		h.lacks("calls Host", "Host", "conform.Hosted", "func(h *conform.Handle[%[1]v], env *host.Env) %[1]v { … }")
		var zero S
		return zero
	}
	return h.build(func(h *Handle[S]) S { return h.config.doors.Host(h, env) })
}

// Hostable reports whether the handle has the door Host, from the option
// [Hosted] or from the doors of a planted defect.
func (h *Handle[S]) Hostable() bool {
	return h.config.doors.Host != nil
}

// Mode returns the execution mode of the world of a check: spec.ModeTasks
// for an application with the option [Tasks], and spec.ModeNative otherwise.
func (h *Handle[S]) Mode() spec.Mode {
	if h.config.mode == spec.ModeTasks {
		return spec.ModeTasks
	}
	return spec.ModeNative
}

// Blocking reports whether the application states the option [Blocking].
func (h *Handle[S]) Blocking() bool {
	return h.config.blocking
}

// RecordWorld adds the record w of the world of one case to the record of the
// check. The steps, the crashes, the faults, the undecided partitions and the
// leaked goroutines of the cases add up. A fault is vacuous for the check
// when a case recorded it as vacuous and no case struck it. The record of the
// check states the mode of the handle, and the replays of a failing case of
// the run. RecordWorld does not keep w.
func (h *Handle[S]) RecordWorld(w *spec.WorldRecord) {
	h.kept.addWorld(w, h.Mode())
}

// AddPoints adds n to the points of the medium that the check enumerated: the
// crash points of a crash, and the flipped bits of a corruption. The record of
// the check states their sum.
func (h *Handle[S]) AddPoints(n int) { h.kept.points.Add(int64(n)) }

// withMedium keeps the medium m of the subject s where m is a medium.Memory,
// and returns s.
func (h *Handle[S]) withMedium(m medium.FS, s S) S {
	if mem, isMemory := m.(*medium.Memory); isMemory {
		h.mediaMu.Lock()
		defer h.mediaMu.Unlock()
		h.media = append(h.media, mediumOfSubject{subject: s, medium: mem})
	}
	return s
}

// mediumOf returns the medium of the last subject that the handle opened and
// that is s, and a new medium.Memory for a subject that the handle did not
// open over a medium in memory. A subject of a type that does not compare is
// none of the subjects that the handle opened.
func (h *Handle[S]) mediumOf(s S) *medium.Memory {
	h.mediaMu.Lock()
	defer h.mediaMu.Unlock()
	for _, o := range slices.Backward(h.media) {
		if reflect.ValueOf(o.subject).Comparable() && o.subject == any(s) {
			return o.medium
		}
	}
	return medium.NewMemory()
}

// mediumOfSubject is a subject that a handle opened over a medium in memory,
// and that medium.
type mediumOfSubject struct {
	subject any
	medium  *medium.Memory
}

// lacks ends the check with the fault of the door that the handle lacks for
// action, such as calls Reference. The fault of a handle on a planted defect
// contains the door of the check, and the fault of any other handle contains
// the option of the application. signature is the signature of the door,
// where %[1]v is the type of the subjects.
func (h *Handle[S]) lacks(action, door, option, signature string) {
	h.Helper()
	subjects := reflect.TypeFor[S]()
	fn := fmt.Sprintf(signature, subjects)
	if h.config.defect {
		assert.Fault(h, fmt.Errorf("%w: %s %s on its planted defect, and the check has no door %s. "+
			"Give the check the door:\n\tconform.Doors[%v]{%s: %s}",
			ErrOption, h.check.ID, action, door, subjects, door, fn))
		return
	}
	assert.Fault(h, fmt.Errorf("%w: %s %s without the option %s. Pass the option to the application:\n\t%s(%s)",
		ErrOption, h.check.ID, action, option, option, fn))
}

// Injector returns the injector of the handle, which every subject that the
// handle builds through the door Faults asks.
func (h *Handle[S]) Injector() *Injector {
	return h.injector
}

// Schedule returns the option of [stateful.Steps] that runs the clients of a
// concurrent section of c. A handle on a subject returns the zero option, so
// the clients run on threads, which interleave the calls of a subject that
// never yields. A handle on a planted defect returns the option that runs the
// clients as tasks of a scheduler of c with the strategy Uniform, so the race
// of the defect replays from the seed of c, and [Handle.Yield] yields to that
// scheduler. Uniform chooses the next task at each yield, so a race behind
// one yield shows in a few cases.
func (h *Handle[S]) Schedule(c *prop.Case) stateful.Option {
	if !h.config.defect {
		return stateful.Option{}
	}
	s := stateful.NewScheduler(c, stateful.Uniform())
	h.scheduler.Store(s)
	return stateful.Tasks(s)
}

// Yield lets the scheduler of [Handle.Schedule] run another client of a
// planted defect before the caller goes on. It returns at once outside a
// task of that scheduler, and on a handle on a subject. A planted defect
// calls it between the two halves of an operation that a correct subject
// runs whole.
func (h *Handle[S]) Yield() {
	if s := h.scheduler.Load(); s != nil {
		s.Yield()
	}
}

// faulty returns a subject of the door Faults over the injector of the handle.
// Without the door it ends the check with a fault that wraps [ErrOption], and
// it returns the zero value to a seat that takes the fault and goes on.
func (h *Handle[S]) faulty() S {
	h.Helper()
	if h.config.doors.Faults == nil {
		h.lacks("builds a subject", "Faults", "conform.Faultable",
			"func(h *conform.Handle[%[1]v], inj *conform.Injector) %[1]v { … }")
		var zero S
		return zero
	}
	return h.config.doors.Faults(h, h.injector)
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

// Cleanup registers f to run when the seat of the check ends, through
// assert.Cleanup: the test of the check, the case of a property check, or the
// seat of a falsification, which ends with the check. A factory registers the
// stop of a subject with it, such as the end of the goroutines of the
// subject. A seat without Cleanup in its chain ends the check with a fault,
// and f does not run.
func (h *Handle[S]) Cleanup(f func()) {
	h.Helper()
	assert.Cleanup(h.seat, f)
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
	why := fmt.Sprintf(format, args...)
	h.kept.skip.CompareAndSwap(nil, &why)
	seat.Skipf(format, args...)
}

// TempDir returns a new directory of the check, which the run removes when
// the seat of the check ends, or when the case ends under a property check.
// Each call
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
// through Fatalf when aborting is true and through Errorf otherwise. The
// suite record of a check with Apply contains the first failure. The record
// of a property check contains the failure of its smallest failing case.
func (h *Handle[S]) Report(f assert.Failure, aborting bool) {
	h.Helper()
	var reason spec.ReasonID
	if set := h.expected.Load(); set != nil {
		reason = *set
	}
	h.kept.reason.CompareAndSwap(nil, &reason)
	h.kept.failure.CompareAndSwap(nil, &f)
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
	h.kept.message.CompareAndSwap(nil, &message)
}

// build returns a subject of newSubject. A read of the clock while build runs
// is a read by a subject.
func (h *Handle[S]) build(newSubject func(h *Handle[S]) S) S {
	h.building.Add(1)
	defer h.building.Add(-1)
	return newSubject(h)
}
