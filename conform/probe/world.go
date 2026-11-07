// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path"
	"slices"
	"sync"
	"syscall"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
	"go.dokimi.dev/spec"
)

// The bounds of a world: the most steps of a case, the longest advance of its
// clock in microseconds, the bound of a call of the recovery without
// World.Bound, and the replays of a world check.
const (
	worldSteps     = 100
	longestAdvance = 10_000_000
	defaultBound   = time.Second
	worldReplays   = 10
)

// The names of the faults of a world, by which the record of a check counts
// the strikes of each fault.
const (
	faultFailBefore      = "fail-before"
	faultFailAfter       = "fail-after"
	faultFailPart        = "fail-part"
	faultTear            = "tear"
	faultFill            = "fill"
	faultFlip            = "flip"
	faultDiskHold        = "disk-hold"
	faultFailNext        = "fail-next"
	faultFailUntilHealed = "fail-until-healed"
	faultDependencyHold  = "dependency-hold"
	faultPause           = "pause"
	faultBump            = "bump"
	faultStrobe          = "strobe"
)

// injected are the errors of the faults that a world injects and of the ends
// of calls that it causes. A call that fails with one of them may have taken
// effect or not.
var injected = []error{
	context.Canceled, context.DeadlineExceeded, medium.ErrCrashed, syscall.EIO, syscall.ENOSPC, host.ErrInjected,
}

// Operation is one callable of a declaration as the world of a subject calls
// it.
type Operation[S any] struct {
	// Input requests the input of a call from the case. It picks its keys
	// from the pools of the case.
	Input func(c *prop.Case) any
	// Args returns the arguments of the call that the history records.
	Args func(input any) []any
	// Keys returns the keys of the call, which partition the check of the
	// history. A call without keys is in every partition.
	Keys func(input any) []any
	// Call calls the callable on s with the input under ctx, and returns the
	// output of the call and the error of the callable, nil for a call that
	// returned no error.
	Call func(ctx context.Context, s S, input any) (conform.Output, error)
	// Produced adds to the pools of the case the keys that a call returned
	// with success, such as the key of a record that it created. It is nil
	// for a callable that returns no key.
	Produced func(out conform.Output)
	// Name is the operation in the history, the name of the callable.
	Name string
}

// World is the world of the subject of a handle: the operations of its
// clients, the reference that judges their history, the error of a corrupted
// record, the number of clients and the bound of a call after the recovery.
type World[S any] struct {
	// Operations returns the operations of the case c. It chooses the
	// starting values of the pools of c once.
	Operations func(c *prop.Case) []Operation[S]
	// Reference applies an operation of the history to a reference.
	Reference Reference[S]
	// Corrupt is the error with which the subject reports a corrupted record,
	// and nil for a declaration that states none. The world flips bits at
	// rest only for a subject with Corrupt.
	Corrupt error
	// Clients is the number of clients, and 1 for 0.
	Clients int
	// Bound is the monotonic time of the host within which a call of the
	// recovery completes, and 1 s for 0.
	Bound time.Duration
}

// WorldOptions returns the options of the property run of a world check of
// h: prop.Confirm(10) in Tasks mode, and prop.Replays(10) in native mode. A
// replay of a case in Tasks mode repeats the whole case, so a replay of every
// tenth valid case finds a decision of the subject outside the case. A case
// in native mode repeats its choices alone, so a failing case is replayed ten
// times before the shrink.
func WorldOptions[S any](h *conform.Handle[S]) []prop.Option {
	if h.Mode() == spec.ModeTasks {
		return []prop.Option{prop.Confirm(worldReplays)}
	}
	return []prop.Option{prop.Replays(worldReplays)}
}

