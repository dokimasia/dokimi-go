// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"context"
	"errors"
	"sync/atomic"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// errBody is the error of the body of a scope that [RollsBackOnFailure] runs.
var errBody = errors.New("probe: the body of the scope fails")

// WriteThenRead calls write, which returns the value that it wrote, then
// read, and fails when read does not return that value. A write that fails
// ends the probe.
func WriteThenRead[S, V any](h *conform.Handle[S], read, write Call[V]) {
	h.Helper()
	written, err := write(h.Context())
	if err != nil {
		return
	}
	got, err := read(h.Context())
	h.Expect(spec.UnobservedWrite)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, written, h.Statement())
}

// PinnedAcrossWrite calls read, write and read on one snapshot, and fails
// when the two reads differ. A first read or a write that fails ends the
// probe.
func PinnedAcrossWrite[S, V any](h *conform.Handle[S], read Call[V], write Act) {
	h.Helper()
	first, err := read(h.Context())
	if err != nil || write(h.Context()) != nil {
		return
	}
	second, err := read(h.Context())
	h.Expect(spec.UnpinnedRead)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, second, first, h.Statement())
}

// SecondWriteReplaces calls call twice, each time with another sample and
// returning the value that it wrote, then read, and fails when read does not
// return the second value. A write that fails ends the probe.
func SecondWriteReplaces[S, V any](h *conform.Handle[S], call, read Call[V]) {
	h.Helper()
	if _, err := call(h.Context()); err != nil {
		return
	}
	second, err := call(h.Context())
	if err != nil {
		return
	}
	got, err := read(h.Context())
	h.Expect(spec.UnreplacedWrite)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, second, h.Statement())
}

// KeepsPartitionsApart calls write twice, in the partition of the read and in
// another, then read, and fails when read does not return the first value. A
// write that fails ends the probe.
func KeepsPartitionsApart[S, V any](h *conform.Handle[S], write, read Call[V]) {
	h.Helper()
	own, err := write(h.Context())
	if err != nil {
		return
	}
	if _, err = write(h.Context()); err != nil {
		return
	}
	got, err := read(h.Context())
	h.Expect(spec.CrossedPartitions)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, own, h.Statement())
}

// RoundTrips calls call, which returns its input and its result, and inverse
// with the result, and fails when inverse does not return the input. A call
// that fails ends the probe.
func RoundTrips[S, V, W any](
	h *conform.Handle[S],
	call Call[Pair[V, W]],
	inverse func(ctx context.Context, w W) (V, error),
) {
	h.Helper()
	forward, err := call(h.Context())
	if err != nil {
		return
	}
	got, err := inverse(h.Context(), forward.Second)
	h.Expect(spec.BrokenRoundTrip)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, forward.First, h.Statement())
}

// WorksAfter calls ready, then call, and fails when call fails. A ready that
// fails ends the probe.
func WorksAfter[S any](h *conform.Handle[S], call, ready Act) {
	h.Helper()
	if ready(h.Context()) != nil {
		return
	}
	err := call(h.Context())
	h.Expect(spec.NeverReady)
	assert.NoError(h, err, h.Statement())
}

// ReportsSealed calls close, then call, and fails unless the error of call
// matches sentinel. A close that fails ends the probe.
func ReportsSealed[S any](h *conform.Handle[S], call, closer Act, sentinel error) {
	h.Helper()
	if closer(h.Context()) != nil {
		return
	}
	err := call(h.Context())
	h.Expect(spec.AliveAfterClose)
	assert.ErrorIs(h, err, sentinel, h.Statement())
}

// SealsAfterClose opens a handle with call, closes it with closer, calls
// after on it, and fails unless the error of after matches sentinel. A call
// or a close that fails ends the probe.
func SealsAfterClose[S, H any](
	h *conform.Handle[S], call Call[H], closer, after func(ctx context.Context, handle H) error, sentinel error,
) {
	h.Helper()
	handle, err := call(h.Context())
	if err != nil || closer(h.Context(), handle) != nil {
		return
	}
	err = after(h.Context(), handle)
	h.Expect(spec.AliveAfterClose)
	assert.ErrorIs(h, err, sentinel, h.Statement())
}

