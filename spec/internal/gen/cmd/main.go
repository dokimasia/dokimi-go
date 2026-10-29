// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Command cmd writes the constants of the package spec in the working
// directory, which go generate sets to the directory of the package spec.
package main

import (
	"fmt"
	"os"

	"go.dokimi.dev/spec/internal/gen"
)

func main() {
	if err := gen.Generate("."); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}
