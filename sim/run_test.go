// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
	"go.dokimi.dev/sim"
	"go.dokimi.dev/spec"
)

// every tolerates one fault of each kind at once.
var every = map[sim.Fault]int{
	sim.FaultCrash: 1, sim.FaultRestart: 1, sim.FaultPause: 1, sim.FaultPartition: 1, sim.FaultDrop: 1,
	sim.FaultDelay: 1, sim.FaultDuplicate: 1, sim.FaultLoseBefore: 1, sim.FaultLoseAfter: 1, sim.FaultOutage: 1,
	sim.FaultSlow: 1, sim.FaultClockJump: 1, sim.FaultClockStrobe: 1,
}

// media are the faults of the media of a host, each in force once at most.
var media = map[sim.Fault]int{sim.FaultTornWrite: 1, sim.FaultCorruption: 1, sim.FaultFullDisk: 1}

// rejecting is a spec that explains no call.
var rejecting = history.Spec[int]{
	Initial: func() int { return 0 },
	Next:    func(int, history.Operation) []int { return nil },
}

// scattered is a machine that replies to its start with the first of 64 keys
// of a map that Go iterates, so two runs of one case reply with other keys.
type scattered struct{}

// Step replies to the start with the first key of a map in the order of its
// iteration.
func (scattered) Step(ev event, out []effect) []effect {
	s, started := ev.(started)
	if !started {
		return out
	}
	keys := map[int]bool{}
	for i := range 64 {
		keys[i] = true
	}
	for k := range keys {
		return append(out, reply{to: s.reply, value: k})
	}
	return out
}

// Err returns nil, because the machine never stops itself.
func (scattered) Err() error { return nil }

// newScattered returns a scattered machine.
func newScattered(*sim.Clock) sim.Machine[event, effect] { return scattered{} }

// replies returns the values of the replies of the steps of the process
// name in the log of w.
func replies(w *sim.World[event, effect], name string) []any {
	var values []any
	for _, e := range w.Log() {
		for _, f := range e.Effects {
			if r, ok := f.(reply); ok && e.Process == name {
				values = append(values, r.value)
			}
		}
	}
	return values
}

// unstored is an invariant that fails once a store has stored a put. Its
// planted defect is the system of the simulation of the specs, on which it
// fails.
func unstored() sim.Invariant[event, effect] {
	return sim.Invariant[event, effect]{
		Statement: "no store replies to a put",
		Check: func(h *sim.Check, w *sim.World[event, effect]) {
			expect.NotContains(h, slices.Concat(replies(w, "a"), replies(w, "b")), "stored", h.Statement())
		},
		Defect: simulation().System,
	}
}

// unstopped is an invariant that fails once the store a stops after it has
// stored a put. Its planted defect is the system of the simulation of the
// specs, on which a scenario with a put fails it.
func unstopped() sim.Invariant[event, effect] {
	return sim.Invariant[event, effect]{
		Statement: "no store stops after it stores a put",
		Check: func(h *sim.Check, w *sim.World[event, effect]) {
			stored := slices.Contains(replies(w, "a"), any("stored"))
			expect.False(h, stored && slices.Contains(events(w, "a"), event(stopped{})), h.Statement())
		},
		Defect: simulation().System,
	}
}

// eventless is a convergence that fails once a store has taken an event by the
// end of the final phase. Its planted defect is the system of the simulation
// of the specs, whose puts give the stores events.
func eventless() sim.Convergence[event, effect] {
	return sim.Convergence[event, effect]{
		Statement: "the stores take no event once the faults end",
		Check: func(h *sim.Check, w *sim.World[event, effect]) {
			expect.Empty(h, w.Log(), h.Statement())
		},
		Defect: simulation().System,
	}
}

// tally is the count of the steps of the cases of a simulation: the steps of
// every case, and the most steps of one case.
type tally struct {
	total, most int
}

// counting returns a simulation without processes whose one action counts the
// steps of its cases into the tally. Its system starts the count of a case at
// 0.
func counting() (sim.Simulation[event, effect, int], *tally) {
	var steps int
	t := &tally{}
	s := simulation()
	s.System = fixed(func(*sim.World[event, effect]) { steps = 0 })
	s.Actions = []sim.Action[event, effect]{{
		Name: "count",
		Run: func(*sim.World[event, effect], int, any) {
			steps++
			t.total++
			t.most = max(t.most, steps)
		},
	}}
	return s, t
}

// observe passes the effects that only the faults of a run cause to h as
// labels: a partition after a repair, a message to the store b that b never
// takes, takes twice or takes out of order, and a step on a clock before the
// start.
func observe(h *sim.Check, w *sim.World[event, effect]) {
	var sent int
	var taken []uint64
	partitions, cut := 0, false
	for _, e := range w.Log() {
		in := slices.Contains(e.Faults, string(sim.FaultPartition))
		if in && !cut {
			partitions++
		}
		cut = in
		if e.Time.Before(sim.Start) {
			h.Classify("a step before the start")
		}
		for _, f := range e.Effects {
			if _, ok := f.(call); ok {
				sent++
			}
		}
		if p, ok := e.Event.(put); ok && e.Process == "b" && !e.Dropped {
			taken = append(taken, p.reply)
		}
	}
	if partitions > 1 {
		h.Classify("a second partition")
	}
	if !slices.IsSorted(taken) {
		h.Classify("a reordered message")
	}
	if slices.Contains(events(w, "b"), event(stopped{})) {
		switch {
		case len(taken) < sent:
			h.Classify("a lost message")
		case len(taken) > sent:
			h.Classify("a duplicated message")
		}
	}
}

// fatal runs fn on a recorder that ends fn at its first fatal failure, and
// returns the message of the failure.
func fatal(fn func(tb assert.TB)) string {
	r := assert.NewRecorder().WithGoexit()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(r)
	}()
	<-done
	return r.Message()
}

// tick returns an action that delivers the tick token to the store a.
func tick(name string, token uint64) sim.Action[event, effect] {
	return sim.Action[event, effect]{
		Name: name,
		Run:  func(w *sim.World[event, effect], _ int, _ any) { w.Deliver("a", ticked{token: token}) },
	}
}

// ticks returns the tokens of the ticks that the process name stepped on, in
// the order of its steps.
func ticks(w *sim.World[event, effect], name string) []uint64 {
	var out []uint64
	for _, ev := range events(w, name) {
		if t, ok := ev.(ticked); ok {
			out = append(out, t.token)
		}
	}
	return out
}

// txn is an inbound call of a transaction with its operations.
type txn struct {
	ops   []any
	reply uint64
}

func (txn) event() {}

// ticker is a machine that sets a timer of no duration at each event.
type ticker struct{}

func (ticker) Step(_ event, out []effect) []effect { return append(out, timer{token: 1}) }
func (ticker) Err() error                          { return nil }

// ledger is a machine that replies to each transaction with its operations.
type ledger struct{}

func (ledger) Step(ev event, out []effect) []effect {
	if t, ok := ev.(txn); ok {
		return append(out, reply{to: t.reply, value: t.ops})
	}
	if s, ok := ev.(started); ok {
		return append(out, reply{to: s.reply})
	}
	return out
}

func (ledger) Err() error { return nil }

// periodic is a machine that sets a timer of every at each event.
type periodic struct{ every time.Duration }

func (m periodic) Step(_ event, out []effect) []effect {
	return append(out, timer{token: 1, after: m.every})
}

func (periodic) Err() error { return nil }

// errFragile is the error with which a fragile machine stops itself.
const errFragile storeError = "fragile: the machine stopped"

// fragile is a machine that stops itself with errFragile once it has run for
// life after its start, and at its start for a life of 0.
type fragile struct {
	err  error
	life time.Duration
}

func (m *fragile) Step(ev event, out []effect) []effect {
	switch ev := ev.(type) {
	case started:
		if m.life == 0 {
			m.err = errFragile
			return out
		}
		return append(out, reply{to: ev.reply}, timer{token: 1, after: m.life})
	case ticked:
		m.err = errFragile
	}
	return out
}

func (m *fragile) Err() error { return m.err }

// timed is a machine that sets one timer of after on its start.
type timed struct{ after time.Duration }

func (m timed) Step(ev event, out []effect) []effect {
	if _, ok := ev.(started); ok {
		return append(out, timer{token: 1, after: m.after})
	}
	return out
}

func (timed) Err() error { return nil }

// halting is a machine that replies to its start and stops itself in the same
// step.
type halting struct{ err error }

func (h *halting) Step(ev event, out []effect) []effect {
	if s, ok := ev.(started); ok {
		h.err = errEmpty
		return append(out, reply{to: s.reply})
	}
	return out
}

func (h *halting) Err() error { return h.err }

// starts counts the starts of the machine of type counted in every run.
var starts atomic.Uint64

// counted is a machine whose reply to its start contains the count of the
// starts of its type, so a replay of a case replies with other values.
type counted struct{}

func (counted) Step(ev event, out []effect) []effect {
	if s, ok := ev.(started); ok {
		return append(out, reply{to: s.reply, value: starts.Add(1)})
	}
	return out
}

func (counted) Err() error { return nil }

