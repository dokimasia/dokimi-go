// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"os"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/stub"
)

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

		t.Run("passes a double whose unconfigured call returns its default results", func(t *testing.T) {
			t.Parallel()

			stub.Behaviour(t, "Get", func(tb assert.TB) stub.Subject[call, ret] {
				f := newFake(tb)
				f.defaults = func() ret { return ret{Value: "default"} }
				return f.subject()
			})
		})

		t.Run("leaves out the checks that a method without results cannot pass", func(t *testing.T) {
			t.Parallel()

			stub.Behaviour(t, "Close", func(tb assert.TB) stub.Subject[call, ret] {
				s := subject(tb)
				s.Fails, s.Result, s.Override = nil, nil, nil
				return s
			})
		})

		t.Run("runs each check in parallel", func(t *testing.T) {
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
				name: "fails the check of the default results for a double that returns a value",
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
				want: []string{"returns_the_default_results_of_an_unconfigured_call"},
			},
			{
				name: "fails the check of the default results for a double that ignores its default results",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					s := subject(tb)
					s.Default = func() ret { return ret{Value: "default"} }
					return s
				},
				want: []string{"returns_the_default_results_of_an_unconfigured_call"},
			},
			{
				name: "fails the check of the function for a double that ignores the function",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					s := subject(tb)
					s.Override = func(func()) {}
					return s
				},
				want: []string{"calls_the_function_of_Func"},
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
				want: []string{"returns_an_injected_fault", "fails_the_call_that_the_count_of_Faults_names"},
			},
			{
				name: "fails the check of the record of a fault for a double that records no failed call",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					s := subject(tb)
					s.Fails = func() error {
						if faulted, err := s.Stub.ShouldFaultFor(call{}); faulted {
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

// subject returns the subject of Get on f.
func (f *fake) subject() stub.Subject[call, ret] {
	return stub.Subject[call, ret]{
		Stub: f.on,
		Call: func() { _, _ = f.Get("k") },
		Fails: func() error {
			_, err := f.Get("k")
			return err
		},
		Result: func() ret {
			value, err := f.Get("k")
			return ret{Value: value, Err: err}
		},
		Default: f.defaults,
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
