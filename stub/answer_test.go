// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/dokimi-go/stub"
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
			fault        bool
			invoke       func() ret
			fallback     *ret
			giveDefaults func() ret
			wantValue    string
			wantErr      error
		}{
			{
				name: "prefers an injected fault over a Func override", fault: true, invoke: override,
				fallback: fallback, giveDefaults: defaults, wantErr: errTest,
			},
			{
				name: "prefers a Func override over a Returns fallback", invoke: override, fallback: fallback,
				giveDefaults: defaults, wantValue: "override",
			},
			{
				name: "prefers a Returns fallback over the default results", fallback: fallback, giveDefaults: defaults,
				wantValue: "fallback",
			},
			{
				name: "answers with the default results when nothing is configured", giveDefaults: defaults,
				wantValue: "default",
			},
			{name: "answers with the zero value when nothing is configured and no default exists"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				s := stub.NewMethodStub[call](t, getName)
				if tt.fault {
					s.Faults(errTest, 1)
				}
				a := arms()
				a.Invoke, a.Fallback, a.Default = tt.invoke, tt.fallback, tt.giveDefaults
				got := stub.Answer(s, &call{Key: "k"}, a)
				expect.Equal(t, got, ret{Value: tt.wantValue, Err: tt.wantErr}, "the first arm that applies answers")
				expect.Equal(t, s.AssertCalledOnce(t, "the arm records the call"), call{Key: "k", Err: tt.wantErr},
					"the recorded call has the stamped error")
			})
		}

		t.Run("stops the test for a fault on a method without an error result", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			s.Faults(errTest, 1)
			a := arms()
			a.Fault, a.Fallback = nil, &ret{Value: "fallback"}
			got := stub.Answer(s, &call{}, a)
			expect.True(t, r.Failed(), "a fault that the method cannot report stops the test")
			expect.Equal(t, got.Value, "fallback", "the call still answers with its fixed result")
		})

		t.Run("passes a method without an error result and without a fault", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			a := arms()
			a.Fault, a.Fallback = nil, &ret{Value: "fallback"}
			got := stub.Answer(s, &call{}, a)
			expect.False(t, r.Failed(), "a method that cannot fail and has no fault reports nothing")
			expect.Equal(t, got.Value, "fallback", "the call answers with its fixed result")
		})

		t.Run("stops the test for an unconfigured call in strict mode", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			s.Strict()
			stub.Answer(s, &call{}, arms())
			assert.True(t, r.Failed(), "strict mode refuses an unconfigured call")
		})

		t.Run("stops the test for an unconfigured call with default results in strict mode", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			s := stub.NewMethodStub[call](r, getName)
			s.Strict()
			a := arms()
			a.Default = func() ret { return ret{Value: "default"} }
			got := stub.Answer(s, &call{}, a)
			expect.True(t, r.Failed(), "strict mode refuses an unconfigured call that has default results")
			expect.Equal(t, got.Value, "default", "the refused call still answers with its default results")
		})

		t.Run("records a call without a stamp", func(t *testing.T) {
			t.Parallel()

			s := stub.NewMethodStub[call](t, getName)
			stub.Answer(s, &call{Key: "k"}, stub.Arms[call, struct{}]{})
			assert.Equal(t, s.AssertCalledOnce(t, "the call is recorded"), call{Key: "k"}, "the call keeps its key")
		})
	})
}
