// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package fault_test

import (
	"testing"

	"go.dokimi.dev/fault"
)

// The witness of major version 1 exists, so a generated double that arms a
// fault strategy compiles against this runtime.
func TestCompat(t *testing.T) {
	t.Parallel()

	t.Run("CompatV1", func(t *testing.T) {
		t.Parallel()

		t.Run("is a function that a double refers to", func(t *testing.T) {
			t.Parallel()

			_ = fault.CompatV1
			fault.CompatV1()
		})
	})
}
