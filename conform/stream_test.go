// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"sync"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// ticks is a defined channel that a subject receives from.
type ticks <-chan int

// A stream is a closed channel with a drawn list of elements in its buffer,
// so a receive from it never blocks.
func TestStream(t *testing.T) {
	t.Parallel()

	t.Run("Stream", func(t *testing.T) {
		t.Parallel()

		t.Run("returns closed channels whose receives return the drawn elements", func(t *testing.T) {
			t.Parallel()

			received := 0
			for _, ch := range prop.Values(conform.Stream[<-chan int](prop.Just(7)), 1, 20) {
				for v := range ch {
					assert.Equal(t, v, 7, "each receive returns a drawn element")
					received++
				}
			}
			expect.True(t, received > 0, "the streams of the seed contain elements")
		})

		t.Run("returns a value of a defined channel type", func(t *testing.T) {
			t.Parallel()

			received := 0
			for _, ch := range prop.Values(conform.Stream[ticks](prop.Just(1)), 1, 20) {
				for range ch {
					received++
				}
			}
			expect.True(t, received > 0, "the streams of the defined type contain elements")
		})

		t.Run("returns a bidirectional channel whose send panics", func(t *testing.T) {
			t.Parallel()

			ch := prop.Values(conform.Stream[chan int](prop.Just(1)), 1, 1)[0]
			assert.Panics(t, func() { ch <- 1 }, "a send to a closed channel panics")
		})

		t.Run("draws a stream without elements among the cases of a run", func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			fewest := -1
			prop.ForAll(t, "a stream has a length", func(c *prop.Case) {
				n := 0
				for range c.Draw(conform.Stream[<-chan int](prop.Of[int]()), "stream") {
					n++
				}
				mu.Lock()
				defer mu.Unlock()
				if fewest < 0 || n < fewest {
					fewest = n
				}
			}, prop.Seed(1))
			assert.Equal(t, fewest, 0, "a case of the run draws a stream without elements")
		})
	})

	t.Run("Closed", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a closed channel without elements", func(t *testing.T) {
			t.Parallel()

			v, open := <-conform.Closed[<-chan int]()
			expect.False(t, open, "a receive reports that the channel is closed")
			expect.Equal(t, v, 0, "a receive returns the zero value")
		})

		t.Run("returns a value of a defined channel type", func(t *testing.T) {
			t.Parallel()

			_, open := <-conform.Closed[ticks]()
			assert.False(t, open, "a receive reports that the channel is closed")
		})
	})
}
