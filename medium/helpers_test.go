// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package medium_test

import (
	"io"
	"os"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/medium"
)

// each runs fn as a subtest on a new FS of each implementation: the FS of the
// operating system over a directory of the test, and a Memory. A spec of the
// contract of FS runs on both, so the Memory models what the operating system
// does.
func each(t *testing.T, fn func(t assert.TB, fsys medium.FS)) {
	t.Helper()

	t.Run("OS", func(t *testing.T) {
		t.Parallel()

		fn(t, medium.OS(t.TempDir()))
	})
	t.Run("Memory", func(t *testing.T) {
		t.Parallel()

		fn(t, medium.NewMemory())
	})
}

// create writes content to the file name of fsys, which it creates or
// truncates, and closes the file without a sync.
func create(tb assert.TB, fsys medium.FS, name, content string) {
	tb.Helper()

	f, err := fsys.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	assert.NoError(tb, err, "the FS opens "+name+" for writing")
	_, err = io.WriteString(f, content)
	assert.NoError(tb, err, "the FS writes "+name)
	assert.NoError(tb, f.Close(), "the FS closes "+name)
}

// read returns the content of the file name of fsys.
func read(tb assert.TB, fsys medium.FS, name string) string {
	tb.Helper()

	f, err := fsys.OpenFile(name, os.O_RDONLY, 0)
	assert.NoError(tb, err, "the FS opens "+name+" for reading")
	content, err := io.ReadAll(f)
	assert.NoError(tb, err, "the FS reads "+name)
	assert.NoError(tb, f.Close(), "the FS closes "+name)
	return string(content)
}

// open opens the file name of fsys for reading and writing, and creates it.
func open(tb assert.TB, fsys medium.FS, name string) medium.File {
	tb.Helper()

	f, err := fsys.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)
	assert.NoError(tb, err, "the FS opens "+name)
	return f
}
