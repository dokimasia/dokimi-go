// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

import (
	"errors"
	"testing"

	"go.dokimi.dev/assert"
)

// Subject is one method of a generated double, as [Behaviour] and
// [ResetBehaviour] drive it. C is the type of the calls of the method, and R
// the type of its results.
//
// Stub, Call and Reset are required. Arguments is nil for a method without
// parameters, and Fails for a method that cannot fail. Result and Returns are
// nil for a method without results, and Override is nil for a method whose
// function a check cannot set. Behaviour and ResetBehaviour leave out each
// check that needs a nil field.
type Subject[C, R any] struct {
	// Stub is the stub of the method on the double.
	Stub *MethodStub[C]
	// Call calls the method with the samples of its arguments, and drops its
	// results.
	Call func()
	// Arguments checks on tb that the call c has the samples of the arguments
	// of Call.
	Arguments func(tb assert.TB, c C)
	// Reset resets the double, as its method ResetCalls does.
	Reset func()
	// Fails calls the method and returns its error.
	Fails func() error
	// Result calls the method and returns its results.
	Result func() R
	// Default returns the results of an unconfigured call, and is nil where
	// they are the zero results.
	Default func() R
	// Returns sets the fixed results of the method to samples, and returns
	// the samples.
	Returns func() R
	// Override sets a function of the method that passes the call of its
	// arguments to seen and returns the zero results.
	Override func(seen func(c C))
}

// Behaviour checks the contract that each method of a generated double keeps,
// whatever its signature. Each check is a parallel subtest of t. newSubject
// returns the subject of the method on a new double that reports to tb.
// Behaviour calls it once for each check, so no check sees the calls of
// another. A check of a failure passes it an [assert.Recorder]. name is the
// name of the method in each message.
//
// The checks cover the recording of a call with its arguments, the hook
// OnRecord, the expected counts of calls, strict mode, the default and the
// fixed results, the function of the method and the faults of a method that
// can fail. The check of the fixed results first checks that the samples of
// Returns differ from the default results, because a check of equal values
// passes a double that ignores Returns.
func Behaviour[C, R any](t *testing.T, name string, newSubject func(tb assert.TB) Subject[C, R]) {
	t.Helper()

	t.Run("records the call", func(t *testing.T) {
		t.Parallel()
		s := newSubject(t)
		s.Call()
		s.Stub.AssertCalledOnce(t, name+" records every call")
	})

	if newSubject(t).Arguments != nil {
		t.Run("records the arguments of the call", func(t *testing.T) {
			t.Parallel()
			s := newSubject(t)
			s.Call()
			s.Arguments(t, s.Stub.AssertCalledOnce(t, name+" records every call"))
		})
	}

	t.Run("runs the OnRecord hook once for each call", func(t *testing.T) {
		t.Parallel()
		s := newSubject(t)
		var seen []C
		s.Stub.OnRecord(func(c C) { seen = append(seen, c) })
		s.Call()
		s.Call()
		assert.Length(t, seen, 2, "OnRecord runs once for each call of "+name)
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
			assert.Equal(t, s.Result(), defaults(&s), "an unconfigured call of "+name+" returns its default results")
		})
	}
	if probe.Returns != nil {
		t.Run("returns the fixed results of Returns", func(t *testing.T) {
			t.Parallel()
			s := newSubject(t)
			want := s.Returns()
			assert.NotEqual(t, want, defaults(&s), "the samples of Returns of "+name+" differ from its default results")
			assert.Equal(t, s.Result(), want, name+" returns the fixed results of Returns")
		})

		t.Run("accepts a call with fixed results in strict mode", func(t *testing.T) {
			t.Parallel()
			r := assert.NewRecorder()
			s := newSubject(r)
			s.Stub.Strict()
			s.Returns()
			s.Call()
			assert.False(t, r.Failed(), "strict mode accepts a call of "+name+" with fixed results")
		})
	}
	if probe.Override != nil {
		t.Run("passes the arguments of each call to the function of Func", func(t *testing.T) {
			t.Parallel()
			s := newSubject(t)
			var seen []C
			s.Override(func(c C) { seen = append(seen, c) })
			s.Call()
			assert.Length(t, seen, 1, name+" calls the function that the test set once for each call")
			if s.Arguments != nil {
				s.Arguments(t, seen[0])
			}
		})
	}
}

// defaults returns the results of an unconfigured call of the method of s.
func defaults[C, R any](s *Subject[C, R]) R {
	var zero R
	if s.Default == nil {
		return zero
	}
	return s.Default()
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
