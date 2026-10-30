// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/spec"
)

func TestSpec(t *testing.T) {
	t.Parallel()

	t.Run("Version", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the version of the copy without its newline", func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile("VERSION")
			assert.NoError(t, err, "the copy has a version")

			assert.Equal(t, spec.Version(), strings.TrimSpace(string(data)), "the version is the file VERSION")
		})
	})

	t.Run("Probes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the probes of probes.json in the order of the file", func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile("probes.json")
			assert.NoError(t, err, "the copy has probes.json")
			var want struct {
				Probes []spec.Probe `json:"probes"`
			}
			assert.NoError(t, json.Unmarshal(data, &want), "probes.json decodes")

			assert.Equal(t, spec.Probes(), want.Probes, "the probes are the probes of the file")
			assert.NotEmpty(t, spec.Probes(), "the copy has probes")
		})
	})

	t.Run("ProbeOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the probe with its reasons and its vacuous condition", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.ProbeOf(spec.ZeroBesideError)

			assert.True(t, ok, "the spec has the probe zero-beside-error")
			assert.Equal(t, got, spec.Probe{
				ID:      spec.ZeroBesideError,
				Reasons: []spec.ReasonID{spec.ValueBesideError},
				Vacuous: "No call fails, with the sample inputs or with a cancelled context.",
			}, "the probe reports the reason value-beside-error")
		})

		t.Run("returns false for an identifier that names no probe", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.ProbeOf("absent")

			assert.False(t, ok, "the spec has no probe absent")
			assert.Equal(t, got, spec.Probe{}, "the probe is the zero probe")
		})
	})
}
