// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"time"

	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
)

// Machine is a sans-IO machine: it applies one event at a time and appends the
// effects of the event to out.
type Machine[E, F any] interface {
	// Step applies ev to the machine and appends the effects of ev to out. It
	// returns the extended slice, as append does.
	Step(ev E, out []F) []F
	// Err returns the error with which the machine has stopped itself, and nil
	// while the machine runs.
	Err() error
}

// Outcome is the result of performing one effect.
type Outcome[E any] struct {
	// Event is the event of the result, and the zero E for an effect without
	// a result, such as a reply.
	Event E
	// Next returns the next event of a wait without blocking. ok reports
	// whether the wait has a next event, and done whether the wait has ended.
	// Next is nil for an effect that does not wait.
	Next func() (ev E, ok, done bool)
	// Message is the inbound call that the effect sends to another process,
	// whose reply is the result of the effect, and nil for any other effect.
	Message *Message[E]
}

// Perform performs the effect f of the process p.
type Perform[E, F any] func(w *World[E, F], p *Process[E, F], f F) Outcome[E]

// Anatomy is how a world reads the events and the effects of its machines.
// The generator sim writes it from the directives of a declaration.
type Anatomy[E, F any] struct {
	// Started returns the first event of a run, the inbound call of the start
	// of a process with the reply token reply.
	Started func(reply uint64) E
	// Stopped returns the last event of a run.
	Stopped func() E
	// Cancelled returns the end of the wait of the caller of the inbound call
	// with the reply token reply, and is nil for machines without such an
	// event.
	Cancelled func(reply uint64) E
	// Call returns the reply token of an inbound call, and false for any
	// other event.
	Call func(ev E) (reply uint64, ok bool)
	// Reply returns the reply token, the value and the error of a reply, and
	// false for any other effect.
	Reply func(f F) (to uint64, value any, err error, ok bool)
	// Unknown reports whether the error err of a reply leaves the outcome of
	// its call unknown, such as the error of a stop that can follow the effect
	// of the call. It is nil for machines whose every error of a reply fails
	// its call.
	Unknown func(err error) bool
	// Token returns the token of an effect that starts a call or a wait, and
	// false for any other effect, such as an unwatch or a reply.
	Token func(f F) (token uint64, ok bool)
	// Dependency returns the name of the dependency that performs the effect
	// f, such as store.Writer, and false for an effect that a function of the
	// consumer performs.
	Dependency func(f F) (name string, ok bool)
	// Fail returns the event of the result of the effect f that failed with
	// err, and false for an effect without a result.
	Fail func(f F, err error) (ev E, ok bool)
	// Errors returns the errors that the directive fault of the dependency
	// declares for the call that performs f.
	Errors func(f F) []error
	// Timeout returns the timeout of the effect f, and 0 for an effect whose
	// call the run alone bounds.
	Timeout func(f F) time.Duration
	// Perform performs each effect other than a reply.
	Perform Perform[E, F]
}

// System builds the processes of a world and their dependencies.
type System[E, F any] func(w *World[E, F])

// ErrRunEnded is the error of a call that the stop of its process leaves
// without a reply.
var ErrRunEnded = errors.New("sim: the run ended before the call returned")

// ErrCrashed is the error of a call whose process crashed before it replied.
var ErrCrashed = errors.New("sim: the process of the call crashed before it replied")

// ErrCancelled is the error of a call whose client stopped waiting for it.
var ErrCancelled = errors.New("sim: the client stopped waiting for the call")

// World is the system of one run: its processes, the events that wait for
// each process, the effects in flight, the faults, the timers, the log of its
// steps and the history of the calls of its clients.
type World[E, F any] struct {
	c       *prop.Case
	calls   map[uint64]inbound[E, F]
	after   func()
	plant   func(m Machine[E, F]) Machine[E, F]
	a       Anatomy[E, F]
	procs   []*Process[E, F]
	log     []Entry[E, F]
	timers  []timer[E, F]
	waits   []wait[E, F]
	deps    []*dependency[E, F]
	sides   [][]string
	crash   point
	elapsed time.Duration
	replies uint64
	sum     uint64
	steps   int
}

