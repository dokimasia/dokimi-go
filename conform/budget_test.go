// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// An application of the allocation ceilings of a suite reports the problems
// of its calls and options, and runs each ceiling as a test of its own.
func TestBudget(t *testing.T) {
	t.Parallel()

	t.Run("RunBudgets", func(t *testing.T) {
		t.Parallel()

		t.Run("skips each allocation ceiling in a run that does not check them", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.RunBudgets(t, budgeted(conform.Budget{Allocs: new(uint64(0))}), newStore)
				return
			}
			out := childOutputWith(t, "DOKIMI_MUTATE_INSTRUMENTED=1")
			expect.Contains(t, out, "--- SKIP: "+t.Name()+"/Get/allocation-budget", "the test of the ceiling skips")
			expect.Contains(t, out, "the run does not check allocation ceilings: ",
				"the skip states the reason of the assert library")
		})

		t.Run("accepts an excusal of a check of the suite", func(t *testing.T) {
			t.Parallel()

			suite := budgeted(conform.Budget{Allocs: new(uint64(0))})
			suite.Checks = []conform.Check[Store]{keeps()}
			a := application(func(t conform.T) {
				conform.RunBudgets(t, suite, newStore,
					conform.Excuse(storeGet.Check("keeps-the-value"), "the store keeps no values"))
			})
			assert.Empty(t, a.errors, "the application reports no problem")
			assert.Equal(t, a.tests, []string{"Get/allocation-budget"}, "the application runs the ceiling")
		})

		t.Run("runs each check that counts allocations after the ceilings", func(t *testing.T) {
			t.Parallel()

			suite := budgeted(conform.Budget{Allocs: new(uint64(0))})
			suite.Checks = []conform.Check[Store]{keeps(), countedEvery()}
			added := misses()
			added.Counted = true
			a := application(func(t conform.T) { conform.RunBudgets(t, suite, newStore, conform.With(added)) })
			assert.Empty(t, a.errors, "the application reports no problem")
			assert.Equal(t, a.tests,
				[]string{"Get/allocation-budget", "Get/keeps-every-value", "Get/misses-unknown-keys"},
				"the application runs the ceiling, then the checks of the suite and of the option With that count "+
					"allocations")
		})

		t.Run("skips a check that counts allocations in a run that does not check them", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{countedEvery()}}
				conform.RunBudgets(t, suite, forget)
				return
			}
			expect.Contains(t, childOutputWith(t, "DOKIMI_MUTATE_INSTRUMENTED=1"),
				"--- SKIP: "+t.Name()+"/Get/keeps-every-value", "the test of the check skips")
		})

		t.Run("runs nothing for a budget without an allocation ceiling", func(t *testing.T) {
			t.Parallel()

			a := application(func(t conform.T) {
				conform.RunBudgets(t, budgeted(conform.Budget{Latency: time.Millisecond}), newStore)
			})
			assert.Empty(t, a.errors, "the application reports no problem")
			assert.Empty(t, a.tests, "the application runs no test")
		})

		t.Run("applies the option Ceilings to its callable alone", func(t *testing.T) {
			t.Parallel()

			suite := budgeted(conform.Budget{})
			put := suite.Calls[0]
			put.Callable = storePut
			suite.Calls = append(suite.Calls, put)
			ceilings := conform.Ceilings(storeGet, conform.Budget{Allocs: new(uint64(0))})
			a := application(func(t conform.T) { conform.RunBudgets(t, suite, newStore, ceilings) })
			assert.Equal(t, a.tests, []string{"Get/allocation-budget"}, "the application runs the ceiling of Get alone")
		})

		tests := []struct {
			name       string
			giveSuite  conform.Suite[Store]
			giveOpts   []conform.Option[Store]
			wantErrors []string
		}{
			{
				name:      "reports the option Ceilings for a callable of no call of the suite",
				giveSuite: budgeted(conform.Budget{}),
				giveOpts: []conform.Option[Store]{
					conform.Ceilings(storePut, conform.Budget{Allocs: new(uint64(0))}),
				},
				wantErrors: []string{"the option Ceilings states a budget of Store.Put, and RunStoreBudgets has " +
					"no call of it"},
			},
			{
				name:      "reports the option Ceilings for a callable whose declaration states a budget",
				giveSuite: budgeted(conform.Budget{Allocs: new(uint64(1))}),
				giveOpts: []conform.Option[Store]{
					conform.Ceilings(storeGet, conform.Budget{Allocs: new(uint64(0))}),
				},
				wantErrors: []string{"the option Ceilings states a budget of Store.Get, whose declaration states " +
					"a budget. Excuse the checks of the declaration instead"},
			},
			{
				name:      "reports two budgets of one callable in the option Ceilings",
				giveSuite: budgeted(conform.Budget{}),
				giveOpts: []conform.Option[Store]{
					conform.Ceilings(storeGet, conform.Budget{Allocs: new(uint64(0))}),
					conform.Ceilings(storeGet, conform.Budget{Allocs: new(uint64(1))}),
				},
				wantErrors: []string{"the option Ceilings states two budgets of Store.Get"},
			},
			{
				name:      "reports a budget with a mean without a p99",
				giveSuite: budgeted(conform.Budget{Mean: 1}),
				wantErrors: []string{"the budget of Store.Get states a mean and no p99, and a mean alone passes " +
					"a tail that grows"},
			},
			{
				name: "reports a call without a call generator",
				giveSuite: func() conform.Suite[Store] {
					s := budgeted(conform.Budget{})
					s.Calls[0].Calls = prop.Generator[func(context.Context, Store) error]{}
					return s
				}(),
				wantErrors: []string{"the call of Store.Get has no call generator"},
			},
			{
				name: "reports a call without a planted defect",
				giveSuite: func() conform.Suite[Store] {
					s := budgeted(conform.Budget{})
					s.Calls[0].Over = nil
					return s
				}(),
				wantErrors: []string{"the call of Store.Get has no planted defect"},
			},
			{
				name: "reports two calls of one callable",
				giveSuite: func() conform.Suite[Store] {
					s := budgeted(conform.Budget{})
					s.Calls = append(s.Calls, s.Calls[0])
					return s
				}(),
				wantErrors: []string{"two calls of RunStoreBudgets have the callable Store.Get"},
			},
			{
				name:      "reports an excusal that matches no check",
				giveSuite: budgeted(conform.Budget{Allocs: new(uint64(0))}),
				giveOpts: []conform.Option[Store]{
					conform.Excuse(storePut.Check("allocation-budget"), "Put copies the value"),
				},
				wantErrors: []string{"the excusal of Store.Put/allocation-budget matches no check of " +
					"RunStoreBudgets. The checks of RunStoreBudgets are:\n\tStore.Get/allocation-budget"},
			},
			{
				name:      "reports an excusal of a ceiling that the budget does not state",
				giveSuite: budgeted(conform.Budget{Latency: time.Millisecond}),
				giveOpts: []conform.Option[Store]{
					conform.Excuse(storeGet.Check("allocation-budget"), "Get copies the key"),
				},
				wantErrors: []string{"the excusal of Store.Get/allocation-budget matches no check of " +
					"RunStoreBudgets. The checks of RunStoreBudgets are:\n\tStore.Get/latency-budget"},
			},
			{
				name:       "reports the option Cases with fewer than one case",
				giveSuite:  budgeted(conform.Budget{Allocs: new(uint64(0))}),
				giveOpts:   []conform.Option[Store]{conform.Cases[Store](0)},
				wantErrors: []string{"the option Cases asks for 0 cases, and a property run needs at least 1"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				a := application(func(t conform.T) { conform.RunBudgets(t, tt.giveSuite, newStore, tt.giveOpts...) })
				assert.Equal(t, a.errors, tt.wantErrors, "the application reports each problem")
				assert.Empty(t, a.tests, "the application runs no check")
			})
		}
	})

	t.Run("FalsifyAllocs", func(t *testing.T) {
		t.Parallel()

		t.Run("runs the allocation ceiling of each callable under its identity", func(t *testing.T) {
			t.Parallel()

			a := application(func(t conform.T) {
				conform.FalsifyAllocs(t, budgeted(conform.Budget{Allocs: new(uint64(2))}))
			})
			assert.Equal(t, a.tests, []string{"Get/allocation-budget"}, "the falsification runs one test")
		})

		t.Run("skips each allocation ceiling in a run that does not check them", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.FalsifyAllocs(t, budgeted(conform.Budget{Allocs: new(uint64(0))}))
				return
			}
			expect.Contains(t, childOutputWith(t, "DOKIMI_MUTATE_INSTRUMENTED=1"),
				"--- SKIP: "+t.Name()+"/Get/allocation-budget", "the test of the ceiling skips")
		})

		t.Run("runs each check that counts allocations after the ceilings", func(t *testing.T) {
			t.Parallel()

			suite := budgeted(conform.Budget{Allocs: new(uint64(0))})
			suite.Checks = []conform.Check[Store]{keeps(), countedEvery()}
			a := application(func(t conform.T) { conform.FalsifyAllocs(t, suite) })
			assert.Equal(t, a.tests, []string{"Get/allocation-budget", "Get/keeps-every-value"},
				"the falsification runs the ceiling, then the check that counts allocations")
		})

		t.Run("skips a check that counts allocations in a run that does not check them", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.FalsifyAllocs(t, conform.Suite[Store]{
					Declaration: "Store", Checks: []conform.Check[Store]{countedEvery()},
				})
				return
			}
			expect.Contains(t, childOutputWith(t, "DOKIMI_MUTATE_INSTRUMENTED=1"),
				"--- SKIP: "+t.Name()+"/Get/keeps-every-value", "the test of the check skips")
		})

		tests := []struct {
			name       string
			giveSuite  conform.Suite[Store]
			wantErrors []string
		}{
			{
				name: "reports two calls of one callable",
				giveSuite: func() conform.Suite[Store] {
					s := budgeted(conform.Budget{Allocs: new(uint64(0))})
					s.Calls = append(s.Calls, s.Calls[0])
					return s
				}(),
				wantErrors: []string{"two calls of StoreSuite have the callable Store.Get"},
			},
			{
				name: "reports a check that counts allocations without a planted defect",
				giveSuite: func() conform.Suite[Store] {
					c := countedEvery()
					c.Defect = nil
					return conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}}
				}(),
				wantErrors: []string{"the check Store.Get/keeps-every-value has no planted defect"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				a := application(func(t conform.T) { conform.FalsifyAllocs(t, tt.giveSuite) })
				assert.Equal(t, a.errors, tt.wantErrors, "the falsification reports each problem")
				assert.Empty(t, a.tests, "the falsification runs no check")
			})
		}
	})
}