// Amnesiac is the door Host of the defect form amnesiac-recovery. From the
// second incarnation of the host of env on, it removes every file and every
// directory of the disk of env, so each restart of a world opens the subject
// over an empty disk. It then opens the subject of the application over env
// through [conform.Handle.FreshHost]. A check without a world opens the
// planted defect through the door Open of the application, so the defect is
// the subject of the application there.
func Amnesiac[S any](h *conform.Handle[S], env *host.Env) S {
	h.Helper()
	if env.Incarnation() > 1 {
		erase(env.Disk())
	}
	return h.FreshHost(env)
}

// erase removes each file and each directory of m that m lets it remove, each
// directory after its entries, and syncs the root of m. A fault of a world in
// force on m can refuse a call, and the subject then opens over what remains.
func erase(m medium.FS) {
	var names []string
	for dirs := []string{"."}; len(dirs) > 0; dirs = dirs[1:] {
		entries, _ := m.ReadDir(dirs[0])
		for _, e := range entries {
			name := path.Join(dirs[0], e.Name())
			names = append(names, name)
			if e.IsDir() {
				dirs = append(dirs, name)
			}
		}
	}
	for _, name := range slices.Backward(names) {
		_ = m.Remove(name)
	}
	_ = m.SyncDir(".")
}

// FaultyWorldHistory runs the world w over the subject of h in the case c:
// concurrent calls of the operations on the clients of w, crashes and
// restarts of the subject, faults of its disk, its dependencies and its
// clock, and advances of its clock. It checks the history of the calls with
// history.Linearizable and opts after every step at which no client has an
// open call, and once more after the last step while a client has one. Each
// check continues the searches of the check before it. The checks judge the
// history against fresh references of h, as LinearizableHistory judges it. A
// check fails when a partition of the history has no order of its calls that
// respects their real-time order and that the reference accepts. A partition
// whose search ends undecided ends the checks of the case, and a history
// without a call does not engage h. A fault of the checker, such as a
// reference that panics, ends the check with that fault. The world fails the
// check for a goroutine of a crashed incarnation that is alive after
// quiescence, because it ignored its lifetime.
func FaultyWorldHistory[S any](h *conform.Handle[S], c *prop.Case, w World[S], opts ...history.Option) {
	h.Helper()
	wd := newWorld(h, c, w)
	defer wd.finish()
	wd.checking = true
	wd.checks = newLinearizability(h, w.Reference, opts)
	wd.start()
	stateful.Steps(c, wd.machine(), stateful.Max(worldSteps), stateful.Swarm(false))
	if !wd.quiet() {
		wd.check()
	}
}

// WorldRecovery runs the world w over the subject of h in the case c, as
// FaultyWorldHistory runs it without the checks of the history. It then ends
// the fault in force, opens a crashed subject again and makes one call of
// each operation on each client, the calls of a client in order. It fails
// when a call of that recovery has not returned once the monotonic clock of
// the host has advanced by w.Bound.
func WorldRecovery[S any](h *conform.Handle[S], c *prop.Case, w World[S]) {
	h.Helper()
	wd := newWorld(h, c, w)
	defer wd.finish()
	wd.start()
	stateful.Steps(c, wd.machine(), stateful.Max(worldSteps), stateful.Swarm(false))
	wd.recovery()
}

// world is the run of a World in one case: the host of its subject, the open
// call of each client and the fault in force.
type world[S any] struct {
	// checks judges the history, and each of its checks continues the
	// searches of the check before it.
	checks linearizability

	subject S
	h       *conform.Handle[S]
	c       *prop.Case

	// s is the scheduler of Tasks mode, and nil in native mode.
	s     *stateful.Scheduler
	host  *host.Host
	inc   *host.Incarnation
	env   *host.Env
	clock *host.Clock

	// kept are the faults that the case keeps, and fault the fault in force,
	// empty without one.
	kept  map[string]bool
	fault string

	def World[S]
	ops []Operation[S]

	// open is the open call of each client, nil for an idle client, and
	// done the outputs of the calls that returned without an error since the
	// last step. mu guards both.
	open []*worldCall
	done []completion[S]

	record spec.WorldRecord
	mu     sync.Mutex

	// checking reports that the world checks the history after each step.
	checking bool
}

