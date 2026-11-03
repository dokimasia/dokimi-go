// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"
)

// The bounds of a run.
const (
	// defaultClients is the number of clients of the zero Clients.
	defaultClients = 3
	// pickLimit is the most picks within which a change point of PCT falls.
	pickLimit = 200
	// settleLimit is the most steps of the final phase.
	settleLimit = 10_000
	// maxJump is the largest jump of a clock and the largest offset of a
	// strobe, in either direction.
	maxJump = time.Hour
	// maxStrobe is the most steps of a strobe.
	maxStrobe = 16
	// maxTear is the most bytes that a torn write keeps, and maxFill the most
	// bytes that full media take before they refuse a write.
	maxTear = 64
	maxFill = 64
)

// kinds are the kinds of the faults that Tolerate and Require name, in the
// order of the actions of a run.
var kinds = []Fault{
	FaultCrash, FaultRestart, FaultPause, FaultPartition, FaultDrop, FaultDelay, FaultDuplicate,
	FaultLoseBefore, FaultLoseAfter, FaultOutage, FaultSlow, FaultClockJump, FaultClockStrobe,
	FaultTornWrite, FaultCorruption, FaultFullDisk,
}

// Simulation is a system of sans-IO machines with its oracles. S is the
// state of the spec of the calls of its clients.
type Simulation[E, F, S any] struct {
	// Anatomy is how the world reads the events and the effects.
	Anatomy Anatomy[E, F]
	// System builds the processes of each world and their dependencies.
	System System[E, F]
	// Spec is the sequential specification of the calls of the clients.
	Spec history.Spec[S]
	// Fingerprint returns a hash of the state of the system. A nil
	// Fingerprint observes the checksum of the log alone.
	Fingerprint func(w *World[E, F]) uint64
	// Tolerate bounds the faults in force at once, by their kind.
	Tolerate map[Fault]int
	// Require is the share of the cases in which each kind of fault strikes.
	Require map[Fault]float64

	// plant plants the defect form of a proof in every machine, and is nil
	// outside a proof of a rule.
	plant func(m Machine[E, F]) Machine[E, F]

	// Actions are the actions of the consumer. A final action sets Drain.
	Actions []Action[E, F]
	// Rules are the checks of the claims of sim that bind to the machines.
	Rules []Rule
	// Invariants, Converges and Scenarios are the oracles of the consumer.
	Invariants []Invariant[E, F]
	Converges  []Convergence[E, F]
	Scenarios  []Scenario[E, F]
	// Clients is the number of clients that make calls, and 3 for 0.
	Clients int
	// Strategy chooses the next process. The zero Strategy is PCT(3).
	Strategy Strategy
	// Isolation is the isolation level of the transactions of the clients,
	// and the zero Isolation checks none.
	Isolation Isolation
}

// Invariant is a safety property that the run checks after every step, with
// the system of its planted defect.
type Invariant[E, F any] struct {
	Check     func(h *Check, w *World[E, F])
	Defect    System[E, F]
	Statement string
}

// Convergence is a liveness property that the run checks once the faults
// have ended and Within has passed.
type Convergence[E, F any] struct {
	Check     func(h *Check, w *World[E, F])
	Statement string
	Within    time.Duration
}

// Scenario is a fixed run, whose events the crash points cover.
type Scenario[E, F any] struct {
	Run  func(w *World[E, F])
	Name string
}

// Action is an action of the consumer: a change that starts outside the
// machines, or a call of a client.
type Action[E, F any] struct {
	// Enabled reports whether a step may take the action, and is nil for an
	// action that every step may take.
	Enabled func(w *World[E, F]) bool
	// Input requests the input of a step from the case, and is nil for an
	// action without an input.
	Input func(c *prop.Case) any
	// Run runs the action on the world. client is a client without an open
	// call for an action with Call set, and 0 otherwise.
	Run func(w *World[E, F], client int, input any)
	// Name names the action in a counterexample.
	Name string
	// Call marks a call of a client. The run enables the action while a
	// client has no open call.
	Call bool
	// Drain marks an action of the final phase, which the run takes once.
	Drain bool
}

