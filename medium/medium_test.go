// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package medium_test

import (
	"io"
	"io/fs"
	"os"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/medium"
)

// The FS of the operating system and the Memory keep one contract, so a
// subject behaves the same over both until a crash.
func TestFS(t *testing.T) {
	t.Parallel()

	t.Run("OpenFile", func(t *testing.T) {
		t.Parallel()

		t.Run("creates a file with os.O_CREATE", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "body")

				assert.Equal(t, read(t, fsys, "a"), "body", "the file has the content that its writer wrote")
			})
		})

		t.Run("creates a new file under os.O_EXCL", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				f, err := fsys.OpenFile("a", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)

				assert.NoError(t, err, "the FS creates the new file a")
				assert.NoError(t, f.Close(), "the file closes")
			})
		})

		t.Run("opens a file that exists under os.O_EXCL without os.O_CREATE", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "body")

				f, err := fsys.OpenFile("a", os.O_RDONLY|os.O_EXCL, 0)

				assert.NoError(t, err, "os.O_EXCL alone opens the file")
				assert.NoError(t, f.Close(), "the file closes")
			})
		})

		t.Run("truncates a file with os.O_TRUNC", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "a longer body")
				create(t, fsys, "a", "short")

				assert.Equal(t, read(t, fsys, "a"), "short", "the second writer truncates the file first")
			})
		})

		{
			tests := []struct {
				name     string
				giveName string
				giveFlag int
				want     error
			}{
				{
					name: "refuses a file that does not exist", giveName: "b", giveFlag: os.O_RDONLY,
					want: fs.ErrNotExist,
				},
				{
					name: "refuses a file that exists under os.O_EXCL", giveName: "a",
					giveFlag: os.O_CREATE | os.O_EXCL | os.O_WRONLY, want: fs.ErrExist,
				},
				{
					name: "refuses a file in a directory that does not exist", giveName: "d/a",
					giveFlag: os.O_CREATE | os.O_WRONLY, want: fs.ErrNotExist,
				},
				{name: "refuses a name outside the root", giveName: "../a", giveFlag: os.O_RDONLY, want: fs.ErrInvalid},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()

					each(t, func(t assert.TB, fsys medium.FS) {
						create(t, fsys, "a", "body")

						_, err := fsys.OpenFile(tt.giveName, tt.giveFlag, 0o644)

						assert.ErrorIs(t, err, tt.want, "the FS refuses to open "+tt.giveName)
					})
				})
			}
		}
	})

	t.Run("Remove", func(t *testing.T) {
		t.Parallel()

		t.Run("removes a file and an empty directory", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "body")
				assert.NoError(t, fsys.MkdirAll("d", 0o755), "the FS makes the directory d")

				assert.NoError(t, fsys.Remove("a"), "the FS removes the file a")
				assert.NoError(t, fsys.Remove("d"), "the FS removes the empty directory d")

				entries, err := fsys.ReadDir(".")
				assert.NoError(t, err, "the FS reads the root")
				assert.Empty(t, entries, "the root has no entry")
			})
		})

		t.Run("refuses a directory with an entry", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				assert.NoError(t, fsys.MkdirAll("d", 0o755), "the FS makes the directory d")
				create(t, fsys, "d/a", "body")

				assert.HasError(t, fsys.Remove("d"), "the FS keeps a directory with an entry")
			})
		})

		t.Run("refuses a name that does not exist", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				assert.ErrorIs(t, fsys.Remove("a"), fs.ErrNotExist, "the FS has no file a")
			})
		})
	})

	t.Run("Rename", func(t *testing.T) {
		t.Parallel()

		t.Run("replaces the file at the new name", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "new")
				create(t, fsys, "b", "old")

				assert.NoError(t, fsys.Rename("a", "b"), "the FS renames a to b")

				assert.Equal(t, read(t, fsys, "b"), "new", "b has the content of a")
				_, err := fsys.Stat("a")
				assert.ErrorIs(t, err, fs.ErrNotExist, "the FS has no file a after the rename")
			})
		})

		t.Run("moves a directory with its files", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				assert.NoError(t, fsys.MkdirAll("d/e", 0o755), "the FS makes the directories d and d/e")
				create(t, fsys, "d/e/a", "body")

				assert.NoError(t, fsys.Rename("d", "f"), "the FS renames d to f")

				assert.Equal(t, read(t, fsys, "f/e/a"), "body", "the file of d moves with it")
			})
		})

		t.Run("refuses a name that does not exist", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				assert.ErrorIs(t, fsys.Rename("a", "b"), fs.ErrNotExist, "the FS has no file a")
			})
		})

		t.Run("refuses a name outside the root", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "body")

				assert.ErrorIs(t, fsys.Rename("a", "../b"), fs.ErrInvalid, "the FS keeps the files under its root")
				assert.ErrorIs(t, fsys.Rename("../b", "a"), fs.ErrInvalid, "the FS reads the files under its root")
			})
		})
	})

	t.Run("MkdirAll", func(t *testing.T) {
		t.Parallel()

		t.Run("creates the directory and each parent that it lacks", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				assert.NoError(t, fsys.MkdirAll("d/e", 0o755), "the FS makes the directories d and d/e")
				assert.NoError(t, fsys.MkdirAll("d/e", 0o755), "the FS keeps a directory that exists")

				info, err := fsys.Stat("d/e")
				assert.NoError(t, err, "the FS has the directory d/e")
				assert.True(t, info.IsDir(), "d/e is a directory")
			})
		})

		t.Run("refuses a path through a file", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "body")

				assert.HasError(t, fsys.MkdirAll("a/d", 0o755), "the FS makes no directory under a file")
			})
		})
	})

	t.Run("ReadDir", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the entries of the directory sorted by name", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				assert.NoError(t, fsys.MkdirAll("d/c", 0o755), "the FS makes the directories d and d/c")
				create(t, fsys, "d/b", "body")
				create(t, fsys, "d/a", "")

				entries, err := fsys.ReadDir("d")

				assert.NoError(t, err, "the FS reads the directory d")
				got := make([]string, 0, len(entries))
				for _, e := range entries {
					got = append(got, e.Name())
				}
				assert.Equal(t, got, []string{"a", "b", "c"}, "the entries of d are sorted by name")
				assert.True(t, entries[2].IsDir(), "the entry c is a directory")
			})
		})

		t.Run("refuses a directory that does not exist", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				_, err := fsys.ReadDir("d")

				assert.ErrorIs(t, err, fs.ErrNotExist, "the FS has no directory d")
			})
		})
	})

	t.Run("Stat", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the name and the size of a file", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				assert.NoError(t, fsys.MkdirAll("d", 0o755), "the FS makes the directory d")
				create(t, fsys, "d/a", "body")

				info, err := fsys.Stat("d/a")

				assert.NoError(t, err, "the FS has the file d/a")
				assert.Equal(t, []any{info.Name(), info.Size(), info.IsDir()}, []any{"a", int64(4), false},
					"the information of a file has its base name and its size")
			})
		})

		t.Run("refuses a name that does not exist", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				_, err := fsys.Stat("a")

				assert.ErrorIs(t, err, fs.ErrNotExist, "the FS has no file a")
			})
		})
	})

	t.Run("SyncDir", func(t *testing.T) {
		t.Parallel()

		t.Run("syncs a directory", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				assert.NoError(t, fsys.MkdirAll("d", 0o755), "the FS makes the directory d")

				assert.NoError(t, fsys.SyncDir("d"), "the FS syncs the directory d")
				assert.NoError(t, fsys.SyncDir("."), "the FS syncs the root")
			})
		})

		t.Run("refuses a directory that does not exist", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				assert.ErrorIs(t, fsys.SyncDir("d"), fs.ErrNotExist, "the FS has no directory d")
			})
		})
	})

	t.Run("refuses a name outside the root in each method", func(t *testing.T) {
		t.Parallel()

		each(t, func(t assert.TB, fsys medium.FS) {
			_, err := fsys.ReadDir("../d")
			assert.ErrorIs(t, err, fs.ErrInvalid, "ReadDir refuses a name outside the root")
			_, err = fsys.Stat("/a")
			assert.ErrorIs(t, err, fs.ErrInvalid, "Stat refuses an absolute name")
			assert.ErrorIs(t, fsys.Remove("a/../b"), fs.ErrInvalid, "Remove refuses a name that is not clean")
			assert.ErrorIs(t, fsys.MkdirAll("", 0o755), fs.ErrInvalid, "MkdirAll refuses the empty name")
			assert.ErrorIs(t, fsys.SyncDir("d/"), fs.ErrInvalid, "SyncDir refuses a trailing slash")
		})
	})
}

