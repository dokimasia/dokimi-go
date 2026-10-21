// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/stub"
)

// A strict order tracker refuses a call before its prerequisite, and a
// lenient tracker checks nothing.
func TestOrderTracker(t *testing.T) {
	t.Parallel()

	t.Run("Record", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			strict bool
		}{
			{name: "marks the method as called in strict mode", strict: true},
			{name: "marks nothing in lenient mode"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				o := stub.NewOrderTracker(t, tt.strict)
				o.Record("Open")
				assert.Equal(t, o.Called("Open"), tt.strict, "a strict tracker marks the call")
			})
		}
	})

	t.Run("AssertAfter", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a call after its prerequisite", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			o := stub.NewOrderTracker(r, true)
			o.Record("Open")
			o.AssertAfter("Read", "Open")
			assert.False(t, r.Failed(), "Read follows Open")
		})

		t.Run("stops the test for a call before its prerequisite", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			stub.NewOrderTracker(r, true).AssertAfter("Read", "Open")
			assert.Equal(t, r.Message(), "Read: the double refuses the call before a call of Open",
				"the failure states both methods")
		})

		tests := []struct {
			name   string
			strict bool
			tb     bool
		}{
			{name: "checks nothing in lenient mode", tb: true},
			{name: "checks nothing without a TB", strict: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				r := assert.NewRecorder()
				var tb assert.TB
				if tt.tb {
					tb = r
				}
				stub.NewOrderTracker(tb, tt.strict).AssertAfter("Read", "Open")
				assert.False(t, r.Failed(), "the tracker reports nothing")
			})
		}
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("clears the marks", func(t *testing.T) {
			t.Parallel()

			o := stub.NewOrderTracker(t, true)
			o.Record("Open")
			assert.True(t, o.Called("Open"), "the tracker marks Open before the reset")
			o.Reset()
			assert.False(t, o.Called("Open"), "the reset clears the mark of Open")
		})
	})

	t.Run("String", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the called methods in the order of their names", func(t *testing.T) {
			t.Parallel()

			o := stub.NewOrderTracker(t, true)
			o.Record("Open")
			expect.Equal(t, o.String(), "OrderTracker{called: [Open]}", "the tracker has the one method")
			o.Record("Close")
			expect.Equal(t, o.String(), "OrderTracker{called: [Close Open]}", "the methods are sorted")
		})

		t.Run("returns an empty list without calls", func(t *testing.T) {
			t.Parallel()

			expect.Equal(t, stub.NewOrderTracker(t, true).String(), "OrderTracker{called: []}",
				"a new tracker has no methods")
		})
	})
}
