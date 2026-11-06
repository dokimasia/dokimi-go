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
)

func TestHost(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a host of the name", func(t *testing.T) {
			t.Parallel()
			var name string
			inCase(t, targets, func(c *prop.Case, _ bool) { name = host.New(c, "node-1", nil, nil).Name() })

			assert.Equal(t, name, "node-1", "the name of the host")
		})

		t.Run("fails the case for a host under another scheduler than the hosts of its network", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				n := host.NewNetwork(c, false)
				host.New(c, "node-1", n, nil)
				host.New(c, "node-2", n, stateful.NewScheduler(c, stateful.Uniform()))
			})

			assert.Equal(t, got.Contract, "host: New of node-2 under another scheduler than the hosts of its network",
				"the failure names the host")
		})
	})

	t.Run("Broadcast", func(t *testing.T) {
		t.Parallel()

		t.Run("wakes a wait whose condition a step of the world made true", func(t *testing.T) {
			t.Parallel()
			var woken int
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				h, i, env := started(c)
				var ready atomic.Bool
				done := make(chan struct{}, 1)
				i.Go(func() {
					env.Wait(ready.Load)
					done <- struct{}{}
				})
				synctest.Wait()
				ready.Store(true)
				h.Broadcast()
				synctest.Wait()
				woken = len(done)
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, woken, 1, "the wait returns after the broadcast")
		})

		t.Run("leaves a parked task to the next run of the scheduler", func(t *testing.T) {
			t.Parallel()
			var runs []int
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
				h := host.New(c, "node-1", nil, s)
				_, env := h.Start()
				ready, done := false, 0
				env.Go(func() {
					env.Wait(func() bool { return ready })
					done++
				})
				s.Run()
				ready = true
				h.Broadcast()
				during := done
				s.Run()
				runs = []int{during, done}
			})

			assert.Equal(t, runs, []int{0, 1}, "the task ends at the next run and not at the broadcast")
		})
	})

	t.Run("Disk", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the disk across the incarnations of a process crash", func(t *testing.T) {
			t.Parallel()
			var got string
			inCase(t, targets, func(c *prop.Case, _ bool) {
				h, i, env := started(c)
				write(c, env.Disk(), "a", "body")
				i.Crash(host.ProcessCrash)
				_, next := h.Start()
				got = read(c, next.Disk(), "a")
			})

			assert.Equal(t, got, "body", "the next incarnation reads the file")
		})

		t.Run("returns a new empty disk after a disk replacement", func(t *testing.T) {
			t.Parallel()
			var entries int
			var same bool
			inCase(t, targets, func(c *prop.Case, _ bool) {
				h, i, env := started(c)
				before := h.Disk()
				write(c, env.Disk(), "a", "body")
				i.Crash(host.DiskReplacement)
				same = h.Disk() == before
				list, err := h.Disk().ReadDir(".")
				assert.NoError(c, err, "the new disk reads its root")
				entries = len(list)
			})

			assert.False(t, same, "the host has another disk")
			assert.Equal(t, entries, 0, "the new disk is empty")
		})
	})

	t.Run("Injector", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the calls of the dependencies of each incarnation", func(t *testing.T) {
			t.Parallel()
			var errs []error
			inCase(t, targets, func(c *prop.Case, _ bool) {
				h, _, env := started(c)
				h.Injector().FailNext(errBacking)
				errs = []error{env.Dependency("db").Call("Get"), env.Dependency("db").Call("Get")}
			})

			assert.Equal(t, errs, []error{errBacking, nil}, "the injector fails the first call")
		})
	})

	t.Run("Start", func(t *testing.T) {
		t.Parallel()

		t.Run("numbers the incarnations from 1", func(t *testing.T) {
			t.Parallel()
			var numbers []int
			inCase(t, targets, func(c *prop.Case, _ bool) {
				h, i, env := started(c)
				i.Crash(host.ProcessCrash)
				_, next := h.Start()
				numbers = []int{env.Incarnation(), next.Incarnation()}
			})

			assert.Equal(t, numbers, []int{1, 2}, "each incarnation has the next number")
		})

		t.Run("fails the case while an incarnation runs", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				h, _, _ := started(c)
				h.Start()
			})

			assert.Equal(
				t,
				got.Contract,
				"host: Start of node-1 while its incarnation 1 runs",
				"the failure names the host",
			)
		})
	})
}
