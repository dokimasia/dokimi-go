// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"flag"
	"fmt"
	"hash/fnv"
	"maps"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/medium"
	"go.dokimi.dev/sim"
)

// every tolerates one fault of each kind at once.
var every = map[sim.Fault]int{
	sim.FaultCrash: 1, sim.FaultRestart: 1, sim.FaultPause: 1, sim.FaultPartition: 1, sim.FaultDrop: 1,
	sim.FaultDelay: 1, sim.FaultDuplicate: 1, sim.FaultLoseBefore: 1, sim.FaultLoseAfter: 1, sim.FaultOutage: 1,
	sim.FaultSlow: 1, sim.FaultClockJump: 1, sim.FaultClockStrobe: 1,
}

// media are the faults of the media of a process, each in force once at
// most.
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

// timed is a machine that sets one timer of after on its start.
type timed struct{ after time.Duration }

func (m timed) Step(ev event, out []effect) []effect {
	if _, ok := ev.(started); ok {
		return append(out, timer{token: 1, after: m.after})
	}
	return out
}

func (timed) Err() error { return nil }

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
// partition of the processes names have, and the empty string for none.
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

		t.Run("finds no failure in machines that keep the rules under every fault", func(t *testing.T) {
			t.Parallel()

			s := simulation()
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
			system := s.System
			s.System = func(w *sim.World[event, effect]) {
				system(w)
				w.Spawn("c", newStore, mediumWith(""))
			}
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

		t.Run("strikes each fault of the media of a process and restores full media", func(t *testing.T) {
			t.Parallel()

			// Two full disks within the bound and a crash leave the run with a
			// process whose media are full, a process without a medium and a
			// process that does not run, which no fault of the media takes.
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

		t.Run("corrupts a file in a directory of the media of a process", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = func(w *sim.World[event, effect]) {
				m := medium.NewMemory()
				_ = m.MkdirAll("d", 0o700)
				f, _ := m.OpenFile("d/f", os.O_CREATE|os.O_WRONLY, 0o600)
				_, _ = f.Write([]byte("data"))
				_ = f.Sync()
				_ = m.SyncDir("d")
				_ = m.SyncDir(".")
				w.Spawn("a", newStore, m)
				w.Spawn("b", newStore)
			}
			s.Tolerate = map[sim.Fault]int{sim.FaultCorruption: 1}
			prop.ForAll(t, "the run corrupts the file in the directory", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require(string(sim.FaultCorruption), 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("runs a system without processes under every fault", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.System = func(*sim.World[event, effect]) {}
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
				name:      "jumps the clock of a process",
				giveFault: sim.FaultClockJump,
				wantLabel: "a step before the start",
			},
			{
				name:      "strobes the clock of a process",
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

		t.Run("cuts the processes in the shapes of the nemesis of Jepsen", func(t *testing.T) {
			t.Parallel()

			names := []string{"a", "b", "c", "d", "e"}
			s := simulation()
			s.System = func(w *sim.World[event, effect]) {
				for _, name := range names {
					w.Spawn(name, newStore)
				}
			}
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
			s.System = func(w *sim.World[event, effect]) {
				w.Spawn("a", func(*sim.Clock) sim.Machine[event, effect] { return timed{after: time.Second} })
			}
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
			s.System = func(w *sim.World[event, effect]) {
				w.Spawn("ticker", func(*sim.Clock) sim.Machine[event, effect] { return ticker{} })
			}
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
				s.System = func(w *sim.World[event, effect]) {
					w.Spawn("ledger", func(*sim.Clock) sim.Machine[event, effect] { return ledger{} })
				}
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
			s.System = func(w *sim.World[event, effect]) {
				w.Spawn("counted", func(*sim.Clock) sim.Machine[event, effect] { return counted{} })
			}
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
			s.System = func(w *sim.World[event, effect]) {
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
			}
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
					"step 10: a, epoch 2, clock 0s: sim_test.stopped{} -> sim_test.reply{value:<nil> " +
					"err:store: stopped to:4}",
				"the history of the calls:\n" +
					"event 0: client 0 invoke Park []\n" +
					"event 1: client 1 invoke Put []\n" +
					"event 2: client 2 invoke Park []\n" +
					"event 3: client 1 ok Put stored\n" +
					"event 4: client 2 fail Park store: a put without a value\n" +
					"event 5: client 0 fail Park store: stopped",
			}, "the failing case notes each step of the log and each event of the history")
		})
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

		t.Run("fails a passing run whose replay observes another checksum", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Actions, s.Rules, s.Invariants, s.Converges, s.Scenarios = nil, nil, nil, nil, nil
			s.Spec = history.Spec[int]{}
			s.System = func(w *sim.World[event, effect]) { w.Spawn("a", newScattered) }
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
			s.System = func(w *sim.World[event, effect]) { w.Spawn("a", newStore) }
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
			system := s.System
			s.System = func(w *sim.World[event, effect]) {
				built.Add(1)
				system(w)
			}
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
	})

	t.Run("Prove", func(t *testing.T) {
		t.Parallel()

		t.Run("fails for a check that the simulation does not have", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Invariants = []sim.Invariant[event, effect]{unstored()}
			expect.Equal(t, fatal(func(tb assert.TB) { s.Prove(tb, "Store/unknown") }),
				`sim: the simulation has no check "Store/unknown"`, "the failure names the check")
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
	})
}

// putOnA is a scenario whose client 0 puts a key on the store a.
func putOnA(w *sim.World[event, effect]) {
	w.Call(0, "a", "Put", nil, nil, func(r uint64) event { return put{key: "k", value: "v", reply: r} })
	for pending := w.Pending(); len(pending) > 0; pending = w.Pending() {
		w.Step(pending[0])
	}
}

func FuzzRun(f *testing.F) {
	var runs atomic.Int32
	s := simulation()
	system := s.System
	s.System = func(w *sim.World[event, effect]) {
		runs.Add(1)
		system(w)
	}
	// Under -fuzz the worker processes run every input. This process then counts no run.
	if flag.Lookup("test.fuzz").Value.String() == "" {
		f.Cleanup(func() { assert.True(f, runs.Load() > 0, "the fuzz target runs the simulation") })
	}
	f.Add([]byte{0})
	sim.Fuzz(f, s)
}
