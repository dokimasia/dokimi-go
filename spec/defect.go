// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

// DefectFormID identifies a form of a planted defect.
type DefectFormID string

// DefectForm is a form of the planted defects. A suite writes a form as a
// type that wraps a fresh subject and overrides the callable that the form
// changes.
type DefectForm struct {
	ID DefectFormID `json:"id"`

	// Does is what the override does, as one sentence.
	Does string `json:"does"`

	Since string `json:"since"`
}

func (f *DefectForm) key() DefectFormID { return f.ID }

var defectForms = section[DefectFormID, DefectForm]("defects.json", "forms")

// DefectForms returns every defect form of the copy, in the order of
// defects.json. The slice is shared with every other call, so a caller does
// not modify it.
func DefectForms() []DefectForm { return defectForms().entries }

// DefectFormOf returns the defect form with the identifier id, and false for
// an identifier that the copy does not contain.
func DefectFormOf(id DefectFormID) (DefectForm, bool) { return defectForms().of(id) }
