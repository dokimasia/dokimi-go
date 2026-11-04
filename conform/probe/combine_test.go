// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"testing"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/spec"
)

// mixer is the subject of the specs of the probes that combine two values.
// mix combines two values or fails.
type mixer struct {
	mix func(a, b int) (int, error)
}

// mixing returns a factory of mixers that combine with mix.
func mixing(mix func(a, b int) (int, error)) func(*conform.Handle[*mixer]) *mixer {
	return func(*conform.Handle[*mixer]) *mixer { return &mixer{mix: mix} }
}

// combines returns the call that combines two values with the subject of h.
func combines(h *conform.Handle[*mixer]) func(ctx context.Context, a, b int) (int, error) {
	return func(_ context.Context, a, b int) (int, error) { return h.Subject().mix(a, b) }
}

// The combinations of the specs: a sum, which is commutative and associative,
// the first value, which is associative alone, twice the sum, which is
// commutative alone, and sums that fail for some values. With the inputs 1, 2
// and 3, descends fails at the first call, unequal at the second call of the
// first grouping, and apart in the other grouping alone.
var (
	sum      = func(a, b int) (int, error) { return a + b, nil }
	leftmost = func(a, _ int) (int, error) { return a, nil }
	twice    = func(a, b int) (int, error) { return 2 * (a + b), nil }
	ascends  = func(a, b int) (int, error) {
		if a > b {
			return 0, errOther
		}
		return a + b, nil
	}
	descends = func(a, b int) (int, error) {
		if a < b {
			return 0, errOther
		}
		return a + b, nil
	}
	unequal = func(a, b int) (int, error) {
		if a == b {
			return 0, errOther
		}
		return a + b, nil
	}
	apart = func(a, b int) (int, error) {
		if b-a > 3 {
			return 0, errOther
		}
		return a + b, nil
	}
)

// provesCombining applies a derived check of the probe p to a subject of good,
// and falsifies it on a subject of bad, which fails it with reason.
func provesCombining(
	t *testing.T, p spec.ProbeID, reason spec.ReasonID, apply func(h *conform.Handle[*mixer]),
	good, bad func(a, b int) (int, error),
) {
	t.Helper()
	conform.Check[*mixer]{
		ID: "Mixer.Mix/claim", Statement: "the probe holds", Claim: "claim", Probe: p, Reason: reason,
		Apply: apply, Defect: mixing(bad),
	}.Run(t, mixing(good))
}

func TestCombine(t *testing.T) {
	t.Parallel()

	t.Run("EitherOrderAgrees", func(t *testing.T) {
		t.Parallel()

		apply := func(h *conform.Handle[*mixer]) { probe.EitherOrderAgrees(h, combines(h), 1, 2) }

		t.Run("proves the claim of a call that ignores the order of its inputs", func(t *testing.T) {
			t.Parallel()

			provesCombining(t, spec.EitherOrderAgrees, spec.OrderDependent, apply, sum, leftmost)
		})

		tests := []struct {
			name string
			give func(a, b int) (int, error)
		}{
			{name: "ends the probe at a call in the order of the inputs that fails", give: descends},
			{name: "ends the probe at a call in the other order that fails", give: ascends},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				vacuous(t, spec.EitherOrderAgrees, apply, mixing(tt.give), nil)
			})
		}
	})

	t.Run("EitherGroupingAgrees", func(t *testing.T) {
		t.Parallel()

		apply := func(h *conform.Handle[*mixer]) { probe.EitherGroupingAgrees(h, combines(h), 1, 2, 3) }

		t.Run("proves the claim of a call that ignores the grouping of its inputs", func(t *testing.T) {
			t.Parallel()

			provesCombining(t, spec.EitherGroupingAgrees, spec.GroupingDependent, apply, sum, twice)
		})

		tests := []struct {
			name string
			give func(a, b int) (int, error)
		}{
			{name: "ends the probe at a first call that fails", give: descends},
			{name: "ends the probe at a call inside the run that fails", give: unequal},
			{name: "ends the probe at a call of the other grouping that fails", give: apart},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				vacuous(t, spec.EitherGroupingAgrees, apply, mixing(tt.give), nil)
			})
		}
	})
}
