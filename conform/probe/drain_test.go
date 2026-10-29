// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"slices"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/spec"
)

// queue is the subject of the specs of drains: entries in the order of their
// writes, with a version that each write raises. broken names the one call
// that a planted defect breaks, and is empty for a correct queue.
type queue struct {
	broken   string
	entries  []int
	drains   int
	version  int
	consumes bool
}

// queues returns a factory of queues with the entries, whose call broken
// breaks.
func queues(broken string, entries ...int) func(*conform.Handle[*queue]) *queue {
	return func(*conform.Handle[*queue]) *queue { return &queue{broken: broken, entries: slices.Clone(entries)} }
}

// write appends the next entry. A broken drop appends nothing, and a broken
// double appends the entry twice. A broken regress lowers the version.
func (q *queue) write(context.Context) error {
	entry := len(q.entries) + 1
	switch q.broken {
	case "drop":
		return nil
	case "double":
		q.entries = append(q.entries, entry)
	case "regress":
		q.version -= 2
	}
	q.entries = append(q.entries, entry)
	if q.broken != "stale" {
		q.version++
	}
	return nil
}

// drain returns the entries. A broken repeat repeats the first entry, a
// broken shuffle reverses every second drain, and a broken unordered
// reverses every drain.
func (q *queue) drain(context.Context) ([]int, error) {
	q.drains++
	got := slices.Clone(q.entries)
	if q.consumes {
		q.entries = nil
	}
	switch {
	case q.broken == "repeat" && len(got) > 0:
		got = append(got, got[0])
	case q.broken == "shuffle" && q.drains%2 == 0, q.broken == "unordered":
		slices.Reverse(got)
	}
	return got, nil
}

// writes returns the write of the queue of h.
func writes(h *conform.Handle[*queue]) probe.Act {
	return func(ctx context.Context) error { return h.Subject().write(ctx) }
}

// drains returns the drain of the queue of h.
func drains(h *conform.Handle[*queue]) probe.Call[[]int] {
	return func(ctx context.Context) ([]int, error) { return h.Subject().drain(ctx) }
}

// versions returns the read of the version of the queue of h.
func versions(h *conform.Handle[*queue]) probe.Call[int] {
	return func(context.Context) (int, error) { return h.Subject().version, nil }
}

// versionedWrites returns the write of the queue of h that returns the
// version after it.
func versionedWrites(h *conform.Handle[*queue]) probe.Call[int] {
	return func(ctx context.Context) (int, error) {
		err := h.Subject().write(ctx)
		return h.Subject().version, err
	}
}

// itself returns its version.
func itself(v int) int { return v }

