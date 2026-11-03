// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package medium_test

import (
	"os"
	"path/filepath"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/files"

	"go.dokimi.dev/medium"
)

func TestOS(t *testing.T) {
	t.Parallel()

	t.Run("OS", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the files of the FS under its root", func(t *testing.T) {
			t.Parallel()
			dir := files.Workspace(t, files.Tree{"d": files.Dir()})
			fsys := medium.OS(dir)

			create(t, fsys, "d/a", "body")

			files.HasContent(t, filepath.Join(dir, "d", "a"), "body", "the FS writes the file under its root")
		})

		t.Run("reads the files of the operating system under its root", func(t *testing.T) {
			t.Parallel()
			dir := files.Workspace(t, files.Tree{"d/a": files.Text("body")})

			assert.Equal(t, read(t, medium.OS(dir), "d/a"), "body", "the FS reads the file under its root")
		})

		t.Run("opens the files of the os package", func(t *testing.T) {
			t.Parallel()

			f := open(t, medium.OS(t.TempDir()), "a")

			_, isOS := f.(*os.File)
			assert.True(t, isOS, "a file of the FS is a file of the os package")
			assert.NoError(t, f.Close(), "the file closes")
		})
	})
}
