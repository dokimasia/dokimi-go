// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package medium

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// OS returns the FS of the operating system under the directory root. Its
// files are the files of the os package. Its errors wrap the errors of the os
// package, which contain the paths of the operating system.
func OS(root string) FS { return osFS{root: root} }

// osFS is the FS of the operating system under root.
type osFS struct{ root string }

// The compiler checks that the files of the os package are files of an FS.
var _ File = (*os.File)(nil)

// OpenFile opens the file name under the root with os.OpenFile.
func (o osFS) OpenFile(name string, flag int, perm fs.FileMode) (File, error) {
	p, err := o.path("open", name)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, flag, perm)
	if err != nil {
		return nil, wrapped(err)
	}
	return f, nil
}

// Remove removes the file or the empty directory name under the root.
func (o osFS) Remove(name string) error {
	p, err := o.path("remove", name)
	if err != nil {
		return err
	}
	return wrapped(os.Remove(p))
}

// Rename moves oldName to newName under the root.
func (o osFS) Rename(oldName, newName string) error {
	from, err := o.path("rename", oldName)
	if err != nil {
		return err
	}
	to, err := o.path("rename", newName)
	if err != nil {
		return err
	}
	return wrapped(os.Rename(from, to))
}

// MkdirAll creates the directory name under the root and each parent that it
// lacks.
func (o osFS) MkdirAll(name string, perm fs.FileMode) error {
	p, err := o.path("mkdir", name)
	if err != nil {
		return err
	}
	return wrapped(os.MkdirAll(p, perm))
}

// ReadDir returns the entries of the directory name under the root, sorted by
// name.
func (o osFS) ReadDir(name string) ([]fs.DirEntry, error) {
	p, err := o.path("readdir", name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p)
	return entries, wrapped(err)
}

// Stat returns the information of name under the root.
func (o osFS) Stat(name string) (fs.FileInfo, error) {
	p, err := o.path("stat", name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(p)
	return info, wrapped(err)
}

// SyncDir makes the entries of the directory name under the root durable.
func (o osFS) SyncDir(name string) error {
	p, err := o.path("syncdir", name)
	if err != nil {
		return err
	}
	return syncDir(p)
}

// path returns the path of the operating system of name under the root. It
// returns an error that wraps fs.ErrInvalid for a name that fs.ValidPath
// refuses, with the operation op.
func (o osFS) path(op, name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	return filepath.Join(o.root, filepath.FromSlash(name)), nil
}

// wrapped returns the error err of the os package with the prefix of the
// module, and nil for a nil err. errors.Is matches the error of the os package
// through it.
func wrapped(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("medium: %w", err)
}
