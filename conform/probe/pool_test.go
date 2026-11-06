// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"sync"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform/probe"
)

// keysOf is the generator of the starting keys of the pools of the specs.
var keysOf = prop.List(prop.Integer(0, 9), prop.MinSize(1), prop.MaxSize(3))

// firstOf returns what body returns in the first case of a run whose first
// case takes the draws.
func firstOf(t *testing.T, draws string, body func(c *prop.Case) int) int {
	t.Helper()

	var mu sync.Mutex
	got, cases := 0, 0
	prop.ForAll(t, "the pool picks a key", func(c *prop.Case) {
		v := body(c)
		mu.Lock()
		defer mu.Unlock()
		if cases++; cases == 1 {
			got = v
		}
	}, prop.Cases(1), prop.Draws(draws), prop.Store(""))
	return got
}

func TestPool(t *testing.T) {
	t.Parallel()

	t.Run("NewPool", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the case for a generator that chooses no key", func(t *testing.T) {
			t.Parallel()
			got := assert.Rejects(t, "the pool fails the case", func(tb assert.TB) {
				prop.ForAll(tb, "a pool without keys", func(c *prop.Case) {
					probe.NewPool(c, prop.List(prop.Integer(0, 9)), "keys")
				}, prop.Cases(1), prop.Draws(`[{"label": "keys", "value": {"type": "list", "of": "int", "value": []}}]`),
					prop.Store(""), prop.Shrink(0))
			})
			assert.Length(t, got, 1, "the property fails once")
			inner, ok := got[0].CaseFailure()
			assert.True(t, ok, "the record of the property contains the failure of its case")

			assert.Equal(t, inner.Contract, "probe: NewPool(keys) of a generator that chose no key",
				"the failure names the pool")
		})
	})

	t.Run("Pick", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveDraws string
			want      int
		}{
			{
				name: "takes the first key at a hot pick of a hot pool",
				giveDraws: `[{"label": "keys", "value": {"type": "list", "of": "int", "value": [7, 8]}},` +
					`{"label": "keys.hot", "value": {"type": "bool", "value": true}},` +
					`{"label": "k.hot", "value": {"type": "bool", "value": true}}]`,
				want: 7,
			},
			{
				name: "takes the key at the index of the case at a cold pick of a hot pool",
				giveDraws: `[{"label": "keys", "value": {"type": "list", "of": "int", "value": [7, 8]}},` +
					`{"label": "keys.hot", "value": {"type": "bool", "value": true}},` +
					`{"label": "k.hot", "value": {"type": "bool", "value": false}},` +
					`{"label": "k", "value": {"type": "int", "value": 1}}]`,
				want: 8,
			},
			{
				name: "takes the key at the index of the case from a pool that is not hot",
				giveDraws: `[{"label": "keys", "value": {"type": "list", "of": "int", "value": [7, 8]}},` +
					`{"label": "keys.hot", "value": {"type": "bool", "value": false}},` +
					`{"label": "k", "value": {"type": "int", "value": 1}}]`,
				want: 8,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got := firstOf(t, tt.giveDraws, func(c *prop.Case) int {
					return probe.NewPool(c, keysOf, "keys").Pick(c, "k")
				})

				assert.Equal(t, got, tt.want, "the pick takes the key")
			})
		}
	})

	t.Run("Add", func(t *testing.T) {
		t.Parallel()

		t.Run("adds a key that a later pick can take", func(t *testing.T) {
			t.Parallel()
			draws := `[{"label": "keys", "value": {"type": "list", "of": "int", "value": [7]}},` +
				`{"label": "keys.hot", "value": {"type": "bool", "value": false}},` +
				`{"label": "k", "value": {"type": "int", "value": 1}}]`

			got := firstOf(t, draws, func(c *prop.Case) int {
				p := probe.NewPool(c, keysOf, "keys")
				p.Add(9)
				return p.Pick(c, "k")
			})

			assert.Equal(t, got, 9, "the pick takes the added key")
		})
	})
}