// worldCall is an open call of a client of a world.
type worldCall struct {
	cancel context.CancelFunc
	call   history.Call
	ended  bool
}

// completion is the output of a call of the operation op that returned
// without an error.
type completion[S any] struct {
	op  *Operation[S]
	out conform.Output
}

// worldInput is the input of a call step: the client and the input of the
// operation.
type worldInput struct {
	value  any
	client int
}

// diskFault is the input of a fault step of the disk: the fault, the bytes
// of a part, a tear or a fill, and the shares of the files with data and of
// the size of a file that locate the byte of a flip.
type diskFault struct {
	kind         string
	bytes        int
	file, offset float64
}

// clockFault is the input of a fault step of the clock: a bump by delta, or a
// strobe by delta every period.
type clockFault struct {
	kind          string
	delta, period time.Duration
}

// newWorld returns the world def of the subject of h in the case c, before the
// start of its first incarnation.
func newWorld[S any](h *conform.Handle[S], c *prop.Case, def World[S]) *world[S] {
	w := &world[S]{
		h: h, c: c, def: def, open: make([]*worldCall, max(def.Clients, 1)),
		record: spec.WorldRecord{Crashes: map[string]int{}, Faults: spec.WorldFaults{Struck: map[string]int{}}},
	}
	if h.Mode() == spec.ModeTasks {
		w.s = stateful.NewScheduler(c, stateful.Uniform())
	}
	w.host = host.New(c, "subject", nil, w.s)
	w.ops = def.Operations(c)
	kinds := []string{faultFailBefore, faultFailAfter, faultFailPart, faultTear, faultFill}
	if def.Corrupt != nil {
		kinds = append(kinds, faultFlip)
	}
	kinds = append(kinds, faultFailNext, faultFailUntilHealed, faultBump, faultStrobe)
	if w.blocking() {
		kinds = append(kinds, faultDiskHold, faultDependencyHold, faultPause)
	}
	w.kept = map[string]bool{}
	for _, kind := range kinds {
		w.kept[kind] = c.Draw(prop.Boolean(), "faults."+kind)
	}
	return w
}

// keptOf returns the faults of kinds that the case keeps, in order.
func (w *world[S]) keptOf(kinds ...string) []string {
	var kept []string
	for _, kind := range kinds {
		if w.kept[kind] {
			kept = append(kept, kind)
		}
	}
	return kept
}

// machine returns the machine of the steps of the world. Each step settles
// after its run. For a world that checks its history, the machine checks it
// after each step at which no client has an open call.
func (w *world[S]) machine() stateful.Machine[struct{}] {
	actions := make([]stateful.Action[struct{}], 0, len(w.ops)+9)
	for i := range w.ops {
		actions = append(actions, w.callAction(&w.ops[i]))
	}
	actions = append(actions, w.advanceAction(), w.cancelAction(), w.crashAction(), w.restartAction(),
		w.diskAction(), w.dependencyAction(), w.clockAction(), w.pauseAction(), w.endAction())
	for i := range actions {
		run := actions[i].Run
		actions[i].Run = func(c *prop.Case, client int, input any) {
			run(c, client, input)
			w.settle()
		}
	}
	m := stateful.Machine[struct{}]{Actions: actions}
	if w.checking {
		m.Invariant = func(*prop.Case, struct{}) {
			if w.quiet() {
				w.check()
			}
		}
	}
	return m
}

// quiet reports whether no client has an open call.
func (w *world[S]) quiet() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !slices.ContainsFunc(w.open, func(wc *worldCall) bool { return wc != nil })
}

