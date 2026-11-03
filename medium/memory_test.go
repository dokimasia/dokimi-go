// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package medium_test

import (
	"io"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/medium"
)

// durable returns a Memory with the file a of content, whose content and entry
// are durable.
func durable(t *testing.T, content string) *medium.Memory {
	t.Helper()

	m := medium.NewMemory()
	f := open(t, m, "a")
	_, err := io.WriteString(f, content)
	assert.NoError(t, err, "the medium writes a")
	assert.NoError(t, f.Sync(), "the medium syncs a")
	assert.NoError(t, m.SyncDir("."), "the medium syncs the root")
	return m
}

// exists reports whether the entry name of m exists.
func exists(t *testing.T, m *medium.Memory, name string) bool {
	t.Helper()

	_, err := m.Stat(name)
	if err != nil {
		assert.ErrorIs(t, err, fs.ErrNotExist, "the medium has no entry "+name)
	}
	return err == nil
}

func TestMemory(t *testing.T) {
	t.Parallel()

	t.Run("NewMemory", func(t *testing.T) {
		t.Parallel()

		t.Run("returns an empty medium", func(t *testing.T) {
			t.Parallel()

			m := medium.NewMemory()

			entries, err := m.ReadDir(".")
			assert.NoError(t, err, "the medium reads its root")
			assert.Empty(t, entries, "the root has no entry")
			assert.Equal(t, []any{m.Points(), m.Written()}, []any{0, int64(0)}, "the medium has no point and no byte")
		})
	})

	t.Run("Crash", func(t *testing.T) {
		t.Parallel()

		t.Run("drops a write that a sync did not cover", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "kept")
			f := open(t, m, "a")
			_, err := f.WriteAt([]byte("lost"), 4)
			assert.NoError(t, err, "the medium writes after the synced content")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "kept", "the crash keeps the synced content alone")
		})

		t.Run("drops an entry that a SyncDir did not cover", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			f := open(t, m, "a")
			assert.NoError(t, f.Sync(), "the medium syncs the content of a")

			m.Crash()

			assert.False(t, exists(t, m, "a"), "the crash drops the entry of a")
		})

		t.Run("keeps a removed entry until a SyncDir covers the removal", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			assert.NoError(t, m.Remove("a"), "the medium removes a")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "body", "the crash restores the durable entry of a")
			assert.NoError(t, m.Remove("a"), "the medium removes a again")
			assert.NoError(t, m.SyncDir("."), "the medium syncs the removal")
			m.Crash()
			assert.False(t, exists(t, m, "a"), "a synced removal survives the crash")
		})

		t.Run("keeps both names of a rename of which one directory is synced", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			assert.NoError(t, m.MkdirAll("d", 0o755), "the medium makes d")
			assert.NoError(t, m.SyncDir("."), "the medium syncs the entry of d")
			assert.NoError(t, m.Rename("a", "d/a"), "the medium moves a into d")
			assert.NoError(t, m.SyncDir("d"), "the medium syncs d")

			m.Crash()

			assert.Equal(t, []bool{exists(t, m, "a"), exists(t, m, "d/a")}, []bool{true, true},
				"the old entry stays until a SyncDir of the root covers the move")
			assert.Equal(t, read(t, m, "d/a"), "body", "the new entry has the content of a")
		})

		t.Run("drops the entries of a directory whose own entry is not durable", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			assert.NoError(t, m.MkdirAll("d", 0o755), "the medium makes d")
			create(t, m, "d/a", "body")
			assert.NoError(t, m.SyncDir("d"), "the medium syncs d")

			m.Crash()

			assert.False(t, exists(t, m, "d"), "the crash drops d")
			assert.False(t, exists(t, m, "d/a"), "the crash drops the entries of d")
		})

		t.Run("drops an entry whose durable directory is a file now", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			assert.NoError(t, m.MkdirAll("d", 0o755), "the medium makes d")
			create(t, m, "d/a", "body")
			assert.NoError(t, m.SyncDir("d"), "the medium syncs the entry d/a")
			assert.NoError(t, m.Remove("d/a"), "the medium removes d/a")
			assert.NoError(t, m.Remove("d"), "the medium removes d")
			create(t, m, "d", "file")
			assert.NoError(t, m.SyncDir("."), "the medium syncs the file d")

			m.Crash()

			assert.Equal(t, read(t, m, "d"), "", "d is the file of the synced entry")
			assert.False(t, exists(t, m, "d/a"), "a file has no entries after the crash")
		})

		t.Run("closes the open files", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			f := open(t, m, "a")

			m.Crash()

			_, err := f.Read(make([]byte, 1))
			assert.ErrorIs(t, err, medium.ErrCrashed, "a file of the crashed process refuses a read")
			assert.Equal(t, read(t, m, "a"), "body", "the medium serves a new process")
		})
	})

	t.Run("CrashAt", func(t *testing.T) {
		t.Parallel()

		t.Run("crashes in place of the persistence point after the armed one", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			m.CrashAt(1)
			f := open(t, m, "a")
			_, err := io.WriteString(f, "first")
			assert.NoError(t, err, "the medium writes the first body")
			assert.NoError(t, m.SyncDir("."), "the first persistence point passes")

			assert.ErrorIs(t, f.Sync(), medium.ErrCrashed, "the second persistence point crashes")

			_, err = m.Stat("a")
			assert.ErrorIs(t, err, medium.ErrCrashed, "the crashed medium refuses each call")
			_, err = f.Write([]byte("more"))
			assert.ErrorIs(t, err, medium.ErrCrashed, "a file of the crashed medium refuses each call")
			assert.Equal(t, m.Points(), 1, "the crashed point does not count")
		})

		t.Run("leaves the state of the crash to the next process", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "kept")
			m.CrashAt(m.Points())
			f := open(t, m, "a")
			_, err := f.WriteAt([]byte("lost"), 0)
			assert.NoError(t, err, "the medium writes over the synced content")
			assert.ErrorIs(t, f.Sync(), medium.ErrCrashed, "the sync crashes the medium")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "kept", "the crash keeps the content of the last point")
		})

		t.Run("ends at a crash that did not happen", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			m.CrashAt(m.Points() + 1)

			m.Crash()

			f := open(t, m, "a")
			assert.NoError(t, f.Sync(), "the medium persists after the crash")
			assert.NoError(t, f.Sync(), "the armed point passes after the crash")
		})
	})

	t.Run("Tear", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the first bytes of the torn write at a crash", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			f := open(t, m, "a")
			_, err := io.WriteString(f, "ab")
			assert.NoError(t, err, "the medium writes a pending body")
			m.Tear(1)
			_, err = io.WriteString(f, "cd")
			assert.NoError(t, err, "the medium writes the torn body")
			_, err = io.WriteString(f, "ef")
			assert.NoError(t, err, "the medium writes after the torn body")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "abc", "the crash keeps the writes before the tear and one byte of it")
		})

		t.Run("ends at a sync of the file", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			f := open(t, m, "a")
			m.Tear(1)
			_, err := io.WriteString(f, "ab")
			assert.NoError(t, err, "the medium writes the torn body")
			assert.NoError(t, f.Sync(), "the medium syncs the torn body")
			_, err = io.WriteString(f, "cd")
			assert.NoError(t, err, "the medium writes after the sync")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "ab", "a synced write is whole, and the write after it drops whole")
		})

		t.Run("keeps no byte of a write torn at zero", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			f := open(t, m, "a")
			_, err := io.WriteString(f, "ab")
			assert.NoError(t, err, "the medium writes a pending body")
			m.Tear(0)
			_, err = io.WriteString(f, "cd")
			assert.NoError(t, err, "the medium writes the torn body")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "ab", "the crash keeps the writes before the torn one")
		})

		t.Run("tears the first write that writes a byte", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			f := open(t, m, "a")
			m.Tear(1)
			_, err := f.Write(nil)
			assert.NoError(t, err, "the medium writes nothing")
			_, err = io.WriteString(f, "ab")
			assert.NoError(t, err, "the medium writes the torn body")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "a", "the crash keeps one byte of the first write with a byte")
		})

		t.Run("ends at a crash", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			m.Tear(1)
			m.Crash()
			f := open(t, m, "a")
			_, err := io.WriteString(f, "ab")
			assert.NoError(t, err, "the medium writes after the crash")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "", "a write after a crash drops whole")
		})

		t.Run("keeps a torn write that is shorter than the tear", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			f := open(t, m, "a")
			m.Tear(5)
			_, err := io.WriteString(f, "ab")
			assert.NoError(t, err, "the medium writes the torn body")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "ab", "the crash keeps each byte of a short torn write")
		})
	})

	t.Run("Corrupt", func(t *testing.T) {
		t.Parallel()

		t.Run("flips the lowest bit of a durable byte", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "ab")

			m.Corrupt("a", 1)

			assert.Equal(t, read(t, m, "a"), "ac", "the read returns the flipped byte")
		})

		t.Run("leaves a byte that a pending write covers", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "ab")
			f := open(t, m, "a")
			_, err := f.WriteAt([]byte("x"), 1)
			assert.NoError(t, err, "the medium writes over the durable byte")

			m.Corrupt("a", 1)
			m.Corrupt("a", 0)

			assert.Equal(t, read(t, m, "a"), "`x", "the pending byte covers the flipped one")
		})

		{
			tests := []struct {
				name       string
				giveName   string
				giveOffset int64
			}{
				{name: "corrupts nothing beyond the durable data", giveName: "a", giveOffset: 2},
				{name: "corrupts nothing before the durable data", giveName: "a", giveOffset: -1},
				{name: "corrupts nothing of a name of no entry", giveName: "b", giveOffset: 0},
				{name: "corrupts nothing of a directory", giveName: ".", giveOffset: 0},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					m := durable(t, "ab")

					m.Corrupt(tt.giveName, tt.giveOffset)

					assert.Equal(t, read(t, m, "a"), "ab", "the file keeps its content")
				})
			}
		}
	})

	t.Run("Fill", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the bytes that fit and fails with syscall.ENOSPC", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			f := open(t, m, "a")
			_, err := io.WriteString(f, "ab")
			assert.NoError(t, err, "the medium writes before the limit")
			m.Fill(2)

			n, err := io.WriteString(f, "cde")

			assert.ErrorIs(t, err, syscall.ENOSPC, "the medium is full")
			assert.Equal(t, n, 2, "the write writes the bytes that fit")
			_, err = io.WriteString(f, "f")
			assert.ErrorIs(t, err, syscall.ENOSPC, "a full medium refuses each write")
			assert.Equal(t, read(t, m, "a"), "abcd", "the file has the bytes that fit")
		})

		t.Run("lifts the limit for a negative count", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			f := open(t, m, "a")
			_, err := io.WriteString(f, "ab")
			assert.NoError(t, err, "the medium writes before the limit")
			m.Fill(0)
			m.Fill(-1)

			_, err = io.WriteString(f, "cd")

			assert.NoError(t, err, "the medium has no limit")
		})

		{
			tests := []struct {
				name      string
				giveLimit int64
				give      string
				wantN     int
				wantErr   error
			}{
				{
					name: "refuses each byte at a limit of zero", giveLimit: 0, give: "ab", wantN: 0,
					wantErr: syscall.ENOSPC,
				},
				{name: "writes a write that fits under the limit", giveLimit: 3, give: "ab", wantN: 2},
				{name: "writes a write that fills the limit", giveLimit: 2, give: "ab", wantN: 2},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					m := medium.NewMemory()
					f := open(t, m, "a")
					m.Fill(tt.giveLimit)

					n, err := io.WriteString(f, tt.give)

					assert.Equal(t, n, tt.wantN, "the write writes the bytes that fit")
					if tt.wantErr == nil {
						assert.NoError(t, err, "the write fits")
						return
					}
					assert.ErrorIs(t, err, tt.wantErr, "the medium is full")
				})
			}
		}
	})

	t.Run("Points", func(t *testing.T) {
		t.Parallel()

		t.Run("counts each Sync and each SyncDir that returned nil", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			f := open(t, m, "a")
			assert.NoError(t, f.Sync(), "the medium syncs a")
			assert.NoError(t, m.SyncDir("."), "the medium syncs the root")
			assert.HasError(t, m.SyncDir("a"), "the medium refuses to sync a file as a directory")

			assert.Equal(t, m.Points(), 2, "the medium has two points")
		})
	})

	t.Run("Written", func(t *testing.T) {
		t.Parallel()

		t.Run("counts the bytes of the writes", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			f := open(t, m, "a")
			_, err := io.WriteString(f, "abc")
			assert.NoError(t, err, "the medium writes 3 bytes")
			_, err = f.WriteAt([]byte("d"), 9)
			assert.NoError(t, err, "the medium writes a byte at an offset")
			assert.NoError(t, f.Truncate(20), "the medium extends the file")

			assert.Equal(t, m.Written(), int64(4), "a truncate writes no byte")
			assert.Equal(t, m.Points(), 0, "the medium serves a call after Written")
		})
	})

	t.Run("OpenFile", func(t *testing.T) {
		t.Parallel()

		t.Run("refuses a directory", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()

			_, err := m.OpenFile(".", os.O_RDONLY, 0)

			assert.ErrorIs(t, err, syscall.EISDIR, "a directory is no file")
		})

		t.Run("refuses a file in a file", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")

			_, err := m.OpenFile("a/b", os.O_CREATE|os.O_WRONLY, 0o644)

			assert.ErrorIs(t, err, syscall.ENOTDIR, "a file has no entries")
		})

		t.Run("keeps the permission bits of a new file", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			f, err := m.OpenFile("a", os.O_CREATE|os.O_WRONLY, 0o640|fs.ModeSetuid)
			assert.NoError(t, err, "the medium creates a")

			info, err := f.Stat()

			assert.NoError(t, err, "the file states its information")
			assert.Equal(t, info.Mode(), fs.FileMode(0o640), "the file has the permission bits alone")
			assert.Equal(t, []any{info.ModTime(), info.Sys(), info.IsDir()}, []any{time.Time{}, nil, false},
				"a file of the medium has no time and no system data")
		})
	})

	t.Run("Remove", func(t *testing.T) {
		t.Parallel()

		{
			tests := []struct {
				name string
				give string
				want error
			}{
				{name: "refuses the root", give: ".", want: fs.ErrInvalid},
				{name: "refuses a directory with an entry", give: "d", want: syscall.ENOTEMPTY},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					m := medium.NewMemory()
					assert.NoError(t, m.MkdirAll("d/e", 0o755), "the medium makes d and d/e")

					assert.ErrorIs(t, m.Remove(tt.give), tt.want, "the medium keeps "+tt.give)
				})
			}
		}
	})

	t.Run("Rename", func(t *testing.T) {
		t.Parallel()

		{
			tests := []struct {
				name    string
				giveOld string
				giveNew string
				want    error
			}{
				{name: "refuses the root as the old name", giveOld: ".", giveNew: "x", want: fs.ErrInvalid},
				{name: "refuses the root as the new name", giveOld: "a", giveNew: ".", want: fs.ErrInvalid},
				{name: "refuses a move of a directory into itself", giveOld: "d", giveNew: "d/x", want: syscall.EINVAL},
				{name: "refuses a directory over a file", giveOld: "d", giveNew: "a", want: syscall.ENOTDIR},
				{name: "refuses a file over a directory", giveOld: "a", giveNew: "e", want: syscall.EISDIR},
				{
					name: "refuses a directory over a directory with an entry", giveOld: "e", giveNew: "d",
					want: syscall.ENOTEMPTY,
				},
				{name: "refuses a new name in no directory", giveOld: "a", giveNew: "x/a", want: fs.ErrNotExist},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					m := durable(t, "body")
					assert.NoError(t, m.MkdirAll("d/f", 0o755), "the medium makes d and d/f")
					assert.NoError(t, m.MkdirAll("e", 0o755), "the medium makes e")

					err := m.Rename(tt.giveOld, tt.giveNew)

					assert.ErrorIs(t, err, tt.want, "the medium refuses the rename")
					le := assert.ErrorAs[*os.LinkError](t, err, "the error is a link error")
					assert.Equal(t, []string{le.Old, le.New}, []string{tt.giveOld, tt.giveNew},
						"the error has both names")
				})
			}
		}

		t.Run("replaces an empty directory with a directory", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			assert.NoError(t, m.MkdirAll("d", 0o755), "the medium makes d")
			assert.NoError(t, m.MkdirAll("e", 0o755), "the medium makes e")
			create(t, m, "d/a", "body")

			assert.NoError(t, m.Rename("d", "e"), "the medium moves d over the empty e")

			assert.Equal(t, read(t, m, "e/a"), "body", "e has the file of d")
		})

		t.Run("keeps a file that it renames to itself", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")

			assert.NoError(t, m.Rename("a", "a"), "the medium renames a to a")

			assert.Equal(t, read(t, m, "a"), "body", "a keeps its content")
		})

		t.Run("moves a directory apart from its neighbours", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			assert.NoError(t, m.MkdirAll("d", 0o755), "the medium makes d")
			create(t, m, "d2", "neighbour")

			assert.NoError(t, m.Rename("d", "e"), "the medium moves d to e")

			assert.Equal(t, read(t, m, "d2"), "neighbour", "the entry d2 stays where it is")
		})
	})

	t.Run("MkdirAll", func(t *testing.T) {
		t.Parallel()

		t.Run("refuses a path through a file", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")

			assert.ErrorIs(t, m.MkdirAll("a/d", 0o755), syscall.ENOTDIR, "a file has no directory")
		})

		t.Run("keeps the root", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()

			assert.NoError(t, m.MkdirAll(".", 0o755), "the root exists")
		})

		t.Run("keeps the permission bits of a new directory", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			assert.NoError(t, m.MkdirAll("d", 0o750), "the medium makes d")

			info, err := m.Stat("d")

			assert.NoError(t, err, "the medium has d")
			assert.Equal(t, info.Mode(), fs.ModeDir|0o750, "d has the bits of a directory and its permission")
		})
	})

	t.Run("ReadDir", func(t *testing.T) {
		t.Parallel()

		t.Run("refuses a file", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")

			_, err := m.ReadDir("a")

			assert.ErrorIs(t, err, syscall.ENOTDIR, "a file has no entries")
		})
	})

	t.Run("SyncDir", func(t *testing.T) {
		t.Parallel()

		t.Run("refuses a call of a crashed medium", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			m.CrashAt(0)
			assert.ErrorIs(t, m.SyncDir("."), medium.ErrCrashed, "the first point crashes the medium")

			assert.ErrorIs(t, m.SyncDir("."), medium.ErrCrashed, "the crashed medium refuses a sync")
			_, err := m.OpenFile("a", os.O_CREATE|os.O_WRONLY, 0o644)
			assert.ErrorIs(t, err, medium.ErrCrashed, "the crashed medium refuses an open")
		})
	})

	t.Run("refuses the calls of a file that the mode of its open does not admit", func(t *testing.T) {
		t.Parallel()
		m := durable(t, "body")
		reader, err := m.OpenFile("a", os.O_RDONLY, 0)
		assert.NoError(t, err, "the medium opens a for reading")
		writer, err := m.OpenFile("a", os.O_WRONLY|os.O_APPEND, 0)
		assert.NoError(t, err, "the medium opens a to append")

		_, err = writer.Read(make([]byte, 1))
		assert.ErrorIs(t, err, syscall.EBADF, "a file for writing alone refuses a read")
		_, err = reader.Write([]byte("b"))
		assert.ErrorIs(t, err, syscall.EBADF, "a file for reading alone refuses a write")
		assert.ErrorIs(t, reader.Truncate(0), syscall.EBADF, "a file for reading alone refuses a truncate")
		_, err = reader.WriteAt([]byte("b"), 0)
		assert.ErrorIs(t, err, syscall.EBADF, "a file for reading alone refuses a write at an offset")
		_, err = writer.WriteAt([]byte("b"), 0)
		assert.ErrorIs(t, err, syscall.EINVAL, "a file that appends refuses a write at an offset")
	})

	t.Run("refuses an offset before the start of a file", func(t *testing.T) {
		t.Parallel()
		m := durable(t, "body")
		f := open(t, m, "a")

		_, err := f.ReadAt(make([]byte, 1), -1)
		assert.ErrorIs(t, err, syscall.EINVAL, "a read refuses a negative offset")
		_, err = f.WriteAt([]byte("b"), -1)
		assert.ErrorIs(t, err, syscall.EINVAL, "a write refuses a negative offset")
		assert.ErrorIs(t, f.Truncate(-1), syscall.EINVAL, "a truncate refuses a negative size")
		_, err = f.Seek(-1, io.SeekStart)
		assert.ErrorIs(t, err, syscall.EINVAL, "a seek refuses a negative offset")
		_, err = f.Seek(0, 7)
		assert.ErrorIs(t, err, syscall.EINVAL, "a seek refuses an unknown whence")
	})

	t.Run("seeks from the offset and from the end of a file", func(t *testing.T) {
		t.Parallel()
		m := durable(t, "body")
		f := open(t, m, "a")

		_, err := f.Seek(1, io.SeekStart)
		assert.NoError(t, err, "the file seeks to 1")
		at, err := f.Seek(1, io.SeekCurrent)
		assert.NoError(t, err, "the file seeks from its offset")
		assert.Equal(t, at, int64(2), "the offset moves from 1 to 2")
		at, err = f.Seek(-1, io.SeekEnd)
		assert.NoError(t, err, "the file seeks from its end")
		assert.Equal(t, at, int64(3), "the offset is 1 before the end")
	})

	t.Run("refuses the calls of a file of a crashed medium", func(t *testing.T) {
		t.Parallel()
		m := durable(t, "body")
		f := open(t, m, "a")
		m.Crash()

		_, err := f.ReadAt(make([]byte, 1), 0)
		assert.ErrorIs(t, err, medium.ErrCrashed, "a stale file refuses ReadAt")
		_, err = f.Seek(0, io.SeekStart)
		assert.ErrorIs(t, err, medium.ErrCrashed, "a stale file refuses Seek")
		_, err = f.Stat()
		assert.ErrorIs(t, err, medium.ErrCrashed, "a stale file refuses Stat")
		assert.ErrorIs(t, f.Sync(), medium.ErrCrashed, "a stale file refuses Sync")
		assert.ErrorIs(t, f.Close(), medium.ErrCrashed, "a stale file refuses Close")
	})

	t.Run("serves the calls of several goroutines at once", func(t *testing.T) {
		t.Parallel()
		m := medium.NewMemory()
		names := []string{"a", "b", "c", "d"}

		var wg sync.WaitGroup
		for _, name := range names {
			wg.Go(func() {
				f, err := m.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)
				if err == nil {
					_, err = io.WriteString(f, name)
				}
				if err == nil {
					err = f.Sync()
				}
				expect.NoError(t, err, "a goroutine writes and syncs "+name)
			})
		}
		wg.Wait()

		assert.Equal(t, m.Points(), len(names), "each goroutine syncs once")
		assert.Equal(t, m.Written(), int64(len(names)), "each goroutine writes one byte")
	})
}
