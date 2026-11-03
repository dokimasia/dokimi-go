// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

//go:build windows

package medium

import "os"

// syncDir returns the error of a directory that does not exist at the path of
// the operating system. Windows cannot sync a directory, so syncDir does not
// sync it, as the filesystems of Pebble and RocksDB do.
func syncDir(path string) error {
	_, err := os.Stat(path)
	return wrapped(err)
}