// callAction returns the step that calls op on a running subject. The case
// chooses a client among all clients, and the call goes to the first client
// without an open call from it on, in the order of the clients and around.
// The step takes no effect while every client has an open call.
func (w *world[S]) callAction(op *Operation[S]) stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    op.Name,
		Enabled: func(struct{}) bool { return w.inc != nil },
		Input: func(c *prop.Case, _ struct{}) any {
			return worldInput{client: w.client(c), value: op.Input(c)}
		},
		Run: func(_ *prop.Case, _ int, input any) {
			in, _ := input.(worldInput)
			if client, idle := w.nextClient(in.client, false); idle {
				w.invoke(client, op, in.value)
			}
		},
	}
}

// advanceAction returns the step that advances the monotonic clock of the
// host by 1 µs to 10 s, which shrinks to 1 µs.
func (w *world[S]) advanceAction() stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name: "advance",
		Input: func(c *prop.Case, _ struct{}) any {
			return time.Duration(c.Draw(prop.Integer(1, longestAdvance), "advance")) * time.Microsecond
		},
		Run: func(_ *prop.Case, _ int, input any) {
			d, _ := input.(time.Duration)
			w.advance(d)
		},
	}
}

// cancelAction returns the step that ends the context of an open call of a
// running subject, which completes the call as unknown. The case chooses a
// client among all clients, and the step ends the call of the first client
// with an open call from it on. The step takes no effect without an open
// call.
func (w *world[S]) cancelAction() stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    "cancel",
		Enabled: func(struct{}) bool { return w.inc != nil },
		Input:   func(c *prop.Case, _ struct{}) any { return w.client(c) },
		Run: func(_ *prop.Case, _ int, input any) {
			from, _ := input.(int)
			client, busy := w.nextClient(from, true)
			if !busy {
				return
			}
			w.mu.Lock()
			defer w.mu.Unlock()
			// The world is quiescent between the search and the end of the
			// call, so the call of the client is still open.
			wc := w.open[client]
			w.end(client, wc)
			wc.call.Unknown(context.Canceled)
		},
	}
}

// crashAction returns the step that crashes a running subject by a process
// crash or a power loss, which shrinks to a process crash.
func (w *world[S]) crashAction() stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    "crash",
		Enabled: func(struct{}) bool { return w.inc != nil },
		Input: func(c *prop.Case, _ struct{}) any {
			return c.Draw(prop.SampledFrom(host.ProcessCrash, host.PowerLoss), "kill")
		},
		Run: func(_ *prop.Case, _ int, input any) {
			k, _ := input.(host.Kill)
			w.record.Crashes[k.String()]++
			w.crash(k, w.h.Fatalf)
		},
	}
}

// restartAction returns the step that starts a new incarnation of a crashed
// subject and opens the subject through its door.
func (w *world[S]) restartAction() stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    "restart",
		Enabled: func(struct{}) bool { return w.inc == nil },
		Run:     func(*prop.Case, int, any) { w.start() },
	}
}

// diskFaults are the faults of the disk.
var diskFaults = []string{
	faultFailBefore, faultFailAfter, faultFailPart, faultTear, faultFill, faultFlip, faultDiskHold,
}

// diskAction returns the step that strikes a fault of the disk that the case
// keeps on a running subject, while no fault is in force. The case chooses the
// byte of a flip as a share of the files with data and a share of the size of
// the file.
func (w *world[S]) diskAction() stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    "disk",
		Enabled: func(struct{}) bool { return w.free() && len(w.keptOf(diskFaults...)) > 0 },
		Input: func(c *prop.Case, _ struct{}) any {
			f := diskFault{kind: c.Draw(prop.SampledFrom(w.keptOf(diskFaults...)...), "disk")}
			switch f.kind {
			case faultFailPart, faultTear, faultFill:
				f.bytes = c.Draw(prop.Integer(0, os.Getpagesize()), "disk.bytes")
			case faultFlip:
				f.file = c.Draw(prop.Float(0.0, 1.0), "disk.file")
				f.offset = c.Draw(prop.Float(0.0, 1.0), "disk.offset")
			}
			return f
		},
		Run: func(_ *prop.Case, _ int, input any) {
			f, _ := input.(diskFault)
			w.disk(f)
		},
	}
}