// MovesObservation observes, calls call, observes again, and fails when the
// two observations are equal. A call that fails ends the probe.
func MovesObservation[S, V any](h *conform.Handle[S], call Act, observe Call[V]) {
	h.Helper()
	moves(h, call, observe, spec.UnmovedObservation)
}

// EachCallCompounds observes around each of two calls, and fails when a call
// leaves the observation unchanged. A call that fails ends the probe.
func EachCallCompounds[S, V any](h *conform.Handle[S], call Act, observe Call[V]) {
	h.Helper()
	for range 2 {
		if !moves(h, call, observe, spec.Uncompounded) {
			return
		}
	}
}

// moves observes, calls call, observes again, and fails with reason when the
// two observations are equal. It reports whether the call succeeded. The
// guard of the probe fails an observation that fails.
func moves[S, V any](h *conform.Handle[S], call Act, observe Call[V], reason spec.ReasonID) bool {
	h.Helper()
	before, err := observe(h.Context())
	assert.NoError(h, err, h.Statement())
	if call(h.Context()) != nil {
		return false
	}
	after, err := observe(h.Context())
	assert.NoError(h, err, h.Statement())
	h.Expect(reason)
	assert.NotEqual(h, after, before, h.Statement())
	return true
}

// SettlesThenObserves observes, calls call and settle, observes again, and
// fails when the two observations are equal. A call or a settle that fails
// ends the probe.
func SettlesThenObserves[S, V any](h *conform.Handle[S], call, settle Act, observe Call[V]) {
	h.Helper()
	settled := func(ctx context.Context) error {
		if err := call(ctx); err != nil {
			return err
		}
		return settle(ctx)
	}
	moves(h, settled, observe, spec.UnsettledEffect)
}

// DetectsTamper calls call, verify, tamper and verify, and fails when the
// last verify succeeds. A call or a first verify that fails ends the probe.
func DetectsTamper[S any](h *conform.Handle[S], call, verify, tamper Act) {
	h.Helper()
	if call(h.Context()) != nil || verify(h.Context()) != nil {
		return
	}
	_ = tamper(h.Context())
	err := verify(h.Context())
	h.Expect(spec.SilentTamper)
	assert.HasError(h, err, h.Statement())
}

// ConvergesAfter calls begin, then call up to attempts times, and fails when
// no call succeeds. A begin that fails ends the probe.
func ConvergesAfter[S any](h *conform.Handle[S], call, begin Act, attempts int) {
	h.Helper()
	if begin(h.Context()) != nil {
		return
	}
	converges(h, call, attempts)
}

// RunsTheHook registers a hook through register, calls call, and fails when
// the hook did not run. A register or a call that fails ends the probe.
func RunsTheHook[S any](h *conform.Handle[S], call Act, register func(ctx context.Context, hook func()) error) {
	h.Helper()
	var ran atomic.Bool
	if register(h.Context(), func() { ran.Store(true) }) != nil || call(h.Context()) != nil {
		return
	}
	h.Expect(spec.HookNotRun)
	assert.True(h, ran.Load(), h.Statement())
}

// RefusesTheInvalid calls validate with invalid, then call with invalid, and
// fails when call succeeds. A validator that accepts invalid ends the probe.
func RefusesTheInvalid[S, V any](h *conform.Handle[S], call, validate func(ctx context.Context, v V) error, invalid V) {
	h.Helper()
	if validate(h.Context(), invalid) == nil {
		return
	}
	err := call(h.Context(), invalid)
	h.Expect(spec.UnscreenedWrite)
	assert.HasError(h, err, h.Statement())
}

// RefusesWhatThePredicateRejects calls call, which writes a first value. It
// then calls match, which reports whether the predicate accepts the value of
// the second call, then call again, and fails when the second call succeeds.
// A first call that fails ends the probe, and so does a predicate that
// accepts the value or fails.
func RefusesWhatThePredicateRejects[S any](h *conform.Handle[S], call Act, match Call[bool]) {
	h.Helper()
	if call(h.Context()) != nil {
		return
	}
	if accepted, err := match(h.Context()); err != nil || accepted {
		return
	}
	err := call(h.Context())
	h.Expect(spec.UnmatchedWrite)
	assert.HasError(h, err, h.Statement())
}

