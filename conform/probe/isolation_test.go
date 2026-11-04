// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
)

// lists keeps a list of numbers under each key, which transactions append to.
// A transaction reads the lists that committed before it began, with its own
// appends after them. Its commit fails where a key that it read or appended
// to committed after its begin, and applies its appends otherwise, so the
// transactions are serializable. A dirty store applies each append at once
// and takes it back on a rollback, so a transaction reads the appends of the
// transactions that are still open. A late store applies the appends of a
// commit whatever committed since its begin. A refusing store fails every
// commit. A dirty and a late store yield to the scheduler of the case after
// each call.
type lists struct {
	committed map[string][]int
	changed   map[string]int64
	open      map[int64]*listTx
	yield     func()
	next      int64
	commits   int64
	mu        sync.Mutex
	dirty     bool
	late      bool
	refuses   bool
}

// listTx is one open transaction of lists: the lists at its begin, the
// number of commits before it, the keys that it read and its appends.
type listTx struct {
	snapshot map[string][]int
	read     map[string]bool
	appends  [][2]any
	start    int64
}

// newLists returns an empty store of lists.
func newLists(*conform.Handle[*lists]) *lists {
	return &lists{committed: map[string][]int{}, changed: map[string]int64{}, open: map[int64]*listTx{}}
}

// dirtyLists returns an empty dirty store of lists.
func dirtyLists(h *conform.Handle[*lists]) *lists {
	l := newLists(h)
	l.dirty, l.yield = true, h.Yield
	return l
}

// lateLists returns an empty late store of lists.
func lateLists(h *conform.Handle[*lists]) *lists {
	l := newLists(h)
	l.late, l.yield = true, h.Yield
	return l
}

// refusingLists returns an empty store of lists that fails each commit.
func refusingLists(h *conform.Handle[*lists]) *lists {
	l := newLists(h)
	l.refuses = true
	return l
}

// pause yields to the scheduler of a dirty or a late store.
func (l *lists) pause() {
	if l.yield != nil {
		l.yield()
	}
}

// begin opens a transaction and returns its number.
func (l *lists) begin() int64 {
	defer l.pause()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	snapshot := make(map[string][]int, len(l.committed))
	for k, v := range l.committed {
		snapshot[k] = slices.Clone(v)
	}
	l.open[l.next] = &listTx{snapshot: snapshot, read: map[string]bool{}, start: l.commits}
	return l.next
}

// add appends v to the list of key in the transaction tx.
func (l *lists) add(tx int64, key string, v int) {
	defer l.pause()
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.open[tx]
	t.appends = append(t.appends, [2]any{key, v})
	if l.dirty {
		l.committed[key] = append(l.committed[key], v)
	}
}

// read returns the list of key in the transaction tx.
func (l *lists) read(tx int64, key string) []int {
	defer l.pause()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dirty {
		return slices.Clone(l.committed[key])
	}
	t := l.open[tx]
	t.read[key] = true
	out := slices.Clone(t.snapshot[key])
	for _, a := range t.appends {
		if a[0] == key {
			out = append(out, a[1].(int))
		}
	}
	return out
}

// settle commits or rolls back the transaction tx. It returns errOther for a
// commit that fails, which rolls the transaction back: a commit of a refusing
// store, and a commit that conflicts with a commit after the begin of tx.
func (l *lists) settle(tx int64, commit bool) error {
	defer l.pause()
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.open[tx]
	delete(l.open, tx)
	refused := l.refuses || commit && !l.dirty && !l.late && l.conflicts(t)
	commit = commit && !refused
	for _, a := range t.appends {
		key, v := a[0].(string), a[1].(int)
		switch {
		case l.dirty && !commit:
			at := slices.Index(l.committed[key], v)
			l.committed[key] = slices.Delete(l.committed[key], at, at+1)
		case !l.dirty && commit:
			l.committed[key] = append(l.committed[key], v)
		}
	}
	if commit {
		l.commits++
		for _, a := range t.appends {
			l.changed[a[0].(string)] = l.commits
		}
	}
	if refused {
		return errOther
	}
	return nil
}

// conflicts reports whether a key that t read or appended to committed after
// the begin of t.
func (l *lists) conflicts(t *listTx) bool {
	for key := range t.read {
		if l.changed[key] > t.start {
			return true
		}
	}
	return slices.ContainsFunc(t.appends, func(a [2]any) bool { return l.changed[a[0].(string)] > t.start })
}

