// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

// ClaimID identifies a claim. It is the segment of each check of the claim.
type ClaimID string

// Claim is the runtime contract of a claim: its statement, its probe and
// its planted defect.
type Claim struct {
	ID ClaimID `json:"id"`

	// Statement is the sentence of the check. The generator replaces
	// {declaration}, {callable} and the name of each partner in braces.
	Statement string `json:"statement"`

	// Law is the law that the check witnesses.
	Law LawID `json:"law"`

	// Probe is the probe that the check calls.
	Probe ProbeID `json:"probe"`

	// Defect is the planted defect of the check.
	Defect Defect `json:"defect"`

	Since string `json:"since"`

	// Machineries are what the run supplies before the check exercises its
	// claim. The first is the deepest.
	Machineries []MachineryID `json:"machineries,omitempty"`
}

func (c *Claim) key() ClaimID { return c.ID }

// Defect is the planted defect of a claim: its form, and the reason code
// with which the check of the claim fails on it.
type Defect struct {
	Form   DefectFormID `json:"form"`
	Reason ReasonID     `json:"reason"`
}

var claims = section[ClaimID, Claim]("claims.json", "claims")

// Claims returns every claim of the copy, in the order of claims.json. The
// slice is shared with every other call, so a caller does not modify it.
func Claims() []Claim { return claims().entries }

// ClaimOf returns the claim with the identifier id, and false for an
// identifier that the copy does not contain.
func ClaimOf(id ClaimID) (Claim, bool) { return claims().of(id) }
