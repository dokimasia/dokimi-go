// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"fmt"
	"strings"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/sim"
)

func TestProcess(t *testing.T) {
	t.Parallel()

	t.Run("Name", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the name of its spawn", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				p := w.Process("store")
				assert.NotNil(c, p, "the world has the process")
				expect.Equal(c, p.Name(), "store", "the process has the name of its spawn")
			})
		})
	})

	t.Run("Epoch", func(t *testing.T) {
		t.Parallel()

		t.Run("returns 1 for a process that has not restarted", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				expect.Equal(c, w.Process("store").Epoch(), uint64(1), "the first epoch is 1")
			})
		})
	})

	t.Run("Clock", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the clock that the machine of the process reads", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				var read *sim.Clock
				w := sim.New(c, anatomy)
				w.Spawn("store", func(clock *sim.Clock) sim.Machine[event, effect] {
					read = clock
					return &store{}
				})
				expect.True(c, w.Process("store").Clock() == read, "the machine reads the clock of its process")
			})
		})
	})

	t.Run("Dependency", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the first dependency of the process of the type", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				first, second := &strings.Builder{}, &strings.Builder{}
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore, first, 7, second)
				got := sim.Dependency[fmt.Stringer](w, w.Process("store"))
				expect.True(c, got == fmt.Stringer(first), "the first dependency of the interface is the first builder")
			})
		})

		t.Run("fails the case for a process without a dependency of the type", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore, 7)
				sim.Dependency[fmt.Stringer](w, w.Process("store"))
			})
			expect.Equal(t, got.Contract, `sim: the process "store" has no dependency of the type fmt.Stringer`,
				"the failure names the process and the type")
		})
	})
}