// listActions are the actions of a store of lists: the action txn, and an
// action Never that fails the case when it runs. A transaction takes its keys
// from a pool of one or two keys of the case, and each append appends the
// next value of a counter of the case.
func listActions(c *prop.Case, subject func() *lists) []stateful.Action[struct{}] {
	pool := c.Draw(prop.List(prop.SampledFrom("a", "b"), prop.Unique(), prop.MinSize(1), prop.MaxSize(2)), "keys")
	var counter atomic.Int64
	return []stateful.Action[struct{}]{
		{
			Name: "Never",
			Run:  func(c *prop.Case, _ int, _ any) { c.Fatalf("the probe ran an action that is no transaction") },
		},
		{
			Name:    "txn",
			Enabled: func(struct{}) bool { return false },
			Input: func(c *prop.Case, _ struct{}) any {
				ops := make([]any, c.Draw(prop.Integer(1, 4), "txn.ops"))
				for i := range ops {
					key := c.Draw(prop.SampledFrom(pool...), "txn.key")
					if c.Draw(prop.Boolean(), "txn.read") {
						ops[i] = []any{"read", key, nil}
						continue
					}
					ops[i] = []any{"append", key, int(counter.Add(1))}
				}
				return []any{ops, c.Draw(prop.Integer(0, 9), "txn.commit") > 0}
			},
			Run: func(c *prop.Case, client int, input any) {
				in, _ := input.([]any)
				ops, _ := in[0].([]any)
				commit, _ := in[1].(bool)
				call := c.History().Invoke(client, "txn", ops)
				s := subject()
				tx := s.begin()
				out := make([]any, len(ops))
				for i, op := range ops {
					m, _ := op.([]any)
					key, _ := m[1].(string)
					if m[0] == "read" {
						out[i] = []any{"read", key, s.read(tx, key)}
						continue
					}
					v, _ := m[2].(int)
					s.add(tx, key, v)
					out[i] = m
				}
				if err := s.settle(tx, commit); err != nil || !commit {
					call.Fail(conform.ErrGenerated)
					return
				}
				call.OK(out)
			},
		},
	}
}

func TestIsolation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		giveProbe spec.ProbeID
		giveRun   func(h *conform.Handle[*lists], c *prop.Case, actions probe.Actions[*lists], clients int)
		giveBad   func(*conform.Handle[*lists]) *lists
		want      spec.ReasonID
	}{
		{
			name: "SerializableHistory", giveProbe: spec.SerializableHistory,
			giveRun: probe.SerializableHistory[*lists], giveBad: dirtyLists, want: spec.NotSerializable,
		},
		{
			name: "SnapshotHistory", giveProbe: spec.SnapshotHistory,
			giveRun: probe.SnapshotHistory[*lists], giveBad: lateLists, want: spec.NotSnapshotIsolated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			apply := func(h *conform.Handle[*lists], c *prop.Case) { tt.giveRun(h, c, listActions, 2) }

			t.Run("passes a store that runs each transaction alone and fails a planted defect with its reason",
				func(t *testing.T) {
					t.Parallel()

					conform.Check[*lists]{
						ID: "Lists/claim", Statement: "the probe holds", Claim: "claim", Probe: tt.giveProbe,
						Reason: tt.want, Property: apply, Defect: tt.giveBad,
					}.Run(t, newLists)
				})

			t.Run("runs the action txn as the first of the actions", func(t *testing.T) {
				t.Parallel()

				alone := func(h *conform.Handle[*lists], c *prop.Case) {
					tt.giveRun(h, c, func(c *prop.Case, subject func() *lists) []stateful.Action[struct{}] {
						return listActions(c, subject)[1:]
					}, 2)
				}
				conform.Check[*lists]{
					ID: "Lists/claim", Statement: "the probe holds", Claim: "claim", Probe: tt.giveProbe,
					Reason: tt.want, Property: alone, Defect: tt.giveBad,
				}.Run(t, newLists)
			})

			t.Run("is vacuous on a store whose transactions never commit", func(t *testing.T) {
				t.Parallel()

				r := assert.NewRecorder()
				conform.Check[*lists]{
					ID: "Lists/claim", Statement: "the probe holds", Probe: tt.giveProbe, Property: apply,
				}.Run(r, refusingLists)
				entry, _ := spec.ProbeOf(tt.giveProbe)
				condition := strings.ToLower(entry.Vacuous[:1]) + strings.TrimSuffix(entry.Vacuous[1:], ".")
				assert.Equal(t, r.Messages(), []string{"Lists/claim never engaged: " + condition + ". Pass a subject " +
					"that engages the check, or excuse the check with the directive excuse or conform.Excuse and a " +
					"reason."}, "the check fails for the vacuous condition of its probe")
			})

			t.Run("is vacuous on actions without the action txn", func(t *testing.T) {
				t.Parallel()

				r := assert.NewRecorder()
				never := func(h *conform.Handle[*lists], c *prop.Case) {
					tt.giveRun(h, c, func(c *prop.Case, subject func() *lists) []stateful.Action[struct{}] {
						return listActions(c, subject)[:1]
					}, 2)
				}
				conform.Check[*lists]{
					ID: "Lists/claim", Statement: "the probe holds", Probe: tt.giveProbe, Property: never,
				}.Run(r, newLists)
				assert.Length(t, r.Messages(), 1, "the check never engages and runs no action")
			})
		})
	}
}
