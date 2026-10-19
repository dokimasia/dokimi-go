// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"os"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/dokimi-go/stub"
)

// fake is a double of Store.Get as a generated double writes it: the stub of
// the method, a function, and a method that answers through stub.Answer.
type fake struct {
	on *stub.MethodStub[call]
	fn func(key string) error
}

// newFake returns a fake that reports to tb.
func newFake(tb assert.TB) *fake {
	return &fake{on: stub.NewMethodStub[call](tb, getName)}
}

// Get answers the call of key.
func (f *fake) Get(key string) error {
	a := arms()
	if f.fn != nil {
		a.Invoke = func() ret { return ret{Err: f.fn(key)} }
	}
	return stub.Answer(f.on, &call{Key: key}, a).Err
}

// subject returns the subject of Get on f.
func (f *fake) subject() stub.Subject[call, ret] {
	return stub.Subject[call, ret]{
		Stub:   f.on,
		Call:   func() { _ = f.Get("k") },
		Fails:  func() error { return f.Get("k") },
		Result: func() ret { return ret{Err: f.Get("k")} },
		Override: func(mark func()) {
			f.fn = func(string) error {
				mark()
				return nil
			}
		},
	}
}

// subject returns the subject of Get on a new fake that reports to tb.
func subject(tb assert.TB) stub.Subject[call, ret] {
	return newFake(tb).subject()
}

// Behaviour passes a double that keeps the contract of a method, and fails
// each check that a broken double breaks.
func TestBehaviour(t *testing.T) {
	t.Parallel()

	t.Run("Behaviour", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a double that keeps the contract", func(t *testing.T) {
			t.Parallel()

			stub.Behaviour(t, "Get", subject)
		})

		t.Run("leaves out the checks of the results and the faults of a method without them", func(t *testing.T) {
			t.Parallel()

			stub.Behaviour(t, "Close", func(tb assert.TB) stub.Subject[call, ret] {
				s := subject(tb)
				s.Fails, s.Result, s.Override = nil, nil, nil
				return s
			})
		})

		t.Run("runs each check as a parallel subtest", func(t *testing.T) {
			t.Parallel()

			var checks atomic.Int32
			stub.Behaviour(t, "Get", func(tb assert.TB) stub.Subject[call, ret] {
				if tb != assert.TB(t) {
					checks.Add(1)
				}
				return subject(tb)
			})
			assert.Equal(t, checks.Load(), int32(0), "no check starts before the test returns")
		})

		tests := []struct {
			name string
			give func(tb assert.TB) stub.Subject[call, ret]
			want []string
		}{
			{
				name: "fails the checks of the failures for a double that reports to no TB",
				give: func(assert.TB) stub.Subject[call, ret] { return subject(nil) },
				want: []string{
					"reports_an_unmet_call_count",
					"reports_an_unmet_minimum_call_count",
					"refuses_an_unconfigured_call_in_strict_mode",
				},
			},
			{
				name: "fails the checks of the count for a double whose call records twice",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					s := subject(tb)
					once := s.Call
					s.Call = func() {
						once()
						s.Stub.Record(call{})
					}
					return s
				},
				want: []string{
					"records_the_call",
					"reports_an_unmet_call_count",
					"reports_an_unmet_minimum_call_count",
				},
			},
			{
				name: "fails the check of the zero results for a double that answers a value",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					s := subject(tb)
					result := s.Result
					s.Result = func() ret {
						r := result()
						r.Value = "value"
						return r
					}
					return s
				},
				want: []string{"answers_with_the_zero_value_when_unconfigured"},
			},
			{
				name: "fails the check of the function for a double that ignores the function",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					s := subject(tb)
					s.Override = func(func()) {}
					return s
				},
				want: []string{"dispatches_to_the_Func_override"},
			},
			{
				name: "fails the checks of the error for a double that drops the error of a fault",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					s := subject(tb)
					s.Fails = func() error {
						s.Call()
						return nil
					}
					return s
				},
				want: []string{"returns_an_injected_fault", "fires_a_counted_fault_on_the_nth_call"},
			},
			{
				name: "fails the check of the record of a fault for a double that records no failed call",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					s := subject(tb)
					s.Fails = func() error {
						if fired, err := s.Stub.ShouldFaultFor(call{}); fired {
							return err
						}
						s.Stub.Record(call{})
						return nil
					}
					return s
				},
				want: []string{"records_the_injected_fault_on_the_call"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if os.Getenv(suiteEnv) == t.Name() {
					stub.Behaviour(t, "Get", tt.give)
					return
				}
				assertFailedChecks(t, tt.want...)
			})
		}
	})
}
