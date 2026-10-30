// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/spec"
)

// errMiss and errOther are the errors that the cells of the specs return.
var (
	errMiss  = errors.New("probe_test: the cell has no value")
	errOther = errors.New("probe_test: the cell fails")
)

// cell is the subject of the specs. answer decides the value and the error of
// each call, from the cell and the context of the call. calls counts the
// calls, the current one included, and poisoned reports a call of induce.
type cell struct {
	answer   answer
	calls    int
	poisoned bool
}

// answer decides the value and the error of a call of a cell.
type answer func(ctx context.Context, c *cell) (int, error)

// answering returns a factory of cells whose calls answer with a.
func answering(a answer) func(*conform.Handle[*cell]) *cell {
	return func(*conform.Handle[*cell]) *cell { return &cell{answer: a} }
}

// calls returns the call of the subject of h.
func calls(h *conform.Handle[*cell]) probe.Call[int] {
	return func(ctx context.Context) (int, error) {
		c := h.Subject()
		c.calls++
		return c.answer(ctx, c)
	}
}

// acts returns the call of the subject of h for its error.
func acts(h *conform.Handle[*cell]) probe.Act {
	call := calls(h)
	return func(ctx context.Context) error {
		_, err := call(ctx)
		return err
	}
}

// proves applies a derived check of the probe p to a subject of good, and
// falsifies it on a subject of bad, which fails it with reason.
func proves(
	t *testing.T, p spec.ProbeID, reason spec.ReasonID, apply func(h *conform.Handle[*cell]),
	good, bad func(*conform.Handle[*cell]) *cell,
) {
	t.Helper()
	conform.Check[*cell]{
		ID: "Cell.Call/claim", Statement: "the probe holds", Claim: "claim", Probe: p, Reason: reason,
		Apply: apply, Defect: bad,
	}.Run(t, good)
}

// vacuous applies a check of the probe p to a subject of source, inside
// around where around is not nil, and fails t unless the check fails once for
// the vacuous condition of p.
func vacuous[S any](
	t *testing.T,
	p spec.ProbeID,
	apply func(h *conform.Handle[S]),
	source func(*conform.Handle[S]) S,
	around func(t *testing.T, run func(*testing.T)),
) {
	t.Helper()
	r := &onTest{Recorder: assert.NewRecorder(), t: t}
	conform.Check[S]{
		ID: "Subject.Call/claim", Statement: "the probe holds", Probe: p, Apply: apply, Around: around,
	}.Run(r, source)
	entry, _ := spec.ProbeOf(p)
	condition := strings.ToLower(entry.Vacuous[:1]) + strings.TrimSuffix(entry.Vacuous[1:], ".")
	assert.Equal(t, r.Messages(), []string{"Subject.Call/claim never engaged: " + condition + ". Pass a subject " +
		"that engages the check, or excuse the check with the directive excuse or conform.Excuse and a reason."},
		"the check fails for the vacuous condition of its probe")
}

// onTest is a recorder of the test t. Its method Unwrap returns t, so a check
// with Around runs inside the environment of t, and the recorder keeps what
// conform reports on the seat.
type onTest struct {
	*assert.Recorder

	t *testing.T
}

// Unwrap returns the test of the recorder.
func (s *onTest) Unwrap() assert.TB { return s.t }
