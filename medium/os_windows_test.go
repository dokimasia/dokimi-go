// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

//go:build windows

package medium_test

import (
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/files"

	"go.dokimi.dev/medium"
)

// On Windows, the FS of the operating system checks a directory and does not
// sync it, because Windows cannot sync a directory.
func TestOSWindows(t *testing.T) {
	t.Parallel()

	t.Run("SyncDir", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil for a directory that exists", func(t *testing.T) {
			t.Parallel()
			dir := files.Workspace(t, files.Tree{"d": files.Dir()})

			assert.NoError(t, medium.OS(dir).SyncDir("d"), "the FS checks the directory d")
		})
	})
}
