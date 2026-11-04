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

	t.Run("Generated", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			give conform.ID[Store]
			want conform.ID[Store]
		}{
			{
				name: "adds the case generated to an identity without a case",
				give: "Store.Get/keeps-the-value",
				want: "Store.Get/keeps-the-value#generated",
			},
			{
				name: "adds the suffix generated to the case of an identity",
				give: "Store.Get/expires#0",
				want: "Store.Get/expires#0-generated",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, tt.give.Generated(), tt.want, "Generated returns the identity of the generated binding")
			})
		}
	})
}
