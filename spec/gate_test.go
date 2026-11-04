// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/spec"
)

func TestGate(t *testing.T) {
	t.Parallel()

	t.Run("Gates", func(t *testing.T) {
		t.Parallel()

		t.Run("returns every gate of gates.json in the order of the file", func(t *testing.T) {
			t.Parallel()

			assert.Equal(
				t,
				spec.Gates(),
				entries[spec.Gate](t, "gates", "gates"),
				"the gates are the gates of the copy",
			)
		})

		t.Run("returns gates that the schema of the copy accepts", func(t *testing.T) {
			t.Parallel()

			validates(t, "gates")
		})
	})

	t.Run("GateOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the gate with its version", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.GateOf(spec.ReturnsBesideError)

			assert.True(t, ok, "the copy has the gate returns-beside-error")
			assert.Equal(t, got, spec.Gate{ID: spec.ReturnsBesideError, Since: "1.0.0"},
				"the gate has the fields of the copy")
		})

		t.Run("returns false for an identifier that the copy does not contain", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.GateOf("absent")

			assert.False(t, ok, "the copy has no gate absent")
			assert.Equal(t, got, spec.Gate{}, "the gate is the zero gate")
		})
	})
}