func TestDrain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*queue])
		giveBreak string
		want      spec.ReasonID
	}{
		{
			name: "NoElementTwice", giveProbe: spec.NoElementTwice, giveBreak: "repeat", want: spec.RepeatedElement,
			giveApply: func(h *conform.Handle[*queue]) { probe.NoElementTwice(h, drains(h), writes(h)) },
		},
		{
			name: "DrainsInOneOrder", giveProbe: spec.DrainsInOneOrder, giveBreak: "shuffle", want: spec.ReorderedDrain,
			giveApply: func(h *conform.Handle[*queue]) { probe.DrainsInOneOrder(h, drains(h), writes(h)) },
		},
		{
			name: "GainsWhatWentIn", giveProbe: spec.GainsWhatWentIn, giveBreak: "double", want: spec.MiscountedDrain,
			giveApply: func(h *conform.Handle[*queue]) { probe.GainsWhatWentIn(h, drains(h), writes(h)) },
		},
		{
			name: "KeepsWhatWentIn", giveProbe: spec.KeepsWhatWentIn, giveBreak: "drop", want: spec.ShrunkDrain,
			giveApply: func(h *conform.Handle[*queue]) { probe.KeepsWhatWentIn(h, drains(h), writes(h)) },
		},
		{
			name: "ReadsNeverRegress", giveProbe: spec.ReadsNeverRegress, giveBreak: "regress", want: spec.Regressed,
			giveApply: func(h *conform.Handle[*queue]) {
				probe.ReadsNeverRegress(h, versions(h), writes(h), itself)
			},
		},
		{
			name: "WritesAdvance", giveProbe: spec.WritesAdvance, giveBreak: "stale", want: spec.UnorderedWrite,
			giveApply: func(h *conform.Handle[*queue]) {
				probe.WritesAdvance(h, versions(h), versionedWrites(h), itself)
			},
		},
		{
			name: "ReplaysInStampOrder", giveProbe: spec.ReplaysInStampOrder, giveBreak: "unordered",
			want:      spec.UnorderedHistory,
			giveApply: func(h *conform.Handle[*queue]) { probe.ReplaysInStampOrder(h, writes(h), drains(h)) },
		},
		{
			name: "ReplayGrows", giveProbe: spec.ReplayGrows, giveBreak: "drop", want: spec.ReplayNotGrown,
			giveApply: func(h *conform.Handle[*queue]) { probe.ReplayGrows(h, writes(h), drains(h)) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("passes a correct subject and fails a planted defect with its reason", func(t *testing.T) {
				t.Parallel()

				conform.Check[*queue]{
					ID: "Queue.Call/claim", Statement: "the probe holds", Claim: "claim",
					Probe: tt.giveProbe, Reason: tt.want, Apply: tt.giveApply, Defect: queues(tt.giveBreak),
				}.Run(t, queues(""))
			})
		})
	}

	t.Run("GainsWhatWentIn counts the gain over entries that each drain keeps", func(t *testing.T) {
		t.Parallel()

		conform.Check[*queue]{
			ID: "Queue.Call/claim", Statement: "the probe holds", Claim: "claim",
			Probe: spec.GainsWhatWentIn, Reason: spec.MiscountedDrain, Defect: queues("double", 0),
			Apply: func(h *conform.Handle[*queue]) { probe.GainsWhatWentIn(h, drains(h), writes(h)) },
		}.Run(t, queues("", 0))
	})

	t.Run("GainsWhatWentIn counts the gain of a drain that consumes its entries", func(t *testing.T) {
		t.Parallel()

		consuming := func(broken string) func(*conform.Handle[*queue]) *queue {
			return func(*conform.Handle[*queue]) *queue {
				return &queue{broken: broken, entries: []int{0}, consumes: true}
			}
		}
		conform.Check[*queue]{
			ID: "Queue.Call/claim", Statement: "the probe holds", Claim: "claim",
			Probe: spec.GainsWhatWentIn, Reason: spec.MiscountedDrain, Defect: consuming("double"),
			Apply: func(h *conform.Handle[*queue]) { probe.GainsWhatWentIn(h, drains(h), writes(h)) },
		}.Run(t, consuming(""))
	})

	one := returning([]int{1})
	failing := func(context.Context) error { return errOther }
	ends := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*queue])
	}{
		{
			name: "NoElementTwice passes a write that fails", giveProbe: spec.NoElementTwice,
			giveApply: func(h *conform.Handle[*queue]) { probe.NoElementTwice(h, returning([]int{1, 1}), failing) },
		},
		{
			name: "NoElementTwice passes a drain of one element", giveProbe: spec.NoElementTwice,
			giveApply: func(h *conform.Handle[*queue]) { probe.NoElementTwice(h, one, passes) },
		},
		{
			name: "DrainsInOneOrder passes a first drain of one element", giveProbe: spec.DrainsInOneOrder,
			giveApply: func(h *conform.Handle[*queue]) { probe.DrainsInOneOrder(h, failsSecond([]int{1}), passes) },
		},
		{
			name: "DrainsInOneOrder passes a write that fails", giveProbe: spec.DrainsInOneOrder,
			giveApply: func(h *conform.Handle[*queue]) { probe.DrainsInOneOrder(h, failsSecond([]int{1, 2}), failing) },
		},
		{
			name: "GainsWhatWentIn passes a write that fails", giveProbe: spec.GainsWhatWentIn,
			giveApply: func(h *conform.Handle[*queue]) { probe.GainsWhatWentIn(h, one, failing) },
		},
		{
			name: "KeepsWhatWentIn passes a write that fails", giveProbe: spec.KeepsWhatWentIn,
			giveApply: func(h *conform.Handle[*queue]) { probe.KeepsWhatWentIn(h, one, failing) },
		},
		{
			name: "ReadsNeverRegress passes a first read that fails", giveProbe: spec.ReadsNeverRegress,
			giveApply: func(h *conform.Handle[*queue]) {
				reads := 0
				read := func(context.Context) (int, error) {
					reads++
					if reads == 1 {
						return 5, errOther
					}
					return 1, nil
				}
				probe.ReadsNeverRegress(h, read, passes, itself)
			},
		},
		{
			name: "ReadsNeverRegress passes a write that fails", giveProbe: spec.ReadsNeverRegress,
			giveApply: func(h *conform.Handle[*queue]) {
				probe.ReadsNeverRegress(h, failsSecond(1), failing, itself)
			},
		},
		{
			name: "WritesAdvance passes a first write that fails", giveProbe: spec.WritesAdvance,
			giveApply: func(h *conform.Handle[*queue]) {
				probe.WritesAdvance(h, returning(1), failsFirst(0), itself)
			},
		},
		{
			name: "WritesAdvance passes a read that fails", giveProbe: spec.WritesAdvance,
			giveApply: func(h *conform.Handle[*queue]) {
				probe.WritesAdvance(h, refusing[int](), failsSecond(2), itself)
			},
		},
		{
			name:      "ReplaysInStampOrder passes an append that fails",
			giveProbe: spec.ReplaysInStampOrder,
			giveApply: func(h *conform.Handle[*queue]) { probe.ReplaysInStampOrder(h, failing, returning([]int{2, 1})) },
		},
		{
			name: "ReplayGrows passes a first replay that fails", giveProbe: spec.ReplayGrows,
			giveApply: func(h *conform.Handle[*queue]) { probe.ReplayGrows(h, passes, failsFirst([]int{1})) },
		},
		{
			name: "ReplayGrows passes an append that fails", giveProbe: spec.ReplayGrows,
			giveApply: func(h *conform.Handle[*queue]) { probe.ReplayGrows(h, failing, failsSecond([]int{1})) },
		},
	}
	for _, tt := range ends {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conform.Check[*queue]{
				ID: "Queue.Call/claim", Statement: "the probe holds", Probe: tt.giveProbe, Apply: tt.giveApply,
			}.Run(t, queues(""))
		})
	}

	guards := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*queue])
	}{
		{
			name: "NoElementTwice fails the guard for a drain that fails", giveProbe: spec.NoElementTwice,
			giveApply: func(h *conform.Handle[*queue]) { probe.NoElementTwice(h, refusing[[]int](), passes) },
		},
		{
			name: "DrainsInOneOrder fails the guard for a second drain that fails", giveProbe: spec.DrainsInOneOrder,
			giveApply: func(h *conform.Handle[*queue]) { probe.DrainsInOneOrder(h, failsSecond([]int{1, 2}), passes) },
		},
		{
			name: "GainsWhatWentIn fails the guard for a first drain that fails", giveProbe: spec.GainsWhatWentIn,
			giveApply: func(h *conform.Handle[*queue]) { probe.GainsWhatWentIn(h, failsFirst([]int{1, 2}), passes) },
		},
		{
			name: "ReadsNeverRegress fails the guard for a second read that fails", giveProbe: spec.ReadsNeverRegress,
			giveApply: func(h *conform.Handle[*queue]) {
				probe.ReadsNeverRegress(h, failsSecond(1), passes, itself)
			},
		},
		{
			name: "WritesAdvance fails the guard for a second write that fails", giveProbe: spec.WritesAdvance,
			giveApply: func(h *conform.Handle[*queue]) {
				probe.WritesAdvance(h, returning(0), failsSecond(2), itself)
			},
		},
		{
			name: "ReplayGrows fails the guard for a second replay that fails", giveProbe: spec.ReplayGrows,
			giveApply: func(h *conform.Handle[*queue]) { probe.ReplayGrows(h, passes, failsSecond([]int{1})) },
		},
	}
	for _, tt := range guards {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			conform.Check[*queue]{
				ID: "Queue.Call/claim", Statement: "the probe holds", Probe: tt.giveProbe, Apply: tt.giveApply,
			}.Run(r, queues(""))
			failures := r.Failures()
			assert.NotEmpty(t, failures, "the guard fails")
			assert.Equal(t, failures[0].Assertion, "err-absent", "the guard asserts the success of the call")
		})
	}
}
