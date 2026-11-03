// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

//go:build !windows

package medium

import (
	"errors"
	"os"
)

// syncDir syncs the directory at the path of the operating system, so the
// entries of the directory are durable.
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return wrapped(err)
	}
	return wrapped(errors.Join(d.Sync(), d.Close()))
}
