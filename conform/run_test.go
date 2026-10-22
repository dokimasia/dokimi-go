// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// An application runs each check of a suite as a test of its own, once it
// finds no problem in the checks and the options.
func TestRun(t *testing.T) {
	t.Parallel()

	suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{keeps()}}

	t.Run("Run", func(t *testing.T) {
		t.Parallel()

		t.Run("applies each check to a subject of the factory", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			t.Cleanup(func() {
				assert.Equal(t, built.Load(), int32(1), "the application builds one subject for the check")
			})
			conform.Run(t, suite, func(*conform.Handle[Store]) Store {
				built.Add(1)
				return newMemory()
			})
		})

		t.Run("applies a check of the suite to the subject alone", func(t *testing.T) {
			t.Parallel()

			var applied atomic.Int32
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				applied.Add(1)
				apply(h)
			}
			t.Cleanup(func() {
				assert.Equal(t, applied.Load(), int32(1), "the application does not falsify a check of the suite")
			})
			conform.Run(t, conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}}, newStore)
		})

		t.Run("runs each check in parallel", func(t *testing.T) {
			t.Parallel()

			var returned atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				assert.True(h, returned.Load(), "the check runs once the test of the application returns")
			}
			conform.Run(t, conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}}, newStore)
			returned.Store(true)
		})

		t.Run("runs each check under its identity without the declaration", func(t *testing.T) {
			t.Parallel()

			a := application(func(t conform.T) { conform.Run(t, suite, newStore) })
			assert.Equal(t, a.tests, []string{"Get/keeps-the-value"},
				"the application runs one test for the check")
		})

		t.Run("runs the check of a function under its identity", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.ID = "Slug/keeps-the-case"
			a := application(func(t conform.T) {
				conform.Run(t, conform.Suite[Store]{Declaration: "Slug", Checks: []conform.Check[Store]{c}}, newStore)
			})
			assert.Equal(t, a.tests, []string{"Slug/keeps-the-case"},
				"the application runs one test for the check of the function")
		})

		t.Run("reports the failure of a check on the subject", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, suite, forget)
				return
			}
			assert.Contains(t, childOutput(t), "--- FAIL: "+t.Name()+"/Get/keeps-the-value",
				"the test of the check fails")
		})

		t.Run("passes the overrides of the suite to each property run", func(t *testing.T) {
			t.Parallel()

			conform.Run(t, declaredKinds(), newStore)
		})

		t.Run("leaves the overrides of the suite unchanged", func(t *testing.T) {
			t.Parallel()

			kinds := declaredKinds()
			kinds.Using = slices.Grow(kinds.Using, 4)
			spare := kinds.Using[:cap(kinds.Using)]
			t.Cleanup(func() {
				assert.Equal(t, spare[len(kinds.Using)], prop.Option{},
					"the application writes no option into the overrides of the suite")
			})
			conform.Run(t, kinds, newStore, conform.Cases[Store](5))
		})

		t.Run("accepts an excusal of each ceiling of a budget", func(t *testing.T) {
			t.Parallel()

			budget := conform.Budget{
				Allocs: new(uint64(1)), Bytes: new(uint64(64)), Latency: time.Millisecond, Mean: time.Microsecond,
			}
			a := application(func(t conform.T) {
				conform.Run(t, conform.Suite[Store]{
					Declaration: "Store",
					Checks:      []conform.Check[Store]{keeps()},
					Calls:       budgeted(budget).Calls,
				}, newStore,
					conform.Excuse(storeGet.Check("allocation-budget"), "Get copies the key"),
					conform.Excuse(storeGet.Check("byte-budget"), "Get copies the key"),
					conform.Excuse(storeGet.Check("latency-budget"), "Get waits for a lock"),
					conform.Excuse(storeGet.Check("mean-latency-budget"), "Get waits for a lock"))
			})
			assert.Empty(t, a.errors, "the application reports no problem")
			assert.Equal(t, a.tests, []string{"Get/keeps-the-value"}, "the application runs the check of the suite")
		})

		t.Run("falsifies an added check against the checks that the application does not excuse", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			conform.Run(t, suite, newStore, conform.With(c),
				conform.Excuse(storeGet.Check("keeps-the-value"), "the defect of the added check scrambles each value"))
		})

		t.Run("falsifies an added check against the other checks of the application", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				c := misses()
				c.Defect = scrambles
				conform.Run(t, suite, newStore, conform.With(c))
				return
			}
			out := childOutput(t)
			assert.Contains(t, out, "--- FAIL: "+t.Name()+"/Get/misses-unknown-keys",
				"the test of the added check fails")
			assert.Contains(t, out, "\tStore.Get/keeps-the-value fails at the assertion equal",
				"the failure lists the check of the suite that the defect of the added check fails")
		})

		tests := []struct {
			name       string
			giveSuite  conform.Suite[Store]
			giveOpts   []conform.Option[Store]
			wantErrors []string
		}{
			{
				name:      "reports an identity outside the grammar",
				giveSuite: broken(func(c *conform.Check[Store]) { c.ID = "Store.Get/KeepsTheValue" }),
				wantErrors: []string{`the identity "Store.Get/KeepsTheValue" of a check does not have the form ` +
					`Declaration.Callable/segment, as in Store.Get/keeps-the-value`},
			},
			{
				name:       "reports a check without a statement",
				giveSuite:  broken(func(c *conform.Check[Store]) { c.Statement = "" }),
				wantErrors: []string{"the check Store.Get/keeps-the-value has no statement"},
			},
			{
				name:      "reports a check without a function",
				giveSuite: broken(func(c *conform.Check[Store]) { c.Apply = nil }),
				wantErrors: []string{"the check Store.Get/keeps-the-value has no function Apply and no function " +
					"Property"},
			},
			{
				name: "reports a check with two functions",
				giveSuite: broken(func(c *conform.Check[Store]) {
					c.Property = func(*conform.Handle[Store], *prop.Case) {}
				}),
				wantErrors: []string{"the check Store.Get/keeps-the-value has both a function Apply and a function " +
					"Property"},
			},
			{
				name:       "reports the option Cases with fewer than one case",
				giveSuite:  suite,
				giveOpts:   []conform.Option[Store]{conform.Cases[Store](0)},
				wantErrors: []string{"the option Cases asks for 0 cases, and a property run needs at least 1"},
			},
			{
				name:       "reports a check without a planted defect",
				giveSuite:  broken(func(c *conform.Check[Store]) { c.Defect = nil }),
				wantErrors: []string{"the check Store.Get/keeps-the-value has no planted defect"},
			},
			{
				name:       "reports two checks with one identity",
				giveSuite:  suite,
				giveOpts:   []conform.Option[Store]{conform.With(keeps())},
				wantErrors: []string{"two checks of RunStoreSuite have the identity Store.Get/keeps-the-value"},
			},
			{
				name:      "reports an excusal that matches no check",
				giveSuite: suite,
				giveOpts: []conform.Option[Store]{
					conform.Excuse(storePut.Check("keeps-the-value"), "the store keeps no values"),
				},
				wantErrors: []string{"the excusal of Store.Put/keeps-the-value matches no check of RunStoreSuite. " +
					"The checks of RunStoreSuite are:\n\tStore.Get/keeps-the-value"},
			},
			{
				name:      "reports an excusal in a run without checks",
				giveSuite: conform.Suite[Store]{Declaration: "Store"},
				giveOpts: []conform.Option[Store]{
					conform.Excuse(storeGet.Check("keeps-the-value"), "the store keeps no values"),
				},
				wantErrors: []string{"the excusal of Store.Get/keeps-the-value matches no check of RunStoreSuite. " +
					"RunStoreSuite has no checks."},
			},
			{
				name:       "reports an excusal without a reason",
				giveSuite:  suite,
				giveOpts:   []conform.Option[Store]{conform.Excuse(storeGet.Check("keeps-the-value"), "")},
				wantErrors: []string{"the excusal of Store.Get/keeps-the-value has no reason"},
			},
			{
				name: "reports a problem of a call of the suite",
				giveSuite: func() conform.Suite[Store] {
					s := budgeted(conform.Budget{})
					s.Checks = []conform.Check[Store]{keeps()}
					s.Calls[0].Over = nil
					return s
				}(),
				wantErrors: []string{"the call of Store.Get has no planted defect"},
			},
			{
				name:      "reports every problem before it stops",
				giveSuite: broken(func(c *conform.Check[Store]) { c.Statement, c.Defect = "", nil }),
				wantErrors: []string{
					"the check Store.Get/keeps-the-value has no statement",
					"the check Store.Get/keeps-the-value has no planted defect",
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				a := application(func(t conform.T) { conform.Run(t, tt.giveSuite, newStore, tt.giveOpts...) })
				assert.Equal(t, a.errors, tt.wantErrors, "the application reports each problem")
				assert.Empty(t, a.tests, "the application runs no check")
			})
		}
	})
}
