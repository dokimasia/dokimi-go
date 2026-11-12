// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"sync"
	"time"

	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
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
type Outcome[E, F any] struct {
	// Event is the event of the result, and the zero E for an effect without
	// a result, such as a reply.
	Event E
	// Next returns the next event of a wait without blocking. ok reports
	// whether the wait has a next event, and done whether the wait has ended.
	// Next is nil for an effect that does not wait.
	Next func() (ev E, ok, done bool)
	// Message is the inbound call that the effect sends to another process,
	// whose reply is the result of the effect, and nil for any other effect.
	// At a loss after the effect, the reply returns to no process.
	Message *Message[E]
	// Launch is the machine that the effect starts on the host of its
	// process, and nil for any other effect.
	Launch *Launch[E, F]
	// Cancel ends what the effect left in its dependency, such as the
	// candidacy of a campaign or the session of a join, as a runtime ends it
	// when it cancels the context of the call of the effect. It is nil for an
	// effect without such an end. The world calls it at most once: at a loss
	// after the effect, which drops the result and the wait, and for a wait at
	// an outage of its dependency, at an effect that ends the wait and at the
	// stop of its process. A crash does not call it, because the dependencies keep what
	// they applied.
	Cancel func()
	// Stop is the name of a process that an effect started and that this
	// effect stops with the stop event, and empty for any other effect. The
	// world stops the process before it queues Event, or the event of the
	// failure at a loss after the effect.
	Stop string
}

// Launch is a machine that an effect starts on the host of its process. The
// first event of the machine is an inbound call, whose reply returns to the
// process of the effect as the result of the effect. At a loss after the
// effect, the machine starts and the reply returns to no process.
type Launch[E, F any] struct {
	// Machine builds the machine from the clock of the host.
	Machine func(clock *Clock) Machine[E, F]
	// Call returns the first event of the machine, an inbound call with the
	// reply token reply, in place of the start event of the anatomy.
	Call func(reply uint64) E
	// Result returns the event of the result of the effect from the value and
	// the error of the reply to the call.
	Result func(value any, err error) E
	// Name is the name of the process of the machine.
	Name string
	// Deps are the dependencies of the process, which a performer reads
	// through Dependency.
	Deps []any
}

// Perform performs the effect f of the process p.
type Perform[E, F any] func(w *World[E, F], p *Process[E, F], f F) Outcome[E, F]

// Scope is the scope of a dependency. The world has one instance of a
// dependency of the scope ScopeWorld, and each host has an instance of a
// dependency of the scope ScopeHost. The world keeps a state of faults for
// each instance.
type Scope uint8

const (
	// ScopeWorld gives the world one instance of the dependency, as a
	// database that every host calls.
	ScopeWorld Scope = iota
	// ScopeHost gives each host an instance of the dependency, as the
	// storage of a member. The name of the instance is the name of its host,
	// a slash and the name of the dependency, such as node-2/quorum.Storage.
	ScopeHost
)

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
	// Unknown reports whether the error err of a reply, or of a call of a
	// component through Invoke, leaves the outcome of its call unknown, such
	// as the error of a stop that can follow the effect of the call. It is nil
	// for machines whose every error of a reply fails its call.
	Unknown func(err error) bool
	// Token returns the token of an effect that starts a call or a wait, and
	// false for any other effect, such as an unwatch or a reply.
	Token func(f F) (token uint64, ok bool)
	// Ends returns the token of the wait that the effect f ends, such as the
	// token of the watch that an unwatch ends, and false for any other effect.
	// It is nil for machines without such an effect.
	Ends func(f F) (token uint64, ok bool)
	// Dependency returns the name of the dependency that performs the effect
	// f or that the function of its performer calls, such as store.Writer,
	// and false for an effect without a dependency.
	Dependency func(f F) (name string, ok bool)
	// Scope returns the scope of the dependency named dependency. It is nil
	// for machines whose every dependency has the scope ScopeWorld.
	Scope func(dependency string) Scope
	// Fail returns the event of the result of the effect f that failed with
	// err, and false for an effect without a result.
	Fail func(f F, err error) (ev E, ok bool)
	// End returns the event that ends the wait of the effect f after the wait
	// has delivered an event, such as the end of a term, with err in its field
	// of the type error where the event has one. It reports false for an effect
	// without such an event, and it is nil for machines without one.
	End func(f F, err error) (ev E, ok bool)
	// Errors returns the errors that the directive fault declares for the
	// dependency of the effect f.
	Errors func(f F) []error
	// Timeout returns the timeout of the effect f, and 0 for an effect whose
	// call the run alone bounds.
	Timeout func(f F) time.Duration
	// Perform performs each effect other than a reply.
	Perform Perform[E, F]
}