// A file of the operating system and a file of a Memory read, write, seek,
// truncate and close alike.
func TestFile(t *testing.T) {
	t.Parallel()

	t.Run("Read", func(t *testing.T) {
		t.Parallel()

		t.Run("reads what the file wrote after a seek to its start", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				f := open(t, fsys, "a")
				_, err := io.WriteString(f, "body")
				assert.NoError(t, err, "the file writes its body")

				at, err := f.Seek(0, io.SeekStart)
				assert.NoError(t, err, "the file seeks to its start")
				assert.Equal(t, at, int64(0), "the offset is the start")
				p := make([]byte, 8)
				n, err := f.Read(p)
				assert.NoError(t, err, "the file reads its body")
				assert.Equal(t, string(p[:n]), "body", "the file reads what it wrote")

				_, err = f.Read(p)
				assert.ErrorIs(t, err, io.EOF, "the file reads io.EOF at its end")
				assert.NoError(t, f.Close(), "the file closes")
			})
		})
	})

	t.Run("ReadAt", func(t *testing.T) {
		t.Parallel()

		t.Run("returns io.EOF beside a short read", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "body")
				f := open(t, fsys, "a")

				p := make([]byte, 4)
				n, err := f.ReadAt(p, 2)

				assert.ErrorIs(t, err, io.EOF, "the file ends before p is full")
				assert.Equal(t, string(p[:n]), "dy", "the file reads from the offset")
				n, err = f.ReadAt(p[:2], 1)
				assert.NoError(t, err, "a read within the file fills p")
				assert.Equal(t, string(p[:n]), "od", "the file reads the bytes at the offset")
				n, err = f.ReadAt(p, 0)
				assert.NoError(t, err, "a read from the start fills p")
				assert.Equal(t, string(p[:n]), "body", "the file reads from its start")
				n, err = f.ReadAt(p, 9)
				assert.ErrorIs(t, err, io.EOF, "a read beyond the end reads nothing")
				assert.Equal(t, n, 0, "the file has no byte beyond its end")
				assert.NoError(t, f.Close(), "the file closes")
			})
		})
	})

	t.Run("Write", func(t *testing.T) {
		t.Parallel()

		t.Run("appends under os.O_APPEND", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "head")
				f, err := fsys.OpenFile("a", os.O_WRONLY|os.O_APPEND, 0)
				assert.NoError(t, err, "the FS opens a to append")

				_, err = io.WriteString(f, "tail")

				assert.NoError(t, err, "the file appends")
				assert.NoError(t, f.Close(), "the file closes")
				assert.Equal(t, read(t, fsys, "a"), "headtail", "the file appends after its content")
			})
		})
	})

	t.Run("Seek", func(t *testing.T) {
		t.Parallel()

		t.Run("moves the offset of the next write", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				f := open(t, fsys, "a")
				_, err := io.WriteString(f, "body")
				assert.NoError(t, err, "the file writes its body")

				_, err = f.Seek(0, io.SeekStart)
				assert.NoError(t, err, "the file seeks to its start")
				_, err = io.WriteString(f, "n")

				assert.NoError(t, err, "the file writes at its start")
				assert.NoError(t, f.Close(), "the file closes")
				assert.Equal(t, read(t, fsys, "a"), "nody", "the write replaces the first byte")
			})
		})
	})

	t.Run("WriteAt", func(t *testing.T) {
		t.Parallel()

		t.Run("fills the gap before the offset with zeros", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				f := open(t, fsys, "a")

				_, err := f.WriteAt([]byte("b"), 2)

				assert.NoError(t, err, "the file writes at the offset 2")
				assert.NoError(t, f.Close(), "the file closes")
				assert.Equal(t, read(t, fsys, "a"), "\x00\x00b", "the gap before the offset is zeros")
			})
		})
	})

	t.Run("Truncate", func(t *testing.T) {
		t.Parallel()

		t.Run("cuts and extends the file", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				create(t, fsys, "a", "body")
				f := open(t, fsys, "a")

				assert.NoError(t, f.Truncate(2), "the file cuts to 2 bytes")
				assert.Equal(t, read(t, fsys, "a"), "bo", "the file has its first 2 bytes")
				assert.NoError(t, f.Truncate(3), "the file extends to 3 bytes")
				assert.Equal(t, read(t, fsys, "a"), "bo\x00", "the extension is zeros")
				assert.NoError(t, f.Truncate(0), "the file cuts to nothing")
				assert.Equal(t, read(t, fsys, "a"), "", "the file is empty")
				assert.NoError(t, f.Close(), "the file closes")
			})
		})
	})

	t.Run("Stat", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the size of the file", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				f := open(t, fsys, "a")
				_, err := io.WriteString(f, "body")
				assert.NoError(t, err, "the file writes its body")

				info, err := f.Stat()

				assert.NoError(t, err, "the file states its information")
				assert.Equal(t, []any{info.Name(), info.Size()}, []any{"a", int64(4)}, "the file has 4 bytes")
				assert.NoError(t, f.Close(), "the file closes")
			})
		})
	})

	t.Run("Sync", func(t *testing.T) {
		t.Parallel()

		t.Run("syncs a written file", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				f := open(t, fsys, "a")
				_, err := io.WriteString(f, "body")
				assert.NoError(t, err, "the file writes its body")

				assert.NoError(t, f.Sync(), "the file syncs")
				assert.NoError(t, f.Close(), "the file closes")
			})
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("fails each later call with fs.ErrClosed", func(t *testing.T) {
			t.Parallel()

			each(t, func(t assert.TB, fsys medium.FS) {
				f := open(t, fsys, "a")

				assert.NoError(t, f.Close(), "the file closes")

				_, err := f.Write([]byte("b"))
				assert.ErrorIs(t, err, fs.ErrClosed, "a closed file refuses a write")
				assert.ErrorIs(t, f.Sync(), fs.ErrClosed, "a closed file refuses a sync")
			})
		})
	})

	t.Run("refuses a read of a file that it opened for writing alone", func(t *testing.T) {
		t.Parallel()

		each(t, func(t assert.TB, fsys medium.FS) {
			create(t, fsys, "a", "body")
			f, err := fsys.OpenFile("a", os.O_WRONLY, 0)
			assert.NoError(t, err, "the FS opens a for writing")

			_, err = f.Read(make([]byte, 1))

			assert.HasError(t, err, "a file for writing alone refuses a read")
			assert.NoError(t, f.Close(), "the file closes")
		})
	})

	t.Run("refuses a write of a file that it opened for reading alone", func(t *testing.T) {
		t.Parallel()

		each(t, func(t assert.TB, fsys medium.FS) {
			create(t, fsys, "a", "body")
			f, err := fsys.OpenFile("a", os.O_RDONLY, 0)
			assert.NoError(t, err, "the FS opens a for reading")

			_, err = f.Write([]byte("b"))

			assert.HasError(t, err, "a file for reading alone refuses a write")
			assert.NoError(t, f.Close(), "the file closes")
		})
	})
}