// point is a crash point of a scenario: the step whose process crashes and
// restarts, before the step or after it, and 0 for none.
type point struct {
	step  int
	after bool
}

// timer is the function run that the world calls once the clock of the
// process has advanced by at or more.
type timer[E, F any] struct {
	process *Process[E, F]
	run     func()
	at      time.Duration
}

// wait is a pending wait of a process, in the epoch of the process when the
// wait started.
type wait[E, F any] struct {
	next    func() (E, bool, bool)
	process *Process[E, F]
	epoch   uint64
}

// New returns an empty world whose machines have the anatomy a, and which
// takes its choices from the case c.
func New[E, F any](c *prop.Case, a Anatomy[E, F]) *World[E, F] {
	return &World[E, F]{c: c, a: a, calls: map[uint64]inbound[E, F]{}}
}

// Spawn adds a process named name to the world, and queues its start.
// newMachine builds the machine of the process from its clock. A process
// that restarts builds a new machine with the same function, in a new epoch.
// deps are the dependencies of the process, such as the doubles of its
// stores, which a performer reads through Dependency. The case chooses the
// drift of the clock of the process.
func (w *World[E, F]) Spawn(name string, newMachine func(clock *Clock) Machine[E, F], deps ...any) {
	drift := w.c.Draw(prop.Integer(minDrift, maxDrift), "drift."+name)
	p := &Process[E, F]{name: name, clock: newClock(drift), newMachine: newMachine, deps: deps}
	w.procs = append(w.procs, p)
	w.start(p)
}

// start gives the process p a new machine in a new epoch, and queues its
// start, an inbound call with a new reply token.
func (w *World[E, F]) start(p *Process[E, F]) {
	p.epoch++
	p.machine = p.newMachine(p.clock)
	if w.plant != nil {
		p.machine = w.plant(p.machine)
	}
	p.state = running
	p.queue = append(p.queue, queued[E]{ev: w.a.Started(w.token()), epoch: p.epoch})
}

// Deliver queues ev for the process named to, in the current epoch of the
// process. It fails the case for a name that no process has.
func (w *World[E, F]) Deliver(to string, ev E) {
	p := w.named(to)
	p.queue = append(p.queue, queued[E]{ev: ev, epoch: p.epoch})
}

// Call queues the inbound call that call builds from a new reply token for
// the process named to, and records its invocation by client in the history
// of the case with the operation, the arguments and the keys. The reply with
// the same token completes the call. A call to a crashed process ends as
// unknown with ErrCrashed, and a call to a stopped process as unknown with
// ErrRunEnded, as a message to such a process does. It fails the case for a
// name that no process has.
func (w *World[E, F]) Call(client int, to, operation string, args, keys []any, call func(reply uint64) E) {
	p := w.named(to)
	invoked := w.c.History().Invoke(client, operation, args, keys...)
	switch p.state {
	case crashed:
		invoked.Unknown(ErrCrashed)
	case stopped:
		invoked.Unknown(ErrRunEnded)
	default:
		token := w.token()
		w.calls[token] = inbound[E, F]{process: p, call: &invoked, client: client}
		p.queue = append(p.queue, queued[E]{ev: call(token), epoch: p.epoch})
	}
}

// Cancel ends the wait of client for its open call: the history records the
// call as unknown with ErrCancelled, and the process of the call takes its
// cancel. It reports whether the client has an open call. The history allows
// a client one open call at a time.
func (w *World[E, F]) Cancel(client int) bool {
	if !slices.Contains(w.callers(), client) {
		return false
	}
	w.abort(client)
	return true
}

