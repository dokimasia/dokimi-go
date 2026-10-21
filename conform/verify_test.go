// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"os"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
)

// An application runs each check of a suite as a test of its own, once it
// finds no problem in the checks and the options.
func TestVerify(t *testing.T) {
	t.Parallel()

	suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{keeps()}}

	t.Run("Verify", func(t *testing.T) {
		t.Parallel()

		t.Run("applies each check to a subject of the factory", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			t.Cleanup(func() {
				assert.Equal(t, built.Load(), int32(1), "the application builds one subject for the check")
			})
			conform.Verify(t, suite, func(*conform.Handle[Store]) Store {
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
			conform.Verify(t, conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}}, newStore)
		})

		t.Run("runs each check as a parallel test", func(t *testing.T) {
			t.Parallel()

			var returned atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				assert.True(h, returned.Load(), "the check runs once the test of the application returns")
			}
			conform.Verify(t, conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}}, newStore)
			returned.Store(true)
		})

		t.Run("runs each check as a test named by its identity", func(t *testing.T) {
			t.Parallel()

			a := application(func(t conform.T) { conform.Verify(t, suite, newStore) })
			assert.Equal(t, a.tests, []string{"Store.Get/keeps-the-value"},
				"the application runs one test for the check")
		})

		t.Run("fails the test of a check that fails on the subject", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Verify(t, suite, forget)
				return
			}
			assert.Contains(t, childOutput(t), "--- FAIL: "+t.Name()+"/Store.Get/keeps-the-value",
				"the test of the check fails")
		})

		broken := func(change func(c *conform.Check[Store])) conform.Suite[Store] {
			c := keeps()
			change(&c)
			return conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}}
		}
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
				name:       "reports a check without a function Apply",
				giveSuite:  broken(func(c *conform.Check[Store]) { c.Apply = nil }),
				wantErrors: []string{"the check Store.Get/keeps-the-value has no function Apply"},
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
				wantErrors: []string{"two checks of RunStore have the identity Store.Get/keeps-the-value"},
			},
			{
				name:      "reports an excusal that matches no check",
				giveSuite: suite,
				giveOpts: []conform.Option[Store]{
					conform.Excuse(storePut.Check("keeps-the-value"), "the store keeps no values"),
				},
				wantErrors: []string{"the excusal of Store.Put/keeps-the-value matches no check of RunStore. " +
					"The checks of RunStore are:\n\tStore.Get/keeps-the-value"},
			},
			{
				name:      "reports an excusal in a run without checks",
				giveSuite: conform.Suite[Store]{Declaration: "Store"},
				giveOpts: []conform.Option[Store]{
					conform.Excuse(storeGet.Check("keeps-the-value"), "the store keeps no values"),
				},
				wantErrors: []string{"the excusal of Store.Get/keeps-the-value matches no check of RunStore. " +
					"RunStore has no checks."},
			},
			{
				name:       "reports an excusal without a reason",
				giveSuite:  suite,
				giveOpts:   []conform.Option[Store]{conform.Excuse(storeGet.Check("keeps-the-value"), "")},
				wantErrors: []string{"the excusal of Store.Get/keeps-the-value has no reason"},
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

				a := application(func(t conform.T) { conform.Verify(t, tt.giveSuite, newStore, tt.giveOpts...) })
				assert.Equal(t, a.errors, tt.wantErrors, "the application reports each problem")
				assert.Empty(t, a.tests, "the application runs no check")
			})
		}
	})
}
