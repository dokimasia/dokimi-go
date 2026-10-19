// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"testing"

	"go.dokimi.dev/dokimi-go/stub"
)

// The witness of major version 1 exists, so a generated double compiles
// against this runtime.
func TestCompat(t *testing.T) {
	t.Parallel()

	t.Run("CompatV1", func(t *testing.T) {
		t.Parallel()

		t.Run("is a function that a double refers to", func(t *testing.T) {
			t.Parallel()

			_ = stub.CompatV1
			stub.CompatV1()
		})
	})
}
