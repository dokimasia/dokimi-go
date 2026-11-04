// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"cmp"
	"reflect"
	"slices"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/conform"
)

// NoElementTwice writes twice, drains once, and fails when an element
// repeats. A write that fails, or a drain of fewer than two elements, ends
// the probe.
func NoElementTwice[S, E any](h *conform.Handle[S], drain Call[[]E], write Act) {
	h.Helper()
	got, _ := drained(h, drain, write)
	if len(got) < 2 {
		return
	}
	h.Engage()
	h.Expect(spec.RepeatedElement)
	assert.NoDuplicates(h, func() ([]E, error) { return got, nil }, h.Statement())
}

// DrainsInOneOrder writes twice, drains twice, and fails when the second
// drain differs from the first. A write that fails, or a first drain of
// fewer than two elements, ends the probe.
func DrainsInOneOrder[S, E any](h *conform.Handle[S], drain Call[[]E], write Act) {
	h.Helper()
	first, _ := drained(h, drain, write)
	if len(first) < 2 {
		return
	}
	h.Engage()
	second, err := drain(h.Context())
	assert.NoError(h, err, h.Statement())
	h.Expect(spec.ReorderedDrain)
	assert.Equal(h, second, first, h.Statement())
}

// GainsWhatWentIn drains, writes twice, drains, and fails unless the second
// drain gains exactly two elements over the first. A write that fails ends
// the probe.
func GainsWhatWentIn[S, E any](h *conform.Handle[S], drain Call[[]E], write Act) {
	h.Helper()
	gained, ok := gain(h, drain, write)
	if !ok {
		return
	}
	h.Engage()
	h.Expect(spec.MiscountedDrain)
	assert.Length(h, gained, 2, h.Statement())
}

// KeepsWhatWentIn drains, writes twice, drains, and fails when the second
// drain gains fewer than two elements over the first. A write that fails
// ends the probe.
func KeepsWhatWentIn[S, E any](h *conform.Handle[S], drain Call[[]E], write Act) {
	h.Helper()
	gained, ok := gain(h, drain, write)
	if !ok {
		return
	}
	h.Engage()
	h.Expect(spec.ShrunkDrain)
	assert.True(h, len(gained) >= 2, h.Statement())
}

// drained writes twice and drains once. It returns no elements and false
// where a write fails, and the guard of the probe fails a drain that fails.
func drained[S, E any](h *conform.Handle[S], drain Call[[]E], write Act) ([]E, bool) {
	h.Helper()
	for range 2 {
		if write(h.Context()) != nil {
			return nil, false
		}
	}
	got, err := drain(h.Context())
	assert.NoError(h, err, h.Statement())
	return got, true
}

// gain drains, writes twice and drains again, and returns the elements of the
// second drain that the first does not contain, each element of the first
// drain matching one of the second. It reports false where a write fails,
// and the guard of the probe fails a drain that fails.
func gain[S, E any](h *conform.Handle[S], drain Call[[]E], write Act) ([]E, bool) {
	h.Helper()
	before, err := drain(h.Context())
	assert.NoError(h, err, h.Statement())
	after, ok := drained(h, drain, write)
	if !ok {
		return nil, false
	}
	return without(after, before), true
}

// without returns the elements of from without one element for each element
// of matched, which it compares as reflect.DeepEqual compares them.
func without[E any](from, matched []E) []E {
	rest := slices.Clone(from)
	for _, m := range matched {
		if at := slices.IndexFunc(rest, func(e E) bool { return reflect.DeepEqual(e, m) }); at >= 0 {
			rest = slices.Delete(rest, at, at+1)
		}
	}
	return rest
}

// ReadsNeverRegress writes, reads, writes and reads, and fails when the
// version of the second read is below the version of the first. The first
// write gives the first read a version to find. A write or the first read
// that fails ends the probe, and the guard of the probe fails a second read
// that fails.
func ReadsNeverRegress[S, V any, O cmp.Ordered](h *conform.Handle[S], read Call[V], write Act, version func(v V) O) {
	h.Helper()
	if write(h.Context()) != nil {
		return
	}
	first, err := read(h.Context())
	if err != nil || write(h.Context()) != nil {
		return
	}
	h.Engage()
	second, err := read(h.Context())
	assert.NoError(h, err, h.Statement())
	h.Expect(spec.Regressed)
	assert.Pairwise(h, []O{version(first), version(second)}, func(a, b O) bool { return b >= a }, h.Statement())
}

// WritesAdvance writes, reads and writes, and fails unless the version of
// the second write is above the version of the read. A first write or a read
// that fails ends the probe, and the guard of the probe fails a second write
// that fails.
func WritesAdvance[S, V any, O cmp.Ordered](h *conform.Handle[S], read, write Call[V], version func(v V) O) {
	h.Helper()
	if _, err := write(h.Context()); err != nil {
		return
	}
	seen, err := read(h.Context())
	if err != nil {
		return
	}
	h.Engage()
	next, err := write(h.Context())
	assert.NoError(h, err, h.Statement())
	h.Expect(spec.UnorderedWrite)
	assert.Pairwise(h, []O{version(seen), version(next)}, func(a, b O) bool { return b > a }, h.Statement())
}

// ReplaysInStampOrder appends twice, replays, which returns the stamp of each
// entry, and fails when a stamp is below the stamp before it. An append that
// fails ends the probe, and the guard of the probe fails a replay that fails.
func ReplaysInStampOrder[S any, O cmp.Ordered](h *conform.Handle[S], call Act, replay Call[[]O]) {
	h.Helper()
	stamps, ok := drained(h, replay, call)
	if !ok {
		return
	}
	h.Engage()
	h.Expect(spec.UnorderedHistory)
	assert.Pairwise(h, stamps, func(a, b O) bool { return b >= a }, h.Statement())
}

// ReplayGrows replays, appends and replays, and fails unless the second
// replay has one entry more than the first. A first replay or an append that
// fails ends the probe, and the guard of the probe fails a second replay
// that fails.
func ReplayGrows[S, E any](h *conform.Handle[S], call Act, replay Call[[]E]) {
	h.Helper()
	before, err := replay(h.Context())
	if err != nil || call(h.Context()) != nil {
		return
	}
	h.Engage()
	after, err := replay(h.Context())
	assert.NoError(h, err, h.Statement())
	h.Expect(spec.ReplayNotGrown)
	assert.Length(h, after, len(before)+1, h.Statement())
}