// abort ends the open call of client as unknown with ErrCancelled, and
// queues its cancel for its process. The client has an open call.
func (w *World[E, F]) abort(client int) {
	token := w.tokens(func(in inbound[E, F]) bool { return in.call != nil && in.client == client })[0]
	w.calls[token].call.Unknown(ErrCancelled)
	w.cancel(token)
}

// After queues ev for the process p once the clock of p has advanced by d.
// The timers that one advance makes due take their places in the order of
// their calls. A jump or a strobe of the clock does not move the timer.
func (w *World[E, F]) After(p *Process[E, F], d time.Duration, ev E) {
	epoch := p.epoch
	w.at(p, d, func() { p.queue = append(p.queue, queued[E]{ev: ev, epoch: epoch}) })
}

// at calls run once the clock of the process p has advanced by d.
func (w *World[E, F]) at(p *Process[E, F], d time.Duration, run func()) {
	w.timers = append(w.timers, timer[E, F]{at: p.clock.advanced() + d, process: p, run: run})
}

// Pending returns the names of the processes that run and have an event of
// their current epoch to step on, in the order of their spawns.
func (w *World[E, F]) Pending() []string {
	runnable := w.runnable()
	out := make([]string, 0, len(runnable))
	for _, p := range runnable {
		out = append(out, p.name)
	}
	return out
}

// runnable returns the processes that run and have an event of their
// current epoch to step on, in the order of their spawns.
func (w *World[E, F]) runnable() []*Process[E, F] {
	var out []*Process[E, F]
	for _, p := range w.procs {
		if p.runs() && slices.ContainsFunc(p.queue, func(q queued[E]) bool { return q.epoch == p.epoch }) {
			out = append(out, p)
		}
	}
	return out
}

// callers returns the clients with an open call, in the order of their
// numbers.
func (w *World[E, F]) callers() []int {
	var out []int
	for _, in := range w.calls {
		if in.call != nil {
			out = append(out, in.client)
		}
	}
	slices.Sort(out)
	return out
}

// Step makes the process named name step on its oldest event, and drops each
// event of an earlier epoch before it, with an entry of the log for each. It
// returns false for a process without such an event, and for a process that
// does not run. The world stops a process whose machine reports an error
// after the step, and takes the next event of each wait after the step.
func (w *World[E, F]) Step(name string) bool {
	p := w.Process(name)
	if p == nil || !p.runs() {
		return false
	}
	for len(p.queue) > 0 && p.queue[0].epoch != p.epoch {
		w.record(Entry[E, F]{
			Process: p.name, Epoch: p.queue[0].epoch, Event: p.queue[0].ev, Time: p.clock.Now(), Dropped: true,
			Faults: w.Faults(),
		})
		p.queue = p.queue[1:]
	}
	if len(p.queue) == 0 {
		return false
	}
	ev := p.queue[0].ev
	p.queue = p.queue[1:]
	w.steps++
	if w.crash == (point{step: w.steps}) {
		w.Crash(p.name)
		w.Restart(p.name)
	} else {
		w.take(p, ev)
	}
	if w.crash == (point{step: w.steps, after: true}) {
		w.Crash(p.name)
		w.Restart(p.name)
	}
	w.poll()
	if w.after != nil {
		w.after()
	}
	return true
}

// take makes the process p take the event ev, at the offset of its strobe,
// and stops p when its machine reports an error after the step.
func (w *World[E, F]) take(p *Process[E, F], ev E) {
	if p.strobe.left > 0 {
		p.clock.strobe = p.strobe.next
		p.strobe.next, p.strobe.then = p.strobe.then, p.strobe.next
		p.strobe.left--
	}
	w.step(p, ev, false)
	p.clock.strobe = 0
	if p.machine.Err() != nil {
		w.stop(p)
	}
}

