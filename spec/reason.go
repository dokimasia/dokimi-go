// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

// ReasonID identifies a reason code.
type ReasonID string

// Reason is the identifier of a violation that a probe reports.
// Falsification compares it with the reason code of the planted defect of a
// claim.
type Reason struct {
	ID ReasonID `json:"id"`

	// Meaning is the violation, as one sentence.
	Meaning string `json:"meaning"`

	Since string `json:"since"`
}

func (r *Reason) key() ReasonID { return r.ID }

var reasons = section[ReasonID, Reason]("reasons.json", "reasons")

// Reasons returns every reason code of the copy, in the order of
// reasons.json. The slice is shared with every other call, so a caller does
// not modify it.
func Reasons() []Reason { return reasons().entries }

// ReasonOf returns the reason code with the identifier id, and false for an
// identifier that the copy does not contain.
func ReasonOf(id ReasonID) (Reason, bool) { return reasons().of(id) }
