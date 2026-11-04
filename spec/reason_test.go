// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/spec"
)

func TestReason(t *testing.T) {
	t.Parallel()

	t.Run("Reasons", func(t *testing.T) {
		t.Parallel()

		t.Run("returns every reason code of reasons.json in the order of the file", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, spec.Reasons(), entries[spec.Reason](t, "reasons", "reasons"),
				"the reason codes are the reason codes of the copy")
		})

		t.Run("returns reason codes that the schema of the copy accepts", func(t *testing.T) {
			t.Parallel()

			validates(t, "reasons")
		})
	})

	t.Run("ReasonOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the reason code with its meaning", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.ReasonOf(spec.ValueBesideError)

			assert.True(t, ok, "the copy has the reason code value-beside-error")
			assert.Equal(t, got, spec.Reason{
				ID:      spec.ValueBesideError,
				Meaning: "The call failed and returned a value other than the zero value.",
				Since:   "1.0.0",
			}, "the reason code has the fields of the copy")
		})

		t.Run("returns false for an identifier that the copy does not contain", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.ReasonOf("absent")

			assert.False(t, ok, "the copy has no reason code absent")
			assert.Equal(t, got, spec.Reason{}, "the reason code is the zero reason code")
		})
	})
}
