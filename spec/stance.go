// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

// StanceID identifies a stance.
type StanceID string

// Stance is the reason why a classification has no check. A suite records
// the stance beside each classification that licenses no claim.
type Stance struct {
	ID StanceID `json:"id"`

	// Meaning is the reason, as one sentence.
	Meaning string `json:"meaning"`

	// Closes is the change that gives the classification a check.
	Closes string `json:"closes"`

	Since string `json:"since"`
}

func (s *Stance) key() StanceID { return s.ID }

var stances = section[StanceID, Stance]("stances.json", "stances")

// Stances returns every stance of the copy, in the order of stances.json.
// The slice is shared with every other call, so a caller does not modify
// it.
func Stances() []Stance { return stances().entries }

// StanceOf returns the stance with the identifier id, and false for an
// identifier that the copy does not contain.
func StanceOf(id StanceID) (Stance, bool) { return stances().of(id) }