// System builds the processes of a world and their dependencies from inputs
// that it takes from the case: the function takes the inputs, and the
// function that it returns builds the world.
type System[E, F any] func(c *prop.Case) func(w *World[E, F])

// ErrRunEnded is the error of a call that the stop of its process leaves
// without a reply.
var ErrRunEnded = errors.New("sim: the run ended before the call returned")

// ErrCrashed is the error of a call whose process crashed before it replied.
var ErrCrashed = errors.New("sim: the process of the call crashed before it replied")

// ErrCancelled is the error of a call whose client stopped waiting for it.
var ErrCancelled = errors.New("sim: the client stopped waiting for the call")

// World is the system of one run: its hosts and their processes, the events
// that wait for each process, the effects in flight, the faults, the timers,
// the log of its steps and the history of the calls of its clients.
type World[E, F any] struct {
	c     *prop.Case
	calls map[uint64]inbound[E, F]
	// after checks the world after each step, and judge checks the rules
	// once the world records a step of a machine, before it performs the
	// effects of the step. Both are nil outside a run.
	after  func()
	judge  func()
	plant  func(m Machine[E, F]) Machine[E, F]
	a      Anatomy[E, F]
	hosts  []*Host[E, F]
	procs  []*Process[E, F]
	log    []Entry[E, F]
	timers []timer[E, F]
	waits  []*wait[E, F]
	deps   []*dependency[E, F]
	sides  [][]string

	// network connects the hosts of the components, and is nil for a world
	// without a component. sched is the task scheduler of Tasks mode, and
	// nil in native mode. netOpts are the options of the network.
	network *host.Network
	sched   *stateful.Scheduler
	netOpts []host.NetworkOption

	// invoked are the open calls of the clients on the components, and slow
	// the links between two components that a slow-link keeps slow.
	invoked []*invocation
	slow    [][2]string

	crash   point
	elapsed time.Duration
	replies uint64
	sum     uint64
	steps   int

	// mu guards invoked, which the goroutines of the components complete.
	mu sync.Mutex

	// blockingOpt reports the option Blocking, and scripted a run of a
	// scenario, in which each call of Invoke on a component that runs is a
	// step.
	blockingOpt bool
	scripted    bool
}

// point is a crash point of a scenario: the step whose host crashes and
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
// wait started: the effect that started it with its token, the cancel of its
// outcome, and the name of the dependency of the effect, empty for an effect
// without one. delivered reports a wait that has queued an event for its
// process, and cancelled a wait whose cancel the world has called.
type wait[E, F any] struct {
	f          F
	next       func() (E, bool, bool)
	cancel     func()
	process    *Process[E, F]
	dependency string
	token      uint64
	epoch      uint64
	delivered  bool
	cancelled  bool
}

// cancelOnce calls the cancel of the outcome of the wait, once.
func (wt *wait[E, F]) cancelOnce() {
	if wt.cancel != nil && !wt.cancelled {
		wt.cancelled = true
		wt.cancel()
	}
}

// New returns an empty world whose machines have the anatomy a, and which
// takes its choices from the case c.
func New[E, F any](c *prop.Case, a Anatomy[E, F]) *World[E, F] {
	return &World[E, F]{c: c, a: a, calls: map[uint64]inbound[E, F]{}}
}

