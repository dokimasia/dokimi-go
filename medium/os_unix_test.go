// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

//go:build !windows

package medium_test

import (
	"io/fs"
	"os"
	"syscall"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/files"

	"go.dokimi.dev/medium"
)

// On a system other than Windows, the FS of the operating system opens a
// directory to sync it.
func TestOSUnix(t *testing.T) {
	t.Parallel()

	t.Run("SyncDir", func(t *testing.T) {
		t.Parallel()

		t.Run("refuses a directory that the process cannot open", func(t *testing.T) {
			t.Parallel()
			dir := files.Workspace(t, files.Tree{"d": files.Dir().WithMode(0o000)})
			t.Cleanup(func() { files.Write(t, dir, files.Tree{"d": files.Dir().WithMode(0o755)}) })

			err := medium.OS(dir).SyncDir("d")

			assert.ErrorIs(t, err, fs.ErrPermission, "the FS opens the directory to sync it")
		})

		t.Run("returns the error of a directory whose sync fails", func(t *testing.T) {
			t.Parallel()
			if _, err := os.Stat("/proc/self"); err != nil {
				t.Skip("the system has no procfs, whose directories refuse a sync")
			}

			err := medium.OS("/proc").SyncDir("self")

			assert.ErrorIs(t, err, syscall.EINVAL, "procfs refuses the sync of a directory")
		})
	})
}