// free reports whether the world can strike a fault: the subject runs, and no
// fault is in force.
func (w *world[S]) free() bool {
	return w.inc != nil && w.fault == ""
}

// written returns the files of the disk of the host that have data.
func (w *world[S]) written() []file {
	return slices.DeleteFunc(filesOf(w.host.Disk()), func(f file) bool { return f.size == 0 })
}

// dependencyFaults are the faults of the dependencies.
var dependencyFaults = []string{faultFailNext, faultFailUntilHealed, faultDependencyHold}

// dependencyAction returns the step that strikes a fault of the dependencies
// that the case keeps on a running subject, while no fault is in force.
func (w *world[S]) dependencyAction() stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    "dependency",
		Enabled: func(struct{}) bool { return w.free() && len(w.keptOf(dependencyFaults...)) > 0 },
		Input: func(c *prop.Case, _ struct{}) any {
			return c.Draw(prop.SampledFrom(w.keptOf(dependencyFaults...)...), "dependency")
		},
		Run: func(_ *prop.Case, _ int, input any) {
			kind, _ := input.(string)
			inj := w.host.Injector()
			switch kind {
			case faultFailNext:
				inj.FailNext(host.ErrInjected)
			case faultFailUntilHealed:
				inj.FailUntilHealed(host.ErrInjected)
				w.fault = kind
			default:
				inj.Hold()
				w.fault = kind
			}
			w.record.Faults.Struck[kind]++
		},
	}
}

// clockAction returns the step that bumps or strobes the wall clock of a
// running subject, while no fault is in force. It chooses each value as the
// clock nemesis of Jepsen does: a bump of 1.5 to the power of 6 to 31 ms
// either way, and a strobe by 2 to the power of 2 to 18 ms every 2 to the
// power of 0 to 10 ms. Each shrinks to its smallest value, and a bump to one
// forward.
func (w *world[S]) clockAction() stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    "clock",
		Enabled: func(struct{}) bool { return w.free() && len(w.keptOf(faultBump, faultStrobe)) > 0 },
		Input: func(c *prop.Case, _ struct{}) any {
			f := clockFault{kind: c.Draw(prop.SampledFrom(w.keptOf(faultBump, faultStrobe)...), "clock")}
			if f.kind == faultBump {
				f.delta = milliseconds(1.5, 6+c.Draw(prop.Float(0.0, 25.0), "bump"))
				if c.Draw(prop.Boolean(), "bump.back") {
					f.delta = -f.delta
				}
				return f
			}
			f.delta = milliseconds(2, 2+c.Draw(prop.Float(0.0, 16.0), "strobe.delta"))
			f.period = milliseconds(2, c.Draw(prop.Float(0.0, 10.0), "strobe.period"))
			return f
		},
		Run: func(_ *prop.Case, _ int, input any) {
			f, _ := input.(clockFault)
			if f.kind == faultBump {
				w.host.Bump(f.delta)
			} else {
				w.host.Strobe(f.delta, f.period)
			}
			w.fault = f.kind
			w.record.Faults.Struck[f.kind]++
		},
	}
}

// pauseAction returns the step that pauses a running subject in a case that
// keeps the pause, while no fault is in force.
func (w *world[S]) pauseAction() stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    "pause",
		Enabled: func(struct{}) bool { return w.free() && w.kept[faultPause] },
		Run: func(*prop.Case, int, any) {
			w.inc.Pause()
			w.fault = faultPause
			w.record.Faults.Struck[faultPause]++
		},
	}
}

// endAction returns the step that ends the fault in force.
func (w *world[S]) endAction() stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    "end",
		Enabled: func(struct{}) bool { return w.fault != "" },
		Run:     func(*prop.Case, int, any) { w.endFault() },
	}
}