// Spawn adds a host named name with one process of that name to the world,
// and queues the start of the process. newMachine builds the machine of the
// process from its clock. A process that restarts builds a new machine with
// the same function, in a new epoch. deps are the dependencies of the
// process, such as the doubles of its stores, which a performer reads through
// Dependency. The case chooses the drift of the clock of the host. Spawn
// fails the case where the world has a host or a process named name.
func (w *World[E, F]) Spawn(name string, newMachine func(clock *Clock) Machine[E, F], deps ...any) {
	w.AddHost(name).Spawn(name, newMachine, deps...)
}

// start gives the process p a new machine in a new epoch, and queues its
// start, an inbound call with a new reply token.
func (w *World[E, F]) start(p *Process[E, F]) {
	w.incarnate(p)
	p.queue = append(p.queue, queued[E]{ev: w.a.Started(w.token()), epoch: p.epoch})
}

// incarnate gives the process p a new machine in a new epoch, and makes it
// run.
func (w *World[E, F]) incarnate(p *Process[E, F]) {
	p.epoch++
	p.machine = p.newMachine(p.host.clock)
	if w.plant != nil {
		p.machine = w.plant(p.machine)
	}
	p.state = running
}

// Deliver queues ev for the process named to, in the current epoch of the
// process. It fails the case for a name that no process has, and for a
// component process, which takes no event.
func (w *World[E, F]) Deliver(to string, ev E) {
	p := w.machineProcess(w.named(to))
	p.queue = append(p.queue, queued[E]{ev: ev, epoch: p.epoch})
}

// machineProcess returns p, and fails the case for a component process, which
// takes no event.
func (w *World[E, F]) machineProcess(p *Process[E, F]) *Process[E, F] {
	if p.comp != nil {
		w.c.Fatalf("sim: the process %q is a component, which takes no event", p.name)
	}
	return p
}

