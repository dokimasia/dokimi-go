// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/spec"
)

// value returns an answer of v and no error.
func value(v int) answer {
	return func(context.Context, *cell) (int, error) { return v, nil }
}

// failing returns an answer of v and err.
func failing(v int, err error) answer {
	return func(context.Context, *cell) (int, error) { return v, err }
}

// byCall answers with the number of the call.
func byCall(_ context.Context, c *cell) (int, error) { return c.calls, nil }

// cancelAware answers with v, and with the zero value and the error of a
// cancelled context.
func cancelAware(v int) answer {
	return func(ctx context.Context, _ *cell) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return v, nil
	}
}

// cancelValue answers with v and no error, and with v and the error of a
// cancelled context.
func cancelValue(v int) answer {
	return func(ctx context.Context, _ *cell) (int, error) { return v, ctx.Err() }
}

// zeroWhenCancelled answers with v and errOther, and with the zero value and
// the error of a cancelled context.
func zeroWhenCancelled(v int) answer {
	return func(ctx context.Context, _ *cell) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return v, errOther
	}
}

// fromCall answers with the answer of the call whose number is the index,
// and with the last answer after them.
func fromCall(answers ...answer) answer {
	return func(ctx context.Context, c *cell) (int, error) {
		return answers[min(c.calls, len(answers))-1](ctx, c)
	}
}

// failsBefore answers with errOther before the call n, and with no error
// from the call n on.
func failsBefore(n int) answer {
	return func(_ context.Context, c *cell) (int, error) {
		if c.calls < n {
			return 0, errOther
		}
		return 0, nil
	}
}

// refusesAfter answers with no error for the first n calls, and with errMiss
// after them.
func refusesAfter(n int) answer {
	return func(_ context.Context, c *cell) (int, error) {
		if c.calls > n {
			return 0, errMiss
		}
		return 0, nil
	}
}

// panicking panics with an index outside a slice without elements.
func panicking(_ context.Context, c *cell) (int, error) {
	none := make([]int, 0, 1)
	return none[c.calls], nil
}

// lookups returns the call of the subject of h with a flag that reports a
// call without an error.
func lookups(h *conform.Handle[*cell]) probe.Lookup[int] {
	call := calls(h)
	return func(ctx context.Context) (int, bool) {
		v, err := call(ctx)
		return v, err == nil
	}
}

// pairs returns the call of the subject of h with its value twice.
func pairs(h *conform.Handle[*cell]) probe.Call[probe.Pair[int, int]] {
	call := calls(h)
	return func(ctx context.Context) (probe.Pair[int, int], error) {
		v, err := call(ctx)
		return probe.Pair[int, int]{First: v, Second: v}, err
	}
}

// pairLookups returns the lookup of the subject of h with its value twice.
func pairLookups(h *conform.Handle[*cell]) probe.Lookup[probe.Pair[int, int]] {
	lookup := lookups(h)
	return func(ctx context.Context) (probe.Pair[int, int], bool) {
		v, ok := lookup(ctx)
		return probe.Pair[int, int]{First: v, Second: v}, ok
	}
}

// pointers returns the call of the subject of h with nil for the value 0
// and a pointer to any other value.
func pointers(h *conform.Handle[*cell]) probe.Call[*int] {
	call := calls(h)
	return func(ctx context.Context) (*int, error) {
		v, err := call(ctx)
		if v == 0 {
			return nil, err
		}
		return &v, err
	}
}

// texts returns the call of the subject of h with a safe text for the value
// 0 and an unsafe text for any other value.
func texts(h *conform.Handle[*cell]) probe.Call[string] {
	call := calls(h)
	return func(ctx context.Context) (string, error) {
		v, err := call(ctx)
		if v == 0 {
			return "&lt;script&gt;", err
		}
		return "<script>", err
	}
}

// edges returns the call of the subject of h with an argument, which the
// call ignores.
func edges(h *conform.Handle[*cell]) func(ctx context.Context, v int) error {
	act := acts(h)
	return func(ctx context.Context, _ int) error { return act(ctx) }
}