// step makes p apply ev, records the step, and performs its effects, or its
// replies alone where replies is set. It passes the type of each effect to
// the case as a label.
func (w *World[E, F]) step(p *Process[E, F], ev E, replies bool) {
	effects := p.machine.Step(ev, nil)
	w.record(Entry[E, F]{
		Process: p.name, Epoch: p.epoch, Event: ev, Effects: effects, Err: p.machine.Err(), Time: p.clock.Now(),
		Faults: w.Faults(),
	})
	for _, f := range effects {
		w.c.Classify(reflect.TypeOf(f).String())
	}
	for _, f := range effects {
		if to, value, err, ok := w.a.Reply(f); ok {
			w.reply(p, to, value, err)
			continue
		}
		if !replies && !w.faulted(p, f) {
			w.apply(p, f)
		}
	}
}

// reply completes the open inbound call to of the process p. For a call of a
// client, it records OK with value for a nil err, Unknown with an err that
// leaves the outcome unknown, and Fail with any other err. For a message, it
// returns the result to the sender. It ignores a reply to a call that p does
// not have open, such as the reply to a start.
func (w *World[E, F]) reply(p *Process[E, F], to uint64, value any, err error) {
	in := w.calls[to]
	if in.process != p {
		return
	}
	delete(w.calls, to)
	switch {
	case in.call == nil:
		w.resolve(in.await, p, value, err)
	case err != nil && w.a.Unknown != nil && w.a.Unknown(err):
		in.call.Unknown(err)
	case err != nil:
		in.call.Fail(err)
	default:
		in.call.OK(value)
	}
}

// apply performs the effect f of the process p: it queues the event of the
// result for a later step of p, starts the wait and sends the message.
func (w *World[E, F]) apply(p *Process[E, F], f F) {
	o := w.a.Perform(w, p, f)
	if !reflect.ValueOf(&o.Event).Elem().IsZero() {
		p.queue = append(p.queue, queued[E]{ev: o.Event, epoch: p.epoch})
	}
	if o.Next != nil {
		w.waits = append(w.waits, wait[E, F]{next: o.Next, process: p, epoch: p.epoch})
	}
	if o.Message != nil {
		w.send(p, f, o.Message)
	}
}

// poll queues the next event of each wait that has one, in the order in which
// the waits started, and ends each wait that has ended.
func (w *World[E, F]) poll() {
	w.waits = slices.DeleteFunc(w.waits, func(wt wait[E, F]) bool {
		ev, ok, done := wt.next()
		if ok {
			wt.process.queue = append(wt.process.queue, queued[E]{ev: ev, epoch: wt.epoch})
		}
		return done
	})
}

// Crash crashes the process named name: its machine, its pause, its strobe,
// its waits and the events that wait for it are gone. Its open calls end as
// unknown with ErrCrashed, the sender of each message to it takes ErrCrashed,
// and the receiver of each message that it sent takes the cancel of the
// call. The dependencies keep what they applied. An event for the process
// until its restart has the old epoch, so the restarted machine never takes
// it. It fails the case for a name that no process has, and for a process
// that does not run.
func (w *World[E, F]) Crash(name string) {
	p := w.named(name)
	if p.state != running {
		w.c.Fatalf("sim: the process %q does not run", name)
	}
	p.queue = nil
	p.state = crashed
	p.paused = false
	p.strobe = strobe{}
	w.forget(p, ErrCrashed)
	w.strike(FaultCrash)
}

// Restart starts a new machine of the crashed process named name, in a new
// epoch, and queues its start. It fails the case for a name that no process
// has, and for a process that has not crashed.
func (w *World[E, F]) Restart(name string) {
	p := w.named(name)
	if p.state != crashed {
		w.c.Fatalf("sim: the process %q has not crashed", name)
	}
	w.start(p)
	w.strike(FaultRestart)
}

// Pause pauses the process named name: it takes no event, and its clock does
// not advance while the time of the run advances. It fails the case for a
// name that no process has, and for a process that does not run or is
// paused.
func (w *World[E, F]) Pause(name string) {
	p := w.named(name)
	if p.state != running || p.paused {
		w.c.Fatalf("sim: the process %q does not run unpaused", name)
	}
	p.paused = true
	w.strike(FaultPause)
}

