// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
)

// keepOne are the draws of a power loss that keeps one pending call of the
// file a.
const keepOne = `[{"label": "drift.node-1", "value": {"type": "int", "value": 0}},` +
	`{"label": "keep.a", "value": {"type": "int", "value": 1}}]`

func TestIncarnation(t *testing.T) {
	t.Parallel()

	t.Run("Go", func(t *testing.T) {
		t.Parallel()

		t.Run("runs f on a goroutine with the labels of the incarnation", func(t *testing.T) {
			t.Parallel()
			var leaked []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, _ := started(c)
				block := make(chan struct{})
				i.Go(func() { <-block })
				synctest.Wait()
				leaked = i.Leaked()
				close(block)
				i.Crash(host.ProcessCrash)
			})

			assert.Length(t, leaked, 1, "one goroutine of the incarnation")
			expect.Contains(t, leaked[0], `"dokimi.host":"node-1"`, "the stack has the label of the host")
			expect.Contains(t, leaked[0], `"dokimi.incarnation":"1"`, "the stack has the label of the incarnation")
		})

		t.Run("runs f as a task of the scheduler of the host", func(t *testing.T) {
			t.Parallel()
			var before, after bool
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
				h := host.New(c, "node-1", nil, s)
				i, _ := h.Start()
				ran := false
				i.Go(func() { ran = true })
				before = ran
				s.Run()
				after = ran
			})

			assert.Equal(t, []bool{before, after}, []bool{false, true}, "the task runs at the run of the scheduler")
		})
	})

	t.Run("Crash", func(t *testing.T) {
		t.Parallel()

		t.Run("ends the lifetime of the incarnation", func(t *testing.T) {
			t.Parallel()
			var err error
			inCase(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				i.Crash(host.ProcessCrash)
				err = env.Lifetime().Err()
			})

			assert.ErrorIs(t, err, context.Canceled, "the crash cancels the lifetime")
		})

		t.Run("keeps every pending write at a process crash", func(t *testing.T) {
			t.Parallel()
			var got string
			inCase(t, targets, func(c *prop.Case, _ bool) {
				h, i, env := started(c)
				pending(c, env.Disk(), "a", "ab", "cd")
				i.Crash(host.ProcessCrash)
				got = read(c, h.Disk(), "a")
			})

			assert.Equal(t, got, "abcd", "the page cache survives the crash of the process")
		})

		t.Run("keeps the pending calls that the case chooses at a power loss", func(t *testing.T) {
			t.Parallel()
			var got string
			inCase(t, keepOne, func(c *prop.Case, first bool) {
				h, i, env := started(c)
				pending(c, env.Disk(), "a", "ab", "cd")
				i.Crash(host.PowerLoss)
				if content := read(c, h.Disk(), "a"); first {
					got = content
				}
			})

			assert.Equal(t, got, "ab", "the case keeps the first pending write")
		})

		t.Run("drops every pending call at a power loss that keeps none", func(t *testing.T) {
			t.Parallel()
			var got string
			inCase(t, targets, func(c *prop.Case, first bool) {
				h, i, env := started(c)
				pending(c, env.Disk(), "a", "ab", "cd")
				i.Crash(host.PowerLoss)
				if content := read(c, h.Disk(), "a"); first {
					got = content
				}
			})

			assert.Equal(t, got, "", "the crash keeps the synced empty file")
		})

		t.Run("fails a blocked call of a dependency with ErrCrashed", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				h, i, env := started(c)
				h.Injector().Hold()
				done := make(chan error, 1)
				i.Go(func() { done <- env.Dependency("db").Call("Get") })
				synctest.Wait()
				i.Crash(host.ProcessCrash)
				err = <-done
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the crash ends the blocked call")
		})

		t.Run("fails a call that a hold of the disk blocks with ErrCrashed", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				h, i, env := started(c)
				h.Disk().Hold()
				done := make(chan error, 1)
				i.Go(func() {
					_, statErr := env.Disk().Stat("a")
					done <- statErr
				})
				synctest.Wait()
				i.Crash(host.DiskReplacement)
				err = <-done
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the crash ends the blocked call")
		})

		t.Run("fails the case at a second crash", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				_, i, _ := started(c)
				i.Crash(host.ProcessCrash)
				i.Crash(host.PowerLoss)
			})

			assert.Equal(t, got.Contract, "host: a second Crash of the incarnation 1 of node-1",
				"the failure names the incarnation")
		})

		t.Run("fails the case for the kind 0", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				_, i, _ := started(c)
				i.Crash(host.Kill(0))
			})

			assert.Equal(t, got.Contract, "host: Crash of node-1 by Kill(0), which is no kind of crash",
				"the failure names the kind")
		})

		t.Run("fails the case for a kind outside the three", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				_, i, _ := started(c)
				i.Crash(host.Kill(9))
			})

			assert.Equal(t, got.Contract, "host: Crash of node-1 by Kill(9), which is no kind of crash",
				"the failure names the kind")
		})
	})

	t.Run("Pause", func(t *testing.T) {
		t.Parallel()

		t.Run("blocks a call of the environment until Resume", func(t *testing.T) {
			t.Parallel()
			var during, after int
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				i.Pause()
				done := make(chan error, 1)
				i.Go(func() { done <- env.Dependency("db").Call("Get") })
				synctest.Wait()
				during = len(done)
				i.Resume()
				assert.NoError(c, <-done, "the call returns after the pause")
				after = 1
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, []int{during, after}, []int{0, 1}, "the call waits during the pause")
		})

		t.Run("fails a blocked call with ErrCrashed at a crash", func(t *testing.T) {
			t.Parallel()
			var err error
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				i.Pause()
				done := make(chan error, 1)
				i.Go(func() { done <- env.Dependency("db").Call("Get") })
				synctest.Wait()
				i.Crash(host.ProcessCrash)
				err = <-done
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the crash ends the paused call")
		})
	})

	t.Run("Leaked", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the goroutine of a crashed incarnation that ignored its lifetime", func(t *testing.T) {
			t.Parallel()
			var leaked []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, _ := started(c)
				block := make(chan struct{})
				i.Go(func() { <-block })
				i.Crash(host.ProcessCrash)
				synctest.Wait()
				leaked = i.Leaked()
				close(block)
			})

			assert.Length(t, leaked, 1, "the goroutine that waits on its channel")
		})

		t.Run("returns nothing once each goroutine of the incarnation has ended", func(t *testing.T) {
			t.Parallel()
			var leaked []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				i.Go(func() { <-env.Lifetime().Done() })
				i.Crash(host.ProcessCrash)
				synctest.Wait()
				leaked = i.Leaked()
			})

			assert.Empty(t, leaked, "the goroutine ended with its lifetime")
		})

		t.Run("leaves out the goroutines of another host of the same name", func(t *testing.T) {
			t.Parallel()
			var leaked []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, _ := started(c)
				_, other, _ := started(c)
				block := make(chan struct{})
				other.Go(func() { <-block })
				synctest.Wait()
				leaked = i.Leaked()
				close(block)
				i.Crash(host.ProcessCrash)
				other.Crash(host.ProcessCrash)
			})

			assert.Empty(t, leaked, "the first host has no goroutine")
		})

		t.Run("leaves out the goroutines of another incarnation", func(t *testing.T) {
			t.Parallel()
			var leaked []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				h, i, _ := started(c)
				i.Crash(host.ProcessCrash)
				next, _ := h.Start()
				block := make(chan struct{})
				next.Go(func() { <-block })
				synctest.Wait()
				leaked = i.Leaked()
				close(block)
				next.Crash(host.ProcessCrash)
			})

			assert.Empty(t, leaked, "the first incarnation has no goroutine")
		})

		t.Run("leaves out the timer of a read deadline that is pending at a crash", func(t *testing.T) {
			t.Parallel()
			var leaked []string
			bubbled(t, targets, func(c *prop.Case, first bool) {
				h := host.New(c, "node-1", host.NewNetwork(c, false), nil)
				i, env := h.Start()
				packets := deadlined(c, env, time.Now().Add(time.Hour))
				i.Go(func() { _, _, _ = packets.ReadFrom(make([]byte, 1)) })
				synctest.Wait()
				i.Crash(host.ProcessCrash)
				synctest.Wait()
				if first {
					leaked = i.Leaked()
				}
			})

			assert.Empty(t, leaked, "the timer of the deadline is no goroutine of the incarnation")
		})

		t.Run(
			"leaves out the timer of a read deadline that is pending at a crash under a scheduler",
			func(t *testing.T) {
				t.Parallel()
				var leaked []string
				scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, first bool) {
					h := host.New(c, "node-1", host.NewNetwork(c, false), s)
					i, env := h.Start()
					packets := deadlined(c, env, s.Clock().Now().Add(time.Hour))
					i.Go(func() { _, _, _ = packets.ReadFrom(make([]byte, 1)) })
					s.Run()
					i.Crash(host.ProcessCrash)
					s.Run()
					if first {
						leaked = i.Leaked()
					}
				})

				assert.Empty(t, leaked, "the timer of the deadline is no task of the incarnation")
			},
		)

		t.Run("leaves out the timer of a sleep that a crash ended under a scheduler", func(t *testing.T) {
			t.Parallel()
			var leaked []string
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, first bool) {
				h := host.New(c, "node-1", nil, s)
				i, env := h.Start()
				env.Go(func() { env.Clock().Sleep(time.Hour) })
				s.Run()
				i.Crash(host.ProcessCrash)
				s.Run()
				if first {
					leaked = i.Leaked()
				}
			})

			assert.Empty(t, leaked, "the timer of the sleep is no task of the incarnation")
		})
	})
}

// deadlined returns a connection of datagrams of env on the port 9000 whose
// reads end at the deadline.
func deadlined(tb assert.TB, env *host.Env, deadline time.Time) net.PacketConn {
	tb.Helper()

	packets, err := env.ListenPacket(9000)
	assert.NoError(tb, err, "the host listens on the port 9000")
	assert.NoError(tb, packets.SetReadDeadline(deadline), "the connection takes the deadline")
	return packets
}

// pending writes each part of parts to the file name of fsys after an empty
// synced content, without a sync, so each part is a pending write.
func pending(tb assert.TB, fsys medium.FS, name string, parts ...string) {
	tb.Helper()

	write(tb, fsys, name, "")
	assert.NoError(tb, fsys.SyncDir("."), "the disk syncs the entry of "+name)
	f, err := fsys.OpenFile(name, os.O_WRONLY, 0)
	assert.NoError(tb, err, "the disk opens "+name)
	for _, part := range parts {
		_, err := io.WriteString(f, part)
		assert.NoError(tb, err, "the disk writes "+name)
	}
}