// shape returns the shape of the nemesis of Jepsen that the sides of a
// partition of the hosts names have, and the empty string for none.
func shape(names []string, sides [][]string) string {
	n := len(names)
	ring := func(start int) []string { return append(slices.Clone(names[start:]), names[:start]...) }
	for at, name := range names {
		rest := slices.Delete(slices.Clone(names), at, at+1)
		if slices.EqualFunc(sides, [][]string{{name}, rest}, slices.Equal) {
			return "isolate"
		}
		half := len(rest) / 2
		bridge := [][]string{append(slices.Clone(rest[:half]), name), append(slices.Clone(rest[half:]), name)}
		if slices.EqualFunc(sides, bridge, slices.Equal) {
			return "bridge"
		}
		if r := ring(at); slices.EqualFunc(sides, [][]string{r[:n/2], r[n/2:]}, slices.Equal) {
			return "halves"
		}
	}
	majorities := make([][]string, n)
	for i := range n {
		majorities[i] = ring(i)[:n/2+1]
	}
	if slices.EqualFunc(sides, majorities, slices.Equal) {
		return "majorities-ring"
	}
	return ""
}

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("Run", func(t *testing.T) {
		t.Parallel()

		// Every case delivers, so the queues of the stores are short and delay
		// strikes a message behind another in fewer cases. A mean of 100 steps
		// gives each fault 1% of the cases.
		t.Run("finds no failure in machines that keep the rules under every fault", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.MeanSteps, s.MaxSteps = 100, 300
			s.Tolerate = maps.Clone(every)
			s.Tolerate[sim.FaultCrash], s.Tolerate[sim.FaultPause] = 2, 2
			always := s.Actions[0]
			always.Name = "always"
			always.Enabled = func(*sim.World[event, effect]) bool { return true }
			s.Actions = append(s.Actions, always)
			labels := make([]string, 0, 14+len(every))
			labels = append(labels,
				"deliver", "advance", "cancel", "resume", "repair", "restore-outage", "restore-slow", "put", "park",
				"forward", "always", "sim_test.commit", "sim_test.reply", "sim_test.call")
			for kind := range every {
				labels = append(labels, string(kind))
			}
			opts := make([]prop.Option, 0, 3+len(labels))
			opts = append(opts, prop.Hermetic(), prop.Seed(1), prop.Store(""))
			for _, label := range labels {
				opts = append(opts, prop.Require(label, 0.01))
			}
			prop.ForAll(t, "the stores keep the rules", func(c *prop.Case) { sim.Run(c, s) }, opts...)
		})

		t.Run("keeps the faults in force within the bounds of tolerate", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = after(s.System, func(w *sim.World[event, effect]) { w.Spawn("c", newStore, mediumWith("")) })
			s.Actions = append(s.Actions, sim.Action[event, effect]{
				Name: "tick c",
				Run:  func(w *sim.World[event, effect], _ int, _ any) { w.Deliver("c", ticked{token: 1}) },
			})
			s.Tolerate = maps.Clone(every)
			maps.Copy(s.Tolerate, media)
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the faults in force stay within tolerate",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					for kind, bound := range s.Tolerate {
						n := 0
						for _, f := range w.Faults() {
							if strings.HasPrefix(f, string(kind)+":") {
								n++
							}
						}
						assert.True(h, n <= bound, h.Statement())
					}
				},
			}}
			prop.ForAll(t, "the faults stay within tolerate", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require(string(sim.FaultCrash), 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("counts a crash of a host of two processes once against tolerate", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				h := w.AddHost("node")
				h.Spawn("a", newStore, mediumWith("data"))
				h.Spawn("b", newStore)
				w.Spawn("c", newStore)
			})
			s.Tolerate = map[sim.Fault]int{sim.FaultCrash: 1}
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "one host at most is down",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					up := func(f string) bool { return !strings.HasPrefix(f, "crash:") }
					down := slices.DeleteFunc(w.Faults(), up)
					assert.True(h, len(down) <= 1, h.Statement())
					if slices.Contains(down, "crash:node") {
						h.Classify("the host node is down")
					}
				},
			}}
			prop.ForAll(t, "one host at most is down", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("the host node is down", 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("restarts an exited host through the action restart", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions = append(s.Actions, sim.Action[event, effect]{
				Name: "empty",
				Call: true,
				Run: func(w *sim.World[event, effect], client int, _ any) {
					w.Call(client, "a", "Put", nil, nil, func(r uint64) event { return put{key: "k", reply: r} })
				},
			})
			s.Tolerate = map[sim.Fault]int{sim.FaultRestart: 1}
			prop.ForAll(t, "the action restart starts the exited store", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require(string(sim.FaultRestart), 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("takes at most MaxSteps steps in the active phase of a case", func(t *testing.T) {
			t.Parallel()

			s, steps := counting()
			s.MaxSteps = 3
			prop.ForAll(t, "each case counts its steps", func(c *prop.Case) { sim.Run(c, s) },
				prop.Hermetic(), prop.Seed(1), prop.Store(""), prop.Workers(1))
			assert.True(t, steps.most <= 3, "no case of the simulation takes more than three steps")
		})

		t.Run("takes a mean of MeanSteps steps in the active phase of a case", func(t *testing.T) {
			t.Parallel()

			long, longSteps := counting()
			long.MeanSteps, long.MaxSteps = 300, 1000
			base, baseSteps := counting()
			base.MaxSteps = 1000
			for _, s := range []sim.Simulation[event, effect, int]{long, base} {
				prop.ForAll(t, "each case counts its steps", func(c *prop.Case) { sim.Run(c, s) },
					prop.Hermetic(), prop.Seed(1), prop.Store(""), prop.Workers(1))
			}
			assert.True(t, longSteps.total > baseSteps.total,
				"the cases of a mean of 300 steps take more steps than the cases of the default mean of 30")
		})

		// The action feed queues more ticks than a run of deliveries takes, so
		// the active phase lists the deliveries and feed alone. advance and
		// cancel are not enabled. A case whose swarm keeps deliver fast takes
		// four deliveries for each feed, at the weight 1 of deliver and the
		// weight 3 of the three other actions. A case without it takes one.
		t.Run("keeps a second delivery at the weight of the other actions in half of the cases", func(t *testing.T) {
			t.Parallel()

			var fed, taken, fastFed, fastTaken, slowFed, slowTaken, fast, slow int
			s := simulation()
			s.MeanSteps, s.MaxSteps = 100, 300
			s.System = fixed(func(w *sim.World[event, effect]) {
				fed, taken = 0, 0
				w.Spawn("a", newStore)
			})
			s.Actions = []sim.Action[event, effect]{{
				Name: "feed",
				Run: func(w *sim.World[event, effect], _ int, _ any) {
					fed++
					for range 50 {
						w.Deliver("a", ticked{})
					}
				},
			}, {
				Name:  "count",
				Drain: true,
				Run: func(w *sim.World[event, effect], _ int, _ any) {
					for _, e := range w.Log() {
						if _, tick := e.Event.(ticked); tick {
							taken++
						}
					}
					switch {
					case fed == 0:
					case taken > 2*fed:
						fast, fastFed, fastTaken = fast+1, fastFed+fed, fastTaken+taken
					default:
						slow, slowFed, slowTaken = slow+1, slowFed+fed, slowTaken+taken
					}
				},
			}}
			prop.ForAll(t, "each case feeds the store a", func(c *prop.Case) { sim.Run(c, s) },
				prop.Hermetic(), prop.Seed(1), prop.Store(""), prop.Workers(1))
			assert.CloseTo(t, float64(fastTaken)/float64(fastFed), 4, 0.5,
				"the cases of fast deliveries take four for each feed")
			assert.CloseTo(t, float64(slowTaken)/float64(slowFed), 1, 0.5,
				"the cases of slow deliveries take one for each feed")
			assert.CloseTo(t, float64(fast)/float64(fast+slow), 0.5, 0.2, "about half of the cases deliver fast")
		})

		t.Run("advances the time in every case that arms a timer", func(t *testing.T) {
			t.Parallel()

			var armed, advanced int
			var arms bool
			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				arms = false
				w.Spawn("a", newStore)
			})
			s.Actions = []sim.Action[event, effect]{{
				Name: "arm",
				Run: func(w *sim.World[event, effect], _ int, _ any) {
					arms = true
					w.After(w.Process("a"), time.Second, ticked{})
				},
			}, {
				Name:  "count",
				Drain: true,
				Run: func(w *sim.World[event, effect], _ int, _ any) {
					if arms {
						armed++
					}
					if arms && w.Now().After(sim.Start) {
						advanced++
					}
				},
			}}
			prop.ForAll(t, "each case arms a timer of the store a", func(c *prop.Case) { sim.Run(c, s) },
				prop.Hermetic(), prop.Seed(1), prop.Store(""), prop.Workers(1))
			assert.True(t, 4*advanced >= 3*armed, "the time advances in at least three of four cases that arm a timer")
		})

		t.Run("builds the world of a case from the inputs that its system takes from the case", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = stores
			s.Actions = nil
			var first *sim.World[event, effect]
			var spawned []bool
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the stores of the first case are read",
				Check: func(_ *sim.Check, w *sim.World[event, effect]) {
					if first == nil {
						first = w
					}
					if w == first {
						spawned = []bool{w.Process("b") != nil, w.Process("c") != nil}
					}
				},
			}}

			runScript(t, s, scriptOf([]entry{drawn("stores", 2), drawn("drift.a", 0), drawn("drift.b", 0)}))

			assert.Equal(t, spawned, []bool{true, false}, "the system spawns the two stores of its input")
		})

		t.Run("strikes each fault of the media of a host and restores full media", func(t *testing.T) {
			t.Parallel()

			// Two full disks within the bound and a crash leave the run with a
			// host whose media are full, a host without a medium and a host that
			// does not run, which no fault of the media takes.
			s := simulation()
			s.Tolerate = maps.Clone(media)
			s.Tolerate[sim.FaultFullDisk], s.Tolerate[sim.FaultCrash] = 2, 1
			labels := []string{"torn-write", "corruption", "full-disk", "restore-disk"}
			opts := make([]prop.Option, 0, 3+len(labels))
			opts = append(opts, prop.Hermetic(), prop.Seed(1), prop.Store(""))
			for _, label := range labels {
				opts = append(opts, prop.Require(label, 0.01))
			}
			prop.ForAll(t, "the stores keep the rules under the faults of media", func(c *prop.Case) { sim.Run(c, s) },
				opts...)
		})

		t.Run("corrupts a file in a directory of the media of a host", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				m := medium.NewMemory()
				_ = m.MkdirAll("d", 0o700)
				f, _ := m.OpenFile("d/f", os.O_CREATE|os.O_WRONLY, 0o600)
				_, _ = f.Write([]byte("data"))
				_ = f.Sync()
				_ = m.SyncDir("d")
				_ = m.SyncDir(".")
				w.Spawn("a", newStore, m)
				w.Spawn("b", newStore)
			})
			s.Tolerate = map[sim.Fault]int{sim.FaultCorruption: 1}
			prop.ForAll(t, "the run corrupts the file in the directory", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require(string(sim.FaultCorruption), 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("runs a system without processes under every fault", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = fixed(func(*sim.World[event, effect]) {})
			s.Actions, s.Rules, s.Tolerate = nil, nil, every
			prop.ForAll(t, "a system without processes runs", func(c *prop.Case) {
				c.Draw(prop.Boolean(), "case")
				sim.Run(c, s)
			}, prop.Store(""))
		})

		tests := []struct {
			name      string
			giveFault sim.Fault
			wantLabel string
		}{
			{
				name:      "restores an outage in the active phase",
				giveFault: sim.FaultOutage,
				wantLabel: "restore-outage",
			},
			{
				name:      "restores a slow dependency in the active phase",
				giveFault: sim.FaultSlow,
				wantLabel: "restore-slow",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				s := simulation()
				s.Tolerate = map[sim.Fault]int{tt.giveFault: 1}
				prop.ForAll(t, "the run restores the dependency", func(c *prop.Case) { sim.Run(c, s) },
					prop.Require(tt.wantLabel, 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
			})
		}

		t.Run("labels a loss of a dependency when it strikes", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Rules = nil
			s.Tolerate = map[sim.Fault]int{sim.FaultLoseBefore: 1}
			s.Actions = []sim.Action[event, effect]{{
				Name:    "put once",
				Call:    true,
				Enabled: func(w *sim.World[event, effect]) bool { return len(w.History().Events()) == 0 },
				Run: func(w *sim.World[event, effect], client int, _ any) {
					w.Call(client, "a", "Put", nil, nil, func(r uint64) event {
						return put{key: "k", value: "v", reply: r}
					})
				},
			}}
			got := assert.Rejects(t, "no loss strikes the one commit of a case", func(tb assert.TB) {
				prop.ForAll(tb, "the run arms losses of the disk", func(c *prop.Case) { sim.Run(c, s) },
					prop.Require(string(sim.FaultLoseBefore), 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
			})
			expect.NotNil(t, got[0].Detail["coverage"], "an armed loss that never strikes gives its case no label")
		})

		effects := []struct {
			name      string
			giveFault sim.Fault
			wantLabel string
		}{
			{
				name:      "repairs a partition in the active phase",
				giveFault: sim.FaultPartition,
				wantLabel: "a second partition",
			},
			{
				name:      "drops a message in flight",
				giveFault: sim.FaultDrop,
				wantLabel: "a lost message",
			},
			{
				name:      "delays a message in flight",
				giveFault: sim.FaultDelay,
				wantLabel: "a reordered message",
			},
			{
				name:      "duplicates a message in flight",
				giveFault: sim.FaultDuplicate,
				wantLabel: "a duplicated message",
			},
			{
				name:      "jumps the clock of a host",
				giveFault: sim.FaultClockJump,
				wantLabel: "a step before the start",
			},
			{
				name:      "strobes the clock of a host",
				giveFault: sim.FaultClockStrobe,
				wantLabel: "a step before the start",
			},
		}
		for _, tt := range effects {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				s := simulation()
				s.Actions = slices.DeleteFunc(s.Actions, func(a sim.Action[event, effect]) bool { return a.Call })
				s.Tolerate = map[sim.Fault]int{tt.giveFault: 1}
				s.Invariants = []sim.Invariant[event, effect]{{
					Statement: "the run observes its faults",
					Check:     observe,
				}}
				prop.ForAll(t, "the fault takes effect", func(c *prop.Case) { sim.Run(c, s) },
					prop.Require(tt.wantLabel, 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
			})
		}

		t.Run("cuts the hosts in the shapes of the nemesis of Jepsen", func(t *testing.T) {
			t.Parallel()

			names := []string{"a", "b", "c", "d", "e"}
			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				for _, name := range names {
					w.Spawn(name, newStore)
				}
			})
			s.Actions = nil
			s.Tolerate = map[sim.Fault]int{sim.FaultPartition: 1}
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "each partition has a shape of the nemesis",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					if w.Sides() != nil {
						got := shape(names, w.Sides())
						assert.NotEmpty(h, got, h.Statement())
						h.Classify(got)
					}
				},
			}}
			prop.ForAll(t, "the partitions have the shapes", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("isolate", 0.01), prop.Require("halves", 0.01), prop.Require("majorities-ring", 0.01),
				prop.Require("bridge", 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("checks each invariant after every step of the world", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "no store has a pending event",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					expect.Empty(h, w.Pending(), h.Statement())
				},
			}}
			expect.Equal(t, refuted(t, s).Contract, "no store has a pending event",
				"a state of a step that the end of the run removes fails the case")
		})

		t.Run("takes the final actions after the actions of the active phase", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			final := tick("final", 99)
			final.Drain = true
			s.Actions = []sim.Action[event, effect]{final, tick("late", 88)}
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "no tick of the active phase follows the final tick",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					got := ticks(w, "a")
					if at := slices.Index(got, 99); at >= 0 {
						expect.NotContains(h, got[at:], uint64(88), h.Statement())
					}
				},
			}}
			prop.ForAll(t, "the final action runs last", func(c *prop.Case) { sim.Run(c, s) }, prop.Store(""))
		})

		t.Run("takes each final action once after the faults have ended", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Tolerate = every
			final := tick("final", 99)
			final.Drain = true
			s.Actions = append(s.Actions, final)
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the final tick arrives once and without a fault in force",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					n := 0
					for _, e := range w.Log() {
						if e.Event == event(ticked{token: 99}) && !e.Dropped {
							n++
							expect.Empty(h, e.Faults, h.Statement())
							h.Classify("final")
						}
					}
					expect.True(h, n <= 1, h.Statement())
				},
			}}
			prop.ForAll(t, "the final action runs once", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("final", 0.9), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("leaves out an action that is not enabled", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			disabled := func(*sim.World[event, effect]) bool { return false }
			never, final := tick("never", 66), tick("never final", 66)
			never.Enabled, final.Enabled, final.Drain = disabled, disabled, true
			s.Actions = append(s.Actions, never, final)
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the store a never ticks",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					expect.Empty(h, ticks(w, "a"), h.Statement())
				},
			}}
			prop.ForAll(t, "the action is never taken", func(c *prop.Case) { sim.Run(c, s) }, prop.Store(""))
		})

		t.Run("checks the rules against a step before the world performs its effects", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				w.Spawn("a", newStore)
				w.Spawn("b", newStore)
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Deliver("a", forward{to: "missing", key: "k", token: 7})
			})
			s.Actions = nil
			expect.Equal(t, refuted(t, s).Contract, rules[4].Statement,
				"the reused token fails the case before the world sends a call to a process that it lacks")
		})

		t.Run("fails the case for a rule whose probe has no judge", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Rules = []sim.Rule{{ID: "Store/unknown", Statement: "the store is unknown", Probe: "unknown"}}
			got := failure(t, func(c *prop.Case) { sim.Run(c, s) })
			expect.Equal(t, got.Contract, "sim: the probe unknown of the rule Store/unknown has no judge",
				"the failure names the probe and the rule")
		})

		t.Run("fails the case for PCT with a depth below 1", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Strategy = sim.PCT(0)
			got := failure(t, func(c *prop.Case) { sim.Run(c, s) })
			expect.Equal(t, got.Contract, "sim: PCT needs a depth of at least 1, not 0", "the failure names the depth")
		})

		t.Run("checks each invariant after every step", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Invariants = []sim.Invariant[event, effect]{unstored()}
			expect.Equal(t, refuted(t, s).Contract, "no store replies to a put",
				"the case fails at the statement of the invariant")
		})

		t.Run("checks each convergence once the faults have ended", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Converges = []sim.Convergence[event, effect]{{
				Statement: "the store a never starts",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					expect.Empty(h, w.Log(), h.Statement())
				},
			}}
			expect.Equal(t, refuted(t, s).Contract, "the store a never starts",
				"the case fails at the statement of the convergence")
		})

		t.Run("advances the time past the bound of each convergence", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Converges = []sim.Convergence[event, effect]{{
				Statement: "the clock of a has advanced by at least half an hour",
				Within:    time.Hour,
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					expect.True(h, !w.Process("a").Clock().Now().Before(sim.Start.Add(30*time.Minute)), h.Statement())
				},
			}}
			prop.ForAll(t, "the run advances past the bound", func(c *prop.Case) { sim.Run(c, s) }, prop.Store(""))
		})

		t.Run("advances past the next timer by an amount that the case chooses", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				w.Spawn("a", func(*sim.Clock) sim.Machine[event, effect] { return timed{after: time.Second} })
			})
			s.Actions, s.Rules = nil, nil
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the tick arrives",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					for _, e := range w.Log() {
						if _, ok := e.Event.(ticked); ok && e.Time.After(sim.Start.Add(time.Second+time.Millisecond)) {
							h.Classify("the clock passes the timer")
						}
					}
				},
			}}
			prop.ForAll(t, "an advance passes the timer", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("the clock passes the timer", 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("fails the case of a system that does not settle", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				w.Spawn("ticker", func(*sim.Clock) sim.Machine[event, effect] { return ticker{} })
			})
			s.Actions, s.Rules = nil, nil
			s.Converges = []sim.Convergence[event, effect]{{
				Statement: "the ticker ticks",
				Within:    time.Second,
				Check:     func(*sim.Check, *sim.World[event, effect]) {},
			}}
			got := failure(t, func(c *prop.Case) { sim.Run(c, s) })
			expect.Equal(t, got.Contract, "sim: the system takes more than 10000 steps to settle",
				"the failure names the limit of the final phase")
		})

		// A timer of 10 ms at each event takes an advance and a delivery for each
		// 10 ms of the 100 s of the convergence, about 20,000 steps.
		t.Run("settles a system of more than 10,000 steps within the bound of its convergence", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			every := func(*sim.Clock) sim.Machine[event, effect] { return periodic{every: 10 * time.Millisecond} }
			s.System = fixed(func(w *sim.World[event, effect]) { w.Spawn("a", every) })
			s.Actions, s.Rules = nil, nil
			s.Converges = []sim.Convergence[event, effect]{{
				Statement: "the timers keep their period",
				Within:    100 * time.Second,
				Check:     func(*sim.Check, *sim.World[event, effect]) {},
			}}
			prop.ForAll(t, "the final phase passes the bound", func(c *prop.Case) { sim.Run(c, s) },
				prop.Cases(1), prop.Seed(1), prop.Store(""))
		})

		restarts := []struct {
			name       string
			giveLife   time.Duration
			giveWithin time.Duration
			wantGaps   []time.Duration
		}{
			{
				name:       "backs off the restarts of a host whose machine stops at each start",
				giveWithin: 1000 * time.Second,
				wantGaps: []time.Duration{
					0, 10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second,
					5 * time.Minute, 5 * time.Minute,
				},
			},
			{
				name:       "restarts a host at once that ran for more than ten minutes after its restart",
				giveLife:   11 * time.Minute,
				giveWithin: 25 * time.Minute,
				wantGaps:   []time.Duration{0, 0},
			},
			{
				name:       "backs off the restart of a host that ran for ten minutes after its restart",
				giveLife:   10 * time.Minute,
				giveWithin: 25 * time.Minute,
				wantGaps:   []time.Duration{0, 10 * time.Second},
			},
		}
		// The host b takes a step each second, so the final phase takes steps
		// between an exit of the host a and its restart.
		for _, tt := range restarts {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var got []time.Duration
				s := simulation()
				s.System = fixed(func(w *sim.World[event, effect]) {
					w.Spawn("a", func(*sim.Clock) sim.Machine[event, effect] { return &fragile{life: tt.giveLife} })
					w.Spawn("b", func(*sim.Clock) sim.Machine[event, effect] { return periodic{every: time.Second} })
				})
				s.Actions, s.Rules = nil, nil
				s.Converges = []sim.Convergence[event, effect]{{
					Statement: "the host a starts again after each exit",
					Within:    tt.giveWithin,
					Check: func(_ *sim.Check, w *sim.World[event, effect]) {
						var exit time.Time
						exited := false
						for _, e := range w.Log() {
							if e.Process != "a" {
								continue
							}
							if _, start := e.Event.(started); start && exited {
								got, exited = append(got, e.Time.Sub(exit)), false
							}
							if e.Err != nil {
								exit, exited = e.Time, true
							}
						}
					},
				}}
				prop.ForAll(t, "the final phase starts the host a again", func(c *prop.Case) { sim.Run(c, s) },
					prop.Replay("prop1:"), prop.Store(""))
				expect.Equal(t, got, tt.wantGaps, "the host a starts again after the delays of its backoff")
			})
		}

		t.Run("fails the case of a history that the spec does not explain", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Spec = rejecting
			expect.Equal(t, refuted(t, s).Contract, "the history of the calls of the clients is linearizable",
				"the smallest case completes its call in the final phase, and fails at the check of the whole history")
		})

		t.Run("checks the calls of the final phase against the spec", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Spec = rejecting
			s.Actions = []sim.Action[event, effect]{{
				Name:  "final put",
				Call:  true,
				Drain: true,
				Run: func(w *sim.World[event, effect], client int, _ any) {
					w.Call(client, "a", "Put", nil, nil, func(r uint64) event {
						return put{key: "k", value: "v", reply: r}
					})
				},
			}}
			expect.Equal(t, refuted(t, s).Contract, "the history of the calls of the clients is linearizable",
				"the case fails at the check of the whole history")
		})

		isolation := []struct {
			name         string
			giveLevel    sim.Isolation
			wantContract string
		}{
			{
				name:         "checks the transactions of the clients for serializability",
				giveLevel:    sim.Serializable,
				wantContract: "the transactions of the clients are serializable",
			},
			{
				name:         "checks the transactions of the clients for snapshot isolation",
				giveLevel:    sim.SnapshotIsolation,
				wantContract: "the transactions of the clients have snapshot isolation",
			},
		}
		for _, tt := range isolation {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				s := simulation()
				s.Rules = nil
				s.Isolation = tt.giveLevel
				s.System = fixed(func(w *sim.World[event, effect]) {
					w.Spawn("ledger", func(*sim.Clock) sim.Machine[event, effect] { return ledger{} })
				})
				s.Actions = []sim.Action[event, effect]{{
					Name: "read",
					Call: true,
					Run: func(w *sim.World[event, effect], client int, _ any) {
						read := []any{[]any{"read", "x", []any{42}}}
						w.Call(client, "ledger", "txn", []any{[]any{"read", "x", nil}}, []any{"x"},
							func(r uint64) event { return txn{ops: read, reply: r} })
					},
				}}
				expect.Equal(t, refuted(t, s).Contract, tt.wantContract,
					"a read of a value that no transaction appended fails the check")
			})
		}

		t.Run("observes the fingerprint of the system after every step", func(t *testing.T) {
			t.Parallel()

			var calls atomic.Uint64
			s := simulation()
			s.Fingerprint = func(*sim.World[event, effect]) uint64 { return calls.Add(1) }
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the stores never start",
				Check:     func(h *sim.Check, w *sim.World[event, effect]) { expect.Empty(h, w.Log(), h.Statement()) },
			}}
			got := assert.Rejects(t, "the run fails", func(tb assert.TB) {
				prop.ForAll(tb, "the run fails", func(c *prop.Case) { sim.Run(c, s) },
					prop.Hermetic(), prop.Seed(1), prop.Store(""))
			})
			expect.Equal(t, got[0].Detail["outcome"], any(prop.Flaky),
				"a fingerprint that differs on the replay of the failing case makes the run flaky")
		})

		t.Run("observes the checksum of the log after every step", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				w.Spawn("counted", func(*sim.Clock) sim.Machine[event, effect] { return counted{} })
			})
			s.Actions, s.Rules = nil, nil
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the counted machine never starts",
				Check:     func(h *sim.Check, w *sim.World[event, effect]) { expect.Empty(h, w.Log(), h.Statement()) },
			}}
			got := assert.Rejects(t, "the run fails", func(tb assert.TB) {
				prop.ForAll(tb, "the run fails", func(c *prop.Case) { sim.Run(c, s) },
					prop.Hermetic(), prop.Seed(1), prop.Store(""))
			})
			expect.Equal(t, got[0].Detail["outcome"], any(prop.Flaky),
				"a log that differs on the replay of the failing case makes the run flaky")
		})

		t.Run("repeats the log of a case from its seed", func(t *testing.T) {
			t.Parallel()

			sums := make([][]uint64, 2)
			for i := range sums {
				s := simulation()
				s.Tolerate = every
				s.Invariants = []sim.Invariant[event, effect]{{
					Statement: "the run records its checksum",
					Check: func(_ *sim.Check, w *sim.World[event, effect]) {
						sums[i] = append(sums[i], w.Checksum())
					},
				}}
				prop.ForAll(t, "the run repeats "+string(rune('a'+i)), func(c *prop.Case) { sim.Run(c, s) },
					prop.Hermetic(), prop.Seed(7), prop.Cases(10), prop.Store(""))
			}
			expect.Equal(t, sums[0], sums[1], "two runs of one seed make the same steps")
		})

		t.Run("takes no cancel action for an anatomy without a cancel event", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Anatomy.Cancelled = nil
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "no client cancels a call",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					for _, e := range w.History().Events() {
						expect.ErrorIsNot(h, e.Error, sim.ErrCancelled, h.Statement())
					}
				},
			}}
			prop.ForAll(t, "the rules hold without a cancel event", func(c *prop.Case) { sim.Run(c, s) },
				prop.Store(""))
		})

		t.Run("accepts a machine that returns no effect after the step of its error", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions = append(s.Actions, sim.Action[event, effect]{
				Name: "empty put",
				Call: true,
				Run: func(w *sim.World[event, effect], client int, _ any) {
					w.Call(client, "a", "Put", nil, nil, func(r uint64) event { return put{key: "k", reply: r} })
				},
			})
			prop.ForAll(t, "a store that stops itself keeps the rules", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("empty put", 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("notes the log and the history of a failing case", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions, s.Rules = nil, nil
			s.System = fixed(func(w *sim.World[event, effect]) {
				w.Spawn("a", newStore)
				w.Spawn("b", newStore)
				w.Partition([]string{"a"}, []string{"b"})
				w.Crash("a")
				w.Deliver("a", ticked{token: 1})
				w.Restart("a")
				w.Call(0, "a", "Park", nil, nil, func(r uint64) event { return park{reply: r} })
				w.Call(1, "a", "Put", nil, nil, func(r uint64) event { return put{key: "k", value: "v", reply: r} })
				w.Call(2, "b", "Park", nil, nil, func(r uint64) event { return park{reply: r} })
				w.Deliver("b", put{key: "k", reply: 9})
				drain(w)
			})
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the store a never stops",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					assert.NotContains(h, events(w, "a"), event(stopped{}), h.Statement())
				},
			}}
			r := assert.NewRecorder()
			prop.ForAll(r, "the run fails at the stop of the store a", func(c *prop.Case) { sim.Run(c, s) },
				prop.Hermetic(), prop.Seed(1), prop.Store(""))
			expect.Equal(t, r.Notes(), []string{
				"the log of the run:\n" +
					"step 1: a, epoch 1, clock 0s, faults [partition], dropped sim_test.ticked{token:1}\n" +
					"step 2: a, epoch 2, clock 0s, faults [partition]: sim_test.started{reply:3} -> " +
					"sim_test.reply{value:<nil> err:<nil> to:3}\n" +
					"step 3: a, epoch 2, clock 0s, faults [partition]: sim_test.park{reply:4}\n" +
					"step 4: a, epoch 2, clock 0s, faults [partition]: sim_test.put{disk:map[] value:v key:k " +
					"reply:5 timeout:0} -> sim_test.commit{disk:map[] key:k value:v token:1 timeout:0}\n" +
					"step 5: a, epoch 2, clock 0s, faults [partition]: sim_test.committed{err:<nil> token:1} -> " +
					"sim_test.reply{value:stored err:<nil> to:5}\n" +
					"step 6: b, epoch 1, clock 0s, faults [partition]: sim_test.started{reply:2} -> " +
					"sim_test.reply{value:<nil> err:<nil> to:2}\n" +
					"step 7: b, epoch 1, clock 0s, faults [partition]: sim_test.park{reply:6}\n" +
					"step 8: b, epoch 1, clock 0s, faults [partition]: sim_test.put{disk:map[] value: key:k " +
					"reply:9 timeout:0} -> sim_test.reply{value:<nil> err:store: a put without a value to:9}, " +
					"sim_test.reply{value:<nil> err:store: a put without a value to:6}, error store: a put without " +
					"a value\n" +
					"step 9: b, epoch 1, clock 0s, faults [partition]: sim_test.stopped{}, error store: a put " +
					"without a value\n" +
					"step 10: b, epoch 2, clock 0s: sim_test.started{reply:7} -> sim_test.reply{value:<nil> " +
					"err:<nil> to:7}\n" +
					"step 11: a, epoch 2, clock 0s: sim_test.stopped{} -> sim_test.reply{value:<nil> " +
					"err:store: stopped to:4}\n" +
					"step 12: b, epoch 2, clock 0s: sim_test.stopped{}",
				"the history of the calls:\n" +
					"event 0: client 0 invoke Park []\n" +
					"event 1: client 1 invoke Put []\n" +
					"event 2: client 2 invoke Park []\n" +
					"event 3: client 1 ok Put stored\n" +
					"event 4: client 2 fail Park store: a put without a value\n" +
					"event 5: client 0 fail Park store: stopped",
			}, "the failing case notes each step of the log and each event of the history")
		})

		t.Run("runs a world of components in each case inside the environment of the simulation", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Actions = unparked(s.Actions)
			s.Tolerate = map[sim.Fault]int{
				sim.FaultCrash: 1, sim.FaultRestart: 1, sim.FaultPartition: 1, sim.FaultClockJump: 1,
				sim.FaultTornWrite: 1, sim.FaultCorruption: 1, sim.FaultFullDisk: 1, sim.FaultReset: 1,
			}

			sim.Test(t, s)
		})

		t.Run("runs the hosts of the components under a task scheduler in Tasks mode", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Mode, s.Around = spec.ModeTasks, nil
			s.Actions = unparked(s.Actions)
			s.Tolerate = map[sim.Fault]int{
				sim.FaultCrash: 1, sim.FaultRestart: 1, sim.FaultPause: 1, sim.FaultPartition: 1, sim.FaultDrop: 1,
				sim.FaultDelay: 1, sim.FaultDuplicate: 1, sim.FaultReset: 1, sim.FaultSlowLink: 1,
			}
			s.Network = []host.NetworkOption{host.Latency(time.Millisecond, time.Millisecond)}

			sim.Test(t, s)
		})

		t.Run("fails the case at the step whose history has no linearization", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.System = cs.system(func(env *host.Env) *cell {
				c := openCell(env)
				c.forgets = true
				return c
			})
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			sets := cs.runCase(r, s, scriptOf(opening(false), set(0, "a"), get(1, "a"), set(0, "b")))

			failures := r.Failures()
			assert.NotEmpty(t, failures, "the run fails")
			inner, _ := failures[0].CaseFailure()
			expect.Equal(t, inner.Contract, "the history of the calls of the clients is linearizable",
				"the check of the history fails")
			expect.Equal(t, sets[0], 1, "the case ends at the Get, before the second Set")
		})

		t.Run("checks the history once no client has an open call", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.System = cs.system(func(env *host.Env) *cell {
				c := openCell(env)
				c.forgets = true
				return c
			})
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			sets := cs.runCase(r, s, scriptOf(opening(false), parkAt(0), get(1, "a"), set(1, "b"),
				stepOf("cancel", drawn("client", 0)), set(1, "b")))

			failures := r.Failures()
			assert.NotEmpty(t, failures, "the run fails")
			inner, _ := failures[0].CaseFailure()
			expect.Equal(t, inner.Contract, "the history of the calls of the clients is linearizable",
				"the check of the history fails")
			expect.Equal(t, sets[0], 1, "the case runs the first Set while the call parks, and ends at the cancel")
		})

		t.Run("ends the case at the step whose check of the history faults", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Spec = history.SpecFrom(func() history.Subject {
				return func(string, []any) any {
					runtime.Goexit()
					return nil
				}
			})
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			sets := cs.runCase(r, s, scriptOf(opening(false), set(0, "a"), set(0, "b")))

			faults := r.Faults()
			assert.NotEmpty(t, faults, "the check of the history faults")
			expect.ErrorIs(t, faults[0], history.ErrSpec, "the fault is the end of the goroutine of the spec")
			expect.Equal(t, sets[0], 1, "the case ends at the first Set")
		})

		t.Run("runs the steps after a history that its checker cannot decide", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.History = []history.Option{history.Budget(1)}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			sets := cs.runCase(r, s, scriptOf(opening(false), set(0, "a"), set(1, "a"), set(0, "b")),
				prop.Require("undecided", 0.01))

			expect.Empty(t, r.Failures(), "a history that its checker cannot decide fails no case, and labels it")
			expect.Equal(t, sets[0], 3, "each Set runs after the undecided check")
		})

		t.Run("cancels an open call of a component without the cancelled event of the anatomy", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			var first *sim.World[sim.None, sim.None]
			var events []history.Event
			s.Invariants = []sim.Invariant[sim.None, sim.None]{{
				Statement: "the history of the first case is read",
				Check: func(_ *sim.Check, w *sim.World[sim.None, sim.None]) {
					if first == nil {
						first = w
					}
					if w == first {
						events = w.History().Events()
					}
				},
			}}

			cs.runCase(t, s, scriptOf(opening(false), parkAt(0), stepOf("cancel", drawn("client", 0))))

			assert.Equal(t, completions(events), []completion{{kind: history.Unknown, err: sim.ErrCancelled}},
				"the cancel ends the parked call")
		})

		t.Run("cancels the open calls of the components in the order of their clients", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			var first *sim.World[sim.None, sim.None]
			var events []history.Event
			s.Invariants = []sim.Invariant[sim.None, sim.None]{{
				Statement: "the history of the first case is read",
				Check: func(_ *sim.Check, w *sim.World[sim.None, sim.None]) {
					if first == nil {
						first = w
					}
					if w == first {
						events = w.History().Events()
					}
				},
			}}

			cs.runCase(t, s, scriptOf(opening(false), parkAt(1), parkAt(0), stepOf("cancel", drawn("client", 0))))

			cancelled := slices.IndexFunc(events, func(e history.Event) bool {
				return errors.Is(e.Error, sim.ErrCancelled)
			})
			assert.True(t, cancelled >= 0, "the cancel ends a parked call")
			expect.Equal(t, events[cancelled].Client, 0, "the first client in the order of the numbers is 0")
		})

		t.Run("calls the first client without an open call from the client that the case picks in a world of "+
			"components", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			events := firstEvents(&s)

			cs.runCase(t, s, scriptOf(opening(false), parkAt(1), parkAt(1)))

			assert.Equal(t, invokersOf(*events), []int{1, 0}, "the second park of client 1 goes to client 0")
		})

		t.Run("takes no effect for a call while every client has an open call in a world of components",
			func(t *testing.T) {
				t.Parallel()
				var cs cellSet
				s := cs.simulation()
				events := firstEvents(&s)

				cs.runCase(t, s, scriptOf(opening(false), parkAt(0), parkAt(0), parkAt(0)))

				assert.Equal(t, invokersOf(*events), []int{0, 1}, "the third park finds no client without an open call")
			})

		t.Run("takes no effect for a cancel without an open call in a world of components", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			events := firstEvents(&s)
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			cs.runCase(r, s, scriptOf(opening(false), set(0, "a"), stepOf("cancel", drawn("client", 0))))

			assert.False(t, r.Failed(), "the world takes the cancel")
			assert.Equal(t, completions(*events), []completion{{kind: history.OK}},
				"the Set completes, and the cancel finds no open call")
		})

		t.Run("flips a byte at the shares of the files of a component", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Tolerate = map[sim.Fault]int{sim.FaultCorruption: 1}
			events := firstEvents(&s)

			cs.runCase(t, s, scriptOf(opening(false), set(0, "a"),
				stepOf("corruption", drawn("process", 0), drawn("file", 0.0), drawn("offset", 0.9)), get(0, "a")))

			assert.Equal(t, completions(*events), []completion{
				{kind: history.OK}, {kind: history.Fail, err: errCorrupt},
			}, "the Get reads the flipped byte of the value of a")
		})

		t.Run("takes no effect for a corruption of a component without a file with data", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Tolerate = map[sim.Fault]int{sim.FaultCorruption: 1}
			events := firstEvents(&s)
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			cs.runCase(r, s, scriptOf(opening(false),
				stepOf("corruption", drawn("process", 0), drawn("file", 0.0), drawn("offset", 0.0)), get(0, "a")))

			assert.False(t, r.Failed(), "the world takes the corruption")
			assert.Equal(t, completions(*events), []completion{{kind: history.OK, output: ""}},
				"the corruption finds no file, and the Get of a returns no value")
		})

		t.Run("flips the byte at the share of the size of a file of a component", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Tolerate = map[sim.Fault]int{sim.FaultCorruption: 1}
			read := firstContents(&s, func() *sim.Component[*cell] { return cs.a }, "cell")

			cs.runCase(t, s, scriptOf(opening(false), set(0, "a"),
				stepOf("corruption", drawn("process", 0), drawn("file", 0.0), drawn("offset", 0.9))))

			assert.Length(t, *read, 2, "the run reads the file after the Set and after the corruption")
			want := []byte((*read)[0])
			want[len(want)-1] ^= 1
			assert.Equal(t, (*read)[1], string(want), "the share of nine tenths flips the last of the five bytes")
		})

		t.Run("flips a byte of the file at the share of the files of a component", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.System = cs.system(func(env *host.Env) *cell {
				f, _ := env.Disk().OpenFile("z", os.O_CREATE|os.O_WRONLY, 0o644)
				_, _ = f.Write([]byte("data"))
				_ = f.Sync()
				_ = f.Close()
				_ = env.Disk().SyncDir(".")
				return openCell(env)
			})
			s.Tolerate = map[sim.Fault]int{sim.FaultCorruption: 1}
			read := firstContents(&s, func() *sim.Component[*cell] { return cs.a }, "z")

			cs.runCase(t, s, scriptOf(opening(false), set(0, "a"),
				stepOf("corruption", drawn("process", 0), drawn("file", 0.75), drawn("offset", 0.0))))

			assert.Equal(t, (*read)[len(*read)-1], "eata",
				"the share of three quarters takes z, the second of the files cell and z")
		})

		t.Run("calls no client past the clients of a world of components", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			cs.runCase(r, s, scriptOf(opening(false), set(2, "a")))

			assert.Length(t, r.Faults(), 1, "the world refuses the client 2 of two clients")
		})

		t.Run("corrupts no machine without media in a world of components", func(t *testing.T) {
			t.Parallel()
			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				sim.SpawnComponent(w, "c", openCell)
				w.Spawn("m", newStore)
			})
			s.Around, s.Actions, s.Rules = conform.Bubble, nil, nil
			s.Tolerate = map[sim.Fault]int{sim.FaultCorruption: 1}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			runScript(r, s, scriptOf([]entry{drawn("drift.c", 0), drawn("drift.m", 0)},
				stepOf("corruption", drawn("process", 1), drawn("file", 0.0), drawn("offset", 0.0))))

			assert.Length(t, r.Faults(), 1, "the world refuses the machine m, which has no medium")
		})

		t.Run("calls the client at the index among the clients without an open call in a world of machines",
			func(t *testing.T) {
				t.Parallel()
				s := simulation()
				s.Rules = nil
				events := firstEvents(&s)

				runScript(t, s, scriptOf([]entry{drawn("drift.a", 0), drawn("drift.b", 0)},
					machinePark(0), machinePark(1)))

				assert.Equal(t, invokersOf(*events), []int{0, 2},
					"the second park takes the second of the clients 1 and 2")
			})

		t.Run("corrupts no medium without a file with data in a world of machines", func(t *testing.T) {
			t.Parallel()
			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				w.Spawn("a", newStore, medium.NewMemory())
				w.Spawn("b", newStore)
			})
			s.Tolerate = map[sim.Fault]int{sim.FaultCorruption: 1}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			runScript(r, s, scriptOf([]entry{drawn("drift.a", 0), drawn("drift.b", 0)},
				stepOf("corruption", drawn("process", 0), drawn("file", 0), drawn("offset", 0))))

			assert.Length(t, r.Faults(), 1, "the world refuses the corruption")
		})

		t.Run("corrupts the byte that the case chooses in a file of the media of a machine", func(t *testing.T) {
			t.Parallel()
			s := simulation()
			var m *medium.Memory
			s.System = fixed(func(w *sim.World[event, effect]) {
				m = mediumWith("data")
				w.Spawn("a", newStore, m)
				w.Spawn("b", newStore)
			})
			s.Tolerate = map[sim.Fault]int{sim.FaultCorruption: 1}
			var first *sim.World[event, effect]
			var firstMedium *medium.Memory
			var got string
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the medium of the first case is read",
				Check: func(check *sim.Check, w *sim.World[event, effect]) {
					if first == nil {
						first, firstMedium = w, m
					}
					if w == first {
						got = contentOf(check, firstMedium)
					}
				},
			}}

			runScript(t, s, scriptOf([]entry{drawn("drift.a", 0), drawn("drift.b", 0)},
				stepOf("corruption", drawn("process", 0), drawn("file", 0), drawn("offset", 1))))

			assert.Equal(t, got, "d`ta", "the corruption flips the lowest bit of the second byte of f")
		})

		t.Run("cancels no call of a machine without the cancelled event of the anatomy", func(t *testing.T) {
			t.Parallel()
			s := simulation()
			s.Anatomy.Cancelled = nil
			s.System = after(s.System, func(w *sim.World[event, effect]) {
				// The goroutine of c waits for the end of its lifetime, so a bubble
				// whose case ends without the crash of c ends in a deadlock.
				sim.SpawnComponent(w, "c", func(env *host.Env) *cell {
					env.Go(func() { env.Wait(func() bool { return env.Lifetime().Err() != nil }) })
					return openCell(env)
				})
			})
			s.Around, s.Rules = conform.Bubble, nil
			var first *sim.World[event, effect]
			var events []history.Event
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the history of the first case is read",
				Check: func(_ *sim.Check, w *sim.World[event, effect]) {
					if first == nil {
						first = w
					}
					if w == first {
						events = w.History().Events()
					}
				},
			}}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			runScript(r, s, scriptOf([]entry{drawn("drift.a", 0), drawn("drift.b", 0), drawn("drift.c", 0)},
				stepOf("park", drawn("client", 0), drawn("to b", false)), stepOf("cancel", drawn("client", 0))))

			assert.False(t, r.Failed(), "the world takes the cancel, and the end of the case crashes the component")
			assert.False(t, slices.ContainsFunc(events, func(e history.Event) bool {
				return errors.Is(e.Error, sim.ErrCancelled)
			}), "the cancel ends no call of a machine")
		})

		t.Run("strikes each fault on the hosts of its kind in a world of machines and components",
			func(t *testing.T) {
				t.Parallel()
				s := simulation()
				s.System = fixed(func(w *sim.World[event, effect]) {
					sim.SpawnComponent(w, "a", openCell)
					w.Spawn("m", newStore)
					sim.SpawnComponent(w, "b", openCell)
				})
				s.Around, s.Actions, s.Rules = conform.Bubble, nil, nil
				s.Tolerate = map[sim.Fault]int{
					sim.FaultCrash: 1, sim.FaultDiskReplacement: 1, sim.FaultClockStrobe: 1, sim.FaultReset: 1,
				}
				var first *sim.World[event, effect]
				var faults [][]string
				s.Invariants = []sim.Invariant[event, effect]{{
					Statement: "the faults of the first case are read",
					Check: func(_ *sim.Check, w *sim.World[event, effect]) {
						if first == nil {
							first = w
						}
						if w == first {
							faults = append(faults, w.Faults())
						}
					},
				}}
				r := &recorded{Recorder: assert.NewRecorder(), t: t}

				runScript(r, s, scriptOf([]entry{drawn("drift.a", 0), drawn("drift.m", 0), drawn("drift.b", 0)},
					stepOf("crash", drawn("process", 0), drawn("kill.a", 1)),
					stepOf("disk-replacement", drawn("process", 0)),
					stepOf("clock-strobe", drawn("process", 0), drawn("strobe.a", 0), drawn("strobe.b", 0),
						drawn("strobe.steps", 1)),
					stepOf("reset", drawn("link", 0))))

				assert.False(t, r.Failed(), "the world takes each step")
				assert.True(t, len(faults) > 3, "the run checks each step of the first case")
				expect.Equal(t, faults[3], []string{"crash:a", "clock-strobe:m", "disk-replacement:b"},
					"a crash strikes a host, a disk replacement a component and a strobe a host of machines")
			})

		refusals := []struct {
			name         string
			giveTolerate map[sim.Fault]int
			giveStep     []entry
		}{
			{
				name:     "takes no advance in a world of machines without a timer",
				giveStep: stepOf("advance", drawn("advance", 1)),
			},
			{
				name:         "resets no link in a world of machines",
				giveTolerate: map[sim.Fault]int{sim.FaultReset: 1},
				giveStep:     stepOf("reset", drawn("link", 0)),
			},
			{
				name:         "replaces no disk in a world of machines",
				giveTolerate: map[sim.Fault]int{sim.FaultDiskReplacement: 1},
				giveStep:     stepOf("disk-replacement", drawn("process", 0)),
			},
		}
		for _, tt := range refusals {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				s := simulation()
				s.Tolerate = tt.giveTolerate
				r := &recorded{Recorder: assert.NewRecorder(), t: t}

				runScript(r, s, scriptOf([]entry{drawn("drift.a", 0), drawn("drift.b", 0)}, tt.giveStep))

				assert.Length(t, r.Faults(), 1, "the world refuses the step")
			})
		}

		t.Run("crashes no host whose processes have stopped", func(t *testing.T) {
			t.Parallel()
			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) {
				w.Spawn("a", func(*sim.Clock) sim.Machine[event, effect] { return &halting{} })
			})
			s.Actions, s.Rules, s.Strategy = nil, nil, sim.Uniform()
			s.Tolerate = map[sim.Fault]int{sim.FaultCrash: 1}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			runScript(r, s, scriptOf([]entry{drawn("drift.a", 0)}, stepOf("deliver", drawn("process", 0)),
				stepOf("crash", drawn("process", 0))))

			assert.Length(t, r.Faults(), 1, "the world refuses the crash of the host of the stopped process")
		})

		t.Run("advances a world of machines by the time until the next timer and the amount of the case",
			func(t *testing.T) {
				t.Parallel()
				s := simulation()
				s.System = fixed(func(w *sim.World[event, effect]) {
					w.Spawn("a", newStore)
					w.After(w.Process("a"), time.Second, ticked{token: 1})
				})
				s.Actions, s.Rules = nil, nil
				var first *sim.World[event, effect]
				var now []time.Time
				s.Invariants = []sim.Invariant[event, effect]{{
					Statement: "the clock of the first case is read",
					Check: func(_ *sim.Check, w *sim.World[event, effect]) {
						if first == nil {
							first = w
						}
						if w == first {
							now = append(now, w.Process("a").Clock().Now())
						}
					},
				}}
				r := &recorded{Recorder: assert.NewRecorder(), t: t}

				runScript(r, s, scriptOf([]entry{drawn("drift.a", 0)}, stepOf("advance", drawn("advance", 0))))

				assert.Empty(t, r.Faults(), "the world takes the advance")
				assert.NotEmpty(t, now, "the run checks the advance")
				expect.Equal(t, now[0], sim.Start.Add(time.Second), "the advance moves the clock to the timer alone")
			})

		t.Run("advances the clocks of the hosts under the task scheduler in Tasks mode", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Mode, s.Around = spec.ModeTasks, nil
			s.Network = []host.NetworkOption{host.Latency(time.Millisecond, time.Millisecond)}

			cs.runCase(t, s, scriptOf(opening(true), send("1"), stepOf("advance", drawn("advance", 2000))))

			assert.Equal(t, cs.received[0], []string{"1"}, "the datagram arrives once the advance passes its latency")
		})

		t.Run("wakes each wait of a component after every step", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			var set, woke atomic.Bool
			s.System = after(s.System, func(*sim.World[sim.None, sim.None]) {
				set.Store(false)
				woke.Store(false)
				env := cs.a.Value().env
				env.Go(func() {
					env.Wait(func() bool { return set.Load() || env.Lifetime().Err() != nil })
					woke.Store(set.Load())
				})
				synctest.Wait()
			})
			s.Actions = []sim.Action[sim.None, sim.None]{{
				Name: "set",
				Run:  func(*sim.World[sim.None, sim.None], int, any) { set.Store(true) },
			}}
			var first *sim.World[sim.None, sim.None]
			var seen []bool
			s.Invariants = []sim.Invariant[sim.None, sim.None]{{
				Statement: "the wait of the first case is read",
				Check: func(_ *sim.Check, w *sim.World[sim.None, sim.None]) {
					if first == nil {
						first = w
					}
					if w == first {
						seen = append(seen, woke.Load())
					}
				},
			}}

			cs.runCase(t, s, scriptOf(opening(false), stepOf("set")))

			assert.NotEmpty(t, seen, "the run checks the step of the first case")
			assert.True(t, seen[0], "the step wakes the wait of the component")
		})

		t.Run("delays no datagram while one datagram is in flight to a component", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Blocking = true
			s.Network = []host.NetworkOption{host.Latency(time.Millisecond, time.Millisecond)}
			s.Tolerate = map[sim.Fault]int{sim.FaultDelay: 1}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			cs.runCase(r, s, scriptOf(opening(true), send("1"), stepOf("delay", drawn("process", 1)),
				stepOf("advance", drawn("advance", 2000))))

			assert.False(t, r.Failed(), "the world takes the delay")
			assert.Equal(t, cs.received[0], []string{"1"}, "the one datagram arrives once, without a delay")
		})

		slowing := []struct {
			name         string
			giveBlocking bool
			giveTolerate int
			giveThird    bool
			giveSteps    [][]entry
		}{
			{
				name:         "slows no link in a world without the blocking faults",
				giveTolerate: 1,
				giveSteps:    [][]entry{slowLink(0)},
			},
			{
				name:         "slows no link that is slow",
				giveBlocking: true,
				giveTolerate: 2,
				giveSteps:    [][]entry{slowLink(0), slowLink(0)},
			},
			{
				name:         "slows no more links at once than Tolerate bounds",
				giveBlocking: true,
				giveTolerate: 1,
				giveThird:    true,
				giveSteps:    [][]entry{slowLink(0), slowLink(0)},
			},
		}
		for _, tt := range slowing {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var cs cellSet
				s := cs.simulation()
				s.Blocking, s.Tolerate = tt.giveBlocking, map[sim.Fault]int{sim.FaultSlowLink: tt.giveTolerate}
				start := opening(tt.giveBlocking)
				if tt.giveThird {
					s.System = after(s.System, func(w *sim.World[sim.None, sim.None]) {
						sim.SpawnComponent(w, "c", openCell)
					})
					start = append(start, drawn("drift.c", 0), drawn("latency.a.c", 0.0), drawn("latency.b.c", 0.0))
				}
				r := &recorded{Recorder: assert.NewRecorder(), t: t}

				cs.runCase(r, s, scriptOf(start, tt.giveSteps...))

				assert.Length(t, r.Faults(), 1, "the world refuses the last slow link")
			})
		}

		taken := []struct {
			name      string
			giveSteps [][]entry
			giveMode  func(s *sim.Simulation[sim.None, sim.None, []history.Operation])
		}{
			{
				name:      "advances a world of components while no machine has a timer",
				giveSteps: [][]entry{stepOf("advance", drawn("advance", 1000))},
			},
			{
				name:      "pauses a component in a world that strikes the blocking faults",
				giveSteps: [][]entry{stepOf("pause", drawn("process", 0))},
				giveMode: func(s *sim.Simulation[sim.None, sim.None, []history.Operation]) {
					s.Blocking, s.Tolerate = true, map[sim.Fault]int{sim.FaultPause: 1}
				},
			},
			{
				name:      "resets the streams between two components",
				giveSteps: [][]entry{stepOf("reset", drawn("link", 0))},
				giveMode: func(s *sim.Simulation[sim.None, sim.None, []history.Operation]) {
					s.Tolerate = map[sim.Fault]int{sim.FaultReset: 1}
				},
			},
			{
				name:      "replaces the disk of a component",
				giveSteps: [][]entry{stepOf("disk-replacement", drawn("process", 0))},
				giveMode: func(s *sim.Simulation[sim.None, sim.None, []history.Operation]) {
					s.Tolerate = map[sim.Fault]int{sim.FaultDiskReplacement: 1}
				},
			},
			{
				name:      "slows a link between two components until restore-link",
				giveSteps: [][]entry{slowLink(0), stepOf("restore-link", drawn("link", 0))},
				giveMode: func(s *sim.Simulation[sim.None, sim.None, []history.Operation]) {
					s.Blocking, s.Tolerate = true, map[sim.Fault]int{sim.FaultSlowLink: 1}
				},
			},
		}
		for _, tt := range taken {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var cs cellSet
				s := cs.simulation()
				if tt.giveMode != nil {
					tt.giveMode(&s)
				}
				r := &recorded{Recorder: assert.NewRecorder(), t: t}

				cs.runCase(r, s, scriptOf(opening(s.Blocking), tt.giveSteps...))

				assert.False(t, r.Failed(), "the world takes the step")
			})
		}

		t.Run("replaces no more disks at once than Tolerate bounds", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Tolerate = map[sim.Fault]int{sim.FaultDiskReplacement: 1}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			cs.runCase(r, s, scriptOf(opening(false), stepOf("disk-replacement", drawn("process", 0)),
				stepOf("disk-replacement", drawn("process", 0))))

			assert.Length(t, r.Faults(), 1, "the world refuses the second replacement")
		})

		t.Run("pauses no component in a world without the blocking faults", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Tolerate = map[sim.Fault]int{sim.FaultPause: 1}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			cs.runCase(r, s, scriptOf(opening(false), stepOf("pause", drawn("process", 0))))

			assert.Length(t, r.Faults(), 1, "the world refuses the pause")
		})

		t.Run("strobes no component", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Tolerate = map[sim.Fault]int{sim.FaultClockStrobe: 1}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			cs.runCase(r, s, scriptOf(opening(false), stepOf("clock-strobe", drawn("process", 0))))

			assert.Length(t, r.Faults(), 1, "the world refuses the strobe")
		})

		t.Run("lists the slow links among the faults in force", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Blocking, s.Tolerate = true, map[sim.Fault]int{sim.FaultSlowLink: 1}
			var faults [][]string
			s.Invariants = []sim.Invariant[sim.None, sim.None]{{
				Statement: "the faults are read", Check: func(_ *sim.Check, w *sim.World[sim.None, sim.None]) {
					faults = append(faults, w.Faults())
				},
			}}

			cs.runCase(t, s, scriptOf(opening(true), slowLink(0), stepOf("restore-link", drawn("link", 0))))

			assert.Equal(t, faults[:2], [][]string{{"slow-link:a,b"}, nil},
				"the slow link is in force until its restore")
		})

		datagrams := []struct {
			name      string
			giveFault []entry
			want      []string
		}{
			{name: "drops the oldest datagram in flight to a component", giveFault: stepOf("drop",
				drawn("process", 1)), want: []string{"2"}},
			{name: "delays the oldest datagram in flight to a component", giveFault: stepOf("delay",
				drawn("process", 1)), want: []string{"2", "1"}},
			{name: "duplicates the oldest datagram in flight to a component", giveFault: stepOf("duplicate",
				drawn("process", 1)), want: []string{"1", "1", "2"}},
			{name: "drops no datagram of a component without a datagram in flight", giveFault: stepOf("drop",
				drawn("process", 0)), want: []string{"1", "2"}},
		}
		for _, tt := range datagrams {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var cs cellSet
				s := cs.simulation()
				s.Blocking = true
				s.Network = []host.NetworkOption{host.Latency(time.Millisecond, time.Millisecond)}
				s.Tolerate = map[sim.Fault]int{sim.FaultDrop: 1, sim.FaultDelay: 1, sim.FaultDuplicate: 1}

				cs.runCase(t, s, scriptOf(opening(true), send("1"), send("2"), tt.giveFault,
					stepOf("advance", drawn("advance", 2000))))

				assert.Equal(t, cs.received[0], tt.want, "the fault changes the datagrams that b receives")
			})
		}
	})

	t.Run("Test", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the random runs and the crash points of each scenario", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Tolerate = map[sim.Fault]int{sim.FaultCrash: 1}
			s.Require = map[sim.Fault]float64{sim.FaultCrash: 0.01}
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "put", Run: putOnA}}
			sim.Test(t, s)
		})

		// A step of the action count makes 12 choices: its flag, its index and
		// ten digits. 8,192 choices then cover 682 steps.
		t.Run("gives a case 82 choices for each step of MaxSteps", func(t *testing.T) {
			t.Parallel()

			s, steps := counting()
			s.MeanSteps, s.MaxSteps = 900, 1000
			s.Actions[0].Input = func(c *prop.Case) any {
				for range 10 {
					c.Draw(prop.Integer(0, 9), "digit")
				}
				return nil
			}
			sim.Test(t, s)
			assert.True(t, steps.most > 8192/12, "a case takes more steps than 8,192 choices of 12 for each step allow")
		})

		t.Run("fails a passing run whose replay observes another checksum", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions, s.Rules, s.Invariants, s.Converges, s.Scenarios = nil, nil, nil, nil, nil
			s.Spec = history.Spec[int]{}
			s.System = fixed(func(w *sim.World[event, effect]) { w.Spawn("a", newScattered) })
			got := assert.Rejects(t, "the determinism check fails the test", func(tb assert.TB) { sim.Test(tb, s) })
			expect.Equal(t, got[0].Detail["outcome"], any(prop.Flaky),
				"a replay of a passing case makes other steps than the case")
			divergence, _ := got[0].Detail["divergence"].(*prop.Divergence)
			assert.NotNil(t, divergence, "the record of the run states the divergence")
			expect.Equal(t, divergence.What, prop.FingerprintDifference, "the checksum of a step differs")
			expect.NotNil(t, divergence.Step, "the divergence names the step at which the two runs differ")
		})

		t.Run("fails for a fault that strikes in fewer cases than it requires", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Require = map[sim.Fault]float64{sim.FaultCrash: 0.5}
			got := assert.Rejects(t, "the requirement fails the test", func(tb assert.TB) { sim.Test(tb, s) })
			expect.NotNil(t, got[0].Detail["coverage"], "the record of the run states the shortfall of its coverage")
		})

		t.Run("runs each scenario on the simplest choice of each input of its system", func(t *testing.T) {
			t.Parallel()

			var runs, wider atomic.Int32
			s := simulation()
			s.System, s.Actions = stores, nil
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "put", Run: func(w *sim.World[event, effect]) {
				runs.Add(1)
				if w.Process("b") != nil {
					wider.Add(1)
				}
				putOnA(w)
			}}}
			sim.Test(t, s)
			assert.True(t, runs.Load() > 1, "the test runs the scenario and its crash points")
			assert.Equal(t, wider.Load(), int32(0), "each run of the scenario takes the simplest input")
		})

		t.Run("checks the history of each scenario against the spec", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions = nil
			s.Spec = rejecting
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "put", Run: putOnA}}
			got := assert.Rejects(t, "the scenario fails the test", func(tb assert.TB) { sim.Test(tb, s) })
			inner, _ := got[0].CaseFailure()
			expect.Equal(t, inner.Contract, "the history of the calls of the clients is linearizable",
				"the scenario fails at the check of its history")
		})

		t.Run("passes a scenario whose history its checker cannot decide within the budget", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions = nil
			s.Spec = rejecting
			s.History = []history.Option{history.Budget(1)}
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "puts", Run: putTwiceOnA}}
			sim.Test(t, s)
		})

		t.Run("fails at a scenario without a crash", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions = nil
			s.Invariants = []sim.Invariant[event, effect]{unstored()}
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "put", Run: putOnA}}
			got := assert.Rejects(t, "the scenario fails the test", func(tb assert.TB) { sim.Test(tb, s) })
			expect.Equal(t, got[0].Contract, "the scenario put finds no failure", "the scenario fails without a crash")
		})

		t.Run("fails at the stop of a scenario", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions = nil
			s.Invariants = []sim.Invariant[event, effect]{unstopped()}
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "put", Run: putOnA}}
			got := assert.Rejects(t, "the scenario fails the test", func(tb assert.TB) { sim.Test(tb, s) })
			expect.Equal(t, got[0].Contract, "the scenario put finds no failure", "the scenario fails at its stop")
		})

		t.Run("fails at a crash point before a step of a scenario", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "each store keeps its first epoch",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					expect.Equal(h, w.Process("a").Epoch(), uint64(1), h.Statement())
				},
			}}
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "put", Run: putOnA}}
			got := assert.Rejects(t, "the crash point fails the test", func(tb assert.TB) { sim.Test(tb, s) })
			expect.Equal(t, got[0].Contract, "the scenario put finds no failure with a crash before step 1",
				"the first crash point fails")
		})

		t.Run("fails at a crash point after the last step of a scenario", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = fixed(func(w *sim.World[event, effect]) { w.Spawn("a", newStore) })
			s.Actions = nil
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the store a does not crash after it stores a put",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					stored := slices.Contains(replies(w, "a"), any("stored"))
					expect.False(h, stored && w.Process("a").Epoch() > 1, h.Statement())
				},
			}}
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "put", Run: putOnA}}
			got := assert.Rejects(t, "the crash point fails the test", func(tb assert.TB) { sim.Test(tb, s) })
			expect.Equal(t, got[0].Contract, "the scenario put finds no failure with a crash after step 3",
				"the crash after the reply of the store fails")
		})
	})

	t.Run("Falsify", func(t *testing.T) {
		t.Parallel()

		t.Run("proves each rule on its planted defect", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			s := simulation()
			s.System = after(s.System, func(*sim.World[event, effect]) { built.Add(1) })
			t.Cleanup(func() { assert.True(t, built.Load() > 0, "the falsification runs the proofs of the rules") })
			sim.Falsify(t, s)
		})

		t.Run("proves each invariant on its planted defect", func(t *testing.T) {
			t.Parallel()

			var checked atomic.Int32
			s := simulation()
			s.Rules = nil
			inv := unstored()
			check := inv.Check
			inv.Check = func(h *sim.Check, w *sim.World[event, effect]) {
				checked.Add(1)
				check(h, w)
			}
			s.Invariants = []sim.Invariant[event, effect]{inv}
			t.Cleanup(func() {
				assert.True(t, checked.Load() > 0, "the falsification runs the proofs of the invariants")
			})
			sim.Falsify(t, s)
		})

		t.Run("proves each convergence on its planted defect", func(t *testing.T) {
			t.Parallel()

			var checked atomic.Int32
			s := simulation()
			s.Rules = nil
			cv := eventless()
			check := cv.Check
			cv.Check = func(h *sim.Check, w *sim.World[event, effect]) {
				checked.Add(1)
				check(h, w)
			}
			s.Converges = []sim.Convergence[event, effect]{cv}
			t.Cleanup(func() {
				assert.True(t, checked.Load() > 0, "the falsification runs the proofs of the convergences")
			})
			sim.Falsify(t, s)
		})
	})

	t.Run("Prove", func(t *testing.T) {
		t.Parallel()

		t.Run("fails for a check that the simulation does not have", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Invariants = []sim.Invariant[event, effect]{unstored()}
			s.Converges = []sim.Convergence[event, effect]{eventless()}
			expect.Equal(t, fatal(func(tb assert.TB) { s.Prove(tb, "Store/unknown") }),
				`sim: the simulation has no check "Store/unknown"`, "the failure names the check")
		})

		t.Run("fails for a name that two checks share", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			cv := eventless()
			cv.Statement = "no store replies to a put"
			s.Invariants = []sim.Invariant[event, effect]{unstored()}
			s.Converges = []sim.Convergence[event, effect]{cv}
			expect.Equal(t, fatal(func(tb assert.TB) { s.Prove(tb, "no store replies to a put") }),
				`sim: the simulation has two checks named "no store replies to a put"`,
				"the failure contains the name of the two checks")
		})

		t.Run("fails for a planted defect that passes its check", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the log never ends",
				Check:     func(*sim.Check, *sim.World[event, effect]) {},
				Defect:    s.System,
			}}
			got := assert.Rejects(t, "the proof fails", func(tb assert.TB) { s.Prove(tb, "the log never ends") })
			seed := fnv.New64a()
			_, _ = seed.Write([]byte("the log never ends"))
			expect.Equal(t, got[0].Contract,
				fmt.Sprintf("the planted defect fails the check the log never ends at the seed %d", seed.Sum64()),
				"the failure of the proof names the seed of the check whose defect passes")
		})

		t.Run("fails for a planted defect that fails another assertion", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the store a takes no event",
				Check: func(h *sim.Check, w *sim.World[event, effect]) {
					expect.Empty(h, w.Log(), "another message")
				},
				Defect: s.System,
			}}
			expect.Equal(t, fatal(func(tb assert.TB) { s.Prove(tb, "the store a takes no event") }),
				`the planted defect of the store a takes no event fails with the message "another message". `+
					`Its proof needs a failure of the assertion whose message is the statement `+
					`"the store a takes no event".`,
				"the failure names the message of the defect and the statement of the check")
		})

		t.Run("proves a rule apart from the other checks of the simulation", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Spec = rejecting
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the stores never start",
				Check:     func(h *sim.Check, w *sim.World[event, effect]) { expect.Empty(h, w.Log(), h.Statement()) },
			}}
			s.Prove(t, rules[1].ID)
		})

		t.Run("proves an invariant by a scenario that its random runs miss", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions = nil
			s.Invariants = []sim.Invariant[event, effect]{unstored()}
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "put", Run: putOnA}}
			s.Prove(t, "no store replies to a put")
		})

		t.Run("proves an invariant at the stop of a scenario", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions = nil
			s.Invariants = []sim.Invariant[event, effect]{unstopped()}
			s.Scenarios = []sim.Scenario[event, effect]{{Name: "put", Run: putOnA}}
			s.Prove(t, "no store stops after it stores a put")
		})

		t.Run("proves a convergence on its planted defect", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Converges = []sim.Convergence[event, effect]{eventless()}
			s.Prove(t, "the stores take no event once the faults end")
		})

		undefended := []struct {
			name      string
			give      func(s *sim.Simulation[event, effect, int])
			giveCheck string
		}{
			{
				name: "fails for an invariant without a planted defect",
				give: func(s *sim.Simulation[event, effect, int]) {
					inv := unstored()
					inv.Defect = nil
					s.Invariants = []sim.Invariant[event, effect]{inv}
				},
				giveCheck: "no store replies to a put",
			},
			{
				name: "fails for a convergence without a planted defect",
				give: func(s *sim.Simulation[event, effect, int]) {
					cv := eventless()
					cv.Defect = nil
					s.Converges = []sim.Convergence[event, effect]{cv}
				},
				giveCheck: "the stores take no event once the faults end",
			},
		}
		for _, tt := range undefended {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				s := simulation()
				tt.give(&s)
				expect.Equal(t, fatal(func(tb assert.TB) { s.Prove(tb, tt.giveCheck) }),
					fmt.Sprintf("sim: the check %q has no planted defect", tt.giveCheck),
					"the failure contains the check without a planted defect")
			})
		}
	})
}

