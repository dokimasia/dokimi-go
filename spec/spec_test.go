// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
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
}
