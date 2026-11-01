// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"context"
	"slices"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// The bounds of the calls of a probe of failing dependencies:
// breakerCalls is the most calls of RefusesWithoutTheDependency, and
// releaseSteps the most steps of ReleasesWhatItAcquires.
const (
	breakerCalls = 100
	releaseSteps = 64
)

// ReportsAFailedDependency makes every call of a dependency fail, calls call,
// and fails when the call returns no error. A call that calls no dependency
// ends the probe.
func ReportsAFailedDependency[S any](h *conform.Handle[S], call Act) {
	h.Helper()
	inj := h.Injector()
	before := inj.Failures()
	inj.FailUntilHealed(conform.ErrInjected)
	err := call(h.Context())
	inj.Heal()
	if inj.Failures() == before {
		return
	}
	h.Engage()
	h.Expect(spec.SwallowedFailure)
	assert.HasError(h, err, h.Statement())
}

// FailsWithoutEffect reads through read and makes every call of a dependency
// fail. It then calls call, calls Heal of the injector and reads again. It
// fails when the second read returns a value other than the first. A call that
// succeeds, or that calls no dependency, ends the probe.
func FailsWithoutEffect[S, V any](h *conform.Handle[S], call Act, read Call[V]) {
	h.Helper()
	was, _ := read(h.Context())
	inj := h.Injector()
	before := inj.Failures()
	inj.FailUntilHealed(conform.ErrInjected)
	err := call(h.Context())
	inj.Heal()
	if err == nil || inj.Failures() == before {
		return
	}
	h.Engage()
	is, _ := read(h.Context())
	h.Expect(spec.PartialWrite)
	assert.Equal(h, is, was, h.Statement())
}

// SucceedsAfterAFailure fails the next call of a dependency, then calls call
// up to attempts times, and fails when no call succeeds. Attempts that call no
// dependency end the probe.
func SucceedsAfterAFailure[S any](h *conform.Handle[S], call Act, attempts int) {
	h.Helper()
	inj := h.Injector()
	before := inj.Failures()
	inj.FailNext(conform.ErrInjected)
	var err error
	for range attempts {
		if err = call(h.Context()); err == nil {
			break
		}
	}
	inj.Heal()
	if inj.Failures() == before {
		return
	}
	h.Engage()
	h.Expect(spec.NeverConverged)
	assert.NoError(h, err, h.Statement())
}

// RollsBackUnderFaults runs scope with a body that writes and then fails the
// next call of a dependency, so the commit of the scope fails. It calls Heal of
// the injector, reads, and fails unless the error of read matches sentinel. A
// write inside the scope that fails, a scope that succeeds, and a commit that
// calls no dependency end the probe.
func RollsBackUnderFaults[S any](
	h *conform.Handle[S], scope func(ctx context.Context, body Act) error, write, read Act, sentinel error,
) {
	h.Helper()
	inj := h.Injector()
	before := inj.Failures()
	err := scope(h.Context(), func(ctx context.Context) error {
		if write(ctx) == nil {
			inj.FailNext(conform.ErrInjected)
		}
		return nil
	})
	inj.Heal()
	if err == nil || inj.Failures() == before {
		return
	}
	h.Engage()
	got := read(h.Context())
	h.Expect(spec.NotRolledBack)
	assert.ErrorIs(h, got, sentinel, h.Statement())
}

// RefusesWithoutTheDependency makes every call of a dependency fail and calls
// call up to 100 times. It fails when the subject calls a dependency during
// each of the calls. A first call that calls no dependency ends the probe.
func RefusesWithoutTheDependency[S any](h *conform.Handle[S], call Act) {
	h.Helper()
	inj := h.Injector()
	inj.FailUntilHealed(conform.ErrInjected)
	defer inj.Heal()
	if len(dependenciesOf(h, inj, call)) == 0 {
		return
	}
	h.Engage()
	opened := false
	for range breakerCalls - 1 {
		if len(dependenciesOf(h, inj, call)) == 0 {
			opened = true
			break
		}
	}
	h.Expect(spec.NeverOpened)
	assert.True(h, opened, h.Statement())
}

// UsesOneInstance calls call twice, and fails when the subject calls other
// dependencies for the second call than for the first. A call that calls no
// dependency ends the probe.
func UsesOneInstance[S any](h *conform.Handle[S], call Act) {
	h.Helper()
	inj := h.Injector()
	first := dependenciesOf(h, inj, call)
	second := dependenciesOf(h, inj, call)
	if len(first) == 0 || len(second) == 0 {
		return
	}
	h.Engage()
	h.Expect(spec.MovedInstance)
	assert.Equal(h, second, first, h.Statement())
}

// ReleasesWhatItAcquires calls call once for each step n from 1, with the n-th
// call of a dependency failing. acquire and release are the callables of the
// dependencies that the params open and close of the mixin leak-free name. It
// fails when the calls of release differ in number from the calls of acquire
// that succeeded, after a step. A call of acquire that failed acquired
// nothing. It stops after the first step whose call makes fewer than n calls of
// a dependency, and after 64 steps. A first call that calls no dependency ends
// the probe.
func ReleasesWhatItAcquires[S any](h *conform.Handle[S], call Act, acquire, release string) {
	h.Helper()
	inj := h.Injector()
	for n := 1; n <= releaseSteps; n++ {
		before := len(inj.Calls())
		inj.FailAt(n, conform.ErrInjected)
		_ = call(h.Context())
		inj.Heal()
		calls := inj.Calls()
		made := len(calls) - before
		if made == 0 {
			return
		}
		h.Engage()
		acquired, released := 0, 0
		for _, c := range calls {
			switch {
			case c.Callable == acquire && !c.Failed:
				acquired++
			case c.Callable == release:
				released++
			}
		}
		h.Expect(spec.LeakedResource)
		assert.Equal(h, released, acquired, h.Statement())
		if made < n {
			return
		}
	}
}

// dependenciesOf calls call and returns the numbers of the dependencies that
// the call called, sorted and without repeats.
func dependenciesOf[S any](h *conform.Handle[S], inj *conform.Injector, call Act) []int {
	before := len(inj.Calls())
	_ = call(h.Context())
	calls := inj.Calls()[before:]
	out := make([]int, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Dependency)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
