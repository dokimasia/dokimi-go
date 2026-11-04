// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"errors"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
)

// errOpen is the error of a breaker of the specs that refuses a call.
var errOpen = errors.New("probe_test: the breaker is open")

// The bounds of the relays of the specs: breakerLimit is the number of
// failures in a row after which a breaker refuses each call, and busyCalls
// the number of calls of a dependency of each call of a busy relay.
const (
	breakerLimit = 3
	busyCalls    = 70
)

// relay is the subject of the specs of the probes of failing dependencies.
// Each call of a relay runs its act, which calls its dependencies. value is the
// value that a write stored, pending the value that a scope buffered, failures
// the failures of the dependency in a row, and calls the number of calls of
// the relay. failed reports a failure of a dependency. The scope of a relay
// commits its buffered value through its first dependency: with keeps set it
// holds on to the value after a commit that fails, with ignores set it returns
// no error for such a commit, with local set it commits without the
// dependency, and with refuses set it fails before the commit.
type relay struct {
	act      relayAct
	deps     []*conform.Dependency
	value    int
	pending  int
	failures int
	calls    int
	failed   bool
	keeps    bool
	ignores  bool
	local    bool
	refuses  bool
}

// relayAct decides the result of a call of a relay.
type relayAct func(ctx context.Context, r *relay) error

// newRelay returns a relay over n dependencies of inj, whose calls run act.
func newRelay(inj *conform.Injector, act relayAct, n int) *relay {
	r := &relay{act: act}
	for range n {
		r.deps = append(r.deps, inj.Dependency())
	}
	return r
}

// relays returns the door of a relay over n dependencies whose calls run act.
func relays(act relayAct, n int) func(*conform.Handle[*relay], *conform.Injector) *relay {
	return func(_ *conform.Handle[*relay], inj *conform.Injector) *relay { return newRelay(inj, act, n) }
}

// over returns the factory of a relay over n dependencies of the injector of
// a handle, whose calls run act.
func over(act relayAct, n int) func(*conform.Handle[*relay]) *relay {
	return func(h *conform.Handle[*relay]) *relay { return newRelay(h.Injector(), act, n) }
}

// scoped returns the factory of a relay over one dependency, with the scope
// that change sets.
func scoped(change func(r *relay)) func(*conform.Handle[*relay]) *relay {
	return func(h *conform.Handle[*relay]) *relay {
		r := newRelay(h.Injector(), idle, 1)
		change(r)
		return r
	}
}

// runs returns the call of the subject of h for its error.
func runs(h *conform.Handle[*relay]) probe.Act {
	return func(ctx context.Context) error {
		r := h.Subject()
		r.calls++
		return r.act(ctx, r)
	}
}

// reads returns the read of the value of the subject of h, which calls the
// first dependency of the subject.
func reads(h *conform.Handle[*relay]) probe.Call[int] {
	return func(context.Context) (int, error) {
		r := h.Subject()
		if err := r.deps[0].Call("Get"); err != nil {
			return 0, err
		}
		return r.value, nil
	}
}

// reporting calls the first dependency, and returns its error.
func reporting(_ context.Context, r *relay) error { return r.deps[0].Call("Get") }

// swallowing calls the first dependency, and returns no error.
func swallowing(_ context.Context, r *relay) error {
	_ = r.deps[0].Call("Get")
	return nil
}

// idle calls no dependency.
func idle(context.Context, *relay) error { return nil }

// failingAlone fails without a call of a dependency.
func failingAlone(context.Context, *relay) error { return errOther }

// latching fails each call once a call of the dependency failed.
func latching(_ context.Context, r *relay) error {
	if r.failed {
		return errOther
	}
	err := r.deps[0].Call("Get")
	r.failed = err != nil
	return err
}

// succeedingOnce calls the first dependency, and fails each call after its
// first success.
func succeedingOnce(_ context.Context, r *relay) error {
	if err := r.deps[0].Call("Get"); err != nil {
		return err
	}
	if r.failed {
		return errOther
	}
	r.failed = true
	return nil
}

// confirming stores the value once the dependency confirms the write.
func confirming(_ context.Context, r *relay) error {
	if err := r.deps[0].Call("Put"); err != nil {
		return err
	}
	r.value = 1
	return nil
}

