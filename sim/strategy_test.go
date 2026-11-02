// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/sim"
)

// queued returns a simulation of the stores a and b, each with its start and
// n ticks to step on, under the strategy s. Its invariant check runs on the
// world after every step.
func queued(
	n int, s sim.Strategy, check func(h *sim.Check, w *sim.World[event, effect]),
) sim.Simulation[event, effect, int] {
	return sim.Simulation[event, effect, int]{
		Anatomy: anatomy,
		System: func(w *sim.World[event, effect]) {
			for _, name := range []string{"a", "b"} {
				w.Spawn(name, newStore)
				for token := range uint64(n) {
					w.Deliver(name, ticked{token: token})
				}
			}
		},
		Strategy:   s,
		Invariants: []sim.Invariant[event, effect]{{Statement: "the strategy picks the processes", Check: check}},
	}
}

// switches returns the steps of the log of w on a start or a tick whose
// process differs from the process of the step before.
func switches(w *sim.World[event, effect]) int {
	n, last := 0, ""
	for _, e := range w.Log() {
		if _, stop := e.Event.(stopped); stop {
			continue
		}
		if last != "" && e.Process != last {
			n++
		}
		last = e.Process
	}
	return n
}

func TestStrategy(t *testing.T) {
	t.Parallel()

	t.Run("Uniform", func(t *testing.T) {
		t.Parallel()

		t.Run("picks each process with a pending event first in some case", func(t *testing.T) {
			t.Parallel()

			s := queued(1, sim.Uniform(), func(h *sim.Check, w *sim.World[event, effect]) {
				if len(w.Log()) == 1 {
					h.Classify("first " + w.Log()[0].Process)
				}
			})
			prop.ForAll(t, "each process steps first in some case", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("first a", 0.2), prop.Require("first b", 0.2),
				prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("moves between the processes more than once", func(t *testing.T) {
			t.Parallel()

			s := queued(20, sim.Uniform(), func(h *sim.Check, w *sim.World[event, effect]) {
				if switches(w) > 1 {
					h.Classify("switches more than once")
				}
			})
			prop.ForAll(t, "a uniform pick moves between the processes", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("switches more than once", 0.5), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})
	})

	t.Run("PCT", func(t *testing.T) {
		t.Parallel()

		t.Run("gives each process a priority that the case chooses", func(t *testing.T) {
			t.Parallel()

			s := queued(1, sim.PCT(1), func(h *sim.Check, w *sim.World[event, effect]) {
				if len(w.Log()) == 1 {
					h.Classify("first " + w.Log()[0].Process)
				}
			})
			prop.ForAll(t, "each process steps first in some case", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("first a", 0.2), prop.Require("first b", 0.2),
				prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("steps the process of the highest priority until it has no event", func(t *testing.T) {
			t.Parallel()

			s := queued(20, sim.PCT(1), func(h *sim.Check, w *sim.World[event, effect]) {
				assert.True(h, switches(w) <= 1, "the second process steps once the first has no event")
			})
			prop.ForAll(t, "PCT of depth 1 moves between the processes once", func(c *prop.Case) { sim.Run(c, s) },
				prop.Store(""))
		})

		t.Run("lowers the picked process at a change point", func(t *testing.T) {
			t.Parallel()

			s := queued(100, sim.PCT(2), func(h *sim.Check, w *sim.World[event, effect]) {
				if switches(w) > 1 {
					h.Classify("switches more than once")
				}
			})
			prop.ForAll(t, "a change point moves to the other process", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("switches more than once", 0.05), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})

		t.Run("draws no pick for a change point that is not present", func(t *testing.T) {
			t.Parallel()

			s := queued(1, sim.PCT(2), func(h *sim.Check, w *sim.World[event, effect]) {
				expect.Empty(h, w.Log(), "the strategy picks no process")
			})
			got := assert.Rejects(t, "the first step fails the case", func(tb assert.TB) {
				prop.ForAll(tb, "the first step fails the case", func(c *prop.Case) { sim.Run(c, s) },
					prop.Hermetic(), prop.Seed(1), prop.Store(""), prop.Shrink(0), prop.Draws(`[]`))
			})
			entries, _ := got[0].Detail["counterexample"].([]prop.Entry)
			var labels []string
			for _, e := range entries {
				if d, ok := e.(prop.Drawn); ok {
					labels = append(labels, d.Label)
				}
			}
			expect.Contains(t, labels, "pct.change", "the case leaves the presence of the change point at its target")
			expect.NotContains(t, labels, "pct.at", "a change point that is not present draws no pick")
		})

		t.Run("bounds the moves between the processes by its depth", func(t *testing.T) {
			t.Parallel()

			s := queued(100, sim.Strategy{}, func(h *sim.Check, w *sim.World[event, effect]) {
				assert.True(h, switches(w) <= 5, "the zero strategy moves as PCT of depth 3 moves")
				if switches(w) > 1 {
					h.Classify("switches more than once")
				}
			})
			prop.ForAll(t, "the zero strategy is PCT of depth 3", func(c *prop.Case) { sim.Run(c, s) },
				prop.Require("switches more than once", 0.01), prop.Hermetic(), prop.Seed(1), prop.Store(""))
		})
	})
}
