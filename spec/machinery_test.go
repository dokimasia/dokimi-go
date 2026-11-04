// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"slices"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/spec"
)

func TestMachinery(t *testing.T) {
	t.Parallel()

	t.Run("Machineries", func(t *testing.T) {
		t.Parallel()

		t.Run("returns every machinery of machineries.json in the order of the file", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, spec.Machineries(), entries[spec.Machinery](t, "machineries", "machineries"),
				"the machineries are the machineries of the copy")
		})

		t.Run("returns machineries that the schema of the copy accepts", func(t *testing.T) {
			t.Parallel()

			validates(t, "machineries")
		})

		t.Run("returns machineries with a supplier of the package", func(t *testing.T) {
			t.Parallel()
			suppliers := []spec.Supplier{spec.ByRuntime, spec.ByConsumer, spec.BySim, spec.ByDeclaration}

			for _, m := range spec.Machineries() {
				expect.True(t, slices.Contains(suppliers, m.By),
					"machinery "+string(m.ID)+" has a supplier of the package")
			}
		})
	})

	t.Run("MachineryOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the machinery with the param of the directive suite", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.MachineryOf(spec.ReferenceImplementation)

			assert.True(t, ok, "the copy has the machinery reference-implementation")
			assert.Equal(t, got, spec.Machinery{
				ID:       spec.ReferenceImplementation,
				Supplies: "A second implementation that gives the expected results.",
				By:       spec.ByConsumer,
				Param:    "reference",
				Since:    "1.0.0",
			}, "the machinery has the fields of the copy")
		})

		t.Run("returns false for an identifier that the copy does not contain", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.MachineryOf("absent")

			assert.False(t, ok, "the copy has no machinery absent")
			assert.Equal(t, got, spec.Machinery{}, "the machinery is the zero machinery")
		})
	})
}
