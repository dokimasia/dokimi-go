// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

import (
	"errors"
	"testing"

	"go.dokimi.dev/assert"
)

// Subject is one method of a generated double, as [Behaviour] drives it. C
// is the type of the calls of the method, and R the type of its results.
//
// Call is required. Fails is nil for a method that cannot fail, Result is nil
// for a method without results, and Override is nil for a method whose
// function a check cannot set. Behaviour leaves out each check that needs a
// nil field.
type Subject[C, R any] struct {
	// Stub is the stub of the method on the double.
	Stub *MethodStub[C]
	// Call calls the method with zero arguments, and drops its results.
	Call func()
	// Fails calls the method and returns its error.
	Fails func() error
	// Result calls the method and returns its results.
	Result func() R
	// Default returns the results of an unconfigured call, and is nil where
	// they are the zero results.
	Default func() R
	// Override sets a function of the method that calls mark and returns the
	// zero results.
	Override func(mark func())
}

// Behaviour checks the contract that each method of a generated double keeps,
// whatever its signature. Each check is a parallel subtest of t. newSubject
// returns the subject of the method on a new double that reports to tb.
// Behaviour calls it once for each check, so no check sees the calls of
// another. A check of a failure passes it an [assert.Recorder]. name is the
// name of the method in each message.
//
// The checks cover the recording of a call, the expected counts of calls,
// strict mode, the default results of an unconfigured call, the function of
// the method and the faults of a method that can fail. A check that the
// method returns its fixed result needs a value that differs from the
// default results. A generated test writes that check with a sample of the
// results.
func Behaviour[C, R any](t *testing.T, name string, newSubject func(tb assert.TB) Subject[C, R]) {
	t.Helper()

	t.Run("records the call", func(t *testing.T) {
		t.Parallel()
		s := newSubject(t)
		s.Call()
		s.Stub.AssertCalledOnce(t, name+" records every call")
	})

	t.Run("reports an unmet call count", func(t *testing.T) {
		t.Parallel()
		// Verify runs here and not through the cleanup of the double. The
		// check then also applies to a double that registers no cleanup.
		r := assert.NewRecorder()
		s := newSubject(r)
		s.Stub.Times(2)
		s.Call()
		s.Stub.Verify()
		assert.True(t, r.Failed(), name+" reports a count of calls below the count of Times")
	})

	t.Run("reports an unmet minimum call count", func(t *testing.T) {
		t.Parallel()
		r := assert.NewRecorder()
		s := newSubject(r)
		s.Stub.TimesAtLeast(2)
		s.Call()
		s.Stub.Verify()
		assert.True(t, r.Failed(), name+" reports a count of calls below the count of TimesAtLeast")
	})

	t.Run("refuses an unconfigured call in strict mode", func(t *testing.T) {
		t.Parallel()
		r := assert.NewRecorder()
		s := newSubject(r)
		s.Stub.Strict()
		s.Call()
		assert.True(t, r.Failed(), "strict mode refuses an unconfigured call of "+name)
	})

	behaviourResults(t, name, newSubject)
	behaviourFaults(t, name, newSubject)
}

// behaviourResults runs the checks of the results of a call, each where the
// subject has the field that the check needs.
func behaviourResults[C, R any](t *testing.T, name string, newSubject func(tb assert.TB) Subject[C, R]) {
	t.Helper()

	probe := newSubject(t)
	if probe.Result != nil {
		t.Run("returns the default results of an unconfigured call", func(t *testing.T) {
			t.Parallel()
			s := newSubject(t)
			var want R
			if s.Default != nil {
				want = s.Default()
			}
			assert.Equal(t, s.Result(), want, "an unconfigured call of "+name+" returns its default results")
		})
	}
	if probe.Override != nil {
		t.Run("calls the function of Func", func(t *testing.T) {
			t.Parallel()
			called := false
			s := newSubject(t)
			s.Override(func() { called = true })
			s.Call()
			assert.True(t, called, name+" calls the function that the test set")
		})
	}
}

// behaviourFaults runs the checks of the faults of a method that can fail.
func behaviourFaults[C, R any](t *testing.T, name string, newSubject func(tb assert.TB) Subject[C, R]) {
	t.Helper()

	if newSubject(t).Fails == nil {
		return
	}
	errFault := errors.New(name + "-behaviour")

	t.Run("returns an injected fault", func(t *testing.T) {
		t.Parallel()
		s := newSubject(t)
		s.Stub.Faults(errFault, 1)
		assert.ErrorIs(t, s.Fails(), errFault, name+" returns the error of the armed fault")
	})

	t.Run("fails the call that the count of Faults names", func(t *testing.T) {
		t.Parallel()
		// A fault on the first call cannot tell a retry loop that works from
		// one that never runs. A fault on the third call can.
		s := newSubject(t)
		s.Stub.Faults(errFault, 3)
		assert.NoError(t, s.Fails(), "the first call of "+name+" succeeds")
		assert.NoError(t, s.Fails(), "the second call of "+name+" succeeds")
		assert.ErrorIs(t, s.Fails(), errFault, "the third call of "+name+" fails")
	})

	t.Run("records the injected fault on the call", func(t *testing.T) {
		t.Parallel()
		s := newSubject(t)
		s.Stub.Faults(errFault, 1)
		_ = s.Fails()
		s.Stub.AssertCalledOnce(t, name+" records a call that a fault failed")
	})
}