// presuming stores the value before the dependency confirms the write.
func presuming(_ context.Context, r *relay) error {
	r.value = 1
	return r.deps[0].Call("Put")
}

// breaking refuses each call after three failures of its dependency in a row.
func breaking(_ context.Context, r *relay) error {
	if r.failures >= breakerLimit {
		return errOpen
	}
	err := r.deps[0].Call("Do")
	r.failures++
	if err == nil {
		r.failures = 0
	}
	return err
}

// rotating sends each call to the next dependency.
func rotating(_ context.Context, r *relay) error {
	return r.deps[r.calls%len(r.deps)].Call("Get")
}

// swapping calls both dependencies, in turns of order.
func swapping(_ context.Context, r *relay) error {
	first, second := r.deps[0], r.deps[1]
	if r.calls%2 == 0 {
		first, second = second, first
	}
	_ = first.Call("Get")
	return second.Call("Get")
}

// firstOnly calls the first dependency on the first call of the relay alone.
func firstOnly(_ context.Context, r *relay) error {
	if r.calls == 1 {
		return r.deps[0].Call("Get")
	}
	return nil
}

// secondOnly calls the first dependency from the second call of the relay on.
func secondOnly(_ context.Context, r *relay) error {
	if r.calls > 1 {
		return r.deps[0].Call("Get")
	}
	return nil
}

// releasing opens a resource, uses it and closes it, also after a use that
// fails.
func releasing(_ context.Context, r *relay) error {
	d := r.deps[0]
	if err := d.Call("Open"); err != nil {
		return err
	}
	defer func() { _ = d.Call("Close") }()
	return d.Call("Use")
}

// leaking opens a resource, uses it and closes it, and returns without the
// close after a use that fails.
func leaking(_ context.Context, r *relay) error {
	d := r.deps[0]
	if err := d.Call("Open"); err != nil {
		return err
	}
	if err := d.Call("Use"); err != nil {
		return err
	}
	return d.Call("Close")
}

// busy calls the first dependency 70 times.
func busy(_ context.Context, r *relay) error {
	for range busyCalls {
		_ = r.deps[0].Call("Use")
	}
	return nil
}

// scopes returns a scope of the subject of h that runs body and commits the
// buffered value, as the flags of the subject state.
func scopes(h *conform.Handle[*relay]) func(ctx context.Context, body probe.Act) error {
	return func(ctx context.Context, body probe.Act) error {
		r := h.Subject()
		r.pending = 0
		if err := body(ctx); err != nil {
			return err
		}
		if r.refuses {
			return errOther
		}
		if r.local {
			r.value = r.pending
			return nil
		}
		if err := r.deps[0].Call("Commit"); err != nil {
			if r.keeps {
				r.value = r.pending
			}
			if r.ignores {
				return nil
			}
			return err
		}
		r.value = r.pending
		return nil
	}
}

// buffers returns the write of the subject of h inside a scope, which fails
// with err.
func buffers(h *conform.Handle[*relay], err error) probe.Act {
	return func(context.Context) error {
		h.Subject().pending = 1
		return err
	}
}

// finds returns the read of the subject of h, which fails with errMiss for a
// value that no scope committed.
func finds(h *conform.Handle[*relay]) probe.Act {
	return func(context.Context) error {
		if h.Subject().value == 0 {
			return errMiss
		}
		return nil
	}
}

// failedBefore returns apply after a call of a dependency of the injector of
// the handle that fails, so a probe counts the failures of its own calls.
func failedBefore(apply func(h *conform.Handle[*relay])) func(h *conform.Handle[*relay]) {
	return func(h *conform.Handle[*relay]) {
		inj := h.Injector()
		inj.FailNext(errOther)
		_ = inj.Dependency().Call("Before")
		apply(h)
	}
}

// faulty is the machinery of the checks of the specs of failing dependencies.
var faulty = []spec.MachineryID{spec.CallThatFailsOnDemand}

