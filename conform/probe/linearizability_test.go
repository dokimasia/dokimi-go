// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/spec"
)

// tally is a register of one integer under each key, the interface of the
// references of the specs of the linearizability check. A *guarded, a
// mapTally and a structTally implement it.
type tally interface {
	call(op string, args []any) (any, error)
}

// mapTally is a tally that is a map, and structTally a tally that is a struct
// value with a map. A Put of either stores the value under the key. A Get
// returns the value of the key, and fails with errMiss for a key without a
// value.
type (
	mapTally    map[string]int
	structTally struct{ values map[string]int }
)

func (m mapTally) call(op string, args []any) (any, error) {
	key := args[0].(string)
	if op == "Put" {
		m[key] = args[1].(int)
		return nil, nil
	}
	v, found := m[key]
	if !found {
		return 0, errMiss
	}
	return v, nil
}

func (s structTally) call(op string, args []any) (any, error) {
	return mapTally(s.values).call(op, args)
}

// tallyReference applies an operation to the tally r by its name.
func tallyReference[S tally](r S) func(ctx context.Context, op string, args []any) conform.Output {
	return func(_ context.Context, op string, args []any) conform.Output {
		v, err := r.call(op, args)
		if v == nil {
			return conform.OutputOf(err)
		}
		return conform.OutputOf(err, v)
	}
}

// overlapping returns the actions of one action, which records once a history
// for each key: n Puts of the values 1 to n under the key, each on a client of
// its own and all at once, and then a Get of the key on client 0 that returns
// got.
func overlapping[S any](n, got int, keys ...string) probe.Actions[S] {
	return func(*prop.Case, func() S) []stateful.Action[struct{}] {
		done := false
		return []stateful.Action[struct{}]{{
			Name: "Script",
			Run: func(c *prop.Case, _ int, _ any) {
				if done {
					return
				}
				done = true
				for _, key := range keys {
					puts := make([]history.Call, n)
					for i := range puts {
						puts[i] = c.History().Invoke(i, "Put", []any{key, i + 1}, key)
					}
					for _, put := range puts {
						conform.Complete(c.Context(), put, nil)
					}
					conform.Complete(c.Context(), c.History().Invoke(0, "Get", []any{key}, key), nil, got)
				}
			},
		}}
	}
}

// frontier runs the check of LinearizableHistory over a history of n Puts of
// the values 1 to n under the key a and a Get of the key that returns n + 1,
// against the references of door through reference. It returns the states of
// the frontier of the violation of the smallest failing case.
func frontier[S any](
	t *testing.T, n int, door func(*conform.Handle[S]) S,
	reference func(r S) func(ctx context.Context, op string, args []any) conform.Output,
) any {
	t.Helper()
	r := assert.NewRecorder()
	conform.Check[S]{
		ID: "Tally/linearizable", Statement: "the probe holds", Probe: spec.LinearizableHistory,
		Property: func(h *conform.Handle[S], c *prop.Case) {
			probe.LinearizableHistory(h, c, overlapping[S](n, n+1, "a"), reference, 2)
		},
	}.Run(r, door, conform.Reference(door))
	failures := r.Failures()
	assert.NotEmpty(t, failures, "the check fails")
	inner, _ := failures[0].CaseFailure()
	return inner.Detail["states"]
}

func TestLinearizability(t *testing.T) {
	t.Parallel()

	t.Run("LinearizableHistory", func(t *testing.T) {
		t.Parallel()

		t.Run("merges the orders of the calls that leave equal references", func(t *testing.T) {
			t.Parallel()

			conform.Check[*guarded]{
				ID: "Guarded/linearizable", Statement: "the probe holds", Probe: spec.LinearizableHistory,
				Property: func(h *conform.Handle[*guarded], c *prop.Case) {
					probe.LinearizableHistory(h, c, overlapping[*guarded](8, 1, "a"), guardedReference, 2,
						history.Budget(3000))
				},
			}.Run(t, newGuarded, conform.Reference(newGuarded))
		})

		t.Run("searches the partitions of the history on several workers", func(t *testing.T) {
			t.Parallel()

			conform.Check[*guarded]{
				ID: "Guarded/linearizable", Statement: "the probe holds", Probe: spec.LinearizableHistory,
				Property: func(h *conform.Handle[*guarded], c *prop.Case) {
					probe.LinearizableHistory(h, c, overlapping[*guarded](6, 1, "a", "b"), guardedReference, 2,
						history.Budget(1000), history.Workers(2))
				},
			}.Run(t, newGuarded, conform.Reference(newGuarded))
		})

		t.Run("states the reference of the frontier of a violation", func(t *testing.T) {
			t.Parallel()

			got := frontier(t, 1, newGuarded, guardedReference)

			expect.Equal(t, got, any([]*guarded{{values: map[string]int{"a": 1}}}),
				"the state of the frontier is the reference after the Put")
		})

		t.Run("states a fresh reference as the frontier of a violation before any call", func(t *testing.T) {
			t.Parallel()

			got := frontier(t, 0, newGuarded, guardedReference)

			expect.Equal(t, got, any([]*guarded{{values: map[string]int{}}}),
				"the state of the frontier is the reference without a call")
		})

		t.Run("states a reference that is no pointer as a pointer to a copy of it", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name     string
				giveDoor func(*conform.Handle[tally]) tally
				want     []tally
			}{
				{
					name:     "states a map as a pointer to the map",
					giveDoor: func(*conform.Handle[tally]) tally { return mapTally{} },
					want:     []tally{&mapTally{"a": 1}},
				},
				{
					name:     "states a struct value as a pointer to the struct",
					giveDoor: func(*conform.Handle[tally]) tally { return structTally{values: map[string]int{}} },
					want:     []tally{&structTally{values: map[string]int{"a": 1}}},
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					got := frontier(t, 1, tt.giveDoor, tallyReference[tally])

					expect.Equal(t, got, any(tt.want),
						"the state of the frontier points at the reference after the Put")
				})
			}
		})

		t.Run("states the calls of the frontier for a subject of a struct type", func(t *testing.T) {
			t.Parallel()

			got := frontier(t, 1, func(*conform.Handle[structTally]) structTally {
				return structTally{values: map[string]int{}}
			}, tallyReference[structTally])

			expect.Equal(t, got, any([][]history.Operation{{{Name: "Put", Args: []any{"a", 1}}}}),
				"the state of the frontier lists the Put, as history.SpecFrom states it")
		})
	})
}
