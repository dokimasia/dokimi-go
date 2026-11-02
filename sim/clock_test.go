// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/sim"
)

func TestClock(t *testing.T) {
	t.Parallel()

	t.Run("Now", func(t *testing.T) {
		t.Parallel()

		t.Run("returns Start for a process of a world whose time has not advanced", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				expect.Equal(c, w.Process("store").Clock().Now(), sim.Start, "every clock starts at Start")
			})
		})

		t.Run("advances between half and twice the time of the run", func(t *testing.T) {
			t.Parallel()

			prop.ForAll(t, "a clock drifts within its bounds", func(c *prop.Case) {
				run := c.Draw(prop.Duration(0, time.Hour), "run")
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Advance(run)
				advanced := w.Process("store").Clock().Now().Sub(sim.Start)
				assert.InRange(c, advanced, float64(run/2), float64(2*run), "a clock drifts within its bounds")
			})
		})

		t.Run("is at the time of a timer on its process once the gap of the timer has passed", func(t *testing.T) {
			t.Parallel()

			prop.ForAll(t, "a timer is due at its time on the clock of its process", func(c *prop.Case) {
				after := c.Draw(prop.Duration(time.Millisecond, time.Hour), "after")
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				before := c.Draw(prop.Duration(0, time.Hour), "before")
				w.Step("store")
				w.Advance(before)
				clock := w.Process("store").Clock()
				set := clock.Now()
				w.After(w.Process("store"), after, ticked{token: 1})
				gap, pending := w.Next()
				assert.True(c, pending, "the timer is pending")
				w.Advance(gap - 1)
				assert.Empty(c, w.Pending(), "the timer is not due before its gap has passed")
				w.Advance(1)
				assert.Equal(c, w.Pending(), []string{"store"}, "the timer is due once its gap has passed")
				assert.InRange(c, clock.Now().Sub(set), float64(after), float64(after+time.Nanosecond),
					"the clock is at the time of the timer, or one nanosecond after it")
			})
		})
	})
}
