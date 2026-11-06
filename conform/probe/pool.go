// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"go.dokimi.dev/assert/prop"
)

// Pool is a pool of the keys of one type that the operations of a case pick
// from: the keys that the case chose at its start, and the keys that the
// calls of the subject produced. The world calls Pick and Add on the
// goroutine of the case.
type Pool[T any] struct {
	keys []T

	// hot reports that the first key is the hot set of the pool.
	hot bool
}

// NewPool returns a pool whose starting keys the case c chooses from g under
// label. The case also chooses once whether the pool is hot, under the label
// with the suffix .hot, and the choice shrinks to a pool that is not hot. It
// fails the case for a generator that chooses no key.
func NewPool[T any](c *prop.Case, g prop.Generator[[]T], label string) *Pool[T] {
	keys := c.Draw(g, label)
	if len(keys) == 0 {
		c.Fatalf("probe: NewPool(%s) of a generator that chose no key", label)
	}
	return &Pool[T]{keys: keys, hot: c.Draw(prop.Boolean(), label+".hot")}
}

// Pick returns a key of the pool, as a choice of the case under label. It
// follows the hotspot distribution of YCSB with a hot set of one key: a pick
// from a hot pool takes the first key in half of its picks, a choice under the
// label with the suffix .hot. Every other pick takes the key at an index that
// the case chooses, which shrinks to the first key.
func (p *Pool[T]) Pick(c *prop.Case, label string) T {
	if p.hot && c.Draw(prop.Boolean(), label+".hot") {
		return p.keys[0]
	}
	return p.keys[c.Draw(prop.Integer(0, len(p.keys)-1), label)]
}

// Add adds the key v, which a call produced.
func (p *Pool[T]) Add(v T) {
	p.keys = append(p.keys, v)
}
