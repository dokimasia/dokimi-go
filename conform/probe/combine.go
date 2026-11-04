// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"context"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/conform"
)

// EitherOrderAgrees calls call with first and second in both orders, and
// fails when the two values differ, as [assert.Commutative] states. A call
// that fails ends the probe.
func EitherOrderAgrees[S, T, R any](
	h *conform.Handle[S], call func(ctx context.Context, first, second T) (R, error), first, second T,
) {
	h.Helper()
	combine, failed := combining(h, call)
	combine(first, second)
	combine(second, first)
	if *failed {
		return
	}
	h.Engage()
	h.Expect(spec.OrderDependent)
	assert.Commutative(h, combine, first, second, h.Statement())
}

// EitherGroupingAgrees calls call with first, second and third in both
// groupings, and fails when the two values differ, as [assert.Associative]
// states. A call that fails ends the probe.
func EitherGroupingAgrees[S, T any](
	h *conform.Handle[S], call func(ctx context.Context, first, second T) (T, error), first, second, third T,
) {
	h.Helper()
	combine, failed := combining(h, call)
	combine(combine(first, second), third)
	combine(first, combine(second, third))
	if *failed {
		return
	}
	h.Engage()
	h.Expect(spec.GroupingDependent)
	assert.Associative(h, combine, first, second, third, h.Statement())
}

// combining returns a function that calls call with the context of h and
// returns its value, and a flag that reports whether a call of the function
// failed.
func combining[S, T, R any](
	h *conform.Handle[S], call func(ctx context.Context, first, second T) (R, error),
) (combine func(a, b T) R, failed *bool) {
	failed = new(bool)
	return func(a, b T) R {
		v, err := call(h.Context(), a, b)
		*failed = *failed || err != nil
		return v
	}, failed
}
