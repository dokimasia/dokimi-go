// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
)

// A callable joins its name and a segment into the identity of a check.
func TestIdentity(t *testing.T) {
	t.Parallel()

	t.Run("Check", func(t *testing.T) {
		t.Parallel()

		t.Run("joins the segment to the name of the callable", func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, storeGet.Check("keeps-the-value"), conform.ID[Store]("Store.Get/keeps-the-value"),
				"the identity is the name of the callable, a slash and the segment")
		})
	})
}
