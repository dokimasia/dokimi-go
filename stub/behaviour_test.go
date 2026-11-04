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
				s.Fails, s.Result, s.Returns, s.Override = nil, nil, nil, nil
				return s
			})
		})

		t.Run("leaves out the check of the arguments for a method without parameters", func(t *testing.T) {
			t.Parallel()

			stub.Behaviour(t, "Ping", func(tb assert.TB) stub.Subject[call, ret] {
				s := subject(tb)
				s.Arguments = nil
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
					"records_the_arguments_of_the_call",
					"runs_the_OnRecord_hook_once_for_each_call",
					"reports_an_unmet_call_count",
					"reports_an_unmet_minimum_call_count",
				},
			},
			{
				name: "fails the checks of the arguments for a double that records another key",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					f := newFake(tb)
					s := f.subject()
					s.Call = func() { _, _ = f.Get("other") }
					return s
				},
				want: []string{
					"records_the_arguments_of_the_call",
					"passes_the_arguments_of_each_call_to_the_function_of_Func",
				},
			},
			{
				name: "fails the checks of the results for a double that returns a value",
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
				want: []string{
					"returns_the_default_results_of_an_unconfigured_call",
					"returns_the_fixed_results_of_Returns",
				},
			},
			{
				name: "fails the checks of the fixed results for a double that ignores Returns",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					s := subject(tb)
					s.Returns = func() ret { return ret{Value: "fixed"} }
					return s
				},
				want: []string{
					"returns_the_fixed_results_of_Returns",
					"accepts_a_call_with_fixed_results_in_strict_mode",
				},
			},
			{
				name: "fails the check of the fixed results for samples of the default results",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					f := newFake(tb)
					s := f.subject()
					s.Returns = func() ret {
						f.fixed = &ret{}
						return ret{}
					}
					return s
				},
				want: []string{"returns_the_fixed_results_of_Returns"},
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
					s.Override = func(func(call)) {}
					return s
				},
				want: []string{"passes_the_arguments_of_each_call_to_the_function_of_Func"},
			},
			{
				name: "fails the check of the function for a function that receives another key",
				give: func(tb assert.TB) stub.Subject[call, ret] {
					f := newFake(tb)
					s := f.subject()
					s.Override = func(seen func(call)) {
						f.fn = func(string) error {
							seen(call{Key: "other"})
							return nil
						}
					}
					return s
				},
				want: []string{"passes_the_arguments_of_each_call_to_the_function_of_Func"},
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
		Arguments: func(tb assert.TB, c call) {
			assert.Equal(tb, c.Key, "k", "the call has the key of Call")
		},
		Reset: f.on.Reset,
		Fails: func() error {
			_, err := f.Get("k")
			return err
		},
		Result: func() ret {
			value, err := f.Get("k")
			return ret{Value: value, Err: err}
		},
		Default: f.defaults,
		Returns: func() ret {
			f.fixed = &ret{Value: "fixed"}
			return *f.fixed
		},
		Override: func(seen func(c call)) {
			f.fn = func(key string) error {
				seen(call{Key: key})
				return nil
			}
		},
	}
}

// subject returns the subject of Get on a new fake that reports to tb.
func subject(tb assert.TB) stub.Subject[call, ret] {
	return newFake(tb).subject()
}