// The allocation ceilings of a suite apply to a fresh subject for each case
// of a property run, and falsification proves each one on its planted
// defect. The test counts the allocations of the process, so neither it nor
// its subtests run in parallel, in the test process or in a child process.
func TestBudgetAllocs(t *testing.T) {
	t.Run("RunBudgets", func(t *testing.T) {
		t.Run("builds a fresh subject in each case", func(t *testing.T) {
			skipUnchecked(t)
			var built atomic.Int32
			conform.RunBudgets(t, budgeted(conform.Budget{Allocs: new(uint64(0))}), func(*conform.Handle[Store]) Store {
				built.Add(1)
				return newMemory()
			}, conform.Cases[Store](5))
			assert.True(t, built.Load() >= 5, "the property run builds a subject in each of its 5 cases at least")
		})

		t.Run("scores each case by the allocations of one call", func(t *testing.T) {
			skipUnchecked(t)
			var calls, built atomic.Int32
			suite := budgeted(conform.Budget{Allocs: new(uint64(10))})
			suite.Calls[0].Calls = counted(&calls)
			conform.RunBudgets(t, suite, func(*conform.Handle[Store]) Store {
				built.Add(1)
				return newMemory()
			}, conform.Cases[Store](1))
			assert.Equal(t, calls.Load(), 103*built.Load(),
				"each case makes 2 calls for its score, and MaxAllocs makes 101 calls for the ceiling")
		})

		t.Run("fails a subject that allocates more than the ceiling", func(t *testing.T) {
			skipUnchecked(t)
			if os.Getenv(childEnv) == t.Name() {
				conform.RunBudgets(t, budgeted(conform.Budget{Allocs: new(uint64(0))}), squander)
				return
			}
			out := childOutput(t)
			expect.Contains(t, out, "--- FAIL: "+t.Name()+"/Get/allocation-budget", "the test of the ceiling fails")
			expect.Contains(t, out, "failure of max-allocs", "the ceiling fails at its assertion")
			expect.Contains(t, out, "want 0, got 3, total 300", "the failure counts 3 allocations per call")
		})

		t.Run("states the ceiling in the statement of its check", func(t *testing.T) {
			skipUnchecked(t)
			if os.Getenv(childEnv) == t.Name() {
				for _, allocs := range []uint64{0, 1, 2} {
					conform.RunBudgets(t, budgeted(conform.Budget{Allocs: new(allocs)}), squander)
				}
				slug := budgeted(conform.Budget{Allocs: new(uint64(0))})
				slug.Declaration, slug.Calls[0].Callable = "Slug", "Slug"
				conform.RunBudgets(t, slug, squander)
				return
			}
			out := childOutput(t)
			expect.Contains(t, out, ": Get allocates at most 0 times per call:", "a ceiling of 0 has the plural")
			expect.Contains(t, out, ": Get allocates at most 1 time per call:", "a ceiling of 1 has the singular")
			expect.Contains(t, out, ": Get allocates at most 2 times per call:", "a ceiling of 2 has the plural")
			expect.Contains(t, out, ": Slug allocates at most 0 times per call:", "a function has its own name")
		})

		t.Run("passes the overrides of the suite to each property run", func(t *testing.T) {
			skipUnchecked(t)
			var undeclared atomic.Bool
			conform.RunBudgets(t, declaredBudget(&undeclared), newStore)
			assert.False(t, undeclared.Load(), "each case draws a declared kind")
		})

		t.Run("skips an excused allocation ceiling", func(t *testing.T) {
			skipUnchecked(t)
			conform.RunBudgets(t, budgeted(conform.Budget{Allocs: new(uint64(0))}), squander,
				conform.Excuse(storeGet.Check("allocation-budget"), "Get copies the key"))
		})

		t.Run("falsifies a ceiling of the option Ceilings before it applies it", func(t *testing.T) {
			skipUnchecked(t)
			var falsified atomic.Bool
			suite := budgeted(conform.Budget{})
			over := suite.Calls[0].Over
			suite.Calls[0].Over = func(h *conform.Handle[Store], spend func()) Store {
				falsified.Store(true)
				return over(h, spend)
			}
			conform.RunBudgets(t, suite, newStore, conform.Ceilings(storeGet, conform.Budget{Allocs: new(uint64(0))}))
			assert.True(t, falsified.Load(), "the application builds the planted defect of the ceiling")
		})

		t.Run("applies a check that counts allocations to a subject of the factory", func(t *testing.T) {
			skipUnchecked(t)
			var built atomic.Int32
			suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{countedEvery()}}
			conform.RunBudgets(t, suite, func(*conform.Handle[Store]) Store {
				built.Add(1)
				return newMemory()
			}, conform.Cases[Store](5))
			assert.True(t, built.Load() >= 5, "the property run builds a subject in each of its 5 cases at least")
		})

		t.Run("fails a subject that fails a check that counts allocations", func(t *testing.T) {
			skipUnchecked(t)
			if os.Getenv(childEnv) == t.Name() {
				suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{countedEvery()}}
				conform.RunBudgets(t, suite, forget)
				return
			}
			expect.Contains(t, childOutput(t), "--- FAIL: "+t.Name()+"/Get/keeps-every-value",
				"the test of the check fails")
		})

		t.Run("skips an excused check that counts allocations", func(t *testing.T) {
			skipUnchecked(t)
			suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{countedEvery()}}
			conform.RunBudgets(t, suite, forget,
				conform.Excuse(storeGet.Check("keeps-every-value"), "the store keeps no values"))
		})

		t.Run("falsifies a check that counts allocations of the option With before it applies it", func(t *testing.T) {
			skipUnchecked(t)
			var falsified atomic.Bool
			c := countedEvery()
			defect := c.Defect
			c.Defect = func(h *conform.Handle[Store]) Store {
				falsified.Store(true)
				return defect(h)
			}
			conform.RunBudgets(t, budgeted(conform.Budget{}), newStore, conform.With(c))
			assert.True(t, falsified.Load(), "the application builds the planted defect of the check")
		})
	})

	t.Run("FalsifyAllocs", func(t *testing.T) {
		t.Run("falsifies the allocation ceiling of each callable on its planted defect", func(t *testing.T) {
			conform.FalsifyAllocs(t, budgeted(conform.Budget{Allocs: new(uint64(2))}))
		})

		t.Run("passes the overrides of the suite to each property run", func(t *testing.T) {
			skipUnchecked(t)
			var undeclared atomic.Bool
			conform.FalsifyAllocs(t, declaredBudget(&undeclared))
			assert.False(t, undeclared.Load(), "each case draws a declared kind")
		})

		t.Run("fails a ceiling that its planted defect does not exceed", func(t *testing.T) {
			skipUnchecked(t)
			if os.Getenv(childEnv) == t.Name() {
				suite := budgeted(conform.Budget{Allocs: new(uint64(0))})
				suite.Calls[0].Over = func(*conform.Handle[Store], func()) Store { return newMemory() }
				conform.FalsifyAllocs(t, suite)
				return
			}
			out := childOutput(t)
			expect.Contains(t, out, "--- FAIL: "+t.Name()+"/Get/allocation-budget", "the test of the ceiling fails")
			expect.Contains(t, out, "the planted defect of Store.Get/allocation-budget fails the check",
				"the failure is the failure of the falsification")
			expect.NotContains(t, out, "before an assertion fails", "the check fails at no other place")
		})

		t.Run("falsifies a check that counts allocations on its planted defect", func(t *testing.T) {
			skipUnchecked(t)
			conform.FalsifyAllocs(t, conform.Suite[Store]{
				Declaration: "Store", Checks: []conform.Check[Store]{misses(), countedEvery()},
			})
		})

		t.Run("fails a planted defect of a check that counts allocations that fails another check", func(t *testing.T) {
			skipUnchecked(t)
			if os.Getenv(childEnv) == t.Name() {
				conform.FalsifyAllocs(t, conform.Suite[Store]{
					Declaration: "Store", Checks: []conform.Check[Store]{keeps(), countedEvery()},
				})
				return
			}
			out := childOutput(t)
			expect.Contains(t, out, "--- FAIL: "+t.Name()+"/Get/keeps-every-value", "the test of the check fails")
			expect.Contains(t, out, "\tStore.Get/keeps-the-value fails at the assertion equal",
				"the failure lists the other check of the suite that the defect fails")
		})
	})
}