// Isolation is the isolation level that the transactions of the clients
// have.
type Isolation uint8

const (
	// Serializable checks the history with history.Serializable.
	Serializable Isolation = iota + 1
	// SnapshotIsolation checks the history with history.HasSnapshotIsolation.
	SnapshotIsolation
)

// Run runs one case of s on a new world: the active phase of the actions and
// the faults as the steps of a machine of the package stateful of the assert
// library, then the recovery and the final phase. It checks the rules and the
// invariants after every step. Once the final phase ends, it checks the
// convergences, the whole history against the spec, partitioned by the keys
// of the calls, and its isolation. The machine has no spec, so no check of
// the history puts every call into one partition. Run passes the name of each
// action that it takes to the case as a label.
func Run[E, F, S any](c *prop.Case, s Simulation[E, F, S]) {
	r := begin(c, s)
	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: r.actions(),
		Settle:  func(*prop.Case, struct{}) { r.settle() },
	})
}

// confirmEvery is the count of the passing cases of Test after which it
// replays a case to check that the run is deterministic.
const confirmEvery = 16

// Test runs the property of s on tb over random runs, with the shares of the
// faults that s requires, and then the crash points of each scenario of s. It
// replays every 16th passing case from its choices, and fails a run whose
// replay observes another checksum of a step as flaky.
func Test[E, F, S any](tb assert.TB, s Simulation[E, F, S]) {
	tb.Helper()
	opts := []prop.Option{prop.Confirm(confirmEvery)}
	for _, kind := range kinds {
		if share, required := s.Require[kind]; required {
			opts = append(opts, prop.Require(string(kind), share))
		}
	}
	prop.ForAll(tb, "the simulation finds no failure", func(c *prop.Case) { Run(c, s) }, opts...)
	for _, sc := range s.Scenarios {
		steps := scenario(tb, s, sc, point{})
		for step := 1; step <= steps; step++ {
			scenario(tb, s, sc, point{step: step})
			scenario(tb, s, sc, point{step: step, after: true})
		}
	}
}

// Falsify proves each rule and each invariant of s on its planted defect, in
// a parallel test of t named after the identity of the rule or the statement
// of the invariant, as Prove proves it.
func Falsify[E, F, S any](t *testing.T, s Simulation[E, F, S]) {
	t.Helper()
	checks := make([]string, 0, len(s.Rules)+len(s.Invariants))
	for _, rule := range s.Rules {
		checks = append(checks, rule.ID)
	}
	for _, inv := range s.Invariants {
		checks = append(checks, inv.Statement)
	}
	for _, check := range checks {
		t.Run(check, func(t *testing.T) {
			t.Helper()
			t.Parallel()
			s.Prove(t, check)
		})
	}
}

