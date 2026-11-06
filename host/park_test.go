// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"context"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
)

func TestPark(t *testing.T) {
	t.Parallel()

	t.Run("Park", func(t *testing.T) {
		t.Parallel()

		t.Run("parks a task under the scheduler until the crash of its incarnation", func(t *testing.T) {
			t.Parallel()
			var during, after bool
			var got error
			synctest.Test(t, func(t *testing.T) {
				scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
					h := host.New(c, "node-1", nil, s)
					i, env := h.Start()
					returned := false
					env.Go(func() {
						got = host.Park(env.Lifetime())
						returned = true
					})
					s.Run()
					during = returned
					i.Crash(host.ProcessCrash)
					s.Run()
					after = returned
				})
			})

			assert.Equal(t, []bool{during, after}, []bool{false, true}, "the task returns at the crash alone")
			assert.ErrorIs(t, got, context.Canceled, "the park returns the error of the lifetime")
		})

		t.Run("parks a task under the scheduler until a context of its lifetime ends", func(t *testing.T) {
			t.Parallel()
			var during, after bool
			var got error
			synctest.Test(t, func(t *testing.T) {
				scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
					h := host.New(c, "node-1", nil, s)
					i, env := h.Start()
					ctx, cancel := context.WithCancel(env.Lifetime())
					returned := false
					env.Go(func() {
						got = host.Park(ctx)
						returned = true
					})
					s.Run()
					during = returned
					cancel()
					s.Run()
					after = returned
					i.Crash(host.ProcessCrash)
				})
			})

			assert.Equal(t, []bool{during, after}, []bool{false, true}, "the task returns at the cancel alone")
			assert.ErrorIs(t, got, context.Canceled, "the park returns the error of the context")
		})

		t.Run("waits for the end of a context of the lifetime without a scheduler", func(t *testing.T) {
			t.Parallel()
			var parked bool
			var got error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				ctx, cancel := context.WithCancel(env.Lifetime())
				done := make(chan error, 1)
				env.Go(func() { done <- host.Park(ctx) })
				synctest.Wait()
				parked = len(done) == 0
				cancel()
				got = <-done
				i.Crash(host.ProcessCrash)
			})

			assert.True(t, parked, "the call waits while its context runs")
			assert.ErrorIs(t, got, context.Canceled, "the park returns the error of the context")
		})

		t.Run("waits for the end of a context outside an incarnation", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- host.Park(ctx) }()
				synctest.Wait()
				parked := len(done) == 0
				cancel()

				assert.True(t, parked, "the call waits while its context runs")
				assert.ErrorIs(t, <-done, context.Canceled, "the park returns the error of the context")
			})
		})
	})
}