// stores is a system of one to three stores a, b and c. The case chooses
// their number under the label stores.
func stores(c *prop.Case) func(w *sim.World[event, effect]) {
	n := c.Draw(prop.Integer(1, 3), "stores")
	return func(w *sim.World[event, effect]) {
		for i := range n {
			w.Spawn(string(rune('a'+i)), newStore)
		}
	}
}

// putOnA is a scenario whose client 0 puts a key on the store a.
func putOnA(w *sim.World[event, effect]) {
	w.Call(0, "a", "Put", nil, nil, func(r uint64) event { return put{key: "k", value: "v", reply: r} })
	for pending := w.Pending(); len(pending) > 0; pending = w.Pending() {
		w.Step(pending[0])
	}
}

// putTwiceOnA is the scenario of two concurrent Puts on the store a, by the
// clients 0 and 1, whose history the checker needs more than one step to
// search.
func putTwiceOnA(w *sim.World[event, effect]) {
	w.Call(0, "a", "Put", nil, nil, func(r uint64) event { return put{key: "k", value: "v", reply: r} })
	w.Call(1, "a", "Put", nil, nil, func(r uint64) event { return put{key: "k", value: "w", reply: r} })
	for pending := w.Pending(); len(pending) > 0; pending = w.Pending() {
		w.Step(pending[0])
	}
}

