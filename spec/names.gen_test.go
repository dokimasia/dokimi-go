// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"testing"

	"go.dokimi.dev/assert"
)

// types contains the Go type of the identifiers of each section of the
// naming table.
var types = map[string]string{
	"laws": "LawID", "claims": "ClaimID", "gates": "GateID", "probes": "ProbeID", "reasons": "ReasonID",
	"forms": "DefectFormID", "machineries": "MachineryID", "stances": "StanceID",
}

// constant is one constant of names.gen.go: its Go type and its identifier.
type constant struct {
	goType, id string
}

func TestNames(t *testing.T) {
	t.Parallel()

	t.Run("constants", func(t *testing.T) {
		t.Parallel()

		t.Run("declares the constant of each identifier of naming.json and no other", func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile("naming.json")
			assert.NoError(t, err, "the copy has naming.json")
			var table struct {
				Names map[string]map[string]map[string]string `json:"names"`
			}
			assert.NoError(t, json.Unmarshal(data, &table), "naming.json decodes")
			want := map[string]constant{}
			for section, ids := range table.Names {
				for id, spellings := range ids {
					want[spellings["go"]] = constant{goType: types[section], id: id}
				}
			}
			file, err := parser.ParseFile(token.NewFileSet(), "names.gen.go", nil, 0)
			assert.NoError(t, err, "names.gen.go parses")
			got := map[string]constant{}
			ast.Inspect(file, func(n ast.Node) bool {
				spec, ok := n.(*ast.ValueSpec)
				if !ok {
					return true
				}
				id, _ := strconv.Unquote(spec.Values[0].(*ast.BasicLit).Value)
				got[spec.Names[0].Name] = constant{goType: spec.Type.(*ast.Ident).Name, id: id}
				return false
			})

			assert.Equal(t, got, want, "names.gen.go spells each identifier as the table spells it in Go")
		})
	})
}