// ResignsLeadership calls call and resign, and fails when leading reports
// leadership. A campaign or a resign that fails ends the probe, and the
// guard of the probe fails a leading that fails.
func ResignsLeadership[S any](h *conform.Handle[S], call, resign Act, leading Call[bool]) {
	h.Helper()
	if call(h.Context()) != nil || resign(h.Context()) != nil {
		return
	}
	lead, err := leading(h.Context())
	assert.NoError(h, err, h.Statement())
	h.Expect(spec.StillLeading)
	assert.False(h, lead, h.Statement())
}

// RollsBackOnFailure runs scope with a body that writes and then fails, then
// read, and fails unless the error of read matches sentinel. A scope that
// does not run its body, or a write inside it that fails, ends the probe.
func RollsBackOnFailure[S any](
	h *conform.Handle[S], scope func(ctx context.Context, body Act) error, write, read Act, sentinel error,
) {
	h.Helper()
	var wrote bool
	_ = scope(h.Context(), func(ctx context.Context) error {
		wrote = write(ctx) == nil
		return errBody
	})
	if !wrote {
		return
	}
	err := read(h.Context())
	h.Expect(spec.NotRolledBack)
	assert.ErrorIs(h, err, sentinel, h.Statement())
}

// AgreesWithTheBacking calls put, then backing and call, and fails when call
// does not return the value of backing. A put or a backing that fails ends
// the probe.
func AgreesWithTheBacking[S, V any](h *conform.Handle[S], call, backing Call[V], put Act) {
	h.Helper()
	if put(h.Context()) != nil {
		return
	}
	want, err := backing(h.Context())
	if err != nil {
		return
	}
	got, err := call(h.Context())
	h.Expect(spec.CacheDisagrees)
	assert.NoError(h, err, h.Statement())
	assert.Equal(h, got, want, h.Statement())
}

// TakesBackWhatItLent gets a value with call and puts it back, and fails
// when put fails. A get that fails ends the probe.
func TakesBackWhatItLent[S, V any](h *conform.Handle[S], call Call[V], put func(ctx context.Context, v V) error) {
	h.Helper()
	lent, err := call(h.Context())
	if err != nil {
		return
	}
	err = put(h.Context(), lent)
	h.Expect(spec.UnreturnedLoan)
	assert.NoError(h, err, h.Statement())
}

// ContinuesFromTheCursor reads the first page with the zero cursor, reads
// with its cursor, and fails when the second page is the first. A first
// page that fails or returns the zero cursor ends the probe, and the guard
// of the probe fails a second page that fails.
func ContinuesFromTheCursor[S, V any, C comparable](
	h *conform.Handle[S], call func(ctx context.Context, cursor C) (V, error), cursor func(page V) C,
) {
	h.Helper()
	var start C
	first, err := call(h.Context(), start)
	if err != nil || cursor(first) == start {
		return
	}
	second, err := call(h.Context(), cursor(first))
	assert.NoError(h, err, h.Statement())
	h.Expect(spec.RepeatedPage)
	assert.NotEqual(h, second, first, h.Statement())
}

// CompensatesTheStep calls call, then compensate, and fails when compensate
// fails. A step that fails ends the probe.
func CompensatesTheStep[S any](h *conform.Handle[S], call, compensate Act) {
	h.Helper()
	if call(h.Context()) != nil {
		return
	}
	err := compensate(h.Context())
	h.Expect(spec.UncompensatedStep)
	assert.NoError(h, err, h.Statement())
}

// RefusedLeavesNothing calls call with refused, then reader, and fails when
// reader returns a value other than zero. A call that accepts refused ends
// the probe.
func RefusedLeavesNothing[S, V, R any](
	h *conform.Handle[S], call func(ctx context.Context, r R) error, reader Call[V], refused R, zero V,
) {
	h.Helper()
	if call(h.Context(), refused) == nil {
		return
	}
	got, _ := reader(h.Context())
	h.Expect(spec.PartialWrite)
	assert.Equal(h, got, zero, h.Statement())
}

// RemovesOnDelete calls put, call and read, and fails unless the error of
// read matches sentinel. A put or a delete that fails ends the probe.
func RemovesOnDelete[S any](h *conform.Handle[S], call, put, read Act, sentinel error) {
	h.Helper()
	if put(h.Context()) != nil || call(h.Context()) != nil {
		return
	}
	err := read(h.Context())
	h.Expect(spec.ReadableAfterDelete)
	assert.ErrorIs(h, err, sentinel, h.Statement())
}