// start starts a new incarnation of the host and opens the subject on a
// goroutine of the incarnation, through the door Host or the door Open over
// the disk of the incarnation. It fails the case for an open that does not
// return within the bound of the world.
func (w *world[S]) start() {
	w.inc, w.env = w.host.Start()
	w.clock = w.env.Clock()
	env := w.env
	var subject S
	opened := w.on(func() {
		if w.h.Hostable() {
			subject = w.h.Host(env)
			return
		}
		subject = w.h.Open(env.Disk())
	})
	if !opened {
		w.h.Fatalf("probe: the open of the incarnation %d did not return within %v of its host", env.Incarnation(),
			w.bound())
	}
	w.subject = subject
}

// on runs f on a goroutine of the incarnation, or as a task under the
// scheduler, and reports whether f returned within the bound of the world.
func (w *world[S]) on(f func()) bool {
	returned := make(chan struct{})
	w.inc.Go(func() {
		f()
		close(returned)
	})
	w.quiesce()
	if !closed(returned) {
		w.advance(w.bound())
	}
	return closed(returned)
}

// invoke records the invocation of op by client with the input in the
// history, and calls op on a goroutine of the incarnation.
func (w *world[S]) invoke(client int, op *Operation[S], input any) {
	ctx, cancel := context.WithCancel(w.env.Lifetime())
	wc := &worldCall{call: w.c.History().Invoke(client, op.Name, op.Args(input), op.Keys(input)...), cancel: cancel}
	w.mu.Lock()
	w.open[client] = wc
	w.mu.Unlock()
	s := w.subject
	w.inc.Go(func() {
		out, err := op.Call(ctx, s, input)
		w.mu.Lock()
		defer w.mu.Unlock()
		if wc.ended {
			return
		}
		w.end(client, wc)
		unknown := slices.ContainsFunc(injected, func(cause error) bool { return errors.Is(err, cause) }) ||
			w.def.Corrupt != nil && errors.Is(err, w.def.Corrupt)
		if unknown {
			wc.call.Unknown(err)
			return
		}
		wc.call.OK(out)
		if err == nil {
			w.done = append(w.done, completion[S]{op: op, out: out})
		}
	})
}

// end completes the open call wc of client and ends its context. The caller
// holds mu.
func (w *world[S]) end(client int, wc *worldCall) {
	wc.ended = true
	w.open[client] = nil
	wc.cancel()
}

// endAll completes each open call as unknown with err.
func (w *world[S]) endAll(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for client, wc := range w.open {
		if wc != nil {
			w.end(client, wc)
			wc.call.Unknown(err)
		}
	}
}

// client returns a client that the case chooses among all clients.
func (w *world[S]) client(c *prop.Case) int {
	return c.Draw(prop.Integer(0, len(w.open)-1), "client")
}

// nextClient returns the first client from the client from on, in the order
// of the clients and around, that has an open call for busy and none
// otherwise. It reports false where no client is in that state.
func (w *world[S]) nextClient(from int, busy bool) (int, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.open {
		client := (from + i) % len(w.open)
		if (w.open[client] != nil) == busy {
			return client, true
		}
	}
	return 0, false
}

// crash completes each open call as unknown with medium.ErrCrashed, crashes
// the incarnation by k and waits for quiescence. A pause in force ends with
// its incarnation, and a hold of the disk in force ends with the crash of the
// disk. It reports each goroutine of the incarnation that is alive after
// quiescence through fail.
func (w *world[S]) crash(k host.Kill, fail func(format string, args ...any)) {
	w.endAll(medium.ErrCrashed)
	inc, number := w.inc, w.env.Incarnation()
	inc.Crash(k)
	w.inc, w.env = nil, nil
	if w.fault == faultPause || w.fault == faultDiskHold {
		w.fault = ""
	}
	w.quiesce()
	if leaked := inc.Leaked(); len(leaked) > 0 {
		w.record.Leaked = append(w.record.Leaked, leaked...)
		fail("probe: a goroutine of the incarnation %d is alive after the crash of its incarnation, so it ignored "+
			"its lifetime:\n\n%s", number, leaked[0])
	}
}

