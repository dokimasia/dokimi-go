// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import (
	"io/fs"

	"go.dokimi.dev/medium"
)

// view is the view of an incarnation on the disk of its host. Each call of the
// view and of its files passes the gate of the incarnation, which blocks it
// while the incarnation is paused and fails it with medium.ErrCrashed once the
// incarnation has crashed. The disk of a crash by DiskReplacement serves no
// later incarnation, so the fence ends each call of a view of it as well.
//
// A view returns each error of the disk as the disk returns it, because a
// subject matches such an error with os.IsNotExist, which no wrap passes.
type view struct {
	fs   medium.FS
	gate *gate
}

// The compiler checks that a view is a medium.FS and a viewFile a
// medium.File.
var (
	_ medium.FS   = (*view)(nil)
	_ medium.File = (*viewFile)(nil)
)

// pass passes the gate g for the operation op on name, and returns an error
// that wraps medium.ErrCrashed once the incarnation has crashed.
func pass(g *gate, op, name string) error {
	if !g.enterDisk() {
		return &fs.PathError{Op: op, Path: name, Err: medium.ErrCrashed}
	}
	return nil
}

// OpenFile opens the file name of the disk, as medium.FS.OpenFile does.
func (v *view) OpenFile(name string, flag int, perm fs.FileMode) (medium.File, error) {
	if err := pass(v.gate, "open", name); err != nil {
		return nil, err
	}
	f, err := v.fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
	}
	return &viewFile{f: f, gate: v.gate, name: name}, nil
}

// Remove removes the file or the empty directory name of the disk.
func (v *view) Remove(name string) error {
	if err := pass(v.gate, "remove", name); err != nil {
		return err
	}
	return v.fs.Remove(name) //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// Rename moves oldName to newName on the disk.
func (v *view) Rename(oldName, newName string) error {
	if err := pass(v.gate, "rename", oldName); err != nil {
		return err
	}
	//nolint:wrapcheck // the view returns the error of the disk as the disk returns it
	return v.fs.Rename(oldName, newName)
}

// MkdirAll creates the directory name of the disk and each parent that it
// lacks.
func (v *view) MkdirAll(name string, perm fs.FileMode) error {
	if err := pass(v.gate, "mkdir", name); err != nil {
		return err
	}
	return v.fs.MkdirAll(name, perm) //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// ReadDir returns the entries of the directory name of the disk.
func (v *view) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pass(v.gate, "readdir", name); err != nil {
		return nil, err
	}
	return v.fs.ReadDir(name) //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// Stat returns the information of the entry name of the disk.
func (v *view) Stat(name string) (fs.FileInfo, error) {
	if err := pass(v.gate, "stat", name); err != nil {
		return nil, err
	}
	return v.fs.Stat(name) //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// SyncDir makes the entries of the directory name of the disk durable.
func (v *view) SyncDir(name string) error {
	if err := pass(v.gate, "syncdir", name); err != nil {
		return err
	}
	return v.fs.SyncDir(name) //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// viewFile is a file that a view opened. Each call passes the gate of its
// incarnation, and returns the error of the file as the file returns it.
type viewFile struct {
	f    medium.File
	gate *gate
	name string
}

// Read reads from the offset of the file.
func (f *viewFile) Read(p []byte) (int, error) {
	//dokimi:mutate-skip ror-false: the crash kills the disk after the fence, so the file refuses the call with the same error
	if err := pass(f.gate, "read", f.name); err != nil {
		return 0, err
	}
	return f.f.Read(p) //nolint:wrapcheck // the file returns io.EOF, which a reader matches with ==
}

// ReadAt reads from the offset off of the file.
func (f *viewFile) ReadAt(p []byte, off int64) (int, error) {
	//dokimi:mutate-skip ror-false: the crash kills the disk after the fence, so the file refuses the call with the same error
	if err := pass(f.gate, "read", f.name); err != nil {
		return 0, err
	}
	return f.f.ReadAt(p, off) //nolint:wrapcheck // the file returns io.EOF, which a reader matches with ==
}

// Write writes at the offset of the file.
func (f *viewFile) Write(p []byte) (int, error) {
	//dokimi:mutate-skip ror-false: the crash kills the disk after the fence, so the file refuses the call with the same error
	if err := pass(f.gate, "write", f.name); err != nil {
		return 0, err
	}
	return f.f.Write(p) //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// WriteAt writes at the offset off of the file.
func (f *viewFile) WriteAt(p []byte, off int64) (int, error) {
	//dokimi:mutate-skip ror-false: the crash kills the disk after the fence, so the file refuses the call with the same error
	if err := pass(f.gate, "write", f.name); err != nil {
		return 0, err
	}
	return f.f.WriteAt(p, off) //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// Seek sets the offset of the file.
func (f *viewFile) Seek(offset int64, whence int) (int64, error) {
	//dokimi:mutate-skip ror-false: the crash kills the disk after the fence, so the file refuses the call with the same error
	if err := pass(f.gate, "seek", f.name); err != nil {
		return 0, err
	}
	return f.f.Seek(offset, whence) //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// Sync makes the content of the file durable.
func (f *viewFile) Sync() error {
	//dokimi:mutate-skip ror-false: the crash kills the disk after the fence, so the file refuses the call with the same error
	if err := pass(f.gate, "sync", f.name); err != nil {
		return err
	}
	return f.f.Sync() //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// Truncate sets the size of the file to size.
func (f *viewFile) Truncate(size int64) error {
	//dokimi:mutate-skip ror-false: the crash kills the disk after the fence, so the file refuses the call with the same error
	if err := pass(f.gate, "truncate", f.name); err != nil {
		return err
	}
	return f.f.Truncate(size) //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// Stat returns the information of the file.
func (f *viewFile) Stat() (fs.FileInfo, error) {
	//dokimi:mutate-skip ror-false: the crash kills the disk after the fence, so the file refuses the call with the same error
	if err := pass(f.gate, "stat", f.name); err != nil {
		return nil, err
	}
	return f.f.Stat() //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}

// Close closes the file.
func (f *viewFile) Close() error {
	//dokimi:mutate-skip ror-false: the crash kills the disk after the fence, so the file refuses the call with the same error
	if err := pass(f.gate, "close", f.name); err != nil {
		return err
	}
	return f.f.Close() //nolint:wrapcheck // the view returns the error of the disk as the disk returns it
}
