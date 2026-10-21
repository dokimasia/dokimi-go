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

// The options of an application add checks, excuse checks, and supply the
// doors of the handle that only the consumer can build.
func TestSuite(t *testing.T) {
	t.Parallel()

	empty := conform.Suite[Store]{Declaration: "Store"}

	t.Run("Option", func(t *testing.T) {
		t.Parallel()

		t.Run("configures nothing as the zero value", func(t *testing.T) {
			t.Parallel()

			keeps().Run(t, newStore, conform.Option[Store]{})
		})
	})

	t.Run("With", func(t *testing.T) {
		t.Parallel()

		t.Run("applies the added check to its planted defect before the subject", func(t *testing.T) {
			t.Parallel()

			var applied atomic.Int32
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				applied.Add(1)
				apply(h)
			}
			t.Cleanup(func() {
				assert.Equal(t, applied.Load(), int32(2),
					"the application applies the check to its defect and to the subject")
			})
			conform.Run(t, empty, newStore, conform.With(c))
		})
	})

	t.Run("Excuse", func(t *testing.T) {
		t.Parallel()

		t.Run("skips the excused check", func(t *testing.T) {
			t.Parallel()

			var applied atomic.Int32
			c := keeps()
			c.Apply = func(*conform.Handle[Store]) { applied.Add(1) }
			t.Cleanup(func() {
				assert.Equal(t, applied.Load(), int32(0), "the application does not apply an excused check")
			})
			conform.Run(t, empty, newStore, conform.With(c),
				conform.Excuse(storeGet.Check("keeps-the-value"), "the store keeps no values"))
		})

		t.Run("reports the reason of the excusal in the skip of the check", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, empty, newStore, conform.With(keeps()),
					conform.Excuse(storeGet.Check("keeps-the-value"), "the store keeps no values"))
				return
			}
			assert.Contains(t, childOutput(t), "Store.Get/keeps-the-value is excused: the store keeps no values",
				"the skip of the check contains the reason of the excusal")
		})
	})

	t.Run("Reference", func(t *testing.T) {
		t.Parallel()

		t.Run("supplies a fresh reference implementation for each call", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				assert.True(h, h.Reference() != h.Reference(), "Reference returns a fresh reference implementation")
			}
			c.Run(t, newStore, conform.Reference(newStore))
		})
	})

	t.Run("Second", func(t *testing.T) {
		t.Parallel()

		t.Run("supplies a fresh second subject for each call", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				assert.True(h, h.Second() != h.Second(), "Second returns a fresh second subject")
			}
			c.Run(t, newStore, conform.Second(newStore))
		})
	})

	t.Run("Restartable", func(t *testing.T) {
		t.Parallel()

		t.Run("supplies the restart of a subject", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				s := h.Subject()
				s.Put("k", "v")
				got, _ := h.Restart(s).Get("k")
				assert.Equal(h, got, "v", "the restarted subject has the values of the subject")
			}
			restart := func(_ *conform.Handle[Store], s Store) Store {
				restarted := newMemory()
				value, _ := s.Get("k")
				restarted.Put("k", value)
				return restarted
			}
			c.Run(t, newStore, conform.Restartable(restart))
		})
	})
}
