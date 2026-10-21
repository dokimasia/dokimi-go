// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"testing"

	"go.dokimi.dev/dokimi-go/conform"
)

// The witness of major version 1 exists, so a generated suite compiles
// against this runtime.
func TestCompat(t *testing.T) {
	t.Parallel()

	t.Run("CompatV1", func(t *testing.T) {
		t.Parallel()

		t.Run("is a function that a suite refers to", func(t *testing.T) {
			t.Parallel()

			_ = conform.CompatV1
			conform.CompatV1()
		})
	})
}
