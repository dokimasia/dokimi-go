// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/conform"
)

// prober is the value of a door of an interface type.
type prober interface{ Probe() bool }

// probing is a prober that always probes.
type probing struct{}

// Probe reports true.
func (probing) Probe() bool { return true }

// opening returns keeps with a check that opens the endpoint of its subject
// and requires want before the statement of keeps.
func opening(want endpoint) conform.Check[Store] {
	c := keeps()
	apply := c.Apply
	c.Apply = func(h *conform.Handle[Store]) {
		assert.Equal(h, conform.Open[endpoint](h, h.Subject()), want, "Open returns the endpoint of the door")
		apply(h)
	}
	return c
}

func TestValue(t *testing.T) {
	t.Parallel()

	t.Run("Open", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the value of the door of an option of the application", func(t *testing.T) {
			t.Parallel()

			opening("primary").Run(t, newStore, conform.Supplies(endpointOf("primary")))
		})

		t.Run("passes the subject to the function of the door", func(t *testing.T) {
			t.Parallel()

			var opened Store
			door := conform.Supply(func(_ *conform.Handle[Store], s Store) endpoint {
				opened = s
				return "primary"
			})
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				other := h.New()
				_ = conform.Open[endpoint](h, other)
				assert.True(h, opened == other, "the function of the door receives the subject of Open")
				apply(h)
			}
			c.Run(t, newStore, conform.Supplies(door))
		})

		t.Run("returns the value of the door of the type of the value", func(t *testing.T) {
			t.Parallel()

			doors := conform.Supplies(
				conform.Supply(func(*conform.Handle[Store], Store) prober { return probing{} }),
				endpointOf("primary"),
			)
			c := opening("primary")
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				_, isProbing := conform.Open[prober](h, h.Subject()).(probing)
				assert.True(h, isProbing, "Open returns the prober of the door of its type")
				apply(h)
			}
			c.Run(t, newStore, doors)
		})

		t.Run("returns the nil value of a door of an interface type", func(t *testing.T) {
			t.Parallel()

			door := conform.Supply(func(*conform.Handle[Store], Store) prober { return nil })
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				assert.Nil(h, conform.Open[prober](h, h.Subject()), "Open returns the nil prober of the door")
				apply(h)
			}
			c.Run(t, newStore, conform.Supplies(door))
		})

		t.Run("returns the value of the door of the check on its planted defect", func(t *testing.T) {
			t.Parallel()

			c := opening("defect")
			c.Doors = conform.Doors[Store]{Values: []conform.Supplied[Store]{endpointOf("defect")}}
			c.Falsify(t)
		})

		t.Run("returns the value of the door of the options on a planted defect whose check lacks it",
			func(t *testing.T) {
				t.Parallel()

				opening("primary").Falsify(t, conform.Supplies(endpointOf("primary")))
			})

		t.Run("prefers the door of the check to the door of the options", func(t *testing.T) {
			t.Parallel()

			c := opening("defect")
			c.Doors = conform.Doors[Store]{Values: []conform.Supplied[Store]{endpointOf("defect")}}
			c.Falsify(t, conform.Supplies(endpointOf("primary")))
		})

		t.Run("counts a read of the clock in the function of the door as a read by a subject", func(t *testing.T) {
			t.Parallel()

			door := conform.Supply(func(h *conform.Handle[Store], _ Store) endpoint {
				_ = h.Clock()
				return "primary"
			})
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				_ = conform.Open[endpoint](h, h.Subject())
				h.Advance(time.Second)
				apply(h)
			}
			c.Run(t, newStore, conform.Supplies(door))
		})

		t.Run("counts a read of the clock after the function of the door as a read by the check", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				_ = conform.Open[endpoint](h, h.Subject())
				_ = h.Clock()
				h.Advance(time.Second)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore, conform.Supplies(endpointOf("primary"))) })
			faults := r.Faults()
			assert.Length(t, faults, 1, "Advance ends the check with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrClock, "no subject read the clock that the check advances")
		})

		t.Run("ends the check with a fault without the option", func(t *testing.T) {
			t.Parallel()

			var continued atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				_ = conform.Open[endpoint](h, h.Subject())
				continued.Store(true)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
			faults := r.Faults()
			assert.Length(t, faults, 1, "Open ends the check with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrOption, "the fault is the fault of a door that the handle lacks")
			expect.Equal(t, faults[0].Error(), "conform: the handle lacks the door: "+
				"Store.Get/keeps-the-value opens a value of the type conform_test.endpoint without the option "+
				"conform.Supplies. Pass the option to the application:\n"+
				"\tconform.Supplies(conform.Supply(func(h *conform.Handle[conform_test.Store], s conform_test.Store) "+
				"conform_test.endpoint { … }))",
				"the fault contains the option of the door")
			expect.False(t, continued.Load(), "the check ends at the door")
		})

		t.Run("ends the falsification with a fault without the door of the check", func(t *testing.T) {
			t.Parallel()

			var continued atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				_ = conform.Open[endpoint](h, h.Subject())
				continued.Store(true)
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			faults := r.Faults()
			assert.Length(t, faults, 1, "Open ends the falsification with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrOption, "the fault is the fault of a door that the handle lacks")
			expect.Equal(t, faults[0].Error(), "conform: the handle lacks the door: "+
				"Store.Get/keeps-the-value opens a value of the type conform_test.endpoint on its planted defect, "+
				"and the check has no door of the type. Give the check the door:\n"+
				"\tconform.Doors[conform_test.Store]{Values: []conform.Supplied[conform_test.Store]{conform.Supply("+
				"func(h *conform.Handle[conform_test.Store], s conform_test.Store) conform_test.endpoint { … })}}",
				"the fault contains the door of the check")
			expect.False(t, continued.Load(), "the check ends at the door")
		})

		t.Run("returns the zero value to a seat that goes on after a fatal failure", func(t *testing.T) {
			t.Parallel()

			got := endpoint("unopened")
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { got = conform.Open[endpoint](h, h.Subject()) }
			c.Run(assert.NewRecorder(), newStore)
			assert.Equal(t, got, "", "Open returns the zero endpoint")
		})

		t.Run("reports the line of the call of Open", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				c := keeps()
				c.Apply = func(h *conform.Handle[Store]) { _ = conform.Open[endpoint](h, h.Subject()) }
				c.Run(t, newStore)
				return
			}
			assert.Matches(t, childOutput(t),
				`value_test\.go:\d+: conform: the handle lacks the door: Store\.Get/keeps-the-value opens a value`,
				"the failure has the line of the call of Open")
		})
	})

	t.Run("Supplies", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the door of a type that an earlier option supplies", func(t *testing.T) {
			t.Parallel()

			opening("replica").Run(t, newStore,
				conform.Supplies(endpointOf("primary")), conform.Supplies(endpointOf("replica")))
		})

		t.Run("replaces the door of a type that an earlier door of the option supplies", func(t *testing.T) {
			t.Parallel()

			opening("replica").Run(t, newStore, conform.Supplies(endpointOf("primary"), endpointOf("replica")))
		})
	})
}