// provesFaults applies a derived check of the probe p and of the machinery
// call-that-fails-on-demand to a relay that the door good builds, and
// falsifies it on a relay that bad builds, which fails it with reason.
func provesFaults(
	t *testing.T, p spec.ProbeID, reason spec.ReasonID, apply func(h *conform.Handle[*relay]),
	good func(*conform.Handle[*relay], *conform.Injector) *relay, bad func(*conform.Handle[*relay]) *relay,
) {
	t.Helper()
	conform.Check[*relay]{
		ID: "Relay.Call/claim", Statement: "the probe holds", Claim: "claim", Probe: p, Reason: reason,
		Machineries: faulty, Apply: apply, Defect: bad,
	}.Run(t, nil, conform.Faultable(good))
}

func TestFaults(t *testing.T) {
	t.Parallel()

	rollsBack := func(h *conform.Handle[*relay]) {
		probe.RollsBackUnderFaults(h, scopes(h), buffers(h, nil), finds(h), errMiss)
	}
	releases := func(h *conform.Handle[*relay]) { probe.ReleasesWhatItAcquires(h, runs(h), "Open", "Close") }
	tests := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*relay])
		giveGood  func(*conform.Handle[*relay], *conform.Injector) *relay
		giveBad   func(*conform.Handle[*relay]) *relay
		want      spec.ReasonID
	}{
		{
			name: "ReportsAFailedDependency", giveProbe: spec.ReportsAFailedDependency,
			giveApply: func(h *conform.Handle[*relay]) { probe.ReportsAFailedDependency(h, runs(h)) },
			giveGood:  relays(reporting, 1), giveBad: over(swallowing, 1), want: spec.SwallowedFailure,
		},
		{
			name: "FailsWithoutEffect", giveProbe: spec.FailsWithoutEffect,
			giveApply: func(h *conform.Handle[*relay]) { probe.FailsWithoutEffect(h, runs(h), reads(h)) },
			giveGood:  relays(confirming, 1), giveBad: over(presuming, 1), want: spec.PartialWrite,
		},
		{
			name: "SucceedsAfterAFailure", giveProbe: spec.SucceedsAfterAFailure,
			giveApply: func(h *conform.Handle[*relay]) { probe.SucceedsAfterAFailure(h, runs(h), 2) },
			giveGood:  relays(reporting, 1), giveBad: over(latching, 1), want: spec.NeverConverged,
		},
		{
			name: "SucceedsAfterAFailure stops at the first success", giveProbe: spec.SucceedsAfterAFailure,
			giveApply: func(h *conform.Handle[*relay]) { probe.SucceedsAfterAFailure(h, runs(h), 3) },
			giveGood:  relays(succeedingOnce, 1), giveBad: over(latching, 1), want: spec.NeverConverged,
		},
		{
			name: "RollsBackUnderFaults", giveProbe: spec.RollsBackUnderFaults, giveApply: rollsBack,
			giveGood: relays(idle, 1), giveBad: scoped(func(r *relay) { r.keeps = true }),
			want: spec.NotRolledBack,
		},
		{
			name: "RefusesWithoutTheDependency", giveProbe: spec.RefusesWithoutTheDependency,
			giveApply: func(h *conform.Handle[*relay]) { probe.RefusesWithoutTheDependency(h, runs(h)) },
			giveGood:  relays(breaking, 1), giveBad: over(reporting, 1), want: spec.NeverOpened,
		},
		{
			name: "UsesOneInstance", giveProbe: spec.UsesOneInstance,
			giveApply: func(h *conform.Handle[*relay]) { probe.UsesOneInstance(h, runs(h)) },
			giveGood:  relays(reporting, 2), giveBad: over(rotating, 2), want: spec.MovedInstance,
		},
		{
			name: "UsesOneInstance compares the dependencies of a call as a set", giveProbe: spec.UsesOneInstance,
			giveApply: func(h *conform.Handle[*relay]) { probe.UsesOneInstance(h, runs(h)) },
			giveGood:  relays(swapping, 2), giveBad: over(rotating, 2), want: spec.MovedInstance,
		},
		{
			name: "ReleasesWhatItAcquires", giveProbe: spec.ReleasesWhatItAcquires, giveApply: releases,
			giveGood: relays(releasing, 1), giveBad: over(leaking, 1), want: spec.LeakedResource,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("passes a correct subject and fails a planted defect with its reason", func(t *testing.T) {
				t.Parallel()

				provesFaults(t, tt.giveProbe, tt.want, tt.giveApply, tt.giveGood, tt.giveBad)
			})
		})
	}

	ends := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*relay])
		give      func(*conform.Handle[*relay]) *relay
	}{
		{
			name: "ReportsAFailedDependency is vacuous on a call without a dependency", give: over(idle, 1),
			giveProbe: spec.ReportsAFailedDependency,
			giveApply: func(h *conform.Handle[*relay]) { probe.ReportsAFailedDependency(h, runs(h)) },
		},
		{
			name: "FailsWithoutEffect is vacuous on a write without a dependency", give: over(idle, 1),
			giveProbe: spec.FailsWithoutEffect,
			giveApply: func(h *conform.Handle[*relay]) { probe.FailsWithoutEffect(h, runs(h), reads(h)) },
		},
		{
			name: "FailsWithoutEffect is vacuous on a write that succeeds", give: over(swallowing, 1),
			giveProbe: spec.FailsWithoutEffect,
			giveApply: func(h *conform.Handle[*relay]) { probe.FailsWithoutEffect(h, runs(h), reads(h)) },
		},
		{
			name:      "FailsWithoutEffect is vacuous on a write that fails without a dependency",
			give:      over(failingAlone, 1),
			giveProbe: spec.FailsWithoutEffect,
			giveApply: func(h *conform.Handle[*relay]) { probe.FailsWithoutEffect(h, runs(h), reads(h)) },
		},
		{
			name: "SucceedsAfterAFailure is vacuous on attempts without a dependency", give: over(idle, 1),
			giveProbe: spec.SucceedsAfterAFailure,
			giveApply: func(h *conform.Handle[*relay]) { probe.SucceedsAfterAFailure(h, runs(h), 2) },
		},
		{
			name: "RollsBackUnderFaults is vacuous on a write that fails", give: over(idle, 1),
			giveProbe: spec.RollsBackUnderFaults,
			giveApply: func(h *conform.Handle[*relay]) {
				probe.RollsBackUnderFaults(h, scopes(h), buffers(h, errOther), finds(h), errMiss)
			},
		},
		{
			name: "RollsBackUnderFaults is vacuous on a commit without a dependency", giveApply: rollsBack,
			giveProbe: spec.RollsBackUnderFaults, give: scoped(func(r *relay) { r.local = true }),
		},
		{
			name: "RollsBackUnderFaults is vacuous on a scope that succeeds", giveApply: rollsBack,
			giveProbe: spec.RollsBackUnderFaults, give: scoped(func(r *relay) { r.ignores = true }),
		},
		{
			name: "RollsBackUnderFaults is vacuous on a scope that fails without a dependency", giveApply: rollsBack,
			giveProbe: spec.RollsBackUnderFaults, give: scoped(func(r *relay) { r.refuses = true }),
		},
		{
			name: "RefusesWithoutTheDependency is vacuous on a first call without a dependency", give: over(idle, 1),
			giveProbe: spec.RefusesWithoutTheDependency,
			giveApply: func(h *conform.Handle[*relay]) { probe.RefusesWithoutTheDependency(h, runs(h)) },
		},
		{
			name: "UsesOneInstance is vacuous on calls without a dependency", give: over(idle, 1),
			giveProbe: spec.UsesOneInstance,
			giveApply: func(h *conform.Handle[*relay]) { probe.UsesOneInstance(h, runs(h)) },
		},
		{
			name: "UsesOneInstance is vacuous on a second call without a dependency", give: over(firstOnly, 1),
			giveProbe: spec.UsesOneInstance,
			giveApply: func(h *conform.Handle[*relay]) { probe.UsesOneInstance(h, runs(h)) },
		},
		{
			name: "UsesOneInstance is vacuous on a first call without a dependency", give: over(secondOnly, 1),
			giveProbe: spec.UsesOneInstance,
			giveApply: func(h *conform.Handle[*relay]) { probe.UsesOneInstance(h, runs(h)) },
		},
		{
			name: "ReleasesWhatItAcquires is vacuous on a call without a dependency", give: over(idle, 1),
			giveProbe: spec.ReleasesWhatItAcquires, giveApply: releases,
		},
		{
			name: "ReportsAFailedDependency is vacuous after a failure before it", give: over(idle, 1),
			giveProbe: spec.ReportsAFailedDependency,
			giveApply: failedBefore(func(h *conform.Handle[*relay]) { probe.ReportsAFailedDependency(h, runs(h)) }),
		},
		{
			name: "FailsWithoutEffect is vacuous after a failure before it", give: over(failingAlone, 1),
			giveProbe: spec.FailsWithoutEffect,
			giveApply: failedBefore(func(h *conform.Handle[*relay]) { probe.FailsWithoutEffect(h, runs(h), reads(h)) }),
		},
		{
			name: "SucceedsAfterAFailure is vacuous after a failure before it", give: over(idle, 1),
			giveProbe: spec.SucceedsAfterAFailure,
			giveApply: failedBefore(func(h *conform.Handle[*relay]) { probe.SucceedsAfterAFailure(h, runs(h), 2) }),
		},
		{
			name: "RollsBackUnderFaults is vacuous after a failure before it", giveApply: failedBefore(rollsBack),
			giveProbe: spec.RollsBackUnderFaults, give: scoped(func(r *relay) { r.refuses = true }),
		},
	}
	for _, tt := range ends {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vacuous(t, tt.giveProbe, tt.giveApply, tt.give, nil)
		})
	}

	t.Run("leaves no failure armed after each probe", func(t *testing.T) {
		t.Parallel()

		armed := []struct {
			name      string
			giveProbe spec.ProbeID
			giveApply func(h *conform.Handle[*relay])
			give      func(*conform.Handle[*relay], *conform.Injector) *relay
		}{
			{
				name: "ReportsAFailedDependency", giveProbe: spec.ReportsAFailedDependency,
				give: relays(reporting, 1), giveApply: tests[0].giveApply,
			},
			{
				name: "FailsWithoutEffect", giveProbe: spec.FailsWithoutEffect,
				give: relays(confirming, 1), giveApply: tests[1].giveApply,
			},
			{
				name: "SucceedsAfterAFailure", giveProbe: spec.SucceedsAfterAFailure,
				give: relays(idle, 1), giveApply: tests[2].giveApply,
			},
			{
				name: "RollsBackUnderFaults", giveProbe: spec.RollsBackUnderFaults, giveApply: rollsBack,
				give: func(h *conform.Handle[*relay], _ *conform.Injector) *relay {
					return scoped(func(r *relay) { r.local = true })(h)
				},
			},
			{
				name: "RefusesWithoutTheDependency", giveProbe: spec.RefusesWithoutTheDependency,
				give: relays(breaking, 1), giveApply: tests[5].giveApply,
			},
			{
				name: "ReleasesWhatItAcquires", giveProbe: spec.ReleasesWhatItAcquires,
				give: relays(releasing, 1), giveApply: releases,
			},
		}
		for _, tt := range armed {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				conform.Check[*relay]{
					ID: "Relay.Call/claim", Statement: "the probe holds", Probe: tt.giveProbe, Machineries: faulty,
					Apply: func(h *conform.Handle[*relay]) {
						tt.giveApply(h)
						h.Engage()
						assert.NoError(h, h.Injector().Dependency().Call("After"), h.Statement())
					},
				}.Run(t, nil, conform.Faultable(tt.give))
			})
		}
	})

	t.Run("ReleasesWhatItAcquires stops after the step whose call makes fewer calls", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			give      relayAct
			wantSteps int
		}{
			{name: "of a dependency than its number", give: releasing, wantSteps: 4},
			{name: "of a dependency than the 64 steps", give: busy, wantSteps: 64},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				conform.Check[*relay]{
					ID: "Relay.Call/claim", Statement: "the probe holds", Probe: spec.ReleasesWhatItAcquires,
					Machineries: faulty,
					Apply: func(h *conform.Handle[*relay]) {
						releases(h)
						assert.Equal(h, h.Subject().calls, tt.wantSteps, h.Statement())
					},
				}.Run(t, nil, conform.Faultable(relays(tt.give, 1)))
			})
		}
	})
}
