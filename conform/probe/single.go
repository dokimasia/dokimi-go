// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"cmp"
	"context"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// Survives calls call, and fails when the call panics.
func Survives[S any](h *conform.Handle[S], call Act) {
	h.Helper()
	h.Engage()
	h.Expect(spec.Panicked)
	assert.NotPanics(h, func() { _ = call(h.Context()) }, h.Statement())
}

// UnderCancelled calls call with a cancelled context, and fails when the call
// returns no error.
func UnderCancelled[S any](h *conform.Handle[S], call Act) {
	h.Helper()
	err := call(cancelled(h.Context()))
	h.Engage()
	h.Expect(spec.IgnoredCancel)
	assert.HasError(h, err, h.Statement())
}

// cancelled returns a context of ctx that is already cancelled.
func cancelled(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	return ctx
}

// ZeroBesideError calls call, and calls it again with a cancelled context
// where the first call succeeds. It fails when a call that returns an error
// returns a value other than zero.
func ZeroBesideError[S, V any](h *conform.Handle[S], call Call[V], zero V) {
	h.Helper()
	v, err := call(h.Context())
	if err == nil {
		v, err = call(cancelled(h.Context()))
	}
	if err == nil {
		return
	}
	h.Engage()
	h.Expect(spec.ValueBesideError)
	assert.Equal(h, v, zero, h.Statement())
}

// ZeroPairBesideError is [ZeroBesideError] for a callable with two values.
func ZeroPairBesideError[S, A, B any](h *conform.Handle[S], call Call[Pair[A, B]], zero Pair[A, B]) {
	h.Helper()
	ZeroBesideError(h, call, zero)
}

// ZeroBesideFalse calls call, and fails when it returns a false flag beside a
// value other than zero.
func ZeroBesideFalse[S, V any](h *conform.Handle[S], call Lookup[V], zero V) {
	h.Helper()
	v, ok := call(h.Context())
	if ok {
		return
	}
	h.Engage()
	h.Expect(spec.ValueBesideFalse)
	assert.Equal(h, v, zero, h.Statement())
}

// ZeroPairBesideFalse is [ZeroBesideFalse] for a callable with two values.
func ZeroPairBesideFalse[S, A, B any](h *conform.Handle[S], call Lookup[Pair[A, B]], zero Pair[A, B]) {
	h.Helper()
	ZeroBesideFalse(h, call, zero)
}

// NilBesideAMiss calls call on the fresh subject, and fails when its value is
// not nil.
func NilBesideAMiss[S, V any](h *conform.Handle[S], call Call[V]) {
	h.Helper()
	v, _ := call(h.Context())
	h.Engage()
	h.Expect(spec.NonNilMiss)
	assert.Nil(h, v, h.Statement())
}

// ZeroBesideAMiss calls call on the fresh subject, and fails when its value
// is not zero.
func ZeroBesideAMiss[S, V any](h *conform.Handle[S], call Call[V], zero V) {
	h.Helper()
	v, _ := call(h.Context())
	h.Engage()
	h.Expect(spec.NonZeroMiss)
	assert.Equal(h, v, zero, h.Statement())
}

// RepeatAgrees calls call twice, and fails when the second value differs from
// the first. A first call that fails ends the probe.
func RepeatAgrees[S, V any](h *conform.Handle[S], call Call[V]) {
	h.Helper()
	first, err := call(h.Context())
	if err != nil {
		return
	}
	second, _ := call(h.Context())
	h.Engage()
	h.Expect(spec.UnstableRepeat)
	assert.Equal(h, second, first, h.Statement())
}

// SurvivesRepeat calls call twice, and fails when the second call panics. A
// first call that panics ends the probe.
func SurvivesRepeat[S any](h *conform.Handle[S], call Act) {
	h.Helper()
	if !returns(h.Context(), call) {
		return
	}
	h.Engage()
	h.Expect(spec.PanickedRepeat)
	assert.NotPanics(h, func() { _ = call(h.Context()) }, h.Statement())
}

// returns reports whether call returns without a panic.
func returns(ctx context.Context, call Act) (returned bool) {
	defer func() { _ = recover() }()
	_ = call(ctx)
	return true
}

// ReportsSentinel calls call on the fresh subject, and fails unless its error
// matches sentinel.
func ReportsSentinel[S, V any](h *conform.Handle[S], call Call[V], sentinel error) {
	h.Helper()
	_, err := call(h.Context())
	h.Engage()
	h.Expect(spec.WrongSentinel)
	assert.ErrorIs(h, err, sentinel, h.Statement())
}