// Resume ends the pause of the process named name. It fails the case for a
// name that no process has, and for a process that is not paused.
func (w *World[E, F]) Resume(name string) {
	p := w.named(name)
	if !p.paused {
		w.c.Fatalf("sim: the process %q is not paused", name)
	}
	p.paused = false
}

// Advance advances the time of the run by d, and the clock of each process
// that is not paused by d times its drift. It runs the function of each timer
// whose clock has advanced to the timer.
func (w *World[E, F]) Advance(d time.Duration) {
	w.elapsed += d
	for _, p := range w.procs {
		if !p.paused {
			p.clock.advance(d)
		}
	}
	var ready []timer[E, F]
	w.timers = slices.DeleteFunc(w.timers, func(t timer[E, F]) bool {
		if t.process.clock.advanced() < t.at {
			return false
		}
		ready = append(ready, t)
		return true
	})
	for _, t := range ready {
		t.run()
	}
}

// Next returns the time of the run until the next timer of a process that is
// not paused, and false when no such timer is pending.
func (w *World[E, F]) Next() (time.Duration, bool) {
	next, found := time.Duration(math.MaxInt64), false
	for _, t := range w.timers {
		if !t.process.paused {
			next, found = min(next, t.process.clock.runTime(t.at-t.process.clock.advanced())), true
		}
	}
	if !found {
		return 0, false
	}
	return next, true
}

// Stop passes the stop event to each process that has not stopped or
// crashed, performs the replies that it returns, and ends each open call of
// the history as unknown with ErrRunEnded.
func (w *World[E, F]) Stop() {
	for _, p := range w.procs {
		if p.state == running {
			w.stop(p)
		}
	}
}

// stop passes the stop event to p, performs the replies that it returns, and
// stops p. Its open calls end as unknown with ErrRunEnded, and its waits and
// its messages end.
func (w *World[E, F]) stop(p *Process[E, F]) {
	w.step(p, w.a.Stopped(), true)
	p.state = stopped
	w.forget(p, ErrRunEnded)
}

// forget ends what the process p leaves in flight when it crashes or stops:
// its waits, the effects that a slow dependency keeps for it, its open calls
// with err, and the messages that it sent.
func (w *World[E, F]) forget(p *Process[E, F], err error) {
	w.waits = slices.DeleteFunc(w.waits, func(wt wait[E, F]) bool { return wt.process == p })
	for _, d := range w.deps {
		for _, s := range d.stalls {
			if s.process == p {
				s.done = true
			}
		}
	}
	for _, token := range w.tokens(func(in inbound[E, F]) bool { return in.process == p }) {
		in := w.calls[token]
		delete(w.calls, token)
		if in.call != nil {
			in.call.Unknown(err)
			continue
		}
		w.resolve(in.await, p, nil, err)
	}
	w.abandon(p)
}

// Process returns the process named name, and nil for a name that no process
// has.
func (w *World[E, F]) Process(name string) *Process[E, F] {
	at := slices.IndexFunc(w.procs, func(p *Process[E, F]) bool { return p.name == name })
	if at < 0 {
		return nil
	}
	return w.procs[at]
}

// named returns the process named name, and fails the case for a name that
// no process has.
func (w *World[E, F]) named(name string) *Process[E, F] {
	p := w.Process(name)
	if p == nil {
		w.c.Fatalf("sim: no process of the world is named %q", name)
	}
	return p
}

// History returns the history of the calls of the clients, which is the
// history of the case.
func (w *World[E, F]) History() *history.History { return w.c.History() }

// Context returns the context of the case of the world, which a performer
// passes to the call of a dependency.
func (w *World[E, F]) Context() context.Context { return w.c.Context() }

// token returns a new reply token. The world numbers the reply tokens of a
// run from 1.
func (w *World[E, F]) token() uint64 {
	w.replies++
	return w.replies
}