func FuzzRun(f *testing.F) {
	var runs atomic.Int32
	s := simulation()
	s.System = after(s.System, func(*sim.World[event, effect]) { runs.Add(1) })
	// Under -fuzz the worker processes run every input. This process then counts no run.
	if flag.Lookup("test.fuzz").Value.String() == "" {
		f.Cleanup(func() { assert.True(f, runs.Load() > 0, "the fuzz target runs the simulation") })
	}
	f.Add([]byte{0})
	sim.Fuzz(f, s)
}

// set returns the step of a Set of the cell on the client at the index or on
// the first client without an open call after it, get the step of a Get,
// parkAt the step of a call that parks at a, and send the step of a datagram
// from a to b.
func set(client int, name string) []entry {
	return stepOf("set", drawn("client", client), drawn("cell", name))
}

func get(client int, name string) []entry {
	return stepOf("get", drawn("client", client), drawn("cell", name))
}

func parkAt(client int) []entry { return stepOf("park", drawn("client", client)) }

// firstEvents adds to s the invariant that reads the history of the first
// case after each step, and returns the events that it read last.
func firstEvents[E, F, S any](s *sim.Simulation[E, F, S]) *[]history.Event {
	var first *sim.World[E, F]
	events := new([]history.Event)
	s.Invariants = append(s.Invariants, sim.Invariant[E, F]{
		Statement: "the history of the first case is read",
		Check: func(_ *sim.Check, w *sim.World[E, F]) {
			if first == nil {
				first = w
			}
			if w == first {
				*events = w.History().Events()
			}
		},
	})
	return events
}

