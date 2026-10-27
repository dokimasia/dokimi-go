// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/stub"
)

// Answer returns the results of the first arm that applies, in the order
// fault, function, fixed result, default results and zero results, and
// records the call on each arm.
func TestAnswer(t *testing.T) {
	t.Parallel()

	t.Run("Answer", func(t *testing.T) {
		t.Parallel()

		override := func() ret { return ret{Value: "override"} }
		fallback := &ret{Value: "fallback"}
		defaults := func() ret { return ret{Value: "default"} }
		tests := []struct {
			name         string
			giveFault    bool
			giveInvoke   func() ret
			giveFallback *ret
			giveDefaults func() ret
			wantValue    string
			wantErr      error
		}{
			{
				name: "prefers an injected fault over a Func override", giveFault: true, giveInvoke: override,
				giveFallback: fallback, giveDefaults: defaults, wantErr: errTest,
			},
			{
				name: "prefers a Func override over a Returns fallback", giveInvoke: override, giveFallback: fallback,
				giveDefaults: defaults, wantValue: "override",
			},
			{
				name: "prefers a Returns fallback over the default results", giveFallback: fallback,
				giveDefaults: defaults, wantValue: "fallback",
			},
			{
				name: "returns the default results without a configuration", giveDefaults: defaults,
				wantValue: "default",
			},
			{name: "returns the zero results without default results"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				s := stub.NewMethodStub[call](t, getName)
				if tt.giveFault {
					s.Faults(errTest, 1)
				}
				a := arms()
				a.Invoke, a.Fallback, a.Default = tt.giveInvoke, tt.giveFallback, tt.giveDefaults
				got := stub.Answer(s, call{Key: "k"}, a)
				expect.Equal(t, got, ret{Value: tt.wantValue, Err: tt.wantErr},
					"the results are the results of the first arm that applies")
				expect.Equal(t, s.AssertCalledOnce(t, "the arm records the call"), call{Key: "k", Err: tt.wantErr},
					"the recorded call has the stamped error")
			})
		}

		t.Run("fails a fault on a method without an error result", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			s.Faults(errTest, 1)
			a := arms()
			a.Fault, a.Fallback = nil, &ret{Value: "fallback"}
			got := stub.Answer(s, call{}, a)
			expect.True(t, r.Failed(), "a fault that the method cannot report stops the test")
			expect.Equal(t, got.Value, "fallback", "the call still returns its fixed result")
		})

		t.Run("passes an unfaulted method without an error result", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			a := arms()
			a.Fault, a.Fallback = nil, &ret{Value: "fallback"}
			got := stub.Answer(s, call{}, a)
			expect.False(t, r.Failed(), "a method that cannot fail without a fault reports nothing")
			expect.Equal(t, got.Value, "fallback", "the call returns its fixed result")
		})

		t.Run("fails an unconfigured call in strict mode", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			s.Strict()
			stub.Answer(s, call{}, arms())
			assert.True(t, r.Failed(), "strict mode refuses an unconfigured call")
		})

		t.Run("fails an unconfigured call with default results in strict mode", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			s.Strict()
			a := arms()
			a.Default = func() ret { return ret{Value: "default"} }
			got := stub.Answer(s, call{}, a)
			expect.True(t, r.Failed(), "strict mode refuses an unconfigured call that has default results")
			expect.Equal(t, got.Value, "default", "the refused call still returns its default results")
		})

		t.Run("records a call without a stamp", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](t, getName)
			stub.Answer(s, call{Key: "k"}, stub.Arms[call, struct{}]{})
			assert.Equal(t, s.AssertCalledOnce(t, "the call is recorded"), call{Key: "k"}, "the call keeps its key")
		})
	})
}

// The allocation ceiling of a call counts the allocations of the whole
// process, so neither the test nor its subtests run in parallel.
func TestAnswerAllocs(t *testing.T) {
	t.Run("Answer", func(t *testing.T) {
		t.Run("does not allocate in bench mode", func(t *testing.T) {
			if checked, reason := assert.AllocsChecked(); !checked {
				t.Skipf("the run does not check allocation ceilings: %s", reason)
			}
			f := newFake(nil)
			f.on.BenchMode()
			f.fn = func(string) error { return nil }
			assert.MaxAllocs(t, func() { _, _ = f.Get("k") }, 0,
				"a call of a double in bench mode allocates only what its function allocates")
		})
	})
}