// disk strikes the fault f of the disk, which puts a fill or a hold in force.
// A flip strikes the byte at its shares of the files with data and of the
// size of the file, and takes no effect without a file with data.
func (w *world[S]) disk(f diskFault) {
	m := w.host.Disk()
	switch f.kind {
	case faultFailBefore:
		m.FailBefore(syscall.EIO)
	case faultFailAfter:
		m.FailAfter(syscall.EIO)
	case faultFailPart:
		m.FailPart(f.bytes, syscall.EIO)
	case faultTear:
		m.Tear(f.bytes)
	case faultFill:
		m.Fill(int64(f.bytes))
		w.fault = f.kind
	case faultFlip:
		files := w.written()
		if len(files) == 0 {
			return
		}
		at := files[shareOf(f.file, len(files))]
		m.Corrupt(at.name, shareOf(f.offset, at.size))
	default:
		m.Hold()
		w.fault = f.kind
	}
	w.record.Faults.Struck[f.kind]++
}

// endFault ends the fault in force, and does nothing without one.
func (w *world[S]) endFault() {
	switch w.fault {
	case faultFill:
		w.host.Disk().Fill(-1)
	case faultDiskHold:
		w.host.Disk().Release()
	case faultFailUntilHealed:
		w.host.Injector().Heal()
	case faultDependencyHold:
		w.host.Injector().Release()
	case faultPause:
		w.inc.Resume()
	case faultBump, faultStrobe:
		w.host.ResetClock()
	}
	w.fault = ""
}

// blocking reports whether the world strikes the blocking faults: in Tasks
// mode, and for an application with the option Blocking.
func (w *world[S]) blocking() bool {
	return w.s != nil || w.h.Blocking()
}

// bound returns the bound of a call of the recovery.
func (w *world[S]) bound() time.Duration {
	if w.def.Bound > 0 {
		return w.def.Bound
	}
	return defaultBound
}

// settle ends a step: it wakes each wait of the subject on Env.Wait, records
// the step for the watchdog of conform.Bubble, waits for quiescence, and
// passes the outputs of the calls that returned without an error to their
// Produced. In Tasks mode it observes the outputs of the step, so a replay of
// the case that returns other outputs ends as flaky.
func (w *world[S]) settle() {
	w.record.Steps++
	w.host.Broadcast()
	//dokimi:mutate-skip sbr-delete: the count of the steps decides the watchdog alone, for two samples a second apart with the same stacks and a wait for a mutex, which no deterministic case makes while its world steps
	conform.RecordStep()
	w.quiesce()
	w.mu.Lock()
	done := w.done
	w.done = nil
	w.mu.Unlock()
	fingerprint := fnv.New64a()
	for _, d := range done {
		if d.op.Produced != nil {
			d.op.Produced(d.out)
		}
		// A write to a hash does not fail.
		//dokimi:mutate-skip uoi-not: the negation of the flag of every output changes each fingerprint alike, so two runs differ where their outputs differ
		_, _ = fmt.Fprintf(fingerprint, "%s %v %t;", d.op.Name, d.out.Values, d.out.Failed)
	}
	if w.s != nil {
		w.c.Observe(fingerprint.Sum64())
	}
}

// quiesce waits until no goroutine or task of the subject can run: the return
// of Scheduler.Run in Tasks mode, and of synctest.Wait in native mode.
func (w *world[S]) quiesce() {
	if w.s != nil {
		w.s.Run()
		return
	}
	synctest.Wait()
}

// advance advances the clock of the bubble, or of the scheduler, until the
// monotonic clock of the host has advanced by d, and waits for quiescence.
func (w *world[S]) advance(d time.Duration) {
	until := w.clock.Monotonic() + d
	for left := d; left > 0; left = until - w.clock.Monotonic() {
		if w.s != nil {
			w.s.Advance(left)
			continue
		}
		time.Sleep(left)
		synctest.Wait()
	}
}

