// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"os"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/conform"
)

// The falsification of a suite applies each check of the suite to its
// planted defect as a test of its own, once it finds no problem in the
// checks. The test also applies every other check of the suite to the
// defect, which passes them unless the checks share the defect.
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

		t.Run("runs each check as a test named by its callable and its segment", func(t *testing.T) {
			t.Parallel()

			a := application(func(t conform.T) { conform.Falsify(t, suite) })
			assert.Equal(t, a.tests, []string{"Get/keeps-the-value"},
				"the falsification runs one test for the check")
		})

		t.Run("runs the check of a function as a test named by its identity", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.ID = "Slug/keeps-the-case"
			a := application(func(t conform.T) {
				conform.Falsify(t, conform.Suite[Store]{Declaration: "Slug", Checks: []conform.Check[Store]{c}})
			})
			assert.Equal(t, a.tests, []string{"Slug/keeps-the-case"},
				"the falsification runs one test for the check of the function")
		})

		t.Run("fails the test of a check that its planted defect passes", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Falsify(t, broken(func(c *conform.Check[Store]) { c.Defect = newStore }))
				return
			}
			assert.Contains(t, childOutput(t), "--- FAIL: "+t.Name()+"/Get/keeps-the-value",
				"the test of the check fails")
		})

		t.Run("passes the overrides of the suite to each property run", func(t *testing.T) {
			t.Parallel()

			conform.Falsify(t, declaredKinds())
		})

		t.Run("passes the planted defects that fail their own checks alone", func(t *testing.T) {
			t.Parallel()

			conform.Falsify(t, conform.Suite[Store]{
				Declaration: "Store", Checks: []conform.Check[Store]{keeps(), misses()},
			})
		})

		t.Run("fails a planted defect that fails another check of the suite", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				c := misses()
				c.Defect = scrambles
				conform.Falsify(t, conform.Suite[Store]{
					Declaration: "Store", Checks: []conform.Check[Store]{keeps(), c},
				})
				return
			}
			out := childOutput(t)
			expect.Contains(t, out, "--- FAIL: "+t.Name()+"/Get/misses-unknown-keys",
				"the test of the check whose defect fails another check fails")
			expect.Contains(t, out, "--- PASS: "+t.Name()+"/Get/keeps-the-value",
				"the test of the check whose defect fails no other check passes")
			expect.Contains(t, out, "the planted defect conform_test.scrambles of Store.Get/misses-unknown-keys "+
				"also fails these checks:", "the failure names the defect and its check")
			expect.Contains(t, out, `Store.Get/keeps-the-value fails at the assertion equal with the message `+
				`"Get returns the value that Put stored"`, "the failure lists the other check and its assertion")
		})

		t.Run("accepts the failures of the checks that share one planted defect", func(t *testing.T) {
			t.Parallel()

			first, second := keeps(), misses()
			first.Defect, second.Defect = scrambles, scrambles
			conform.Falsify(t, conform.Suite[Store]{
				Declaration: "Store", Checks: []conform.Check[Store]{first, second},
			})
		})

		t.Run("fails a planted defect that panics under another check of the suite", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				panicking := keeps()
				panicking.Apply = func(h *conform.Handle[Store]) {
					if _, isInventive := h.Subject().(inventive); isInventive {
						var gone *memory
						gone.Put("k", "v")
					}
					keeps().Apply(h)
				}
				conform.Falsify(t, conform.Suite[Store]{
					Declaration: "Store", Checks: []conform.Check[Store]{panicking, misses()},
				})
				return
			}
			out := childOutput(t)
			expect.Contains(t, out, "--- FAIL: "+t.Name()+"/Get/misses-unknown-keys",
				"the test of the check whose defect panics under another check fails")
			expect.Contains(t, out, "Store.Get/keeps-the-value panics with runtime error: invalid memory address "+
				"or nil pointer dereference", "the failure lists the check under which the defect panics")
		})

		t.Run("ignores another check that ends with a fault on the planted defect", func(t *testing.T) {
			t.Parallel()

			faulting := keeps()
			faulting.Apply = func(h *conform.Handle[Store]) {
				if _, isInventive := h.Subject().(inventive); isInventive {
					_ = h.Reference()
				}
				keeps().Apply(h)
			}
			conform.Falsify(t, conform.Suite[Store]{
				Declaration: "Store", Checks: []conform.Check[Store]{faulting, misses()},
			})
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