// Call queues the inbound call that call builds from a new reply token for
// the process named to, and records its invocation by client in the history
// of the case with the operation, the arguments and the keys. The reply with
// the same token completes the call. A call to a crashed process ends as
// unknown with ErrCrashed, and a call to a stopped process as unknown with
// ErrRunEnded, as a message to such a process does. It fails the case for a
// name that no process has, and for a component process, which takes no
// event.
func (w *World[E, F]) Call(client int, to, operation string, args, keys []any, call func(reply uint64) E) {
	p := w.machineProcess(w.named(to))
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

// abort ends the open call of client as unknown with ErrCancelled. It queues
// the cancel of a call of a machine for its process, and ends the context of
// a call of a component. The client has an open call.
func (w *World[E, F]) abort(client int) {
	w.mu.Lock()
	at := slices.IndexFunc(w.invoked, func(in *invocation) bool { return in.client == client })
	if at >= 0 {
		in := w.invoked[at]
		w.end(in)
		in.call.Unknown(ErrCancelled)
	}
	w.mu.Unlock()
	if at >= 0 {
		return
	}
	token := w.tokens(func(in inbound[E, F]) bool { return in.call != nil && in.client == client })[0]
	w.calls[token].call.Unknown(ErrCancelled)
	w.cancel(token)
}

// After queues ev for the process p once the clock of p has advanced by d.
// The timers that one advance makes due take their places in the order of
// their calls. A jump or a strobe of the clock does not move the timer. It
// fails the case for a component process, which takes no event.
func (w *World[E, F]) After(p *Process[E, F], d time.Duration, ev E) {
	epoch := w.machineProcess(p).epoch
	w.at(p, d, func() { p.queue = append(p.queue, queued[E]{ev: ev, epoch: epoch}) })
}

// AfterFunc calls f once the clock of the process p has advanced by d, and then
// polls each wait, as the world polls the waits after a step. A jump or a
// strobe of the clock does not move the timer. The world calls f only while p
// runs in the epoch in which AfterFunc armed the timer. AfterFunc fails the
// case for a component process, which takes no event.
func (w *World[E, F]) AfterFunc(p *Process[E, F], d time.Duration, f func()) {
	epoch := w.machineProcess(p).epoch
	w.at(p, d, func() {
		if p.epoch == epoch && p.runs() {
			f()
			w.poll()
		}
	})
}

// at calls run once the monotonic clock of the process p has advanced by d.
func (w *World[E, F]) at(p *Process[E, F], d time.Duration, run func()) {
	w.timers = append(w.timers, timer[E, F]{at: p.host.clock.Monotonic() + d, process: p, run: run})
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

// callers returns the clients with an open call, of a machine or of a
// component, in the order of their numbers.
func (w *World[E, F]) callers() []int {
	var out []int
	for _, in := range w.calls {
		if in.call != nil {
			out = append(out, in.client)
		}
	}
	w.mu.Lock()
	for _, in := range w.invoked {
		out = append(out, in.client)
	}
	w.mu.Unlock()
	slices.Sort(out)
	return out
}

// Step makes the process named name step on its oldest event, and drops each
// event of an earlier epoch before it, with an entry of the log for each. It
// returns false for a process without such an event, and for a process that
// does not run. The world ends the host of a process whose machine reports an
// error after the step, and takes the next event of each wait after the step. An
// effect that ends a wait cancels the wait of the process with its token,
// after the world has performed the effect, and the world takes the events of
// the wait until its Next reports its end. The effect of the process with
// that token that a slow dependency keeps in flight fails with
// context.Canceled.
func (w *World[E, F]) Step(name string) bool {
	p := w.Process(name)
	if p == nil || !p.runs() {
		return false
	}
	for len(p.queue) > 0 && p.queue[0].epoch != p.epoch {
		w.record(Entry[E, F]{
			Process: p.name, Epoch: p.queue[0].epoch, Event: p.queue[0].ev, Time: p.host.clock.Now(),
			Dropped: true, Faults: w.Faults(),
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
		w.Crash(p.host.name)
		w.Restart(p.host.name)
	} else {
		w.take(p, ev)
	}
	if w.crash == (point{step: w.steps, after: true}) {
		w.Crash(p.host.name)
		w.Restart(p.host.name)
	}
	w.poll()
	if w.after != nil {
		w.after()
	}
	return true
}

// take makes the process p take the event ev, at the offset of the strobe of
// its host, and ends the host of p when its machine reports an error after
// the step.
func (w *World[E, F]) take(p *Process[E, F], ev E) {
	h := p.host
	if h.strobe.left > 0 {
		h.clock.strobe = h.strobe.next
		h.strobe.next, h.strobe.then = h.strobe.then, h.strobe.next
		h.strobe.left--
	}
	w.step(p, ev, false)
	h.clock.strobe = 0
	if p.machine.Err() != nil {
		w.exit(p)
	}
}

// exit ends the host of the process p, whose machine reported an error: it
// stops p, then each other process of the host that runs, in the order of
// their spawns, and the host exits until a restart. The pause and the strobe
// of the host end, as at a crash.
func (w *World[E, F]) exit(p *Process[E, F]) {
	h := p.host
	w.stop(p)
	for _, q := range h.procs {
		if q.state == running {
			w.stop(q)
		}
	}
	h.state, h.paused, h.strobe = exited, false, strobe{}
}

// step makes p apply ev, records the step, judges the rules of a run, and
// performs its effects, or its replies alone where replies is set. It passes
// the type of each effect to the case as a label.
func (w *World[E, F]) step(p *Process[E, F], ev E, replies bool) {
	effects := p.machine.Step(ev, nil)
	w.record(Entry[E, F]{
		Process: p.name, Epoch: p.epoch, Event: ev, Effects: effects, Err: p.machine.Err(),
		Time: p.host.clock.Now(), Faults: w.Faults(),
	})
	if w.judge != nil {
		w.judge()
	}
	for _, f := range effects {
		w.c.Classify(reflect.TypeOf(f).String())
	}
	for _, f := range effects {
		if to, value, err, ok := w.a.Reply(f); ok {
			w.reply(p, to, value, err)
			continue
		}
		if replies {
			continue
		}
		if !w.faulted(p, f) {
			w.apply(p, f, false)
		}
		if token, ends := w.ends(f); ends {
			w.cancelWait(p, token)
		}
	}
}

// ends returns the token of the wait that the effect f ends, and false for
// any other effect and for an anatomy without such an effect.
func (w *World[E, F]) ends(f F) (uint64, bool) {
	if w.a.Ends == nil {
		return 0, false
	}
	return w.a.Ends(f)
}

// cancelWait cancels the wait of the process p whose effect has the token.
// The wait remains until its Next reports its end. An effect of p with the
// token that a slow dependency keeps in flight does not run, and p takes the
// event of its failure with context.Canceled, as a runtime ends a call whose
// context it cancels.
func (w *World[E, F]) cancelWait(p *Process[E, F], token uint64) {
	for _, wt := range w.waits {
		if wt.process == p && wt.token == token {
			wt.cancelOnce()
		}
	}
	for _, d := range w.deps {
		for _, s := range d.stalls {
			if t, ok := w.a.Token(s.f); ok && t == token && s.process == p && !s.done {
				s.done = true
				w.lose(p, s.f, context.Canceled)
			}
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

// apply performs the effect f of the process p: it stops the process of the
// stop of the outcome, queues the event of the result for a later step of p,
// starts the wait, sends the message and starts the machine of the launch.
// Where lost is set, a loss after the effect has taken the result: apply calls
// the cancel of the outcome in place of the event and the wait, and the
// replies to the message and to the launch return to no process.
func (w *World[E, F]) apply(p *Process[E, F], f F, lost bool) {
	o := w.a.Perform(w, p, f)
	if o.Stop != "" {
		w.halt(p, o.Stop)
	}
	switch {
	case !lost:
		w.result(p, f, o)
	case o.Cancel != nil:
		o.Cancel()
	}
	if o.Message != nil {
		w.send(p, o.Message, w.awaiting(p, f, o.Message.Result, lost))
	}
	if o.Launch != nil {
		w.launch(p, o.Launch, w.awaiting(p, f, o.Launch.Result, lost))
	}
}

// result queues the event of the outcome o of the effect f of the process p
// for a later step of p, and starts the wait of o. A wait whose outcome has an
// event has delivered that event.
func (w *World[E, F]) result(p *Process[E, F], f F, o Outcome[E, F]) {
	evented := !reflect.ValueOf(&o.Event).Elem().IsZero()
	if evented {
		p.queue = append(p.queue, queued[E]{ev: o.Event, epoch: p.epoch})
	}
	if o.Next != nil {
		name, _ := w.instance(p, f)
		token, _ := w.a.Token(f)
		w.waits = append(w.waits, &wait[E, F]{
			f: f, next: o.Next, cancel: o.Cancel, process: p, dependency: name, token: token, epoch: p.epoch,
			delivered: evented,
		})
	}
}

// launch starts the machine s on the host of the process p, the process of
// the effect of s. The process of s takes a new machine in a new epoch, and
// the call of s as its first event. The reply to the call completes the await
// a. launch fails the case for a process of another host, for a process that
// Spawn added and for a process that runs.
func (w *World[E, F]) launch(p *Process[E, F], s *Launch[E, F], a *await[E, F]) {
	q := w.Process(s.Name)
	switch {
	case q == nil:
		q = p.host.add(s.Name, s.Machine, s.Deps)
		q.started = true
	case q.host != p.host:
		w.c.Fatalf("sim: the process %q runs on the host %q, so the process %q of the host %q cannot start it",
			s.Name, q.host.name, p.name, p.host.name)
	case !q.started:
		w.c.Fatalf("sim: Spawn added the process %q, so no effect starts it", s.Name)
	case q.state == running:
		w.c.Fatalf("sim: the process %q runs, so no effect starts it", s.Name)
	default:
		q.newMachine, q.deps = s.Machine, s.Deps
	}
	w.incarnate(q)
	token := w.token()
	w.calls[token] = inbound[E, F]{process: q, await: a, build: s.Call}
	q.queue = append(q.queue, queued[E]{ev: s.Call(token), epoch: q.epoch})
}

// halt stops the process named name, which an effect started on the host of
// the process p: it passes the stop event to its machine, performs the
// replies that it returns and stops it. halt fails the case for a process
// that no effect started, for a process of another host and for a process
// that does not run.
func (w *World[E, F]) halt(p *Process[E, F], name string) {
	q := w.named(name)
	switch {
	case !q.started:
		w.c.Fatalf("sim: no effect started the process %q, so no effect stops it", name)
	case q.host != p.host:
		w.c.Fatalf("sim: the process %q runs on the host %q, so the process %q of the host %q cannot stop it",
			name, q.host.name, p.name, p.host.name)
	case q.state != running:
		w.c.Fatalf("sim: the process %q does not run, so no effect stops it", name)
	}
	w.stop(q)
}

// poll queues the next event of each wait that has one, in the order in which
// the waits started, and ends each wait that has ended.
func (w *World[E, F]) poll() {
	w.waits = slices.DeleteFunc(w.waits, func(wt *wait[E, F]) bool {
		ev, ok, done := wt.next()
		if ok {
			wt.process.queue = append(wt.process.queue, queued[E]{ev: ev, epoch: wt.epoch})
			wt.delivered = true
		}
		return done
	})
}

// Crash crashes the host named name: each of its processes that runs crashes.
// The machine of a crashed process, its waits and the events that wait for it
// are gone, and so are the pause and the strobe of the host. The open calls
// of a crashed process end as unknown with ErrCrashed, the sender of each
// message to it takes ErrCrashed, and the receiver of each message that it
// sent takes the cancel of the call. The dependencies keep what they applied,
// so the world calls the Cancel of no wait of the crashed processes. Each
// medium.Memory among the dependencies of each process of the host
// crashes: it loses each write that a sync did not cover. An event for a
// process until its restart has the old epoch, so the restarted machine never
// takes it. A component process crashes its incarnation by a process crash or
// by a power loss, which the case chooses and which shrinks to a process
// crash. Its open calls end as unknown with medium.ErrCrashed. A crash of an
// exited host crashes its media alone. Crash fails the case for a name that
// no host has, and for a host that has crashed or stopped.
func (w *World[E, F]) Crash(name string) {
	h := w.down(name)
	for _, p := range h.procs {
		if p.state == running {
			p.queue, p.state = nil, crashed
			if p.comp != nil {
				w.crashComponent(p, w.c.Draw(prop.SampledFrom(host.ProcessCrash, host.PowerLoss), "kill."+p.name))
			}
			w.forget(p, ErrCrashed)
		}
		for _, m := range media(p) {
			m.Crash()
		}
	}
	w.strike(FaultCrash)
}

// down marks the host named name as crashed and returns it: its pause and its
// strobe are gone. It fails the case for a name that no host has, and for a
// host that has crashed or stopped.
func (w *World[E, F]) down(name string) *Host[E, F] {
	h := w.hostNamed(name)
	if h.state != running && h.state != exited {
		w.c.Fatalf("sim: the host %q does not run", name)
	}
	h.state, h.paused, h.strobe = crashed, false, strobe{}
	return h
}

// Restart restarts the crashed or exited host named name. Each process of the
// host that the crash crashed or the exit stopped, and that Spawn added, takes
// a new machine in a new epoch, and its start. A component process starts a
// new incarnation and opens its value. A process that an effect started
// remains down until an effect starts it again. Restart fails the case for a
// name that no host has, and for a host that has neither crashed nor exited.
func (w *World[E, F]) Restart(name string) {
	h := w.hostNamed(name)
	if h.state != crashed && h.state != exited {
		w.c.Fatalf("sim: the host %q has neither crashed nor exited", name)
	}
	w.restart(h)
	w.strike(FaultRestart)
}

// restart starts the crashed or exited host h again, and each process of h
// that Spawn added: a machine through start, and a component through
// startComponent. Each such process of a host that is down is down too.
func (w *World[E, F]) restart(h *Host[E, F]) {
	h.state, h.replaced = running, false
	for _, p := range h.procs {
		switch {
		case p.started:
		case p.comp != nil:
			w.startComponent(p)
		default:
			w.start(p)
		}
	}
}

// Pause pauses the host named name: its processes take no event, and its
// clock does not advance while the time of the run advances. A component
// process pauses its incarnation. Pause fails the case for a name that no
// host has, and for a host that does not run or is paused.
func (w *World[E, F]) Pause(name string) {
	h := w.hostNamed(name)
	if h.state != running || h.paused {
		w.c.Fatalf("sim: the host %q does not run unpaused", name)
	}
	h.paused = true
	if p := h.component(); p != nil {
		p.comp.inc.Pause()
	}
	w.strike(FaultPause)
}

// Resume ends the pause of the host named name. It fails the case for a name
// that no host has, and for a host that is not paused.
func (w *World[E, F]) Resume(name string) {
	h := w.hostNamed(name)
	if !h.paused {
		w.c.Fatalf("sim: the host %q is not paused", name)
	}
	resume(h)
}

// resume ends the pause of the paused host h.
func resume[E, F any](h *Host[E, F]) {
	h.paused = false
	if p := h.component(); p != nil && p.state == running {
		p.comp.inc.Resume()
	}
}

// Advance advances the time of the run by d, the clock of each host that is
// not paused by d times its drift, and the monotonic clock of each host of a
// component by d times its rate error. It runs the function of each timer
// whose clock has advanced to the timer.
func (w *World[E, F]) Advance(d time.Duration) {
	w.elapsed += d
	for _, h := range w.hosts {
		if !h.paused {
			h.clock.advance(d)
		}
	}
	w.advanceHosts(d)
	var ready []timer[E, F]
	w.timers = slices.DeleteFunc(w.timers, func(t timer[E, F]) bool {
		if t.process.host.clock.Monotonic() < t.at {
			return false
		}
		ready = append(ready, t)
		return true
	})
	for _, t := range ready {
		t.run()
	}
}

// Now returns the time of the run: Start, plus the time by which Advance has
// advanced the run. It has no drift, and no fault of a host moves it.
func (w *World[E, F]) Now() time.Time { return Start.Add(w.elapsed) }

// Next returns the time of the run until the next timer of a process whose
// host is not paused, and false when no such timer is pending.
func (w *World[E, F]) Next() (time.Duration, bool) {
	next, found := time.Duration(math.MaxInt64), false
	for _, t := range w.timers {
		if c := t.process.host.clock; !t.process.host.paused {
			next, found = min(next, c.runTime(t.at-c.Monotonic())), true
		}
	}
	if !found {
		return 0, false
	}
	return next, true
}

// Stop passes the stop event to each process that has not stopped or
// crashed, performs the replies that it returns, and ends each open call of
// the history as unknown with ErrRunEnded. It calls the Cancel of each wait of
// such a process before the wait ends. It ends the incarnation of each
// component process that runs by a process crash, and fails the case for a
// goroutine of the incarnation that is alive after quiescence. Each host that
// runs stops, so no fault of a host strikes it afterwards.
func (w *World[E, F]) Stop() {
	for _, h := range w.hosts {
		if h.state == running {
			h.state = stopped
		}
	}
	for _, p := range w.procs {
		switch {
		case p.state != running:
		case p.comp != nil:
			w.endInvocations(func(in *invocation) bool { return in.process == p.name }, ErrRunEnded)
			w.endComponent(p, host.ProcessCrash)
			p.state = stopped
		default:
			w.stop(p)
		}
	}
}

// stop passes the stop event to p, performs the replies that it returns, and
// stops p. Its open calls end as unknown with ErrRunEnded, its waits end after
// their cancel, and its messages end.
func (w *World[E, F]) stop(p *Process[E, F]) {
	w.step(p, w.a.Stopped(), true)
	p.state = stopped
	for _, wt := range w.waits {
		if wt.process == p {
			wt.cancelOnce()
		}
	}
	w.forget(p, ErrRunEnded)
}

// forget ends what the process p leaves in flight when it crashes or stops:
// its waits, the effects that a slow dependency keeps for it, its open calls
// with err, and the messages that it sent.
func (w *World[E, F]) forget(p *Process[E, F], err error) {
	w.waits = slices.DeleteFunc(w.waits, func(wt *wait[E, F]) bool { return wt.process == p })
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
