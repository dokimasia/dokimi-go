// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"errors"
	"maps"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/spec"
)

// errClosed is the error of a call of a store after its close.
var errClosed = errors.New("probe_test: the store is closed")

// store is the subject of the specs of partners: values by key, with a
// version that each write raises. broken names the one call that a planted
// defect breaks, and is empty for a correct store.
type store struct {
	values  map[int]int
	hooks   []func()
	handles map[int]bool
	lent    map[int]bool
	broken  string
	version int
	writes  int
	pending int
	applied int
	sum     int
	closed  bool
	opened  bool
	leading bool
}

// stores returns a factory of stores with the value 1 under the key 1, whose
// call broken breaks.
func stores(broken string) func(*conform.Handle[*store]) *store {
	return func(*conform.Handle[*store]) *store {
		return &store{values: map[int]int{1: 1}, handles: map[int]bool{}, lent: map[int]bool{}, broken: broken}
	}
}

// put stores v under k. A broken put drops the value, a broken overwrite
// keeps the first value of a key, a broken partition writes every key under
// 1, and a broken hook runs no hook.
func (s *store) put(k, v int) error {
	if s.closed {
		return errClosed
	}
	s.writes++
	switch {
	case s.broken == "put":
		return nil
	case s.broken == "partition":
		k = 1
	case s.broken == "overwrite" && s.writes > 1:
		return nil
	}
	s.values[k] = v
	if s.broken != "compound" || s.version == 0 {
		s.version++
	}
	if s.broken != "hook" {
		for _, hook := range s.hooks {
			hook()
		}
	}
	return nil
}

// get returns the value under k, and errMiss for a key without one. A broken
// get returns the value plus one, and a broken error returns the value beside
// errOther.
func (s *store) get(k int) (int, error) {
	if s.closed {
		return 0, errClosed
	}
	v, found := s.values[k]
	switch {
	case !found:
		return 0, errMiss
	case s.broken == "get":
		return v + 1, nil
	case s.broken == "error":
		return v, errOther
	}
	return v, nil
}

// writer returns a write of the store of h that writes the values in turn
// under the keys in turn, and returns the value that it wrote.
func writer(h *conform.Handle[*store], keys, values []int) probe.Call[int] {
	n := 0
	return func(context.Context) (int, error) {
		k, v := keys[min(n, len(keys)-1)], values[min(n, len(values)-1)]
		n++
		return v, h.Subject().put(k, v)
	}
}

// reader returns the read of the key k of the store of h.
func reader(h *conform.Handle[*store], k int) probe.Call[int] {
	return func(context.Context) (int, error) { return h.Subject().get(k) }
}

// act returns the call fn of the store of h as an Act.
func act(h *conform.Handle[*store], fn func(s *store) error) probe.Act {
	return func(context.Context) error { return fn(h.Subject()) }
}

// version returns the observation of the version of the store of h.
func version(h *conform.Handle[*store]) probe.Call[int] {
	return func(context.Context) (int, error) { return h.Subject().version, nil }
}

// passes is an Act that succeeds, and fails an Act that fails.
var (
	passes probe.Act = func(context.Context) error { return nil }
	fails  probe.Act = func(context.Context) error { return errOther }
)

// refusing returns a call that fails.
func refusing[V any]() probe.Call[V] {
	return func(context.Context) (V, error) {
		var zero V
		return zero, errOther
	}
}

// returning returns a call that returns v.
func returning[V any](v V) probe.Call[V] {
	return func(context.Context) (V, error) { return v, nil }
}

// failsFirst returns a call that fails at its first call and returns v after
// it.
func failsFirst[V any](v V) probe.Call[V] {
	n := 0
	return func(context.Context) (V, error) {
		n++
		if n == 1 {
			return v, errOther
		}
		return v, nil
	}
}

