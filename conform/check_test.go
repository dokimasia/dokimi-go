// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// A check applies to a subject on a seat, and falsification proves that it
// fails on its planted defect at the assertion of its statement.
func TestCheck(t *testing.T) {
	t.Parallel()

	t.Run("Run", func(t *testing.T) {
		t.Parallel()

		t.Run("applies the check to a subject of the factory", func(t *testing.T) {
			t.Parallel()

			keeps().Run(t, newStore)
		})

		t.Run("stops the seat at the first failed assertion of the check", func(t *testing.T) {
			t.Parallel()

			r := recorded(func(tb assert.TB) { keeps().Run(tb, forget) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "the check stops at its first failure")
			assert.Equal(t, failures[0].Contract, keepsTheValue, "the check fails at the assertion of its statement")
		})

		t.Run("applies a property check to a fresh subject of the factory in each case", func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			subjects := map[Store]bool{}
			cases := 0
			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				s := h.Subject()
				mu.Lock()
				defer mu.Unlock()
				subjects[s] = true
				cases++
			}
			c.Run(t, newStore)
			expect.True(t, cases >= 100, "the property engine runs 100 valid cases by default")
			expect.Equal(t, len(subjects), cases, "no two cases share a subject")
		})

		t.Run("fails the seat with the record of the property and of its smallest case", func(t *testing.T) {
			t.Parallel()

			r := recorded(func(tb assert.TB) { keepsEvery().Run(tb, forget) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "the property fails once")
			expect.Equal(t, failures[0].Assertion, "prop-for-all", "the record is the record of the property")
			expect.Equal(t, failures[0].Contract, keepsEveryValue, "the contract of the property is the statement")
			inner, isCase := failures[0].CaseFailure()
			assert.True(t, isCase, "the record contains the failure of the smallest case")
			expect.Equal(t, inner.Contract, keepsEveryValue, "the case fails at the assertion of the statement")
		})
	})

	t.Run("Falsify", func(t *testing.T) {
		t.Parallel()

		t.Run("passes when the defect fails the assertion of the statement", func(t *testing.T) {
			t.Parallel()

			keeps().Falsify(t)
		})

		t.Run("fails when the defect passes the check", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Defect = newStore
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "falsification fails once")
			assert.Equal(t, failures[0].Contract, "the planted defect of Store.Get/keeps-the-value fails the check",
				"falsification fails because the defect passes the check")
		})

		t.Run("reports one failure to a seat that goes on after a fatal failure", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Defect = newStore
			r := assert.NewRecorder()
			c.Falsify(r)
			assert.Length(t, r.Failures(), 1, "falsification fails once")
		})

		t.Run("fails when the defect fails another assertion first", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				s := h.Subject()
				s.Put("k", "v")
				_, found := s.Get("k")
				assert.True(h, found, "Get finds the key that Put stored")
				got, _ := s.Get("k")
				assert.Equal(h, got, "v", h.Statement())
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-the-value fails the assertion true with the message `+
					`"Get finds the key that Put stored". Falsification needs a failure of the assertion whose `+
					`message is the statement "Get returns the value that Put stored". Pass h.Statement() as the `+
					`message of the decisive assertion, or change the defect so that it passes the other `+
					`assertions of the check.`,
				"the message contains the assertion that failed first")
		})

		t.Run("ends with the fault of a door that the check calls without its option", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { _ = h.Reference() }
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			faults := r.Faults()
			assert.Length(t, faults, 1, "falsification ends with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrOption, "the fault is the fault of the door")
			expect.Empty(t, r.Failures(), "falsification reports no verdict on the defect")
		})

		t.Run("fails when the check stops through Fatalf before an assertion fails", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { h.Fatalf("the store is %s", "closed") }
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-the-value fails the check before an assertion fails. `+
					`Falsification needs a failure of the assertion whose message is the statement `+
					`"Get returns the value that Put stored". The check failed with this message:`+"\n"+
					`the store is closed`,
				"the message contains the failure of the check")
		})

		t.Run("fails when the check reports a failure through Errorf", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { h.Errorf("the store is %s", "empty") }
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-the-value fails the check before an assertion fails. `+
					`Falsification needs a failure of the assertion whose message is the statement `+
					`"Get returns the value that Put stored". The check failed with this message:`+"\n"+
					`the store is empty`,
				"the message contains the failure of the check")
		})

		t.Run("passes a planted defect that the checks of the options pass", func(t *testing.T) {
			t.Parallel()

			misses().Falsify(t, conform.With(keeps()))
		})

		t.Run("fails a planted defect that fails a check of the options", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			r := recorded(func(tb assert.TB) { c.Falsify(tb, conform.With(keeps())) })
			assert.Equal(t, r.Message(),
				"the planted defect conform_test.scrambles of Store.Get/misses-unknown-keys also fails these "+
					"checks:\n"+
					"\tStore.Get/keeps-the-value fails at the assertion equal with the message "+
					`"Get returns the value that Put stored"`+"\n"+
					"A planted defect fails the check that it proves and no other. Change the defect so that it "+
					"passes these checks, or give each check that it fails the same defect.",
				"the message lists the defect, its check and each other check that the defect fails")
		})

		t.Run("lists a check of the options that fails the defect through Errorf", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			complaining := keeps()
			complaining.Apply = func(h *conform.Handle[Store]) {
				if _, isScrambled := h.Subject().(scrambled); isScrambled {
					h.Errorf("the store scrambles %s", "values")
				}
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb, conform.With(complaining)) })
			assert.Contains(t, r.Message(),
				"\n\tStore.Get/keeps-the-value fails with the message \"the store scrambles values\"\n",
				"the message lists the check with the text of its failure")
		})

		t.Run("lists a check of the options under which the defect panics", func(t *testing.T) {
			t.Parallel()

			panicking := keeps()
			panicking.Apply = func(h *conform.Handle[Store]) {
				if _, isInventive := h.Subject().(inventive); isInventive {
					var gone *memory
					gone.Put("k", "v")
				}
				keeps().Apply(h)
			}
			r := recorded(func(tb assert.TB) { misses().Falsify(tb, conform.With(panicking)) })
			assert.Contains(t, r.Message(), "\n\tStore.Get/keeps-the-value panics with runtime error: invalid memory "+
				"address or nil pointer dereference\n", "the message lists the check with the value of the panic")
		})

		t.Run("accepts a check of the options that shares the planted defect", func(t *testing.T) {
			t.Parallel()

			c, other := misses(), keeps()
			c.Defect, other.Defect = scrambles, scrambles
			c.Falsify(t, conform.With(other))
		})

		t.Run("ignores a check of the options that ends with a fault on the planted defect", func(t *testing.T) {
			t.Parallel()

			faulting := keeps()
			faulting.Apply = func(h *conform.Handle[Store]) {
				if _, isInventive := h.Subject().(inventive); isInventive {
					_ = h.Reference()
				}
				keeps().Apply(h)
			}
			misses().Falsify(t, conform.With(faulting))
		})

		t.Run("passes when the defect fails the property at the assertion of the statement", func(t *testing.T) {
			t.Parallel()

			keepsEvery().Falsify(t)
		})

		t.Run("fails when the defect passes the property", func(t *testing.T) {
			t.Parallel()

			c := keepsEvery()
			c.Defect = newStore
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "falsification fails once")
			expect.Equal(t, failures[0].Contract, "the planted defect of Store.Get/keeps-every-value fails the check",
				"falsification fails because the defect passes the property")
		})

		t.Run("fails when the defect fails the property at another assertion first", func(t *testing.T) {
			t.Parallel()

			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				key := pc.Draw(prop.String(), "key")
				s := h.Subject()
				s.Put(key, "v")
				_, found := s.Get(key)
				assert.True(h, found, "Get finds the key that Put stored")
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-every-value fails the assertion true with the message `+
					`"Get finds the key that Put stored". Falsification needs a failure of the assertion whose `+
					`message is the statement "Get returns the value that Put stored under any key". Pass `+
					`h.Statement() as the message of the decisive assertion, or change the defect so that it `+
					`passes the other assertions of the check.`,
				"the message contains the assertion of the case that failed first")
		})

		t.Run("fails when the defect stops a case of the property through Fatalf", func(t *testing.T) {
			t.Parallel()

			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				h.Fatalf("the store is %s", "closed")
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-every-value fails the check before an assertion fails. `+
					`Falsification needs a failure of the assertion whose message is the statement `+
					`"Get returns the value that Put stored under any key". The check failed with this message:`+
					"\n"+`the store is closed`,
				"the message contains the failure of the case")
		})

		t.Run("runs the property of the planted defect without shrinking", func(t *testing.T) {
			t.Parallel()

			var runs atomic.Int32
			c := keepsEvery()
			property := c.Property
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				runs.Add(1)
				property(h, pc)
			}
			c.Falsify(t)
			expect.Equal(t, runs.Load(), int32(1), "the first failing case ends the run of the defect")
		})

		t.Run("lists a property check of the options that the defect fails", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			r := recorded(func(tb assert.TB) { c.Falsify(tb, conform.With(keepsEvery())) })
			assert.Contains(t, r.Message(), "\n\tStore.Get/keeps-every-value fails at the assertion equal with the "+
				`message "Get returns the value that Put stored under any key"`+"\n",
				"the message lists the property check with the assertion of its smallest case")
		})

		t.Run("applies the check to its defect with the doors of the options", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				_, isMemory := h.Reference().(*memory)
				assert.True(h, isMemory, "Reference returns the reference implementation of the option")
				apply(h)
			}
			c.Falsify(t, conform.Reference(newStore))
		})
	})
}
