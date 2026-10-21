// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/dokimi-go/conform"
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

		t.Run("fails when the defect stops the check before an assertion fails", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { _ = h.Reference() }
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-the-value fails the check before an assertion fails. `+
					`Falsification needs a failure of the assertion whose message is the statement `+
					`"Get returns the value that Put stored". The check failed with this message:`+"\n"+
					`Store.Get/keeps-the-value calls Reference without the option conform.Reference. `+
					`Pass the option to the application:`+"\n"+
					"\tconform.Reference(func(h *conform.Handle[conform_test.Store]) conform_test.Store { … })",
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
