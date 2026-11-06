// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import "strconv"

// Kill is a kind of crash of an incarnation.
type Kill uint8

// The kinds of a crash.
const (
	// ProcessCrash ends the process and keeps the page cache, as a kill of
	// the process does: the writes that a sync did not cover remain pending
	// on the disk.
	ProcessCrash Kill = iota + 1
	// PowerLoss ends the process and loses the page cache: the disk keeps a
	// crash state of the writes that a sync did not cover.
	PowerLoss
	// DiskReplacement ends the process, and the next incarnation starts over
	// an empty disk.
	DiskReplacement
)

// String returns the name of the kind that the suite record states, such as
// power-loss, and Kill(n) for a value outside the three kinds.
func (k Kill) String() string {
	switch k {
	case ProcessCrash:
		return "process-crash"
	case PowerLoss:
		return "power-loss"
	case DiskReplacement:
		return "disk-replacement"
	}
	return "Kill(" + strconv.Itoa(int(k)) + ")"
}
