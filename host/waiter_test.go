// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
)

// TestWaiter checks the waits of a host in its two modes, through the waits
// of its environment and of its clock.
func TestWaiter(t *testing.T) {
	t.Parallel()

	t.Run("Wait", func(t *testing.T) {
		t.Parallel()

		t.Run("wakes a wait without a scheduler at the return of a function of the incarnation", func(t *testing.T) {
			t.Parallel()
			var woke bool
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				var set atomic.Bool
				done := make(chan struct{})
				env.Go(func() {
					env.Wait(set.Load)
					close(done)
				})
				synctest.Wait()
				i.Go(func() { set.Store(true) })
				<-done
				woke = true
				i.Crash(host.ProcessCrash)
			})

			assert.True(t, woke, "the return of the function wakes the wait")
		})

		t.Run("returns at once for a condition that is true", func(t *testing.T) {
			t.Parallel()
			var returned bool
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				env.Wait(func() bool { return true })
				returned = true
			})

			assert.True(t, returned, "the wait returns")
		})
	})

	t.Run("Sleep", func(t *testing.T) {
		t.Parallel()

		t.Run("sleeps a task on the clock of the scheduler until an advance passes its end", func(t *testing.T) {
			t.Parallel()
			var during, after bool
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, first bool) {
				h := host.New(c, "node-1", nil, s)
				_, env := h.Start()
				woke := false
				env.Go(func() {
					env.Clock().Sleep(time.Second)
					woke = true
				})
				s.Advance(999 * time.Millisecond)
				before := woke
				s.Advance(time.Millisecond)
				if first {
					during, after = before, woke
				}
			})

			assert.Equal(t, []bool{during, after}, []bool{false, true}, "the sleep ends at one second")
		})

		t.Run("wakes a task at the instant at which the bytes of a stream arrive", func(t *testing.T) {
			t.Parallel()
			var at time.Duration
			scheduled(t, slowLink, func(c *prop.Case, s *stateful.Scheduler, first bool) {
				n := host.NewNetwork(c, true, millisecond)
				h1 := host.New(c, "node-1", n, s)
				h2 := host.New(c, "node-2", n, s)
				_, e1 := h1.Start()
				_, e2 := h2.Start()
				l, err := e2.Listen(7000)
				assert.NoError(c, err, "node-2 listens")
				var arrived time.Duration
				e2.Go(func() {
					conn, acceptErr := l.Accept()
					assert.NoError(c, acceptErr, "node-2 accepts")
					_, readErr := conn.Read(make([]byte, 1))
					assert.NoError(c, readErr, "node-2 reads")
					arrived = s.Clock().Now().Sub(midnight)
				})
				e1.Go(func() {
					conn, dialErr := e1.Dial(c.Context(), "node-2:7000")
					assert.NoError(c, dialErr, "node-1 dials")
					_, writeErr := conn.Write([]byte("a"))
					assert.NoError(c, writeErr, "node-1 writes")
				})
				s.Advance(10 * time.Millisecond)
				if first {
					at = arrived
				}
			})

			assert.Equal(t, at, time.Millisecond, "the read returns at the latency, before the end of the advance")
		})

		t.Run("ends the sleep of a task at the crash of its incarnation", func(t *testing.T) {
			t.Parallel()
			var woke bool
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
				h := host.New(c, "node-1", nil, s)
				i, env := h.Start()
				ended := false
				env.Go(func() {
					env.Clock().Sleep(time.Hour)
					ended = true
				})
				s.Run()
				i.Crash(host.ProcessCrash)
				s.Run()
				woke = ended
			})

			assert.True(t, woke, "the sleep returns at the crash, before its hour")
		})
	})
}
