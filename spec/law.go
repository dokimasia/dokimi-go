// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

// LawID identifies a law.
type LawID string

// Law is a statement about a subject that is true or false. The checks of
// the claims witness it.
type Law struct {
	ID LawID `json:"id"`

	// Statement is the law as one sentence.
	Statement string `json:"statement"`

	Since string `json:"since"`
}

func (l *Law) key() LawID { return l.ID }

var laws = section[LawID, Law]("laws.json", "laws")

// Laws returns every law of the copy, in the order of laws.json. The slice
// is shared with every other call, so a caller does not modify it.
func Laws() []Law { return laws().entries }

// LawOf returns the law with the identifier id, and false for an identifier
// that the copy does not contain.
func LawOf(id LawID) (Law, bool) { return laws().of(id) }
