// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import "go.dokimi.dev/assert/prop"

// Check is the test handle of an invariant or a convergence. It is the case
// of the run, so it implements assert.TB, and every assertion of the assert
// library works on it. Statement returns the statement of its directive.
//
// An invariant or a convergence must not wait. It checks the world between
// two steps, and the time of the run advances only through the world. An
// assertion that waits on a Check, such as one that polls a condition, waits
// on the clock of the case. That clock is assert.System unless the test has a
// clock of its own.
type Check struct {
	*prop.Case

	statement string
}

// Statement returns the statement of the check: the first sentence of the
// doc comment of its function.
func (h *Check) Statement() string { return h.statement }
