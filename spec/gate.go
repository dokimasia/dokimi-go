// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

// GateID identifies a gate.
type GateID string

// Gate is a condition on the facts of the subject of a claim. A suite
// records a licensed claim whose gate fails as a refusal with the gate.
type Gate struct {
	ID GateID `json:"id"`

	Since string `json:"since"`
}

func (g *Gate) key() GateID { return g.ID }

var gates = section[GateID, Gate]("gates.json", "gates")

// Gates returns every gate of the copy, in the order of gates.json. The
// slice is shared with every other call, so a caller does not modify it.
func Gates() []Gate { return gates().entries }

// GateOf returns the gate with the identifier id, and false for an
// identifier that the copy does not contain.
func GateOf(id GateID) (Gate, bool) { return gates().of(id) }
