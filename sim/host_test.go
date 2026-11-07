// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/sim"
)

func TestHost(t *testing.T) {
	t.Parallel()

	t.Run("AddHost", func(t *testing.T) {
		t.Parallel()

		t.Run("drifts the clock of the host by the draw of the case under drift.node", func(t *testing.T) {
			t.Parallel()
			var got time.Time
			inBubble(t, `[{"label": "drift.node", "value": {"type": "int", "value": 500}}]`,
				func(c *prop.Case, first bool) {
					w := sim.New(c, anatomy)
					w.AddHost("node").Spawn("store", newStore)
					w.Advance(time.Second)
					if first {
						got = w.Process("store").Clock().Now()
					}
				})

			assert.Equal(t, got, sim.Start.Add(1500*time.Millisecond),
				"a drift of 500 per mille runs the clock of the host at one and a half times the rate of the run")
		})

		t.Run("fails the case for a name that a host has", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.AddHost("node")
				w.AddHost("node")
			})
			expect.Equal(t, got.Contract, `sim: the world has a host named "node"`, "the failure names the host")
		})
	})

	t.Run("Name", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the name of its addition", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				h := sim.New(c, anatomy).AddHost("node")
				expect.Equal(c, h.Name(), "node", "the host has the name of its addition")
			})
		})
	})

	t.Run("Spawn", func(t *testing.T) {
		t.Parallel()

		t.Run("queues the start of each process of the host", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				h := w.AddHost("node")
				h.Spawn("store", newStore)
				h.Spawn("cache", newStore)
				expect.Equal(c, w.Pending(), []string{"store", "cache"}, "each process of the host waits for its start")
			})
		})

		t.Run("gives each process of the host the clock of the host", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := node(c)
				expect.True(c, w.Process("store").Clock() == w.Process("cache").Clock(),
					"the processes of one host read one clock")
				expect.False(c, w.Process("store").Clock() == w.Process("other").Clock(),
					"the processes of two hosts read two clocks")
			})
		})

		t.Run("gives the process its dependencies", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.AddHost("node").Spawn("store", newStore, "dependency")
				expect.Equal(c, sim.Dependency[string](w, w.Process("store")), "dependency",
					"the process has the dependency of its spawn")
			})
		})

		t.Run("fails the case for a host that does not run", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				h := w.AddHost("node")
				h.Spawn("store", newStore)
				w.Crash("node")
				h.Spawn("cache", newStore)
			})
			expect.Equal(t, got.Contract, `sim: the host "node" does not run`, "the failure names the host")
		})

		t.Run("fails the case for a name that a process has", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.AddHost("node").Spawn("store", newStore)
			})
			expect.Equal(t, got.Contract, `sim: the world has a process named "store"`, "the failure names the process")
		})
	})
}
