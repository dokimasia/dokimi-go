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

func TestProbe(t *testing.T) {
	t.Parallel()

	t.Run("Probes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns every probe of probes.json in the order of the file", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, spec.Probes(), entries[spec.Probe](t, "probes", "probes"),
				"the probes are the probes of the copy")
		})

		t.Run("returns probes that the schema of the copy accepts", func(t *testing.T) {
			t.Parallel()

			validates(t, "probes")
		})

		t.Run("returns probes whose inputs have a kind of the package", func(t *testing.T) {
			t.Parallel()
			kinds := []spec.InputKind{
				spec.KindCall, spec.KindValue, spec.KindError, spec.KindDuration, spec.KindCount, spec.KindProjection,
				spec.KindScope, spec.KindHook, spec.KindPredicate, spec.KindClock, spec.KindValues,
				spec.KindActions, spec.KindOperation, spec.KindApply, spec.KindReference,
				spec.KindPull, spec.KindChannel, spec.KindHandleMethod, spec.KindDurable, spec.KindWorld,
			}

			for _, p := range spec.Probes() {
				for _, in := range p.Inputs {
					expect.True(t, slices.Contains(kinds, in.Kind),
						"input "+in.Name+" of probe "+string(p.ID)+" has a kind of the package")
				}
			}
		})
	})

	t.Run("ProbeOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the probe with its inputs, its reasons and its vacuous condition", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.ProbeOf(spec.ZeroBesideError)

			assert.True(t, ok, "the copy has the probe zero-beside-error")
			assert.Equal(t, got, spec.Probe{
				ID:      spec.ZeroBesideError,
				Summary: "A call that fails returns the zero value of its result.",
				Vacuous: "No call fails, with the sample inputs or with a cancelled context.",
				Since:   "1.0.0",
				Inputs:  []spec.Input{{Name: "call", Kind: spec.KindCall}, {Name: "zero", Kind: spec.KindValue}},
				Reasons: []spec.ReasonID{spec.ValueBesideError},
			}, "the probe has the fields of the copy")
		})

		t.Run("returns false for an identifier that the copy does not contain", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.ProbeOf("absent")

			assert.False(t, ok, "the copy has no probe absent")
			assert.Equal(t, got, spec.Probe{}, "the probe is the zero probe")
		})
	})
}
