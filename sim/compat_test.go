// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"testing"

	"go.dokimi.dev/sim"
)

// The witness of major version 1 exists, so a generated simulation compiles
// against this runtime.
func TestCompat(t *testing.T) {
	t.Parallel()

	t.Run("CompatV1", func(t *testing.T) {
		t.Parallel()

		t.Run("is a function that a simulation refers to", func(t *testing.T) {
			t.Parallel()

			_ = sim.CompatV1
			sim.CompatV1()
		})
	})
}
