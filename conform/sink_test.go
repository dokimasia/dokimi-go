// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// A sink is the one channel of a send channel type that every draw of the
// type returns while the test of an entry point runs. A goroutine receives
// each value that a subject sends to it.
func TestSink(t *testing.T) {
	t.Parallel()

	t.Run("SinkOf", func(t *testing.T) {
		t.Parallel()

		// The bubble fails at once when a send blocks, and when the goroutine of
		// the sink outlives the test of the bubble.
		t.Run("takes every send until the test of the entry point ends", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				b := measured(t, 10, func(b conform.B) {
					conform.Bench(b, sinkBudget(conform.Budget{}), storeGet, newStore)
				})
				assert.Equal(t, b.metric(t, errorsUnit), 0.0, "each call returns after its sends")
			})
		})

		t.Run("takes every send of a test without the method Context", func(t *testing.T) {
			t.Parallel()

			conform.Run(noContext{t: t}, sinkSuite(), newStore, conform.Cases[Store](1))
		})

		t.Run("yields its type to the generator of the option Using", func(t *testing.T) {
			t.Parallel()

			own := make(chan int, 100*sendsPerCase)
			t.Cleanup(func() {
				assert.True(t, len(own) >= sendsPerCase, "the cases send to the channel of the option Using")
			})
			conform.Run(t, sinkSuite(), newStore, conform.Cases[Store](1),
				conform.Using[Store](prop.Just[chan<- int](own)))
		})
	})

	t.Run("Sink", func(t *testing.T) {
		t.Parallel()

		t.Run("opens no channel as the zero value", func(t *testing.T) {
			t.Parallel()

			suite := everySuite()
			suite.Sinks = []conform.Sink{{}}
			conform.Run(t, suite, newStore)
		})
	})
}

// noContext is the test t without its method Context, as the test of another
// harness can be.
type noContext struct {
	t *testing.T
}

// Helper marks the caller as a helper of t.
func (n noContext) Helper() { n.t.Helper() }

// Errorf records a failure of t.
func (n noContext) Errorf(format string, args ...any) { n.t.Errorf(format, args...) }

// FailNow stops t.
func (n noContext) FailNow() { n.t.FailNow() }

// Run runs f as a subtest of t.
func (n noContext) Run(name string, f func(t *testing.T)) bool { return n.t.Run(name, f) }