// Prove proves the check named check on tb with its planted defect: the rule
// whose identity is check on its defect form, or the invariant whose
// statement is check on its defect system. The random runs of the defect,
// and then the scenarios for an invariant, must fail at the seed of the
// 64-bit FNV-1a hash of check. Their first failure must be the failure of the
// assertion whose message is the statement of the check. Prove fails tb for
// a check that s does not have.
func (s Simulation[E, F, S]) Prove(tb assert.TB, check string) {
	tb.Helper()
	d := s.alone()
	var statement string
	var scenarios []Scenario[E, F]
	rule := slices.IndexFunc(s.Rules, func(r Rule) bool { return r.ID == check })
	inv := slices.IndexFunc(s.Invariants, func(i Invariant[E, F]) bool { return i.Statement == check })
	switch {
	case rule >= 0:
		form := s.Rules[rule].Form
		d.Rules, statement = s.Rules[rule:rule+1], s.Rules[rule].Statement
		d.plant = func(m Machine[E, F]) Machine[E, F] { return Plant(m, s.Anatomy, form) }
	case inv >= 0:
		d.System, d.Invariants, statement = s.Invariants[inv].Defect, s.Invariants[inv:inv+1], check
		scenarios = s.Scenarios
	default:
		tb.Fatalf("sim: the simulation has no check %q", check)
		return
	}
	seed := seedOf(check)
	contract := fmt.Sprintf("the planted defect fails the check %s at the seed %d", check, seed)
	got := assert.Rejects(tb, contract, func(tb assert.TB) {
		opts := []prop.Option{prop.Hermetic(), prop.Seed(seed), prop.Store(""), prop.Shrink(0)}
		prop.ForAll(tb, statement, func(c *prop.Case) { Run(c, d) }, opts...)
		for _, sc := range scenarios {
			prop.ForAll(tb, statement, func(c *prop.Case) {
				r := begin(c, d)
				sc.Run(r.w)
				r.w.Stop()
				r.check()
			}, append(slices.Clip(opts), prop.Cases(1))...)
		}
	})
	first := got[0]
	if inner, isCase := first.CaseFailure(); isCase {
		first = inner
	}
	if first.Contract != statement {
		tb.Fatalf("the planted defect of %s fails with the message %q. Its proof needs a failure of the assertion "+
			"whose message is the statement %q.", check, first.Contract, statement)
	}
}

// Fuzz registers the choices of a run of s as the input of the fuzz target f.
func Fuzz[E, F, S any](f *testing.F, s Simulation[E, F, S]) {
	f.Helper()
	prop.Fuzz(f, "the simulation finds no failure", func(c *prop.Case) { Run(c, s) })
}

// alone returns s without the spec of its calls and its oracles, for the
// proof of one check.
func (s Simulation[E, F, S]) alone() Simulation[E, F, S] {
	s.Spec = history.Spec[S]{}
	s.Rules, s.Invariants, s.Converges = nil, nil, nil
	return s
}

// scenario runs the scenario sc of s on tb once in a case of its own, with
// the crash point at, and returns the steps of the run.
func scenario[E, F, S any](tb assert.TB, s Simulation[E, F, S], sc Scenario[E, F], at point) int {
	tb.Helper()
	contract := "the scenario " + sc.Name + " finds no failure"
	switch {
	case at.after:
		contract += fmt.Sprintf(" with a crash after step %d", at.step)
	case at.step > 0:
		contract += fmt.Sprintf(" with a crash before step %d", at.step)
	}
	var steps int
	prop.ForAll(tb, contract, func(c *prop.Case) {
		r := begin(c, s)
		r.w.crash = at
		sc.Run(r.w)
		r.w.Stop()
		r.check()
		if s.Spec.Next != nil {
			history.Linearizable(c, r.w.History(), s.Spec, "the history of the calls of the clients is linearizable")
		}
		steps = r.w.steps
	}, prop.Cases(1), prop.Store(""))
	return steps
}