// firstContents adds to s the invariant that reads the file name of the disk
// of the host of the component that comp returns in the first case after each
// step that leaves the file, and returns the content after each such step.
func firstContents(
	s *sim.Simulation[sim.None, sim.None, []history.Operation], comp func() *sim.Component[*cell], name string,
) *[]string {
	var first *sim.World[sim.None, sim.None]
	read := new([]string)
	s.Invariants = append(s.Invariants, sim.Invariant[sim.None, sim.None]{
		Statement: "the file of the first case is read",
		Check: func(check *sim.Check, w *sim.World[sim.None, sim.None]) {
			if first == nil {
				first = w
			}
			if w != first || !comp().Running() {
				return
			}
			env := comp().Value().env
			if _, err := env.Disk().Stat(name); err == nil {
				*read = append(*read, contents(check, env, name))
			}
		},
	})
	return read
}

// machinePark returns the step of a park of a store machine on the client at
// the index among the clients without an open call.
func machinePark(client int) []entry {
	return stepOf("park", drawn("client", client), drawn("to b", false))
}

// invokersOf returns the client of each invocation of the events, in order.
func invokersOf(events []history.Event) []int {
	var out []int
	for _, e := range events {
		if e.Kind == history.Invoke {
			out = append(out, e.Client)
		}
	}
	return out
}

func send(datagram string) []entry { return stepOf("send", drawn("datagram", datagram)) }

// unparked returns the actions of the cells without the call that parks at
// a. A parked call ends as unknown alone, and each call of unknown outcome
// widens the search of the check of the history.
func unparked(actions []sim.Action[sim.None, sim.None]) []sim.Action[sim.None, sim.None] {
	return slices.DeleteFunc(slices.Clone(actions), func(a sim.Action[sim.None, sim.None]) bool {
		return a.Name == "park"
	})
}