// failsSecond returns a call that returns v and fails from its second call
// on.
func failsSecond[V any](v V) probe.Call[V] {
	n := 0
	return func(context.Context) (V, error) {
		n++
		if n > 1 {
			return v, errOther
		}
		return v, nil
	}
}

// page is one page of the keys of a store, with the cursor of the next.
type page struct {
	keys []int
	next int
}

func TestPartner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*store])
		giveBreak string
		want      spec.ReasonID
		giveError bool
	}{
		{
			name: "WriteThenRead", giveProbe: spec.WriteThenRead, giveBreak: "put", want: spec.UnobservedWrite,
			giveError: true,
			giveApply: func(h *conform.Handle[*store]) {
				probe.WriteThenRead(h, reader(h, 1), writer(h, []int{1}, []int{7}))
			},
		},
		{
			name: "PinnedAcrossWrite", giveProbe: spec.PinnedAcrossWrite, giveBreak: "pinned", want: spec.UnpinnedRead,
			giveError: true,
			giveApply: func(h *conform.Handle[*store]) {
				var snapshot map[int]int
				read := func(context.Context) (int, error) {
					s := h.Subject()
					if snapshot != nil && s.broken == "error" {
						return snapshot[1], errOther
					}
					if snapshot == nil || s.broken == "pinned" {
						snapshot = maps.Clone(s.values)
					}
					return snapshot[1], nil
				}
				probe.PinnedAcrossWrite(h, read, act(h, func(s *store) error { return s.put(1, 8) }))
			},
		},
		{
			name: "SecondWriteReplaces", giveProbe: spec.SecondWriteReplaces, giveBreak: "overwrite",
			want: spec.UnreplacedWrite, giveError: true,
			giveApply: func(h *conform.Handle[*store]) {
				probe.SecondWriteReplaces(h, writer(h, []int{2}, []int{10, 20}), reader(h, 2))
			},
		},
		{
			name: "KeepsPartitionsApart", giveProbe: spec.KeepsPartitionsApart, giveBreak: "partition",
			want: spec.CrossedPartitions, giveError: true,
			giveApply: func(h *conform.Handle[*store]) {
				probe.KeepsPartitionsApart(h, writer(h, []int{1, 2}, []int{10, 20}), reader(h, 1))
			},
		},
		{
			name: "RoundTrips", giveProbe: spec.RoundTrips, giveBreak: "decode", want: spec.BrokenRoundTrip,
			giveError: true,
			giveApply: func(h *conform.Handle[*store]) {
				encode := func(context.Context) (probe.Pair[int, string], error) {
					return probe.Pair[int, string]{First: 5, Second: strconv.Itoa(5)}, nil
				}
				decode := func(_ context.Context, text string) (int, error) {
					v, err := strconv.Atoi(text)
					switch h.Subject().broken {
					case "decode":
						v++
					case "error":
						err = errOther
					}
					return v, err
				}
				probe.RoundTrips(h, encode, decode)
			},
		},
		{
			name: "WorksAfter", giveProbe: spec.WorksAfter, giveBreak: "open", want: spec.NeverReady,
			giveApply: func(h *conform.Handle[*store]) {
				use := act(h, func(s *store) error {
					if !s.opened {
						return errClosed
					}
					return nil
				})
				open := act(h, func(s *store) error {
					s.opened = s.broken != "open"
					return nil
				})
				probe.WorksAfter(h, use, open)
			},
		},
		{
			name: "ReportsSealed", giveProbe: spec.ReportsSealed, giveBreak: "close", want: spec.AliveAfterClose,
			giveApply: func(h *conform.Handle[*store]) {
				closer := act(h, func(s *store) error {
					s.closed = s.broken != "close"
					return nil
				})
				probe.ReportsSealed(
					h,
					act(h, func(s *store) error { _, err := s.get(1); return err }),
					closer,
					errClosed,
				)
			},
		},
		{
			name: "SealsAfterClose", giveProbe: spec.SealsAfterClose, giveBreak: "close", want: spec.AliveAfterClose,
			giveApply: func(h *conform.Handle[*store]) {
				open := func(context.Context) (int, error) {
					s := h.Subject()
					s.handles[1] = true
					return 1, nil
				}
				closer := func(_ context.Context, handle int) error {
					s := h.Subject()
					s.handles[handle] = s.broken == "close"
					return nil
				}
				after := func(_ context.Context, handle int) error {
					if !h.Subject().handles[handle] {
						return errClosed
					}
					return nil
				}
				probe.SealsAfterClose(h, open, closer, after, errClosed)
			},
		},
		{
			name: "MovesObservation", giveProbe: spec.MovesObservation, giveBreak: "put",
			want: spec.UnmovedObservation,
			giveApply: func(h *conform.Handle[*store]) {
				probe.MovesObservation(h, act(h, func(s *store) error { return s.put(1, 2) }), version(h))
			},
		},
		{
			name: "EachCallCompounds", giveProbe: spec.EachCallCompounds, giveBreak: "compound",
			want: spec.Uncompounded,
			giveApply: func(h *conform.Handle[*store]) {
				probe.EachCallCompounds(h, act(h, func(s *store) error { return s.put(1, 2) }), version(h))
			},
		},
		{
			name: "SettlesThenObserves", giveProbe: spec.SettlesThenObserves, giveBreak: "settle",
			want: spec.UnsettledEffect,
			giveApply: func(h *conform.Handle[*store]) {
				publish := act(h, func(s *store) error {
					s.pending++
					return nil
				})
				settle := act(h, func(s *store) error {
					if s.broken != "settle" {
						s.applied, s.pending = s.applied+s.pending, 0
					}
					return nil
				})
				applied := func(context.Context) (int, error) { return h.Subject().applied, nil }
				probe.SettlesThenObserves(h, publish, settle, applied)
			},
		},
		{
			name: "DetectsTamper", giveProbe: spec.DetectsTamper, giveBreak: "verify", want: spec.SilentTamper,
			giveApply: func(h *conform.Handle[*store]) {
				seal := act(h, func(s *store) error {
					s.sum = s.values[1]
					return nil
				})
				verify := act(h, func(s *store) error {
					if s.broken != "verify" && s.sum != s.values[1] {
						return errOther
					}
					return nil
				})
				tamper := act(h, func(s *store) error {
					s.values[1]++
					return nil
				})
				probe.DetectsTamper(h, seal, verify, tamper)
			},
		},
		{
			name: "ConvergesAfter", giveProbe: spec.ConvergesAfter, giveBreak: "begin", want: spec.NeverConverged,
			giveApply: func(h *conform.Handle[*store]) {
				begin := act(h, func(s *store) error {
					s.opened = s.broken != "begin"
					return nil
				})
				call := act(h, func(s *store) error {
					if !s.opened {
						return errClosed
					}
					return nil
				})
				probe.ConvergesAfter(h, call, begin, 2)
			},
		},
		{
			name: "RunsTheHook", giveProbe: spec.RunsTheHook, giveBreak: "hook", want: spec.HookNotRun,
			giveApply: func(h *conform.Handle[*store]) {
				register := func(_ context.Context, hook func()) error {
					h.Subject().hooks = append(h.Subject().hooks, hook)
					return nil
				}
				probe.RunsTheHook(h, act(h, func(s *store) error { return s.put(1, 2) }), register)
			},
		},
		{
			name: "RefusesTheInvalid", giveProbe: spec.RefusesTheInvalid, giveBreak: "screen",
			want: spec.UnscreenedWrite,
			giveApply: func(h *conform.Handle[*store]) {
				validate := func(_ context.Context, v int) error {
					if v < 0 {
						return errOther
					}
					return nil
				}
				call := func(ctx context.Context, v int) error {
					if h.Subject().broken != "screen" {
						if err := validate(ctx, v); err != nil {
							return err
						}
					}
					return h.Subject().put(1, v)
				}
				probe.RefusesTheInvalid(h, call, validate, -1)
			},
		},
		{
			name: "RefusesWhatThePredicateRejects", giveProbe: spec.RefusesWhatThePredicateRejects,
			giveBreak: "match", want: spec.UnmatchedWrite,
			giveApply: func(h *conform.Handle[*store]) {
				match := func(context.Context) (bool, error) { return false, nil }
				calls := 0
				call := act(h, func(s *store) error {
					calls++
					if calls > 1 && s.broken != "match" {
						return errOther
					}
					return s.put(1, calls)
				})
				probe.RefusesWhatThePredicateRejects(h, call, match)
			},
		},
		{
			name: "ResignsLeadership", giveProbe: spec.ResignsLeadership, giveBreak: "resign", want: spec.StillLeading,
			giveApply: func(h *conform.Handle[*store]) {
				campaign := act(h, func(s *store) error {
					s.leading = true
					return nil
				})
				resign := act(h, func(s *store) error {
					s.leading = s.broken == "resign"
					return nil
				})
				leading := func(context.Context) (bool, error) { return h.Subject().leading, nil }
				probe.ResignsLeadership(h, campaign, resign, leading)
			},
		},
		{
			name: "RollsBackOnFailure", giveProbe: spec.RollsBackOnFailure, giveBreak: "rollback",
			want: spec.NotRolledBack,
			giveApply: func(h *conform.Handle[*store]) {
				scope := func(ctx context.Context, body probe.Act) error {
					s := h.Subject()
					saved := maps.Clone(s.values)
					err := body(ctx)
					if err != nil && s.broken != "rollback" {
						s.values = saved
					}
					return err
				}
				write := act(h, func(s *store) error { return s.put(2, 1) })
				read := act(h, func(s *store) error { _, err := s.get(2); return err })
				probe.RollsBackOnFailure(h, scope, write, read, errMiss)
			},
		},
		{
			name: "AgreesWithTheBacking", giveProbe: spec.AgreesWithTheBacking, giveBreak: "get",
			want: spec.CacheDisagrees, giveError: true,
			giveApply: func(h *conform.Handle[*store]) {
				backing := func(context.Context) (int, error) { return h.Subject().values[1], nil }
				probe.AgreesWithTheBacking(
					h,
					reader(h, 1),
					backing,
					act(h, func(s *store) error { return s.put(1, 3) }),
				)
			},
		},
		{
			name: "TakesBackWhatItLent", giveProbe: spec.TakesBackWhatItLent, giveBreak: "back",
			want: spec.UnreturnedLoan,
			giveApply: func(h *conform.Handle[*store]) {
				lend := func(context.Context) (int, error) {
					h.Subject().lent[4] = true
					return 4, nil
				}
				back := func(_ context.Context, token int) error {
					s := h.Subject()
					if !s.lent[token] || s.broken == "back" {
						return errOther
					}
					return nil
				}
				probe.TakesBackWhatItLent(h, lend, back)
			},
		},
		{
			name: "ContinuesFromTheCursor", giveProbe: spec.ContinuesFromTheCursor, giveBreak: "page",
			want: spec.RepeatedPage,
			giveApply: func(h *conform.Handle[*store]) {
				read := func(_ context.Context, cursor int) (page, error) {
					if h.Subject().broken == "page" {
						cursor = 0
					}
					return page{keys: []int{cursor}, next: cursor + 1}, nil
				}
				probe.ContinuesFromTheCursor(h, read, func(p page) int { return p.next })
			},
		},
		{
			name: "CompensatesTheStep", giveProbe: spec.CompensatesTheStep, giveBreak: "compensate",
			want: spec.UncompensatedStep,
			giveApply: func(h *conform.Handle[*store]) {
				step := act(h, func(s *store) error { return s.put(3, 1) })
				compensate := act(h, func(s *store) error {
					if _, found := s.values[3]; !found || s.broken == "compensate" {
						return errOther
					}
					delete(s.values, 3)
					return nil
				})
				probe.CompensatesTheStep(h, step, compensate)
			},
		},
		{
			name: "RefusedLeavesNothing", giveProbe: spec.RefusedLeavesNothing, giveBreak: "batch",
			want: spec.PartialWrite,
			giveApply: func(h *conform.Handle[*store]) {
				batch := func(_ context.Context, v int) error {
					s := h.Subject()
					if s.broken == "batch" {
						s.values[5] = v
					}
					if v < 0 {
						return errOther
					}
					return s.put(5, v)
				}
				read := func(context.Context) (int, error) { return h.Subject().values[5], nil }
				probe.RefusedLeavesNothing(h, batch, read, -1, 0)
			},
		},
		{
			name: "RemovesOnDelete", giveProbe: spec.RemovesOnDelete, giveBreak: "delete",
			want: spec.ReadableAfterDelete,
			giveApply: func(h *conform.Handle[*store]) {
				del := act(h, func(s *store) error {
					if s.broken != "delete" {
						delete(s.values, 1)
					}
					return nil
				})
				put := act(h, func(s *store) error { return s.put(1, 2) })
				read := act(h, func(s *store) error { _, err := s.get(1); return err })
				probe.RemovesOnDelete(h, del, put, read, errMiss)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("passes a correct subject and fails a planted defect with its reason", func(t *testing.T) {
				t.Parallel()

				conform.Check[*store]{
					ID: "Store.Call/claim", Statement: "the probe holds", Claim: "claim",
					Probe: tt.giveProbe, Reason: tt.want, Apply: tt.giveApply, Defect: stores(tt.giveBreak),
				}.Run(t, stores(""))
			})

			if tt.giveError {
				t.Run("fails a read that returns the value beside an error with its reason", func(t *testing.T) {
					t.Parallel()

					conform.Check[*store]{
						ID: "Store.Call/claim", Statement: "the probe holds", Claim: "claim",
						Probe: tt.giveProbe, Reason: tt.want, Apply: tt.giveApply, Defect: stores("error"),
					}.Run(t, stores(""))
				})
			}
		})
	}

	closeHandle := func(context.Context, int) error { return nil }
	failHandle := func(context.Context, int) error { return errOther }
	ends := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*store])
	}{
		{
			name: "WriteThenRead passes a write that fails", giveProbe: spec.WriteThenRead,
			giveApply: func(h *conform.Handle[*store]) { probe.WriteThenRead(h, reader(h, 1), refusing[int]()) },
		},
		{
			name: "PinnedAcrossWrite passes a first read that fails", giveProbe: spec.PinnedAcrossWrite,
			giveApply: func(h *conform.Handle[*store]) { probe.PinnedAcrossWrite(h, refusing[int](), passes) },
		},
		{
			name: "PinnedAcrossWrite passes a write that fails", giveProbe: spec.PinnedAcrossWrite,
			giveApply: func(h *conform.Handle[*store]) { probe.PinnedAcrossWrite(h, failsSecond(1), fails) },
		},
		{
			name: "SecondWriteReplaces passes a first write that fails", giveProbe: spec.SecondWriteReplaces,
			giveApply: func(h *conform.Handle[*store]) { probe.SecondWriteReplaces(h, failsFirst(1), reader(h, 3)) },
		},
		{
			name: "SecondWriteReplaces passes a second write that fails", giveProbe: spec.SecondWriteReplaces,
			giveApply: func(h *conform.Handle[*store]) { probe.SecondWriteReplaces(h, failsSecond(1), reader(h, 3)) },
		},
		{
			name: "KeepsPartitionsApart passes a first write that fails", giveProbe: spec.KeepsPartitionsApart,
			giveApply: func(h *conform.Handle[*store]) { probe.KeepsPartitionsApart(h, failsFirst(1), reader(h, 3)) },
		},
		{
			name: "KeepsPartitionsApart passes a second write that fails", giveProbe: spec.KeepsPartitionsApart,
			giveApply: func(h *conform.Handle[*store]) { probe.KeepsPartitionsApart(h, failsSecond(1), reader(h, 3)) },
		},
		{
			name: "RoundTrips passes a forward call that fails", giveProbe: spec.RoundTrips,
			giveApply: func(h *conform.Handle[*store]) {
				probe.RoundTrips(h, refusing[probe.Pair[int, int]](), func(context.Context, int) (int, error) {
					return 0, errOther
				})
			},
		},
		{
			name: "WorksAfter passes a predecessor that fails", giveProbe: spec.WorksAfter,
			giveApply: func(h *conform.Handle[*store]) { probe.WorksAfter(h, fails, fails) },
		},
		{
			name: "ReportsSealed passes a close that fails", giveProbe: spec.ReportsSealed,
			giveApply: func(h *conform.Handle[*store]) { probe.ReportsSealed(h, passes, fails, errClosed) },
		},
		{
			name: "SealsAfterClose passes an open that fails", giveProbe: spec.SealsAfterClose,
			giveApply: func(h *conform.Handle[*store]) {
				probe.SealsAfterClose(h, refusing[int](), closeHandle, closeHandle, errClosed)
			},
		},
		{
			name: "SealsAfterClose passes a close that fails", giveProbe: spec.SealsAfterClose,
			giveApply: func(h *conform.Handle[*store]) {
				probe.SealsAfterClose(h, returning(1), failHandle, closeHandle, errClosed)
			},
		},
		{
			name: "MovesObservation passes a call that fails", giveProbe: spec.MovesObservation,
			giveApply: func(h *conform.Handle[*store]) { probe.MovesObservation(h, fails, version(h)) },
		},
		{
			name: "EachCallCompounds passes a second call that fails", giveProbe: spec.EachCallCompounds,
			giveApply: func(h *conform.Handle[*store]) {
				call := failsSecond(0)
				probe.EachCallCompounds(h, func(ctx context.Context) error {
					if _, err := call(ctx); err != nil {
						return err
					}
					return h.Subject().put(1, 2)
				}, version(h))
			},
		},
		{
			name: "SettlesThenObserves passes a call that fails", giveProbe: spec.SettlesThenObserves,
			giveApply: func(h *conform.Handle[*store]) { probe.SettlesThenObserves(h, fails, passes, version(h)) },
		},
		{
			name: "SettlesThenObserves passes a settle that fails", giveProbe: spec.SettlesThenObserves,
			giveApply: func(h *conform.Handle[*store]) { probe.SettlesThenObserves(h, passes, fails, version(h)) },
		},
		{
			name: "DetectsTamper passes a call that fails", giveProbe: spec.DetectsTamper,
			giveApply: func(h *conform.Handle[*store]) { probe.DetectsTamper(h, fails, passes, passes) },
		},
		{
			name: "DetectsTamper passes a first verify that fails", giveProbe: spec.DetectsTamper,
			giveApply: func(h *conform.Handle[*store]) {
				tampered := false
				verify := func(context.Context) error {
					if !tampered {
						return errOther
					}
					return nil
				}
				tamper := func(context.Context) error {
					tampered = true
					return nil
				}
				probe.DetectsTamper(h, passes, verify, tamper)
			},
		},
		{
			name: "ConvergesAfter passes a begin that fails", giveProbe: spec.ConvergesAfter,
			giveApply: func(h *conform.Handle[*store]) { probe.ConvergesAfter(h, fails, fails, 2) },
		},
		{
			name: "RunsTheHook passes a register that fails", giveProbe: spec.RunsTheHook,
			giveApply: func(h *conform.Handle[*store]) {
				probe.RunsTheHook(h, passes, func(context.Context, func()) error { return errOther })
			},
		},
		{
			name: "RunsTheHook passes a call that fails", giveProbe: spec.RunsTheHook,
			giveApply: func(h *conform.Handle[*store]) {
				probe.RunsTheHook(h, fails, func(context.Context, func()) error { return nil })
			},
		},
		{
			name: "RefusesTheInvalid passes a validator that accepts the value", giveProbe: spec.RefusesTheInvalid,
			giveApply: func(h *conform.Handle[*store]) {
				accept := func(context.Context, int) error { return nil }
				probe.RefusesTheInvalid(h, accept, accept, -1)
			},
		},
		{
			name:      "RefusesWhatThePredicateRejects passes a first write that fails",
			giveProbe: spec.RefusesWhatThePredicateRejects,
			giveApply: func(h *conform.Handle[*store]) {
				calls := 0
				failsFirst := func(context.Context) error {
					calls++
					if calls == 1 {
						return errOther
					}
					return nil
				}
				probe.RefusesWhatThePredicateRejects(h, failsFirst, returning(false))
			},
		},
		{
			name:      "RefusesWhatThePredicateRejects passes a predicate that accepts the value",
			giveProbe: spec.RefusesWhatThePredicateRejects,
			giveApply: func(h *conform.Handle[*store]) { probe.RefusesWhatThePredicateRejects(h, passes, returning(true)) },
		},
		{
			name:      "RefusesWhatThePredicateRejects passes a predicate that fails",
			giveProbe: spec.RefusesWhatThePredicateRejects,
			giveApply: func(h *conform.Handle[*store]) {
				probe.RefusesWhatThePredicateRejects(h, passes, refusing[bool]())
			},
		},
		{
			name: "ResignsLeadership passes a campaign that fails", giveProbe: spec.ResignsLeadership,
			giveApply: func(h *conform.Handle[*store]) { probe.ResignsLeadership(h, fails, passes, returning(true)) },
		},
		{
			name: "ResignsLeadership passes a resign that fails", giveProbe: spec.ResignsLeadership,
			giveApply: func(h *conform.Handle[*store]) { probe.ResignsLeadership(h, passes, fails, returning(true)) },
		},
		{
			name: "RollsBackOnFailure passes a write in the scope that fails", giveProbe: spec.RollsBackOnFailure,
			giveApply: func(h *conform.Handle[*store]) {
				scope := func(ctx context.Context, body probe.Act) error { return body(ctx) }
				probe.RollsBackOnFailure(h, scope, fails, passes, errMiss)
			},
		},
		{
			name: "RollsBackOnFailure passes a scope that does not run its body", giveProbe: spec.RollsBackOnFailure,
			giveApply: func(h *conform.Handle[*store]) {
				scope := func(context.Context, probe.Act) error { return nil }
				probe.RollsBackOnFailure(h, scope, passes, passes, errMiss)
			},
		},
		{
			name: "AgreesWithTheBacking passes a put that fails", giveProbe: spec.AgreesWithTheBacking,
			giveApply: func(h *conform.Handle[*store]) {
				probe.AgreesWithTheBacking(h, returning(1), returning(2), fails)
			},
		},
		{
			name: "AgreesWithTheBacking passes a backing that fails", giveProbe: spec.AgreesWithTheBacking,
			giveApply: func(h *conform.Handle[*store]) {
				probe.AgreesWithTheBacking(h, returning(1), refusing[int](), passes)
			},
		},
		{
			name: "TakesBackWhatItLent passes a get that fails", giveProbe: spec.TakesBackWhatItLent,
			giveApply: func(h *conform.Handle[*store]) {
				probe.TakesBackWhatItLent(h, refusing[int](), failHandle)
			},
		},
		{
			name: "ContinuesFromTheCursor passes a first page that fails", giveProbe: spec.ContinuesFromTheCursor,
			giveApply: func(h *conform.Handle[*store]) {
				read := func(context.Context, int) (page, error) { return page{next: 1}, errOther }
				probe.ContinuesFromTheCursor(h, read, func(p page) int { return p.next })
			},
		},
		{
			name:      "ContinuesFromTheCursor passes a first page without a cursor",
			giveProbe: spec.ContinuesFromTheCursor,
			giveApply: func(h *conform.Handle[*store]) {
				read := func(context.Context, int) (page, error) { return page{}, nil }
				probe.ContinuesFromTheCursor(h, read, func(p page) int { return p.next })
			},
		},
		{
			name: "CompensatesTheStep passes a step that fails", giveProbe: spec.CompensatesTheStep,
			giveApply: func(h *conform.Handle[*store]) { probe.CompensatesTheStep(h, fails, fails) },
		},
		{
			name:      "RefusedLeavesNothing passes a call that accepts the refused value",
			giveProbe: spec.RefusedLeavesNothing,
			giveApply: func(h *conform.Handle[*store]) {
				probe.RefusedLeavesNothing(h, closeHandle, returning(1), -1, 0)
			},
		},
		{
			name:      "RefusedLeavesNothing passes a reader that fails beside a value",
			giveProbe: spec.RefusedLeavesNothing,
			giveApply: func(h *conform.Handle[*store]) {
				refuses := func(context.Context, int) error { return errOther }
				reads := func(context.Context) (int, error) { return 1, errOther }
				probe.RefusedLeavesNothing(h, refuses, reads, -1, 0)
			},
		},
		{
			name: "RemovesOnDelete passes a put that fails", giveProbe: spec.RemovesOnDelete,
			giveApply: func(h *conform.Handle[*store]) { probe.RemovesOnDelete(h, passes, fails, passes, errMiss) },
		},
		{
			name: "RemovesOnDelete passes a delete that fails", giveProbe: spec.RemovesOnDelete,
			giveApply: func(h *conform.Handle[*store]) { probe.RemovesOnDelete(h, fails, passes, passes, errMiss) },
		},
	}
	for _, tt := range ends {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conform.Check[*store]{
				ID: "Store.Call/claim", Statement: "the probe holds", Probe: tt.giveProbe, Apply: tt.giveApply,
			}.Run(t, stores(""))
		})
	}

	guards := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*store])
	}{
		{
			name:      "MovesObservation fails the guard for a first observation that fails",
			giveProbe: spec.MovesObservation,
			giveApply: func(h *conform.Handle[*store]) { probe.MovesObservation(h, passes, failsFirst(0)) },
		},
		{
			name:      "MovesObservation fails the guard for a second observation that fails",
			giveProbe: spec.MovesObservation,
			giveApply: func(h *conform.Handle[*store]) { probe.MovesObservation(h, passes, failsSecond(0)) },
		},
		{
			name: "ResignsLeadership fails the guard for a leading that fails", giveProbe: spec.ResignsLeadership,
			giveApply: func(h *conform.Handle[*store]) { probe.ResignsLeadership(h, passes, passes, refusing[bool]()) },
		},
		{
			name:      "ContinuesFromTheCursor fails the guard for a second page that fails",
			giveProbe: spec.ContinuesFromTheCursor,
			giveApply: func(h *conform.Handle[*store]) {
				read := func(_ context.Context, cursor int) (page, error) {
					if cursor > 0 {
						return page{}, errOther
					}
					return page{next: 1}, nil
				}
				probe.ContinuesFromTheCursor(h, read, func(p page) int { return p.next })
			},
		},
	}
	for _, tt := range guards {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			conform.Check[*store]{
				ID: "Store.Call/claim", Statement: "the probe holds", Probe: tt.giveProbe, Apply: tt.giveApply,
			}.Run(r, stores(""))
			failures := r.Failures()
			assert.NotEmpty(t, failures, "the guard fails")
			assert.Equal(t, failures[0].Assertion, "err-absent", "the guard asserts the success of the call")
		})
	}
}