// seedOf returns the seed of a proof from the key of its check: the 64-bit
// FNV-1a hash of the key.
func seedOf(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

// run is one case of a simulation: its world and its strategy.
type run[E, F, S any] struct {
	c    *prop.Case
	w    *World[E, F]
	pick *picker[E, F]
	s    Simulation[E, F, S]
}

// input is the input of an action of the consumer: its client and its value.
type input struct {
	value  any
	client int
}

// begin builds the world of the case c for s, checks it after every step, and
// notes its log and its history once the case ends.
func begin[E, F, S any](c *prop.Case, s Simulation[E, F, S]) *run[E, F, S] {
	w := New(c, s.Anatomy)
	w.plant = s.plant
	r := &run[E, F, S]{c: c, w: w, pick: newPicker[E, F](c, s.Strategy, pickLimit), s: s}
	w.after = r.check
	c.Cleanup(r.report)
	s.System(w)
	return r
}

// report notes the log of the world and the history of the case on the case,
// which reports its notes after its counterexample when it fails. A line of
// the log has the step, its process, its epoch, the time of its clock and the
// faults in force. It ends with the event and the effects of the step and the
// error of the machine after the step, or with the event that the world
// dropped. A line of the history has the event, its client, its kind, its
// operation, and the arguments, the output or the error of the call. The
// report writes a value as the verb %+v of fmt writes it, after its type.
func (r *run[E, F, S]) report() {
	var b strings.Builder
	b.WriteString("the log of the run:")
	for i, e := range r.w.Log() {
		fmt.Fprintf(&b, "\nstep %d: %s, epoch %d, clock %v", i+1, e.Process, e.Epoch, e.Time.Sub(Start))
		if len(e.Faults) > 0 {
			fmt.Fprintf(&b, ", faults %v", e.Faults)
		}
		if e.Dropped {
			fmt.Fprintf(&b, ", dropped %T%+v", e.Event, e.Event)
			continue
		}
		fmt.Fprintf(&b, ": %T%+v", e.Event, e.Event)
		sep := " -> "
		for _, f := range e.Effects {
			fmt.Fprintf(&b, "%s%T%+v", sep, f, f)
			sep = ", "
		}
		if e.Err != nil {
			fmt.Fprintf(&b, ", error %v", e.Err)
		}
	}
	r.c.Logf("%s", b.String())
	b.Reset()
	b.WriteString("the history of the calls:")
	events := r.w.History().Events()
	for i := range events {
		e := &events[i]
		var value any
		switch e.Kind {
		case history.Invoke:
			value = e.Args
		case history.OK:
			value = e.Output
		default:
			value = e.Error
		}
		fmt.Fprintf(&b, "\nevent %d: client %d %s %s %v", e.Index, e.Client, e.Kind, events[e.Call].Operation, value)
	}
	r.c.Logf("%s", b.String())
}

// act returns the action of the run named name, which a step may take while
// enabled reports true, whose input the case chooses through input, and which
// passes its name to the case as a label and its input to run.
func act[I any](
	name string, enabled func() bool, input func(c *prop.Case) I, run func(in I),
) stateful.Action[struct{}] {
	a := fault(name, enabled, input, run)
	unlabelled := a.Run
	a.Run = func(c *prop.Case, client int, in any) {
		c.Classify(name)
		unlabelled(c, client, in)
	}
	return a
}

// fault returns the action of the run named name that strikes or arms a
// fault, as act does, without a label. The world passes the kind of the fault
// to the case when the fault strikes, so an armed loss that never strikes
// gives its case no label.
func fault[I any](
	name string, enabled func() bool, input func(c *prop.Case) I, run func(in I),
) stateful.Action[struct{}] {
	return stateful.Action[struct{}]{
		Name:    name,
		Enabled: func(struct{}) bool { return enabled() },
		Input:   func(c *prop.Case, _ struct{}) any { return input(c) },
		Run: func(_ *prop.Case, _ int, in any) {
			typed, _ := in.(I)
			run(typed)
		},
	}
}

// actions returns the actions of the active phase of the run: the delivery
// and the advance of time, the cancel of a call, the faults within the
// bounds of Tolerate and their ends, and the actions of the consumer that are
// not final.
func (r *run[E, F, S]) actions() []stateful.Action[struct{}] {
	w := r.w
	out := []stateful.Action[struct{}]{
		act("deliver", func() bool { return len(w.runnable()) > 0 },
			func(*prop.Case) string { return r.pick.pick(w.runnable()).name }, func(name string) { w.Step(name) }),
		act("advance", func() bool {
			_, pending := w.Next()
			return pending
		}, func(c *prop.Case) time.Duration {
			gap, _ := w.Next()
			return gap + c.Draw(prop.Duration(0, gap), "advance")
		}, w.Advance),
	}
	if r.s.Anatomy.Cancelled != nil {
		out = append(out, act("cancel", func() bool { return len(w.callers()) > 0 },
			func(c *prop.Case) int { return choose(c, w.callers(), "client") }, w.abort))
	}
	out = append(out, r.faults()...)
	for _, a := range r.s.Actions {
		if !a.Drain {
			out = append(out, stateful.Action[struct{}]{
				Name:    a.Name,
				Enabled: func(struct{}) bool { return r.available(a) },
				Input:   func(c *prop.Case, _ struct{}) any { return r.input(c, a) },
				Run: func(c *prop.Case, _ int, x any) {
					c.Classify(a.Name)
					in, _ := x.(input)
					a.Run(r.w, in.client, in.value)
				},
			})
		}
	}
	return out
}

// available reports whether the run may take the action a of the consumer: a
// call needs a client without an open call.
func (r *run[E, F, S]) available(a Action[E, F]) bool {
	if a.Call && len(r.idle()) == 0 {
		return false
	}
	return a.Enabled == nil || a.Enabled(r.w)
}

// input requests the input of the action a of the consumer from the case c:
// a client without an open call for a call, and the value of the input of a.
func (r *run[E, F, S]) input(c *prop.Case, a Action[E, F]) input {
	var in input
	if a.Call {
		in.client = choose(c, r.idle(), "client")
	}
	if a.Input != nil {
		in.value = a.Input(c)
	}
	return in
}

// settleDown delivers each pending event and advances the time until the
// bound of each convergence has passed. It fails the case for a system that
// does not settle within settleLimit steps.
func (r *run[E, F, S]) settleDown() {
	bound := r.w.elapsed
	for _, cv := range r.s.Converges {
		bound = max(bound, r.w.elapsed+cv.Within)
	}
	for range settleLimit {
		if pending := r.w.runnable(); len(pending) > 0 {
			r.w.Step(r.pick.pick(pending).name)
			continue
		}
		left := bound - r.w.elapsed
		if left <= 0 {
			return
		}
		gap, pending := r.w.Next()
		if !pending {
			gap = left
		}
		r.w.Advance(min(gap, left))
	}
	r.c.Fatalf("sim: the system takes more than %d steps to settle", settleLimit)
}

// check observes the checksum and the fingerprint, and checks each rule and
// each invariant. It fails the case for a rule whose probe has no judge.
func (r *run[E, F, S]) check() {
	log := r.w.Log()
	r.c.Observe(r.w.Checksum())
	if r.s.Fingerprint != nil {
		r.c.Observe(r.s.Fingerprint(r.w))
	}
	for _, rule := range r.s.Rules {
		problems, known := judge(rule.Probe, r.s.Anatomy, log)
		if !known {
			r.c.Fatalf("sim: the probe %s of the rule %s has no judge", rule.Probe, rule.ID)
		}
		assert.Empty(r.c, problems, rule.Statement)
	}
	for _, inv := range r.s.Invariants {
		inv.Check(&Check{Case: r.c, statement: inv.Statement}, r.w)
	}
}

// settle runs the recovery and the final phase once the steps of the active
// phase have ended: it ends each fault in force, takes each final action of
// the consumer that is available once, in the order of the actions, and
// settles. It then checks each convergence, stops the world, checks the rules
// and the invariants once more, and checks the history against the spec,
// partitioned by the keys of the calls, and its isolation.
func (r *run[E, F, S]) settle() {
	r.w.Recover()
	for _, a := range r.s.Actions {
		if a.Drain && r.available(a) {
			in := r.input(r.c, a)
			a.Run(r.w, in.client, in.value)
		}
	}
	r.settleDown()
	for _, cv := range r.s.Converges {
		cv.Check(&Check{Case: r.c, statement: cv.Statement}, r.w)
	}
	r.w.Stop()
	r.check()
	h := r.w.History()
	if r.s.Spec.Next != nil {
		history.Linearizable(r.c, h, r.s.Spec, "the history of the calls of the clients is linearizable")
	}
	switch r.s.Isolation {
	case Serializable:
		history.Serializable(r.c, h, "the transactions of the clients are serializable")
	case SnapshotIsolation:
		history.HasSnapshotIsolation(r.c, h, "the transactions of the clients have snapshot isolation")
	}
}

// idle returns the clients without an open call.
func (r *run[E, F, S]) idle() []int {
	busy := r.w.callers()
	var out []int
	for client := range cmp.Or(r.s.Clients, defaultClients) {
		if !slices.Contains(busy, client) {
			out = append(out, client)
		}
	}
	return out
}

// faults returns the action of each fault that Tolerate names, and the end
// of each fault that lasts, in the order of kinds.
func (r *run[E, F, S]) faults() []stateful.Action[struct{}] {
	w := r.w
	within := func(kind Fault) bool { return r.inForce(kind) < r.s.Tolerate[kind] }
	process := func(match func(p *Process[E, F]) bool) func(c *prop.Case) string {
		return func(c *prop.Case) string { return choose(c, r.processes(match), "process") }
	}
	some := func(match func(p *Process[E, F]) bool) func() bool {
		return func() bool { return len(r.processes(match)) > 0 }
	}
	target := func(match func(d *dependency[E, F]) bool) func(c *prop.Case) string {
		return func(c *prop.Case) string { return choose(c, r.dependencies(match), "dependency") }
	}
	tolerated := func(kind Fault, match func(d *dependency[E, F]) bool) func() bool {
		return func() bool { return within(kind) && len(r.dependencies(match)) > 0 }
	}
	alive := func(p *Process[E, F]) bool { return p.state == running }
	up := func(p *Process[E, F]) bool { return p.state == running && !p.paused }
	down := func(p *Process[E, F]) bool { return p.state == crashed }
	asleep := func(p *Process[E, F]) bool { return p.paused }
	seen := func(*dependency[E, F]) bool { return true }
	calm := func(d *dependency[E, F]) bool { return !d.outage }
	fast := func(d *dependency[E, F]) bool { return !d.slow }
	out := func(d *dependency[E, F]) bool { return d.outage }
	slow := func(d *dependency[E, F]) bool { return d.slow }
	spawned := func() bool { return len(w.procs) > 0 }
	byKind := map[Fault][]stateful.Action[struct{}]{
		FaultCrash: {
			fault(string(FaultCrash), func() bool { return within(FaultCrash) && some(alive)() }, process(alive),
				w.Crash),
		},
		FaultRestart: {fault(string(FaultRestart), some(down), process(down), w.Restart)},
		FaultPause: {
			fault(string(FaultPause), func() bool { return within(FaultPause) && some(up)() }, process(up), w.Pause),
			act("resume", some(asleep), process(asleep), w.Resume),
		},
		FaultPartition: {
			fault(string(FaultPartition), func() bool { return w.sides == nil && spawned() }, r.sides,
				func(sides [][]string) { w.Partition(sides...) }),
			fault(string(FaultRepair), func() bool { return w.sides != nil }, func(*prop.Case) any { return nil },
				func(any) { w.Repair() }),
		},
		FaultDrop: {
			fault(string(FaultDrop), some(w.sent), process(w.sent), func(to string) { w.drop(w.named(to)) }),
		},
		FaultDelay: {
			fault(string(FaultDelay), some(w.behind), process(w.behind), func(to string) { w.delay(w.named(to)) }),
		},
		FaultDuplicate: {
			fault(string(FaultDuplicate), some(w.sent), process(w.sent),
				func(to string) { w.duplicate(w.named(to)) }),
		},
		FaultLoseBefore: {
			fault(string(FaultLoseBefore), tolerated(FaultLoseBefore, seen), target(seen), w.LoseBefore),
		},
		FaultLoseAfter: {
			fault(string(FaultLoseAfter), tolerated(FaultLoseAfter, seen), target(seen), w.LoseAfter),
		},
		FaultOutage: {
			fault(string(FaultOutage), tolerated(FaultOutage, calm), target(calm), w.Outage),
			act("restore-outage", func() bool { return len(r.dependencies(out)) > 0 }, target(out), w.Restore),
		},
		FaultSlow: {
			fault(string(FaultSlow), tolerated(FaultSlow, fast), target(fast), w.Slow),
			act("restore-slow", func() bool { return len(r.dependencies(slow)) > 0 }, target(slow), w.Restore),
		},
		FaultClockJump: {
			fault(string(FaultClockJump), spawned, r.jump, func(j jump) { w.Jump(j.name, j.d) }),
		},
		FaultClockStrobe: {
			fault(string(FaultClockStrobe), func() bool { return within(FaultClockStrobe) && spawned() }, r.strobe,
				func(s strobing) { w.Strobe(s.name, s.a, s.b, s.steps) }),
		},
		FaultTornWrite: {
			fault(string(FaultTornWrite), some(stored[E, F]), r.tear, func(t tearing) { w.Tear(t.name, t.n) }),
		},
		FaultCorruption: {
			fault(string(FaultCorruption), some(filed[E, F]), r.corruption,
				func(k corrupting) { w.Corrupt(k.name, k.file, k.offset) }),
		},
		FaultFullDisk: {
			fault(string(FaultFullDisk), func() bool { return within(FaultFullDisk) && some(roomy[E, F])() }, r.fill,
				func(f filling) { w.Fill(f.name, f.n) }),
			act("restore-disk", some(full[E, F]), process(full[E, F]), w.RestoreDisk),
		},
	}
	var actions []stateful.Action[struct{}]
	for _, kind := range kinds {
		if r.s.Tolerate[kind] > 0 {
			actions = append(actions, byKind[kind]...)
		}
	}
	return actions
}

// The processes that the faults of media strike: stored runs over media in
// memory, filed has a file with a byte on its media, roomy has media that are
// not full, and full has full media.
func stored[E, F any](p *Process[E, F]) bool { return p.state == running && len(media(p)) > 0 }

func filed[E, F any](p *Process[E, F]) bool { return len(filesOf(p)) > 0 }

func roomy[E, F any](p *Process[E, F]) bool { return len(media(p)) > 0 && !p.full }

func full[E, F any](p *Process[E, F]) bool { return p.full }

// tearing is the input of a torn write: a process and the bytes that its
// torn write keeps.
type tearing struct {
	name string
	n    int
}

// tear returns the input of a torn write that the case chooses: a process
// that runs over media in memory, and the bytes that the write keeps.
func (r *run[E, F, S]) tear(c *prop.Case) tearing {
	return tearing{name: choose(c, r.processes(stored[E, F]), "process"), n: c.Draw(prop.Integer(0, maxTear), "tear")}
}

// corrupting is the input of a corruption: a process, a file of its media and
// the offset of the byte whose bit flips.
type corrupting struct {
	name, file string
	offset     int64
}

// corruption returns the input of a corruption that the case chooses: a
// process with a file on its media, the file, and an offset in it.
func (r *run[E, F, S]) corruption(c *prop.Case) corrupting {
	name := choose(c, r.processes(filed[E, F]), "process")
	f := choose(c, filesOf(r.w.named(name)), "file")
	return corrupting{name: name, file: f.name, offset: c.Draw(prop.Integer(0, f.size-1), "offset")}
}

// filling is the input of a fill: a process, and the bytes that its media
// take before they refuse a write.
type filling struct {
	name string
	n    int64
}

// fill returns the input of a fill that the case chooses: a process with
// media that are not full, and the bytes that they take.
func (r *run[E, F, S]) fill(c *prop.Case) filling {
	return filling{
		name: choose(c, r.processes(roomy[E, F]), "process"),
		n:    c.Draw(prop.Integer[int64](0, maxFill), "fill"),
	}
}

// file is a file of a medium with a byte: its name and its size.
type file struct {
	name string
	size int64
}

// filesOf returns each file with a byte on the media of p, from the root of
// each medium down and in the order of the names in each directory.
func filesOf[E, F any](p *Process[E, F]) []file {
	var out []file
	for _, m := range media(p) {
		for dirs := []string{"."}; len(dirs) > 0; dirs = dirs[1:] {
			entries, _ := m.ReadDir(dirs[0])
			for _, e := range entries {
				name := path.Join(dirs[0], e.Name())
				info, _ := e.Info()
				switch {
				case e.IsDir():
					dirs = append(dirs, name)
				case info.Size() > 0:
					out = append(out, file{name: name, size: info.Size()})
				}
			}
		}
	}
	return out
}

// jump is the input of a jump of a clock.
type jump struct {
	name string
	d    time.Duration
}

// strobing is the input of a strobe of a clock.
type strobing struct {
	name  string
	a, b  time.Duration
	steps int
}

// jump returns the input of a jump that the case chooses: a process and an
// amount.
func (r *run[E, F, S]) jump(c *prop.Case) jump {
	return jump{name: choose(c, r.names(), "process"), d: c.Draw(prop.Duration(-maxJump, maxJump), "jump")}
}

// strobe returns the input of a strobe that the case chooses: a process, two
// offsets and the steps of the strobe.
func (r *run[E, F, S]) strobe(c *prop.Case) strobing {
	return strobing{
		name:  choose(c, r.names(), "process"),
		a:     c.Draw(prop.Duration(-maxJump, maxJump), "strobe.a"),
		b:     c.Draw(prop.Duration(-maxJump, maxJump), "strobe.b"),
		steps: c.Draw(prop.Integer(1, maxStrobe), "strobe.steps"),
	}
}

// inForce returns the faults of the kind in force at a target, such as
// crash:store.
func (r *run[E, F, S]) inForce(kind Fault) int {
	n := 0
	for _, f := range r.w.Faults() {
		if strings.HasPrefix(f, string(kind)+":") {
			n++
		}
	}
	return n
}

// processes returns the names of the processes that match, in the order of
// their spawns.
func (r *run[E, F, S]) processes(match func(p *Process[E, F]) bool) []string {
	var out []string
	for _, p := range r.w.procs {
		if match(p) {
			out = append(out, p.name)
		}
	}
	return out
}

// names returns the names of the processes in the order of their spawns.
func (r *run[E, F, S]) names() []string {
	return r.processes(func(*Process[E, F]) bool { return true })
}

// dependencies returns the names of the dependencies that the world has seen
// and that match, in the order in which the world first saw them.
func (r *run[E, F, S]) dependencies(match func(d *dependency[E, F]) bool) []string {
	var out []string
	for _, d := range r.w.deps {
		if match(d) {
			out = append(out, d.name)
		}
	}
	return out
}

// sides returns the sides of a partition in a shape that the case chooses:
// isolate, halves, majorities-ring or bridge.
func (r *run[E, F, S]) sides(c *prop.Case) [][]string {
	names := r.names()
	n := len(names)
	switch c.Draw(prop.Integer(0, 3), "partition") {
	case 0:
		at := c.Draw(prop.Integer(0, n-1), "isolate")
		return [][]string{{names[at]}, slices.Delete(slices.Clone(names), at, at+1)}
	case 1:
		start := c.Draw(prop.Integer(0, n-1), "halves")
		ring := append(slices.Clone(names[start:]), names[:start]...)
		return [][]string{ring[:n/2], ring[n/2:]}
	case 2:
		out := make([][]string, n)
		for i := range n {
			for k := range n/2 + 1 {
				out[i] = append(out[i], names[(i+k)%n])
			}
		}
		return out
	default:
		at := c.Draw(prop.Integer(0, n-1), "bridge")
		rest := slices.Delete(slices.Clone(names), at, at+1)
		half := len(rest) / 2
		return [][]string{append(slices.Clone(rest[:half]), names[at]), append(slices.Clone(rest[half:]), names[at])}
	}
}

// choose returns the item of items that the case chooses under label.
func choose[T any](c *prop.Case, items []T, label string) T {
	return items[c.Draw(prop.Integer(0, len(items)-1), label)]
}
