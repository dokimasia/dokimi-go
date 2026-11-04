// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/spec"
)

func TestStance(t *testing.T) {
	t.Parallel()

	t.Run("Stances", func(t *testing.T) {
		t.Parallel()

		t.Run("returns every stance of stances.json in the order of the file", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, spec.Stances(), entries[spec.Stance](t, "stances", "stances"),
				"the stances are the stances of the copy")
		})

		t.Run("returns stances that the schema of the copy accepts", func(t *testing.T) {
			t.Parallel()

			validates(t, "stances")
		})
	})

	t.Run("StanceOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the stance with its meaning and what closes it", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.StanceOf(spec.Needs)

			assert.True(t, ok, "the copy has the stance needs")
			assert.Equal(t, got, spec.Stance{
				ID:      spec.Needs,
				Meaning: "The claim needs a machinery that the run does not supply.",
				Closes:  "The param machineries on the declaration, or a runtime that supplies the machinery.",
				Since:   "1.0.0",
			}, "the stance has the fields of the copy")
		})

		t.Run("returns false for an identifier that the copy does not contain", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.StanceOf("absent")

			assert.False(t, ok, "the copy has no stance absent")
			assert.Equal(t, got, spec.Stance{}, "the stance is the zero stance")
		})
	})
}
