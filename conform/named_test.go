// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
)

func TestNamed(t *testing.T) {
	t.Parallel()

	t.Run("Named", func(t *testing.T) {
		t.Parallel()

		t.Run("pairs a value with the name of its declaration", func(t *testing.T) {
			t.Parallel()

			got := conform.Named[kind]{Name: "Second", Value: 2}

			assert.Equal(t, got.Name, "Second", "the name is the name of the declaration")
			assert.Equal(t, got.Value, kind(2), "the value is the declared value")
		})
	})
}
