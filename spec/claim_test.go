// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/spec"
)

func TestClaim(t *testing.T) {
	t.Parallel()

	t.Run("Claims", func(t *testing.T) {
		t.Parallel()

		t.Run("returns every claim of claims.json in the order of the file", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, spec.Claims(), entries[spec.Claim](t, "claims", "claims"),
				"the claims are the claims of the copy")
		})

		t.Run("returns claims that the schema of the copy accepts", func(t *testing.T) {
			t.Parallel()

			validates(t, "claims")
		})
	})

	t.Run("ClaimOf", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the claim with its law, its probe and its defect", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.ClaimOf(spec.ReadAfterWrite)

			assert.True(t, ok, "the copy has the claim read-after-write")
			assert.Equal(t, got, spec.Claim{
				ID:          spec.ReadAfterWrite,
				Statement:   "{callable} returns what {write} stored before it",
				Law:         spec.ReadReturnsTheWriteBeforeIt,
				Probe:       spec.WriteThenRead,
				Defect:      spec.Defect{Form: spec.ReadsAnotherValue, Reason: spec.UnobservedWrite},
				Since:       "1.0.0",
				Machineries: []spec.MachineryID{spec.NothingBeyondACaller},
			}, "the claim has the fields of the copy")
		})

		t.Run("returns false for an identifier that the copy does not contain", func(t *testing.T) {
			t.Parallel()

			got, ok := spec.ClaimOf("absent")

			assert.False(t, ok, "the copy has no claim absent")
			assert.Equal(t, got, spec.Claim{}, "the claim is the zero claim")
		})
	})
}
