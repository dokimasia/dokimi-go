// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/stub"
)

// The setters of Configurable without a return value reach the state that the
// chaining setters of a method stub reach.
func TestConfigurable(t *testing.T) {
	t.Parallel()

	t.Run("SetClock", func(t *testing.T) {
		t.Parallel()

		t.Run("sets the clock of the stub", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](t, getName)
			clk := assert.NewControlled(origin)
			var c stub.Configurable = s
			c.SetClock(clk)
			assert.Equal(
				t,
				s.Clock(),
				assert.Clock(clk),
				"SetClock reaches the state of WithClock",
				assert.ByIdentity(),
			)
		})
	})

	t.Run("SetRandSource", func(t *testing.T) {
		t.Parallel()

		t.Run("sets the source of the faults of the stub", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](t, getName)
			var c stub.Configurable = s
			c.SetRandSource(quarter{})
			s.FaultsWithProbability(0.5, errTest)
			f, _ := s.ShouldFaultFor(call{})
			assert.True(t, f, "the number 0.25 of the source is below 0.5")
		})
	})
}