// RepeatIsQuiet calls call twice, and fails when the second call fails after
// a first call that succeeded.
func RepeatIsQuiet[S any](h *conform.Handle[S], call Act) {
	h.Helper()
	if call(h.Context()) != nil {
		return
	}
	err := call(h.Context())
	h.Engage()
	h.Expect(spec.RefusedRepeat)
	assert.NoError(h, err, h.Statement())
}

// NeverDecreases calls call twice, and fails when the second value is below
// the first. A first call that fails ends the probe.
func NeverDecreases[S any, O cmp.Ordered](h *conform.Handle[S], call Call[O]) {
	h.Helper()
	ordered(h, call, spec.Regressed, func(first, second O) bool { return second >= first })
}

// RepeatAscends calls call twice, and fails unless the second value is above
// the first. A first call that fails ends the probe.
func RepeatAscends[S any, O cmp.Ordered](h *conform.Handle[S], call Call[O]) {
	h.Helper()
	ordered(h, call, spec.NotAscending, func(first, second O) bool { return second > first })
}

// ordered calls call twice, and fails with reason unless the two values are
// in the order of in. A first call that fails ends the probe, and the guard
// of the probe fails a second call that fails.
func ordered[S any, O cmp.Ordered](h *conform.Handle[S], call Call[O], reason spec.ReasonID, in func(a, b O) bool) {
	h.Helper()
	first, err := call(h.Context())
	if err != nil {
		return
	}
	h.Engage()
	second, err := call(h.Context())
	assert.NoError(h, err, h.Statement())
	h.Expect(reason)
	assert.Pairwise(h, []O{first, second}, in, h.Statement())
}

// EscapesTheUnsafe calls call, and fails when its output contains unsafe. A
// call that fails ends the probe.
func EscapesTheUnsafe[S any](h *conform.Handle[S], call Call[string], unsafe string) {
	h.Helper()
	out, err := call(h.Context())
	if err != nil {
		return
	}
	h.Engage()
	h.Expect(spec.UnescapedOutput)
	assert.NotContains(h, out, unsafe, h.Statement())
}

// AcceptsTheEdge calls call with edge, and fails when the call fails.
func AcceptsTheEdge[S, V any](h *conform.Handle[S], call func(ctx context.Context, v V) error, edge V) {
	h.Helper()
	err := call(h.Context(), edge)
	h.Engage()
	h.Expect(spec.RefusedTheEdge)
	assert.NoError(h, err, h.Statement())
}

// RemainsPoisoned calls induce, then call twice, and fails when a call
// succeeds.
func RemainsPoisoned[S any](h *conform.Handle[S], call, induce Act) {
	h.Helper()
	_ = induce(h.Context())
	h.Engage()
	for range 2 {
		err := call(h.Context())
		h.Expect(spec.HealthyAfterInduce)
		assert.HasError(h, err, h.Statement())
	}
}

// SucceedsWithin calls call up to attempts times, and fails when no call
// succeeds.
func SucceedsWithin[S any](h *conform.Handle[S], call Act, attempts int) {
	h.Helper()
	h.Engage()
	converges(h, call, attempts)
}

// converges calls call up to attempts times, and fails when no call
// succeeds.
func converges[S any](h *conform.Handle[S], call Act, attempts int) {
	h.Helper()
	var err error
	for range attempts {
		if err = call(h.Context()); err == nil {
			return
		}
	}
	h.Expect(spec.NeverConverged)
	assert.NoError(h, err, h.Statement())
}

// RefusesTheSecondWrite calls call twice, and fails unless the error of the
// second call matches sentinel. A first call that fails ends the probe.
func RefusesTheSecondWrite[S any](h *conform.Handle[S], call Act, sentinel error) {
	h.Helper()
	if call(h.Context()) != nil {
		return
	}
	err := call(h.Context())
	h.Engage()
	h.Expect(spec.UnguardedWrite)
	assert.ErrorIs(h, err, sentinel, h.Statement())
}

// RefusesPastTheBurst calls call burst times, then once more, and fails
// unless the error of the last call matches sentinel. A call inside the
// burst that fails ends the probe.
func RefusesPastTheBurst[S any](h *conform.Handle[S], call Act, burst int, sentinel error) {
	h.Helper()
	for range burst {
		if call(h.Context()) != nil {
			return
		}
	}
	err := call(h.Context())
	h.Engage()
	h.Expect(spec.PastTheBurst)
	assert.ErrorIs(h, err, sentinel, h.Statement())
}

// RefusesEarly calls call on the fresh subject, and fails unless its error
// matches sentinel.
func RefusesEarly[S any](h *conform.Handle[S], call Act, sentinel error) {
	h.Helper()
	err := call(h.Context())
	h.Engage()
	h.Expect(spec.AcceptedEarly)
	assert.ErrorIs(h, err, sentinel, h.Statement())
}