// poisonable answers with errOther once induce has run.
func poisonable(_ context.Context, c *cell) (int, error) {
	if c.poisoned {
		return 0, errOther
	}
	return 0, nil
}

// induces returns the call that poisons the subject of h.
func induces(h *conform.Handle[*cell]) probe.Act {
	return func(context.Context) error {
		h.Subject().poisoned = true
		return nil
	}
}

func TestSingle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*cell])
		giveGood  answer
		giveBad   answer
		want      spec.ReasonID
	}{
		{
			name: "Survives", giveProbe: spec.Survives,
			giveApply: func(h *conform.Handle[*cell]) { probe.Survives(h, acts(h)) },
			giveGood:  value(1), giveBad: panicking, want: spec.Panicked,
		},
		{
			name: "UnderCancelled", giveProbe: spec.UnderCancelled,
			giveApply: func(h *conform.Handle[*cell]) { probe.UnderCancelled(h, acts(h)) },
			giveGood:  cancelAware(1), giveBad: value(1), want: spec.IgnoredCancel,
		},
		{
			name: "ZeroBesideError", giveProbe: spec.ZeroBesideError,
			giveApply: func(h *conform.Handle[*cell]) { probe.ZeroBesideError(h, calls(h), 0) },
			giveGood:  cancelAware(1), giveBad: cancelValue(1), want: spec.ValueBesideError,
		},
		{
			name: "ZeroBesideError on a first call that fails", giveProbe: spec.ZeroBesideError,
			giveApply: func(h *conform.Handle[*cell]) { probe.ZeroBesideError(h, calls(h), 0) },
			giveGood:  failing(0, errOther), giveBad: zeroWhenCancelled(1), want: spec.ValueBesideError,
		},
		{
			name: "ZeroPairBesideError", giveProbe: spec.ZeroPairBesideError,
			giveApply: func(h *conform.Handle[*cell]) {
				probe.ZeroPairBesideError(h, pairs(h), probe.Pair[int, int]{})
			},
			giveGood: failing(0, errOther), giveBad: failing(1, errOther), want: spec.ValueBesideError,
		},
		{
			name: "ZeroBesideFalse", giveProbe: spec.ZeroBesideFalse,
			giveApply: func(h *conform.Handle[*cell]) { probe.ZeroBesideFalse(h, lookups(h), 0) },
			giveGood:  failing(0, errMiss), giveBad: failing(1, errMiss), want: spec.ValueBesideFalse,
		},
		{
			name: "ZeroPairBesideFalse", giveProbe: spec.ZeroPairBesideFalse,
			giveApply: func(h *conform.Handle[*cell]) {
				probe.ZeroPairBesideFalse(h, pairLookups(h), probe.Pair[int, int]{})
			},
			giveGood: failing(0, errMiss), giveBad: failing(1, errMiss), want: spec.ValueBesideFalse,
		},
		{
			name: "NilBesideAMiss", giveProbe: spec.NilBesideAMiss,
			giveApply: func(h *conform.Handle[*cell]) { probe.NilBesideAMiss(h, pointers(h)) },
			giveGood:  failing(0, errMiss), giveBad: failing(1, errMiss), want: spec.NonNilMiss,
		},
		{
			name: "ZeroBesideAMiss", giveProbe: spec.ZeroBesideAMiss,
			giveApply: func(h *conform.Handle[*cell]) { probe.ZeroBesideAMiss(h, calls(h), 0) },
			giveGood:  value(0), giveBad: value(1), want: spec.NonZeroMiss,
		},
		{
			name: "RepeatAgrees", giveProbe: spec.RepeatAgrees,
			giveApply: func(h *conform.Handle[*cell]) { probe.RepeatAgrees(h, calls(h)) },
			giveGood:  value(1), giveBad: byCall, want: spec.UnstableRepeat,
		},
		{
			name: "SurvivesRepeat", giveProbe: spec.SurvivesRepeat,
			giveApply: func(h *conform.Handle[*cell]) { probe.SurvivesRepeat(h, acts(h)) },
			giveGood:  value(1), giveBad: fromCall(value(1), panicking), want: spec.PanickedRepeat,
		},
		{
			name: "ReportsSentinel", giveProbe: spec.ReportsSentinel,
			giveApply: func(h *conform.Handle[*cell]) { probe.ReportsSentinel(h, calls(h), errMiss) },
			giveGood:  failing(0, errMiss), giveBad: failing(0, errOther), want: spec.WrongSentinel,
		},
		{
			name: "RepeatIsQuiet", giveProbe: spec.RepeatIsQuiet,
			giveApply: func(h *conform.Handle[*cell]) { probe.RepeatIsQuiet(h, acts(h)) },
			giveGood:  value(1), giveBad: fromCall(value(1), failing(0, errOther)), want: spec.RefusedRepeat,
		},
		{
			name: "NeverDecreases", giveProbe: spec.NeverDecreases,
			giveApply: func(h *conform.Handle[*cell]) { probe.NeverDecreases(h, calls(h)) },
			giveGood:  value(1), giveBad: fromCall(value(2), value(1)), want: spec.Regressed,
		},
		{
			name: "RepeatAscends", giveProbe: spec.RepeatAscends,
			giveApply: func(h *conform.Handle[*cell]) { probe.RepeatAscends(h, calls(h)) },
			giveGood:  byCall, giveBad: value(1), want: spec.NotAscending,
		},
		{
			name: "EscapesTheUnsafe", giveProbe: spec.EscapesTheUnsafe,
			giveApply: func(h *conform.Handle[*cell]) { probe.EscapesTheUnsafe(h, texts(h), "<script>") },
			giveGood:  value(0), giveBad: value(1), want: spec.UnescapedOutput,
		},
		{
			name: "AcceptsTheEdge", giveProbe: spec.AcceptsTheEdge,
			giveApply: func(h *conform.Handle[*cell]) { probe.AcceptsTheEdge(h, edges(h), 0) },
			giveGood:  value(0), giveBad: failing(0, errOther), want: spec.RefusedTheEdge,
		},
		{
			name: "RemainsPoisoned", giveProbe: spec.RemainsPoisoned,
			giveApply: func(h *conform.Handle[*cell]) { probe.RemainsPoisoned(h, acts(h), induces(h)) },
			giveGood:  poisonable, giveBad: fromCall(failing(0, errOther), value(0)), want: spec.HealthyAfterInduce,
		},
		{
			name: "SucceedsWithin", giveProbe: spec.SucceedsWithin,
			giveApply: func(h *conform.Handle[*cell]) { probe.SucceedsWithin(h, acts(h), 3) },
			giveGood:  fromCall(value(0), failing(0, errOther)), giveBad: failsBefore(4), want: spec.NeverConverged,
		},
		{
			name: "SucceedsWithin on the last attempt", giveProbe: spec.SucceedsWithin,
			giveApply: func(h *conform.Handle[*cell]) { probe.SucceedsWithin(h, acts(h), 3) },
			giveGood:  failsBefore(3), giveBad: failsBefore(4), want: spec.NeverConverged,
		},
		{
			name: "RefusesTheSecondWrite", giveProbe: spec.RefusesTheSecondWrite,
			giveApply: func(h *conform.Handle[*cell]) { probe.RefusesTheSecondWrite(h, acts(h), errMiss) },
			giveGood:  fromCall(value(0), failing(0, errMiss)), giveBad: value(0), want: spec.UnguardedWrite,
		},
		{
			name: "RefusesPastTheBurst", giveProbe: spec.RefusesPastTheBurst,
			giveApply: func(h *conform.Handle[*cell]) { probe.RefusesPastTheBurst(h, acts(h), 2, errMiss) },
			giveGood:  refusesAfter(2), giveBad: value(0), want: spec.PastTheBurst,
		},
		{
			name: "RefusesEarly", giveProbe: spec.RefusesEarly,
			giveApply: func(h *conform.Handle[*cell]) { probe.RefusesEarly(h, acts(h), errMiss) },
			giveGood:  failing(0, errMiss), giveBad: value(0), want: spec.AcceptedEarly,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("passes a correct subject and fails a planted defect with its reason", func(t *testing.T) {
				t.Parallel()

				proves(t, tt.giveProbe, tt.want, tt.giveApply, answering(tt.giveGood), answering(tt.giveBad))
			})
		})
	}

	ends := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*cell])
		give      answer
	}{
		{
			name:      "ZeroBesideError passes a call that never fails",
			giveProbe: spec.ZeroBesideError, give: value(1),
			giveApply: func(h *conform.Handle[*cell]) { probe.ZeroBesideError(h, calls(h), 0) },
		},
		{
			name:      "ZeroBesideFalse passes a call that returns a true flag",
			giveProbe: spec.ZeroBesideFalse, give: value(1),
			giveApply: func(h *conform.Handle[*cell]) { probe.ZeroBesideFalse(h, lookups(h), 0) },
		},
		{
			name:      "RepeatAgrees passes a first call that fails",
			giveProbe: spec.RepeatAgrees, give: fromCall(failing(1, errOther), value(2)),
			giveApply: func(h *conform.Handle[*cell]) { probe.RepeatAgrees(h, calls(h)) },
		},
		{
			name:      "SurvivesRepeat passes a first call that panics",
			giveProbe: spec.SurvivesRepeat, give: panicking,
			giveApply: func(h *conform.Handle[*cell]) { probe.SurvivesRepeat(h, acts(h)) },
		},
		{
			name:      "RepeatIsQuiet passes a first call that fails",
			giveProbe: spec.RepeatIsQuiet, give: failing(0, errOther),
			giveApply: func(h *conform.Handle[*cell]) { probe.RepeatIsQuiet(h, acts(h)) },
		},
		{
			name:      "NeverDecreases passes a first call that fails",
			giveProbe: spec.NeverDecreases, give: fromCall(failing(2, errOther), value(1)),
			giveApply: func(h *conform.Handle[*cell]) { probe.NeverDecreases(h, calls(h)) },
		},
		{
			name:      "EscapesTheUnsafe passes a call that fails",
			giveProbe: spec.EscapesTheUnsafe, give: failing(1, errOther),
			giveApply: func(h *conform.Handle[*cell]) { probe.EscapesTheUnsafe(h, texts(h), "<script>") },
		},
		{
			name:      "RefusesTheSecondWrite passes a first call that fails",
			giveProbe: spec.RefusesTheSecondWrite, give: failing(0, errOther),
			giveApply: func(h *conform.Handle[*cell]) { probe.RefusesTheSecondWrite(h, acts(h), errMiss) },
		},
		{
			name:      "RefusesPastTheBurst passes a call inside the burst that fails",
			giveProbe: spec.RefusesPastTheBurst, give: fromCall(value(0), failing(0, errOther)),
			giveApply: func(h *conform.Handle[*cell]) { probe.RefusesPastTheBurst(h, acts(h), 2, errMiss) },
		},
	}
	for _, tt := range ends {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			holds(t, tt.giveProbe, tt.giveApply, answering(tt.give))
		})
	}

	t.Run("NeverDecreases fails the guard for a second call that fails", func(t *testing.T) {
		t.Parallel()

		r := assert.NewRecorder()
		conform.Check[*cell]{
			ID: "Cell.Call/claim", Statement: "the probe holds", Probe: spec.NeverDecreases,
			Apply: func(h *conform.Handle[*cell]) { probe.NeverDecreases(h, calls(h)) },
		}.Run(r, answering(fromCall(value(1), failing(2, errOther))))
		failures := r.Failures()
		assert.Length(t, failures, 1, "the guard fails once")
		assert.Equal(t, failures[0].Assertion, "err-absent", "the guard asserts the success of the second call")
	})
}
