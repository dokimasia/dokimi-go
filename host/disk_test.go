// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"io"
	"os"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
)

// TestDisk checks the view of an incarnation on the disk of its host, which
// Env.Disk returns.
func TestDisk(t *testing.T) {
	t.Parallel()

	t.Run("Disk", func(t *testing.T) {
		t.Parallel()

		t.Run("passes each call of the view to the disk of the host", func(t *testing.T) {
			t.Parallel()
			var names []string
			var content string
			inCase(t, targets, func(c *prop.Case, _ bool) {
				h, _, env := started(c)
				disk := env.Disk()
				assert.NoError(c, disk.MkdirAll("d", 0o755), "the view makes d")
				write(c, disk, "d/a", "body")
				assert.NoError(c, disk.Rename("d/a", "d/b"), "the view renames d/a")
				assert.NoError(c, disk.SyncDir("d"), "the view syncs d")
				write(c, disk, "c", "gone")
				assert.NoError(c, disk.Remove("c"), "the view removes c")
				entries, err := disk.ReadDir("d")
				assert.NoError(c, err, "the view reads d")
				names = names[:0]
				for _, e := range entries {
					names = append(names, e.Name())
				}
				_, err = disk.Stat("d/b")
				assert.NoError(c, err, "the view stats d/b")
				content = read(c, h.Disk(), "d/b")
				_, err = h.Disk().Stat("c")
				assert.ErrorIs(c, err, os.ErrNotExist, "the disk has no c")
			})

			assert.Equal(t, names, []string{"b"}, "the renamed file")
			assert.Equal(t, content, "body", "the host reads the content")
		})

		t.Run("passes each call of a file of the view to the file of the disk", func(t *testing.T) {
			t.Parallel()
			var got string
			var size, offset int64
			var closed error
			inCase(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				f, err := env.Disk().OpenFile("a", os.O_CREATE|os.O_RDWR, 0o644)
				assert.NoError(c, err, "the view opens a")
				_, err = f.WriteAt([]byte("xyz"), 0)
				assert.NoError(c, err, "the file writes at an offset")
				offset, err = f.Seek(1, io.SeekStart)
				assert.NoError(c, err, "the file seeks")
				assert.NoError(c, f.Truncate(2), "the file truncates")
				assert.NoError(c, f.Sync(), "the file syncs")
				buf := make([]byte, 2)
				_, err = f.ReadAt(buf, 0)
				assert.NoError(c, err, "the file reads at an offset")
				info, err := f.Stat()
				assert.NoError(c, err, "the file stats")
				got, size = string(buf), info.Size()
				assert.NoError(c, f.Close(), "the file closes")
				_, closed = f.Read(buf)
			})

			assert.Equal(t, got, "xy", "the file reads what it wrote")
			assert.Equal(
				t,
				[]int64{size, offset},
				[]int64{2, 1},
				"the size after the truncate, and the offset of the seek",
			)
			assert.ErrorIs(t, closed, os.ErrClosed, "the close closes the file of the disk")
		})

		t.Run(
			"keeps a write that a file of the view syncs at a power loss that keeps no pending call",
			func(t *testing.T) {
				t.Parallel()
				var got string
				inCase(t, targets, func(c *prop.Case, first bool) {
					h, i, env := started(c)
					write(c, env.Disk(), "a", "body")
					assert.NoError(c, env.Disk().SyncDir("."), "the view syncs the entry of a")
					i.Crash(host.PowerLoss)
					if content := read(c, h.Disk(), "a"); first {
						got = content
					}
				})

				assert.Equal(t, got, "body", "the sync of the file of the view makes its write durable")
			},
		)

		pauses := []struct {
			name string
			give func(f medium.File) error
		}{
			{name: "blocks aread of a file while the incarnation is paused", give: func(f medium.File) error {
				_, err := f.Read(make([]byte, 1))
				return err
			}},
			{name: "blocks aread at an offset while the incarnation is paused", give: func(f medium.File) error {
				_, err := f.ReadAt(make([]byte, 1), 0)
				return err
			}},
			{name: "blocks awrite while the incarnation is paused", give: func(f medium.File) error {
				_, err := f.Write([]byte("x"))
				return err
			}},
			{name: "blocks awrite at an offset while the incarnation is paused", give: func(f medium.File) error {
				_, err := f.WriteAt([]byte("x"), 0)
				return err
			}},
			{name: "blocks aseek while the incarnation is paused", give: func(f medium.File) error {
				_, err := f.Seek(0, io.SeekStart)
				return err
			}},
			{name: "blocks async of a file while the incarnation is paused", give: func(f medium.File) error {
				return f.Sync()
			}},
			{name: "blocks atruncate while the incarnation is paused", give: func(f medium.File) error {
				return f.Truncate(0)
			}},
			{name: "blocks astat of a file while the incarnation is paused", give: func(f medium.File) error {
				_, err := f.Stat()
				return err
			}},
			{name: "blocks aclose while the incarnation is paused", give: func(f medium.File) error {
				return f.Close()
			}},
		}
		for _, tt := range pauses {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var during int
				bubbled(t, targets, func(c *prop.Case, _ bool) {
					_, i, env := started(c)
					write(c, env.Disk(), "a", "body")
					f, err := env.Disk().OpenFile("a", os.O_RDWR, 0)
					assert.NoError(c, err, "the view opens a")
					i.Pause()
					done := make(chan error, 1)
					i.Go(func() { done <- tt.give(f) })
					synctest.Wait()
					during = len(done)
					i.Resume()
					assert.NoError(c, <-done, "the call returns after the pause")
					i.Crash(host.ProcessCrash)
				})

				assert.Equal(t, during, 0, "the call waits during the pause")
			})
		}

		tests := []struct {
			name string
			give func(disk medium.FS, f medium.File) error
		}{
			{name: "fails an open after the crash", give: func(d medium.FS, _ medium.File) error {
				_, err := d.OpenFile("b", os.O_CREATE|os.O_RDWR, 0o644)
				return err
			}},
			{
				name: "fails a remove after the crash",
				give: func(d medium.FS, _ medium.File) error { return d.Remove("a") },
			},
			{
				name: "fails a rename after the crash",
				give: func(d medium.FS, _ medium.File) error { return d.Rename("a", "b") },
			},
			{
				name: "fails a mkdir after the crash",
				give: func(d medium.FS, _ medium.File) error { return d.MkdirAll("d", 0o755) },
			},
			{name: "fails a read of a directory after the crash", give: func(d medium.FS, _ medium.File) error {
				_, err := d.ReadDir(".")
				return err
			}},
			{name: "fails a stat after the crash", give: func(d medium.FS, _ medium.File) error {
				_, err := d.Stat("a")
				return err
			}},
			{
				name: "fails a sync of a directory after the crash",
				give: func(d medium.FS, _ medium.File) error { return d.SyncDir(".") },
			},
			{name: "fails a read of a file after the crash", give: func(_ medium.FS, f medium.File) error {
				_, err := f.Read(make([]byte, 1))
				return err
			}},
			{name: "fails a read at an offset after the crash", give: func(_ medium.FS, f medium.File) error {
				_, err := f.ReadAt(make([]byte, 1), 0)
				return err
			}},
			{name: "fails a write after the crash", give: func(_ medium.FS, f medium.File) error {
				_, err := f.Write([]byte("x"))
				return err
			}},
			{name: "fails a write at an offset after the crash", give: func(_ medium.FS, f medium.File) error {
				_, err := f.WriteAt([]byte("x"), 0)
				return err
			}},
			{name: "fails a seek after the crash", give: func(_ medium.FS, f medium.File) error {
				_, err := f.Seek(0, io.SeekStart)
				return err
			}},
			{
				name: "fails a sync of a file after the crash",
				give: func(_ medium.FS, f medium.File) error { return f.Sync() },
			},
			{
				name: "fails a truncate after the crash",
				give: func(_ medium.FS, f medium.File) error { return f.Truncate(0) },
			},
			{name: "fails a stat of a file after the crash", give: func(_ medium.FS, f medium.File) error {
				_, err := f.Stat()
				return err
			}},
			{name: "fails a close after the crash", give: func(_ medium.FS, f medium.File) error { return f.Close() }},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var err error
				inCase(t, targets, func(c *prop.Case, _ bool) {
					_, i, env := started(c)
					write(c, env.Disk(), "a", "body")
					f, openErr := env.Disk().OpenFile("a", os.O_RDWR, 0)
					assert.NoError(c, openErr, "the view opens a")
					i.Crash(host.DiskReplacement)
					err = tt.give(env.Disk(), f)
				})

				assert.ErrorIs(t, err, medium.ErrCrashed, "the fence of the crash refuses the call")
			})
		}

		t.Run("returns the error of the disk for a file that does not open", func(t *testing.T) {
			t.Parallel()
			var err error
			inCase(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				_, err = env.Disk().OpenFile("missing", os.O_RDONLY, 0)
			})

			assert.ErrorIs(t, err, os.ErrNotExist, "the disk has no such file")
		})

		t.Run("blocks a call of the view while the incarnation is paused", func(t *testing.T) {
			t.Parallel()
			var during int
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				i.Pause()
				done := make(chan error, 1)
				i.Go(func() {
					_, err := env.Disk().Stat(".")
					done <- err
				})
				synctest.Wait()
				during = len(done)
				i.Resume()
				assert.NoError(c, <-done, "the stat returns after the pause")
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, during, 0, "the stat waits during the pause")
		})

		t.Run("blocks a call of the view while the disk is held until the release", func(t *testing.T) {
			t.Parallel()
			var runs []int
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				h, i, env := started(c)
				h.Disk().Hold()
				done := make(chan error, 1)
				i.Go(func() {
					_, err := env.Disk().Stat(".")
					done <- err
				})
				synctest.Wait()
				during := len(done)
				h.Disk().Release()
				synctest.Wait()
				runs = []int{during, len(done)}
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, runs, []int{0, 1}, "the stat returns at the release")
		})

		t.Run("parks the task of a call of the view while the incarnation is paused", func(t *testing.T) {
			t.Parallel()
			var runs []int
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
				h := host.New(c, "node-1", nil, s)
				i, env := h.Start()
				i.Pause()
				done := 0
				i.Go(func() {
					_, err := env.Disk().Stat(".")
					assert.NoError(c, err, "the stat returns after the pause")
					done++
				})
				s.Run()
				during := done
				i.Resume()
				s.Run()
				runs = []int{during, done}
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, runs, []int{0, 1}, "the task returns at the run after the resume")
		})

		t.Run("fails a call of the view that a pause parks with ErrCrashed at a crash", func(t *testing.T) {
			t.Parallel()
			var err error
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
				h := host.New(c, "node-1", nil, s)
				i, env := h.Start()
				i.Pause()
				i.Go(func() { _, err = env.Disk().Stat(".") })
				s.Run()
				i.Crash(host.ProcessCrash)
				s.Run()
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the crash ends the paused call")
		})

		t.Run("parks the task of a call of the view while the disk is held", func(t *testing.T) {
			t.Parallel()
			var runs []int
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
				h := host.New(c, "node-1", nil, s)
				i, env := h.Start()
				h.Disk().Hold()
				done := 0
				i.Go(func() {
					_, err := env.Disk().Stat(".")
					assert.NoError(c, err, "the stat returns after the release")
					done++
				})
				s.Run()
				during := done
				h.Disk().Release()
				s.Run()
				runs = []int{during, done}
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, runs, []int{0, 1}, "the task returns at the run after the release")
		})

		t.Run("fails a call of the view that a hold of the disk parks with ErrCrashed at a crash", func(t *testing.T) {
			t.Parallel()
			var err error
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
				h := host.New(c, "node-1", nil, s)
				i, env := h.Start()
				h.Disk().Hold()
				i.Go(func() { _, err = env.Disk().Stat(".") })
				s.Run()
				i.Crash(host.PowerLoss)
				s.Run()
			})

			assert.ErrorIs(t, err, medium.ErrCrashed, "the crash ends the call")
		})
	})
}
