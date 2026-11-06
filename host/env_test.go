// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
)

func TestEnv(t *testing.T) {
	t.Parallel()

	t.Run("Name", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the name of the host", func(t *testing.T) {
			t.Parallel()
			var name string
			inCase(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				name = env.Name()
			})

			assert.Equal(t, name, "node-1", "the name of the host")
		})
	})

	t.Run("Lifetime", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a context that runs until the crash", func(t *testing.T) {
			t.Parallel()
			var err error
			inCase(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				err = env.Lifetime().Err()
			})

			assert.NoError(t, err, "the lifetime of a running incarnation")
		})
	})

	t.Run("Disk", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a view of the disk of the host", func(t *testing.T) {
			t.Parallel()
			var got string
			inCase(t, targets, func(c *prop.Case, _ bool) {
				h, _, env := started(c)
				write(c, env.Disk(), "a", "body")
				got = read(c, h.Disk(), "a")
			})

			assert.Equal(t, got, "body", "the host reads what the view wrote")
		})
	})

	t.Run("Clock", func(t *testing.T) {
		t.Parallel()

		t.Run("returns one clock for the incarnation", func(t *testing.T) {
			t.Parallel()
			var same bool
			inCase(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				first, second := env.Clock(), env.Clock()
				same = first == second
			})

			assert.True(t, same, "two calls return the same clock")
		})
	})

	t.Run("Rand", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a source of the choices of the case", func(t *testing.T) {
			t.Parallel()
			var hosted, direct uint64
			inCase(t, targets, func(c *prop.Case, first bool) {
				_, _, env := started(c)
				if v := env.Rand().Uint64(); first {
					hosted = v
				}
			})
			inCase(t, targets, func(c *prop.Case, first bool) {
				c.Draw(prop.Integer(-500, 1000), "drift.node-1")
				if v := c.Rand().Uint64(); first {
					direct = v
				}
			})

			assert.Equal(t, hosted, direct, "the value of the source is the next choice of the case")
		})
	})

	t.Run("Dependency", func(t *testing.T) {
		t.Parallel()

		t.Run("returns one dependency for each name", func(t *testing.T) {
			t.Parallel()
			var same, other bool
			inCase(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				db, again, cache := env.Dependency("db"), env.Dependency("db"), env.Dependency("cache")
				same, other = db == again, db == cache
			})

			assert.Equal(t, []bool{same, other}, []bool{true, false}, "a dependency of its own for each name")
		})

		t.Run("fails each call after the crash of the incarnation with ErrCrashed", func(t *testing.T) {
			t.Parallel()
			var err error
			inCase(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				db := env.Dependency("db")
				i.Crash(host.ProcessCrash)
				err = db.Call("Get")
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the fence of the crash ends the dependency")
		})
	})

	t.Run("Go", func(t *testing.T) {
		t.Parallel()

		t.Run("runs f on a goroutine of the incarnation", func(t *testing.T) {
			t.Parallel()
			var leaked int
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				block := make(chan struct{})
				env.Go(func() { <-block })
				synctest.Wait()
				leaked = len(i.Leaked())
				close(block)
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, leaked, 1, "the goroutine has the labels of the incarnation")
		})
	})

	t.Run("Yield", func(t *testing.T) {
		t.Parallel()

		t.Run("wakes a wait of the incarnation without a scheduler", func(t *testing.T) {
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
				set.Store(true)
				block := make(chan struct{})
				env.Go(func() {
					env.Yield()
					<-block
				})
				<-done
				woke = true
				close(block)
				i.Crash(host.ProcessCrash)
			})

			assert.True(t, woke, "the yield wakes the wait")
		})

		t.Run("ends the turn of the task under the scheduler", func(t *testing.T) {
			t.Parallel()
			var order []string
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
				h := host.New(c, "node-1", nil, s)
				_, env := h.Start()
				var log []string
				env.Go(func() {
					log = append(log, "a1")
					env.Yield()
					log = append(log, "a2")
				})
				env.Go(func() { log = append(log, "b") })
				s.Run()
				order = log
			})

			assert.Equal(t, order, []string{"a1", "b", "a2"}, "b runs in the turn after the yield of a")
		})
	})

	t.Run("Wait", func(t *testing.T) {
		t.Parallel()

		t.Run("parks the task under the scheduler until its condition is true", func(t *testing.T) {
			t.Parallel()
			var order []string
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
				h := host.New(c, "node-1", nil, s)
				_, env := h.Start()
				var log []string
				set := false
				env.Go(func() {
					env.Wait(func() bool { return set })
					log = append(log, "woke")
				})
				env.Go(func() {
					log = append(log, "set")
					set = true
				})
				s.Run()
				order = log
			})

			assert.Equal(t, order, []string{"set", "woke"}, "the wait ends after the other task sets the flag")
		})
	})

	t.Run("Listen", func(t *testing.T) {
		t.Parallel()

		t.Run("fails with ErrNoNetwork for a host without a network", func(t *testing.T) {
			t.Parallel()
			var errs []error
			inCase(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				_, listen := env.Listen(7000)
				_, dial := env.Dial(c.Context(), "node-2:7000")
				_, packet := env.ListenPacket(7000)
				errs = []error{listen, dial, packet}
			})

			for _, err := range errs {
				assert.ErrorIs(t, err, host.ErrNoNetwork, "the host has no network")
			}
		})
	})
}
