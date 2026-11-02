// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"cmp"
	"slices"

	"go.dokimi.dev/assert/prop"
)

// defaultDepth is the depth of PCT of the zero Strategy.
const defaultDepth = 3

// Strategy chooses the process that steps next. The zero Strategy is PCT(3).
type Strategy struct {
	depth   int
	pct     bool
	uniform bool
}

// Uniform returns the strategy that picks a process with a pending event
// uniformly.
func Uniform() Strategy { return Strategy{uniform: true} }

// PCT returns the strategy of probabilistic concurrency testing of depth
// depth. A run with a depth below 1 fails its case.
func PCT(depth int) Strategy { return Strategy{pct: true, depth: depth} }

// picker is a strategy in one case: the rank of each process that it has
// seen, the picks after which a change point lowers the picked process, the
// most picks of a run, and the picks and the change points so far.
type picker[E, F any] struct {
	c       *prop.Case
	ranks   map[*Process[E, F]]rank
	changes []int
	depth   int
	limit   int
	picks   int
	lowered int
	uniform bool
	started bool
}

// rank is the rank of a process under PCT: the change points that lowered
// it, and its priority. A higher rank steps first.
type rank struct {
	priority uint64
	level    int
}

// newPicker returns the picker of the strategy s in the case c, whose change
// points fall within the first limit picks. It fails the case for PCT with a
// depth below 1.
func newPicker[E, F any](c *prop.Case, s Strategy, limit int) *picker[E, F] {
	k := &picker[E, F]{c: c, ranks: map[*Process[E, F]]rank{}, depth: s.depth, limit: limit, uniform: s.uniform}
	switch {
	case s.pct && s.depth < 1:
		c.Fatalf("sim: PCT needs a depth of at least 1, not %d", s.depth)
	case !s.pct:
		k.depth = defaultDepth
	}
	return k
}

// pick returns the process of pending that steps next: one that the case
// chooses uniformly, or the one of the highest rank under PCT, the earliest
// among equals.
func (k *picker[E, F]) pick(pending []*Process[E, F]) *Process[E, F] {
	if k.uniform {
		return pending[k.c.Draw(prop.Integer(0, len(pending)-1), "process")]
	}
	if !k.started {
		k.start()
	}
	for _, p := range pending {
		if _, seen := k.ranks[p]; !seen {
			k.ranks[p] = rank{priority: k.c.Rand().Uint64()}
		}
	}
	best := slices.MaxFunc(pending, k.compare)
	k.picks++
	for _, at := range k.changes {
		if at == k.picks {
			k.lowered++
			k.ranks[best] = rank{priority: k.ranks[best].priority, level: -k.lowered}
		}
	}
	return best
}

// start takes the change points of the run from the case: for each, whether
// it is present and the pick after which it lowers the picked process.
func (k *picker[E, F]) start() {
	k.started = true
	for range k.depth - 1 {
		if k.c.Draw(prop.Boolean(), "pct.change") {
			k.changes = append(k.changes, k.c.Draw(prop.Integer(1, k.limit), "pct.at"))
		}
	}
}

// compare compares the ranks of the processes a and b.
func (k *picker[E, F]) compare(a, b *Process[E, F]) int {
	ra, rb := k.ranks[a], k.ranks[b]
	return cmp.Or(cmp.Compare(ra.level, rb.level), cmp.Compare(ra.priority, rb.priority))
}