// check checks the history of the case after a step, continuing the searches
// of the check before it. A violation fails the case with the reason
// not-linearizable-under-faults, and an undecided search ends the checks of
// the case.
func (w *world[S]) check() {
	if !w.checking {
		return
	}
	r := assert.NewRecorder().WithContext(w.h.Context())
	w.checks.check(r, w.c.History(), w.h.Statement())
	if faults := r.Faults(); len(faults) > 0 {
		assert.Fault(w.h, faults[0])
		return
	}
	failures := r.Failures()
	if len(failures) > 0 && failures[0].Detail[outcomeField] == history.Undecided {
		w.record.Undecided++
		w.checking = false
		return
	}
	if len(w.c.History().Events()) == 0 {
		return
	}
	w.h.Engage()
	w.h.Expect(spec.NotLinearizableUnderFaults)
	for _, f := range failures {
		w.h.Report(f, true)
	}
}

// recovery ends the fault in force, opens a crashed subject again and makes
// one call of each operation on each client. It fails the case for a call
// that has not returned once the monotonic clock of the host has advanced by
// the bound of the world. The calls of the steps that remain open stay open
// until the end of the case.
func (w *world[S]) recovery() {
	w.endFault()
	if w.inc == nil {
		w.start()
	}
	type recoveryCall struct {
		op       *Operation[S]
		input    any
		returned chan struct{}
		name     string
	}
	calls := make([]recoveryCall, 0, len(w.open)*len(w.ops))
	ctx, s := w.env.Lifetime(), w.subject
	for client := range w.open {
		own := make([]recoveryCall, 0, len(w.ops))
		for i := range w.ops {
			own = append(own, recoveryCall{
				op: &w.ops[i], input: w.ops[i].Input(w.c), returned: make(chan struct{}),
				name: fmt.Sprintf("%s on client %d", w.ops[i].Name, client),
			})
		}
		calls = append(calls, own...)
		w.inc.Go(func() {
			for _, rc := range own {
				_, _ = rc.op.Call(ctx, s, rc.input)
				close(rc.returned)
			}
		})
	}
	w.advance(w.bound())
	var open []string
	for _, rc := range calls {
		if !closed(rc.returned) {
			open = append(open, rc.name)
		}
	}
	w.h.Engage()
	w.h.Expect(spec.CallOpenAfterRecovery)
	assert.Empty(w.h, open, w.h.Statement())
}

// finish ends the case: it crashes a running incarnation by a process crash,
// as a crash step does, and adds the world of the case to the record of the
// check. It records the faults of the clock as vacuous for a subject that
// never read its wall clock, and the faults of the dependencies for a subject
// that never called a dependency. A goroutine that is alive after the crash
// fails the case, and the record still contains the world.
func (w *world[S]) finish() {
	if w.inc != nil {
		w.crash(host.ProcessCrash, w.h.Errorf)
	}
	if !w.host.WallRead() {
		w.vacate(faultBump, faultStrobe)
	}
	if len(w.host.Injector().Calls()) == 0 {
		w.vacate(dependencyFaults...)
	}
	w.h.RecordWorld(&w.record)
}

// vacate records each fault of faults that the world strikes as vacuous, and
// removes the count of its strikes, for a subject that never used the
// resource of the faults.
func (w *world[S]) vacate(faults ...string) {
	for _, fault := range faults {
		if _, strikes := w.kept[fault]; strikes {
			delete(w.record.Faults.Struck, fault)
			w.record.Faults.Vacuous = append(w.record.Faults.Vacuous, fault)
		}
	}
}

// milliseconds returns base to the power of exponent milliseconds.
func milliseconds(base, exponent float64) time.Duration {
	return time.Duration(math.Pow(base, exponent) * float64(time.Millisecond))
}

// closed reports whether ch is closed.
func closed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
