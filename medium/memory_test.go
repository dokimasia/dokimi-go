// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package medium_test

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/medium"
)

// errDisk is the error that the specs arm for a call of a medium.
var errDisk = errors.New("medium_test: the disk failed")

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

// keeping returns a keep function of CrashKeeping that returns the count of
// counts for each name, 0 for a name without one, and the calls that it
// receives, each as name:pending.
func keeping(counts map[string]int) (func(name string, pending int) int, *[]string) {
	calls := &[]string{}
	return func(name string, pending int) int {
		*calls = append(*calls, fmt.Sprintf("%s:%d", name, pending))
		return counts[name]
	}, calls
}

// tornAfterAB returns a Memory whose durable file a is empty, with three
// pending writes of ab, cd and ef, of which Tear armed the second at one byte.
func tornAfterAB(t *testing.T) *medium.Memory {
	t.Helper()

	m := durable(t, "")
	f := open(t, m, "a")
	for i, part := range []string{"ab", "cd", "ef"} {
		if i == 1 {
			m.Tear(1)
		}
		_, err := io.WriteString(f, part)
		assert.NoError(t, err, "the medium writes a pending body")
	}
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

	t.Run("CrashKeeping", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveKeep  map[string]int
			wantRead  string
			wantCalls []string
		}{
			{
				name:      "keeps the first pending writes of a file that keep chooses",
				giveKeep:  map[string]int{"a": 2},
				wantRead:  "abcd",
				wantCalls: []string{"a:3"},
			},
			{
				name:      "drops every pending call of a file for a keep of 0",
				giveKeep:  map[string]int{},
				wantRead:  "",
				wantCalls: []string{"a:3"},
			},
			{
				name:      "keeps every pending call of a file for a keep above their count",
				giveKeep:  map[string]int{"a": 9},
				wantRead:  "abcdef",
				wantCalls: []string{"a:3"},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := durable(t, "")
				f := open(t, m, "a")
				for _, part := range []string{"ab", "cd", "ef"} {
					_, err := io.WriteString(f, part)
					assert.NoError(t, err, "the medium writes a pending body")
				}
				keep, calls := keeping(tt.giveKeep)

				m.CrashKeeping(keep)

				assert.Equal(t, read(t, m, "a"), tt.wantRead, "the crash keeps the chosen writes in order")
				assert.Equal(t, *calls, tt.wantCalls, "keep receives the name and the count of the pending calls")
			})
		}

		t.Run("keeps a pending truncate in the order of the calls", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "abcd")
			f := open(t, m, "a")
			assert.NoError(t, f.Truncate(1), "the medium truncates a")
			_, err := f.WriteAt([]byte("z"), 1)
			assert.NoError(t, err, "the medium writes after the truncate")
			keep, _ := keeping(map[string]int{"a": 1})

			m.CrashKeeping(keep)

			assert.Equal(t, read(t, m, "a"), "a", "the crash keeps the truncate and drops the write after it")
		})

		t.Run("tears the last kept write where Tear armed it", func(t *testing.T) {
			t.Parallel()
			m := tornAfterAB(t)
			keep, _ := keeping(map[string]int{"a": 2})

			m.CrashKeeping(keep)

			assert.Equal(t, read(t, m, "a"), "abc", "the second write is the last kept, and keeps one byte")
		})

		t.Run("keeps a torn write whole when a later write is kept", func(t *testing.T) {
			t.Parallel()
			m := tornAfterAB(t)
			keep, _ := keeping(map[string]int{"a": 3})

			m.CrashKeeping(keep)

			assert.Equal(t, read(t, m, "a"), "abcdef", "the device wrote past the armed write")
		})

		t.Run("keeps the pending entries of a directory for which keep returns more than 0", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			f := open(t, m, "a")
			assert.NoError(t, f.Sync(), "the medium syncs the content of a")
			keep, calls := keeping(map[string]int{".": 1})

			m.CrashKeeping(keep)

			assert.True(t, exists(t, m, "a"), "the crash keeps the entry of a")
			assert.Equal(t, *calls, []string{".:1"}, "keep receives the directory with a count of 1")
		})

		t.Run("drops the pending entries of a directory for which keep returns 0", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			assert.NoError(t, m.Remove("a"), "the medium removes a")
			keep, _ := keeping(map[string]int{})

			m.CrashKeeping(keep)

			assert.Equal(t, read(t, m, "a"), "body", "the crash restores the durable entry of a")
		})

		t.Run("asks for the directories, then for the files, each in the order of their names", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			create(t, m, "c", "new")
			for _, name := range []string{"c", "a"} {
				f := open(t, m, name)
				_, err := f.WriteAt([]byte("x"), 0)
				assert.NoError(t, err, "the medium writes "+name)
			}
			keep, calls := keeping(map[string]int{".": 1})

			m.CrashKeeping(keep)

			assert.Equal(t, *calls, []string{".:1", "a:1", "c:3"}, "the root, then a and c")
		})

		t.Run("asks for no entry of a directory that the crash drops", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			assert.NoError(t, m.MkdirAll("d", 0o755), "the medium makes d")
			create(t, m, "d/a", "body")
			keep, calls := keeping(map[string]int{})

			m.CrashKeeping(keep)

			assert.Equal(t, *calls, []string{".:1"}, "the root drops d, so keep receives neither d nor d/a")
		})

		t.Run("keeps the pending removal of an entry of a directory that the process removed", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			assert.NoError(t, m.MkdirAll("d", 0o755), "the medium makes d")
			assert.NoError(t, open(t, m, "d/a").Sync(), "the medium syncs the content of d/a")
			for _, dir := range []string{"d", "."} {
				assert.NoError(t, m.SyncDir(dir), "the medium syncs "+dir)
			}
			assert.NoError(t, m.Remove("d/a"), "the medium removes d/a")
			assert.NoError(t, m.Remove("d"), "the medium removes d")
			keep, calls := keeping(map[string]int{"d": 1})

			m.CrashKeeping(keep)

			assert.Equal(t, []bool{exists(t, m, "d"), exists(t, m, "d/a")}, []bool{true, false},
				"the crash drops the removal of d and keeps the removal of d/a")
			assert.Equal(t, *calls, []string{".:1", "d:1"}, "keep receives the removed directory")
		})

		t.Run("asks for no directory whose entries are not pending", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			assert.NoError(t, m.MkdirAll("d", 0o755), "the medium makes d")
			assert.NoError(t, m.SyncDir("."), "the medium syncs the entry of d")
			create(t, m, "d/b", "x")
			keep, calls := keeping(map[string]int{"d": 1})

			m.CrashKeeping(keep)

			assert.Equal(t, *calls, []string{"d:1", "d/b:2"}, "keep receives d and d/b, and not the root")
		})

		t.Run("asks for no directory whose parent the crash removes", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			assert.NoError(t, m.MkdirAll("p/d", 0o755), "the medium makes p/d")
			create(t, m, "p/d/x", "")
			for _, dir := range []string{"p/d", "p", "."} {
				assert.NoError(t, m.SyncDir(dir), "the medium syncs "+dir)
			}
			for _, name := range []string{"p/d/x", "p/d", "p"} {
				assert.NoError(t, m.Remove(name), "the medium removes "+name)
			}
			keep, calls := keeping(map[string]int{".": 1, "p/d": 1})

			m.CrashKeeping(keep)

			assert.Equal(t, *calls, []string{".:1"}, "the root drops p, so keep does not receive p/d")
		})

		t.Run("asks for no file that replaced a durable directory", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			assert.NoError(t, m.MkdirAll("d", 0o755), "the medium makes d")
			create(t, m, "d/a", "body")
			assert.NoError(t, m.SyncDir("d"), "the medium syncs the entry d/a")
			assert.NoError(t, m.Remove("d/a"), "the medium removes d/a")
			assert.NoError(t, m.Remove("d"), "the medium removes d")
			create(t, m, "d", "file")
			assert.NoError(t, m.SyncDir("."), "the medium syncs the file d")
			keep, calls := keeping(map[string]int{})

			m.CrashKeeping(keep)

			assert.Equal(t, *calls, []string{"d:2"}, "keep receives the pending calls of the file d alone")
		})

		t.Run("asks for nothing after a crash in place of a persistence point", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "kept")
			m.CrashAt(m.Points())
			f := open(t, m, "a")
			_, err := f.WriteAt([]byte("lost"), 0)
			assert.NoError(t, err, "the medium writes over the synced content")
			assert.ErrorIs(t, f.Sync(), medium.ErrCrashed, "the sync crashes the medium")
			keep, calls := keeping(map[string]int{"a": 1})

			m.CrashKeeping(keep)

			assert.Equal(t, read(t, m, "a"), "kept", "the crash keeps the content of the last point")
			assert.Empty(t, *calls, "the medium dropped its pending calls already")
		})

		t.Run("closes the open files", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			f := open(t, m, "a")
			keep, _ := keeping(map[string]int{})

			m.CrashKeeping(keep)

			_, err := f.Read(make([]byte, 1))
			assert.ErrorIs(t, err, medium.ErrCrashed, "a file of the crashed process refuses a read")
		})
	})

	t.Run("Kill", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps a write that a sync did not cover", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "kept")
			f := open(t, m, "a")
			_, err := f.WriteAt([]byte("more"), 4)
			assert.NoError(t, err, "the medium writes after the synced content")

			m.Kill()

			assert.Equal(t, read(t, m, "a"), "keptmore", "the page cache survives the kill")
		})

		t.Run("leaves a pending write pending for a later crash", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "kept")
			f := open(t, m, "a")
			_, err := f.WriteAt([]byte("lost"), 4)
			assert.NoError(t, err, "the medium writes after the synced content")
			m.Kill()

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "kept", "the crash drops the write that no sync covered")
		})

		t.Run("keeps an entry that a SyncDir did not cover", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			create(t, m, "a", "body")

			m.Kill()

			assert.Equal(t, read(t, m, "a"), "body", "the kill keeps the pending entry of a")
		})

		t.Run("closes the open files", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			f := open(t, m, "a")

			m.Kill()

			_, err := f.Read(make([]byte, 1))
			assert.ErrorIs(t, err, medium.ErrCrashed, "a file of the killed process refuses a read")
		})

		t.Run("fails a blocked call with ErrCrashed", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				m := durable(t, "body")
				m.Hold()
				done := make(chan error, 1)
				go func() {
					_, err := m.Stat("a")
					done <- err
				}()
				synctest.Wait()

				m.Kill()

				assert.ErrorIs(t, <-done, medium.ErrCrashed, "the kill ends the blocked call")
				assert.Equal(t, read(t, m, "a"), "body", "the kill ends the hold")
			})
		})

		t.Run("ends the failure that FailBefore armed", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			m.FailBefore(errDisk)

			m.Kill()

			assert.Equal(t, read(t, m, "a"), "body", "the next process reads a")
		})

		t.Run("ends a tear that no write took", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			m.Tear(1)
			m.Kill()
			f := open(t, m, "a")
			_, err := io.WriteString(f, "ab")
			assert.NoError(t, err, "the medium writes after the kill")

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "", "the write after the kill drops whole")
		})

		t.Run("ends a crash that CrashAt armed", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			m.CrashAt(m.Points())

			m.Kill()

			assert.NoError(t, open(t, m, "a").Sync(), "the armed point passes after the kill")
		})

		t.Run("serves the calls of a new process after a crash in place of a persistence point", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "kept")
			m.CrashAt(m.Points())
			assert.ErrorIs(t, open(t, m, "a").Sync(), medium.ErrCrashed, "the sync crashes the medium")

			m.Kill()

			assert.Equal(t, read(t, m, "a"), "kept", "the next process reads the state of the crash")
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

	t.Run("FailBefore", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the next call without its effect", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			m.FailBefore(errDisk)

			_, err := m.OpenFile("a", os.O_CREATE|os.O_WRONLY, 0o644)

			assert.ErrorIs(t, err, errDisk, "the open fails with the armed error")
			assert.False(t, exists(t, m, "a"), "the failed open creates nothing")
			_, err = m.OpenFile("a", os.O_CREATE|os.O_WRONLY, 0o644)
			assert.NoError(t, err, "the call after the failed one creates a")
		})

		t.Run("fails the next call of a file without its effect", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			f := open(t, m, "a")
			m.FailBefore(errDisk)

			_, err := f.WriteAt([]byte("x"), 0)

			assert.ErrorIs(t, err, errDisk, "the write fails with the armed error")
			assert.Equal(t, read(t, m, "a"), "body", "the failed write writes nothing")
		})

		{
			tests := []struct {
				name string
				give func(m *medium.Memory, f medium.File) error
			}{
				{
					name: "fails the next Remove",
					give: func(m *medium.Memory, _ medium.File) error { return m.Remove("a") },
				},
				{
					name: "fails the next Rename",
					give: func(m *medium.Memory, _ medium.File) error { return m.Rename("a", "b") },
				},
				{
					name: "fails the next MkdirAll",
					give: func(m *medium.Memory, _ medium.File) error { return m.MkdirAll("d", 0o755) },
				},
				{name: "fails the next ReadDir", give: func(m *medium.Memory, _ medium.File) error {
					_, err := m.ReadDir(".")
					return err
				}},
				{name: "fails the next Stat", give: func(m *medium.Memory, _ medium.File) error {
					_, err := m.Stat("a")
					return err
				}},
				{
					name: "fails the next SyncDir",
					give: func(m *medium.Memory, _ medium.File) error { return m.SyncDir(".") },
				},
				{name: "fails the next Read of a file", give: func(_ *medium.Memory, f medium.File) error {
					_, err := f.Read(make([]byte, 1))
					return err
				}},
				{name: "fails the next ReadAt of a file", give: func(_ *medium.Memory, f medium.File) error {
					_, err := f.ReadAt(make([]byte, 1), 0)
					return err
				}},
				{name: "fails the next Write of a file", give: func(_ *medium.Memory, f medium.File) error {
					_, err := f.Write([]byte("x"))
					return err
				}},
				{name: "fails the next Seek of a file", give: func(_ *medium.Memory, f medium.File) error {
					_, err := f.Seek(1, io.SeekStart)
					return err
				}},
				{
					name: "fails the next Sync of a file",
					give: func(_ *medium.Memory, f medium.File) error { return f.Sync() },
				},
				{
					name: "fails the next Truncate of a file",
					give: func(_ *medium.Memory, f medium.File) error { return f.Truncate(1) },
				},
				{name: "fails the next Stat of a file", give: func(_ *medium.Memory, f medium.File) error {
					_, err := f.Stat()
					return err
				}},
				{
					name: "fails the next Close of a file",
					give: func(_ *medium.Memory, f medium.File) error { return f.Close() },
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					m := durable(t, "body")
					f := open(t, m, "a")
					m.FailBefore(errDisk)

					assert.ErrorIs(t, tt.give(m, f), errDisk, "the call fails with the armed error")
					assert.NoError(t, tt.give(m, f), "the same call after it succeeds")
				})
			}
		}

		t.Run("ends at a crash", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			m.FailBefore(errDisk)

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "body", "the first call after the crash succeeds")
		})
	})

	t.Run("FailAfter", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the next call after its whole effect", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			f := open(t, m, "a")
			m.FailAfter(errDisk)

			n, err := f.WriteAt([]byte("west"), 0)

			assert.ErrorIs(t, err, errDisk, "the write fails with the armed error")
			assert.Equal(t, n, 4, "the write writes each byte")
			assert.Equal(t, read(t, m, "a"), "west", "the failed write takes its effect")
		})

		t.Run("returns the error of a call that fails on its own", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			m.FailAfter(errDisk)

			_, err := m.Stat("a")

			assert.ErrorIs(t, err, fs.ErrNotExist, "the stat of no file fails with its own error")
			assert.NoError(t, m.MkdirAll("d", 0o755), "the call after it succeeds")
		})

		t.Run("ends at a crash", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			m.FailAfter(errDisk)

			m.Crash()

			assert.Equal(t, read(t, m, "a"), "body", "the first call after the crash succeeds")
		})
	})

	t.Run("FailPart", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the first bytes of the next write, and fails it", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			f := open(t, m, "a")
			m.FailPart(2, errDisk)
			_, err := f.Stat()
			assert.NoError(t, err, "a call that writes nothing succeeds")

			n, err := io.WriteString(f, "abc")

			assert.ErrorIs(t, err, errDisk, "the write fails with the armed error")
			assert.Equal(t, n, 2, "the write writes its first two bytes")
			n, err = io.WriteString(f, "cd")
			assert.NoError(t, err, "the write after the failed one succeeds")
			assert.Equal(t, n, 2, "the write after the failed one writes each byte")
			assert.Equal(t, read(t, m, "a"), "abcd", "the file has the bytes of both writes")
		})

		t.Run("fails the next WriteAt after its first bytes", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")
			f := open(t, m, "a")
			m.FailPart(1, errDisk)

			n, err := f.WriteAt([]byte("xyz"), 0)

			assert.ErrorIs(t, err, errDisk, "the write fails with the armed error")
			assert.Equal(t, n, 1, "the write writes its first byte")
			assert.Equal(t, read(t, m, "a"), "xody", "the file has the byte of the failed write")
		})

		t.Run("writes no byte for a part below zero", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			f := open(t, m, "a")
			m.FailPart(-1, errDisk)

			n, err := io.WriteString(f, "ab")

			assert.ErrorIs(t, err, errDisk, "the write fails with the armed error")
			assert.Equal(t, n, 0, "the write writes no byte")
		})

		t.Run("writes each byte of a write shorter than the part", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			f := open(t, m, "a")
			m.FailPart(5, errDisk)

			n, err := io.WriteString(f, "ab")

			assert.ErrorIs(t, err, errDisk, "the write fails with the armed error")
			assert.Equal(t, n, 2, "the write writes each of its bytes")
		})

		t.Run("ends at a crash", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "")
			m.FailPart(0, errDisk)

			m.Crash()

			f := open(t, m, "a")
			_, err := io.WriteString(f, "ab")
			assert.NoError(t, err, "the first write after the crash succeeds")
		})
	})

	t.Run("Hold", func(t *testing.T) {
		t.Parallel()

		t.Run("blocks each later call until Release", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				m := durable(t, "body")
				f := open(t, m, "a")
				m.Hold()
				done := make(chan error, 2)
				go func() {
					_, err := m.Stat("a")
					done <- err
				}()
				synctest.Wait()
				m.Hold()
				go func() {
					_, err := f.Write([]byte("x"))
					done <- err
				}()
				synctest.Wait()

				assert.Equal(t, len(done), 0, "both calls wait for the release of the one hold")
				m.Release()

				assert.NoError(t, <-done, "a call goes on after the release")
				assert.NoError(t, <-done, "the other call goes on after the release")
			})
		})

		t.Run("fails a blocked call with ErrCrashed at a crash", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				m := durable(t, "body")
				m.Hold()
				done := make(chan error, 1)
				go func() {
					_, err := m.ReadDir(".")
					done <- err
				}()
				synctest.Wait()

				m.Crash()

				assert.ErrorIs(t, <-done, medium.ErrCrashed, "the crash ends the blocked call")
				assert.Equal(t, read(t, m, "a"), "body", "the crash ends the hold")
			})
		})
	})

	t.Run("Release", func(t *testing.T) {
		t.Parallel()

		t.Run("changes nothing without a hold", func(t *testing.T) {
			t.Parallel()
			m := durable(t, "body")

			m.Release()

			assert.Equal(t, read(t, m, "a"), "body", "the medium serves its calls")
		})
	})

	t.Run("Held", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			giveHold func(m *medium.Memory)
			want     bool
		}{
			{name: "reports a hold in force", giveHold: (*medium.Memory).Hold, want: true},
			{name: "reports no hold without one", giveHold: func(*medium.Memory) {}},
			{name: "reports no hold after a release", giveHold: func(m *medium.Memory) {
				m.Hold()
				m.Release()
			}},
			{name: "reports no hold after a crash", giveHold: func(m *medium.Memory) {
				m.Hold()
				m.Crash()
			}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				m := medium.NewMemory()

				tt.giveHold(m)

				assert.Equal(t, m.Held(), tt.want, "the medium reports its hold")
			})
		}

		t.Run("reports a hold until its release", func(t *testing.T) {
			t.Parallel()
			m := medium.NewMemory()
			m.Hold()
			held := m.Held()
			m.Release()

			assert.Equal(t, []bool{held, m.Held()}, []bool{true, false}, "the release ends the hold that Held reports")
		})
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
