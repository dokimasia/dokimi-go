// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform/probe"
)

func TestCall(t *testing.T) {
	t.Parallel()

	t.Run("Call", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value and the error of the callable", func(t *testing.T) {
			t.Parallel()

			call := probe.Call[int](func(context.Context) (int, error) { return 7, errOther })
			got, err := call(t.Context())

			assert.Equal(t, got, 7, "the call returns the value of the callable")
			assert.ErrorIs(t, err, errOther, "the call returns the error of the callable")
		})
	})

	t.Run("Act", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the error of the callable", func(t *testing.T) {
			t.Parallel()

			act := probe.Act(func(context.Context) error { return errOther })

			assert.ErrorIs(t, act(t.Context()), errOther, "the act returns the error of the callable")
		})
	})

	t.Run("Lookup", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value and the presence flag of the callable", func(t *testing.T) {
			t.Parallel()

			lookup := probe.Lookup[probe.Pair[int, string]](func(context.Context) (probe.Pair[int, string], bool) {
				return probe.Pair[int, string]{First: 7, Second: "seven"}, true
			})
			got, found := lookup(t.Context())

			assert.Equal(t, got, probe.Pair[int, string]{First: 7, Second: "seven"},
				"the lookup returns the two values of the callable as a pair")
			assert.True(t, found, "the lookup returns the presence flag of the callable")
		})
	})
}
