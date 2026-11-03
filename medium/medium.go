// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package medium

import (
	"errors"
	"io"
	"io/fs"
)

// ErrCrashed is the error of each call of a crashed [Memory], and of each call
// of a file that a Memory opened before a crash.
var ErrCrashed = errors.New("medium: the medium crashed")

// FS is the storage of a subject. Each method takes slash-separated names
// relative to the root of the FS, and fails with an error that wraps
// [fs.ErrInvalid] for a name that [fs.ValidPath] refuses.
type FS interface {
	// OpenFile opens the file name with the flags of os.OpenFile, and
	// creates it with perm where flag contains os.O_CREATE.
	OpenFile(name string, flag int, perm fs.FileMode) (File, error)

	// Remove removes the file or the empty directory name.
	Remove(name string) error

	// Rename moves the file or the directory oldName to newName, and
	// replaces a file at newName.
	Rename(oldName, newName string) error

	// MkdirAll creates the directory name and each parent that it lacks.
	MkdirAll(name string, perm fs.FileMode) error

	// ReadDir returns the entries of the directory name, sorted by name.
	ReadDir(name string) ([]fs.DirEntry, error)

	// Stat returns the information of the file or the directory name.
	Stat(name string) (fs.FileInfo, error)

	// SyncDir makes the entries of the directory durable, such as a file
	// created or renamed in it.
	SyncDir(name string) error
}

// File is an open file of an FS.
type File interface {
	io.Reader
	io.ReaderAt
	io.Writer
	io.WriterAt
	io.Seeker

	// Sync makes the written data of the file durable.
	Sync() error

	// Truncate changes the size of the file to size.
	Truncate(size int64) error

	// Stat returns the information of the file.
	Stat() (fs.FileInfo, error)

	// Close closes the file. Each later call fails with an error that wraps
	// fs.ErrClosed.
	Close() error
}
