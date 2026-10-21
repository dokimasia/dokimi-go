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

// The falsification of a suite applies each check of the suite to its
// planted defect as a test of its own, once it finds no problem in the
// checks.
func TestFalsify(t *testing.T) {
	t.Parallel()

	suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{keeps()}}

	t.Run("Falsify", func(t *testing.T) {
		t.Parallel()

		t.Run("applies each check to its planted defect", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			c := keeps()
			defect := c.Defect
			c.Defect = func(h *conform.Handle[Store]) Store {
				built.Add(1)
				return defect(h)
			}
			t.Cleanup(func() {
				assert.Equal(t, built.Load(), int32(1), "the falsification builds the planted defect of the check")
			})
			conform.Falsify(t, conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}})
		})

		t.Run("runs each check as a parallel test", func(t *testing.T) {
			t.Parallel()

			var returned, late atomic.Bool
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				late.Store(returned.Load())
				apply(h)
			}
			t.Cleanup(func() {
				assert.True(t, late.Load(), "the check runs once the test of the falsification returns")
			})
			conform.Falsify(t, conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}})
			returned.Store(true)
		})

		t.Run("runs each check as a test named by its identity", func(t *testing.T) {
			t.Parallel()

			a := application(func(t conform.T) { conform.Falsify(t, suite) })
			assert.Equal(t, a.tests, []string{"Store.Get/keeps-the-value"},
				"the falsification runs one test for the check")
		})

		t.Run("fails the test of a check that its planted defect passes", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Falsify(t, broken(func(c *conform.Check[Store]) { c.Defect = newStore }))
				return
			}
			assert.Contains(t, childOutput(t), "--- FAIL: "+t.Name()+"/Store.Get/keeps-the-value",
				"the test of the check fails")
		})

		tests := []struct {
			name       string
			giveSuite  conform.Suite[Store]
			wantErrors []string
		}{
			{
				name:      "reports an identity outside the grammar",
				giveSuite: broken(func(c *conform.Check[Store]) { c.ID = "Store.Get/KeepsTheValue" }),
				wantErrors: []string{`the identity "Store.Get/KeepsTheValue" of a check does not have the form ` +
					`Declaration.Callable/segment, as in Store.Get/keeps-the-value`},
			},
			{
				name:       "reports a check without a planted defect",
				giveSuite:  broken(func(c *conform.Check[Store]) { c.Defect = nil }),
				wantErrors: []string{"the check Store.Get/keeps-the-value has no planted defect"},
			},
			{
				name: "reports two checks with one identity",
				giveSuite: conform.Suite[Store]{
					Declaration: "Store", Checks: []conform.Check[Store]{keeps(), keeps()},
				},
				wantErrors: []string{"two checks of StoreSuite have the identity Store.Get/keeps-the-value"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				a := application(func(t conform.T) { conform.Falsify(t, tt.giveSuite) })
				assert.Equal(t, a.errors, tt.wantErrors, "the falsification reports each problem")
				assert.Empty(t, a.tests, "the falsification runs no check")
			})
		}
	})
}
