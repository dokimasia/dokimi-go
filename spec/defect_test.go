// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/spec"
)

func TestDefect(t *testing.T) {
	t.Parallel()

	t.Run("DefectForms", func(t *testing.T) {
		t.Parallel()

		t.Run("returns every form of defects.json in the order of the file", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, spec.DefectForms(), entries[spec.DefectForm](t, "defects", "forms"),
				"the forms are the forms of the copy")
		})

		t.Run("returns forms that the schema of the copy accepts", func(t *testing.T) {
			t.Parallel()

			validates(t, "defects")
		})
	})

	t.Run("DefectFormOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the form with what it does", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.DefectFormOf(spec.PutRefuses)

			assert.True(t, ok, "the copy has the form put-refuses")
			assert.Equal(
				t,
				got,
				spec.DefectForm{ID: spec.PutRefuses, Does: "The put fails on every call.", Since: "1.0.0"},
				"the form has the fields of the copy",
			)
		})

		t.Run("returns false for an identifier that the copy does not contain", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.DefectFormOf("absent")

			assert.False(t, ok, "the copy has no form absent")
			assert.Equal(t, got, spec.DefectForm{}, "the form is the zero form")
		})
	})
}
