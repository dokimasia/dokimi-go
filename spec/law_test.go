// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/spec"
)

func TestLaw(t *testing.T) {
	t.Parallel()

	t.Run("Laws", func(t *testing.T) {
		t.Parallel()

		t.Run("returns every law of laws.json in the order of the file", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, spec.Laws(), entries[spec.Law](t, "laws", "laws"), "the laws are the laws of the copy")
		})

		t.Run("returns laws that the schema of the copy accepts", func(t *testing.T) {
			t.Parallel()

			validates(t, "laws")
		})
	})

	t.Run("LawOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the law with its statement", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.LawOf(spec.NoValueBesideAnError)

			assert.True(t, ok, "the copy has the law no-value-beside-an-error")
			assert.Equal(t, got, spec.Law{
				ID:        spec.NoValueBesideAnError,
				Statement: "A call that fails returns the zero value of each value result.",
				Since:     "1.0.0",
			}, "the law has the fields of the copy")
		})

		t.Run("returns false for an identifier that the copy does not contain", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.LawOf("absent")

			assert.False(t, ok, "the copy has no law absent")
			assert.Equal(t, got, spec.Law{}, "the law is the zero law")
		})
	})
}
