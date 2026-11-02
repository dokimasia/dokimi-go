// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"testing"

	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/sim"
)

func TestCheck(t *testing.T) {
	t.Parallel()

	t.Run("Statement", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the statement of the invariant", func(t *testing.T) {
			t.Parallel()

			s := simulation()
			s.Invariants = []sim.Invariant[event, effect]{{
				Statement: "the stores run",
				Check: func(h *sim.Check, _ *sim.World[event, effect]) {
					expect.Equal(h, h.Statement(), "the stores run", "the handle has the statement of its invariant")
				},
			}}
			prop.ForAll(t, "the handle of an invariant has its statement", func(c *prop.Case) { sim.Run(c, s) },
				prop.Store(""))
		})
	})
}
