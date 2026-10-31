// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/files"
)

// childVar is the variable of the environment that makes the test binary run
// main in place of its tests. The cases run the test binary again with it,
// so go test merges the coverage of main into the profile.
const childVar = "DOKIMI_GEN_MAIN"

// specDir is the directory of the package spec. It contains the naming table
// and the constants that a run of the command writes from it.
const specDir = "../../.."

func TestMain(m *testing.M) {
	if os.Getenv(childVar) != "" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The command writes the constants of the package spec in its working
// directory, and exits with status 1 and the error where it cannot.
func TestCmd(t *testing.T) {
	t.Parallel()

	t.Run("main", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the committed constants of the spec from its naming table", func(t *testing.T) {
			t.Parallel()
			table := files.Read(t, filepath.Join(specDir, "naming.json"))
			dir := files.Workspace(t, files.Tree{"naming.json": files.Text(table)})

			out, status := run(t, dir)

			assert.Equal(t, status, 0, "the command passes: "+out)
			want := files.Read(t, filepath.Join(specDir, "names.gen.go"))
			files.HasContent(t, filepath.Join(dir, "names.gen.go"), want,
				"the command writes the committed names.gen.go")
		})

		t.Run("exits with status 1 where the directory has no naming table", func(t *testing.T) {
			t.Parallel()

			out, status := run(t, t.TempDir())

			assert.Equal(t, status, 1, "a directory without naming.json fails the command")
			expect.HasPrefix(t, out, "gen: ", "the command writes the error of the generator")
		})
	})
}

// run runs main in the directory dir through the test binary, and returns
// its output and its exit status.
func run(t *testing.T, dir string) (string, int) {
	t.Helper()

	binary, err := os.Executable()
	assert.NoError(t, err, "the path of the test binary")
	cmd := exec.CommandContext(t.Context(), binary)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), childVar+"=1")
	out, err := cmd.CombinedOutput()
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	assert.NoError(t, err, "the test binary runs")
	return string(out), 0
}
