// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"go.dokimi.dev/assert"

	"go.dokimi.dev/spec"
)

// example is the suite record of the example of the spec.
const example = `{
  "spec": "2.0.0",
  "suite": {"language": "go", "package": "example.com/store", "declaration": "Store"},
  "generator": {"name": "dokimi", "version": "v0.1.0"},
  "runtime": {"name": "go.dokimi.dev/conform", "version": "v0.1.0"},
  "seed": "0x5f3a1c9e27d4b860",
  "application": "subject",
  "subject": "example.com/store/storetest.InMemory",
  "checks": [
    {
      "identity": "Store.Read/zero-on-error",
      "key": "store.read/zero-on-error",
      "claim": "zero-on-error",
      "law": "no-value-beside-an-error",
      "machineries": ["nothing-beyond-a-caller"],
      "shallow": false,
      "result": "pass"
    },
    {"identity": "Store.Read/keeps-the-item", "key": "store.read/keeps-the-item", "authored": true, "result": "pass"}
  ],
  "refusals": [
    {
      "claim": "zero-on-error",
      "callable": "Store.Scan",
      "gate": "input-samples",
      "why": "eidos derives no sample of the parameter visit of Scan",
      "remedy": "author a sample of the parameter visit with the directive sample"
    }
  ],
  "unclaimed": [
    {
      "classification": "mixin:time-aware",
      "callable": "Store.Stale",
      "stance": "needs",
      "note": "the claim needs a clock that the run advances"
    }
  ]
}`

// failing is the subject record of a property check that fails, with the
// counterexample of its smallest failing case in place of the verb.
const failing = `{
  "spec": "2.0.0",
  "suite": {"language": "go", "package": "example.com/store", "declaration": "Store"},
  "generator": {"name": "dokimi", "version": "v0.1.0"},
  "runtime": {"name": "go.dokimi.dev/conform", "version": "v0.1.0"},
  "seed": "0x0000000000000001",
  "application": "subject",
  "subject": "InMemory",
  "checks": [
    {
      "identity": "Store.Get/draws-a-small-number",
      "key": "store.get/draws-a-small-number",
      "authored": true,
      "result": "fail",
      "seed": "0x0000000000000001",
      "cases": 1,
      "counterexample": %s,
      "failure": {
        "assertion": "in-range",
        "contract": "the number is below 10",
        "detail": {"got": {"type": "int", "value": 10}, "low": {"type": "float", "value": 0}, "high": {"type": "float", "value": 9}},
        "where": {"file": "store_test.go", "line": 16}
      }
    }
  ],
  "refusals": [],
  "unclaimed": []
}`

// jsonFields returns the names of the JSON fields of the struct type t, in
// order.
func jsonFields(t reflect.Type) []string {
	names := make([]string, 0, t.NumField())
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestRecord(t *testing.T) {
	t.Parallel()

	t.Run("ValidateSuiteRecord", func(t *testing.T) {
		t.Parallel()

		t.Run("accepts the example record of the spec", func(t *testing.T) {
			t.Parallel()

			assert.NoError(t, spec.ValidateSuiteRecord([]byte(example)), "the example validates")
		})

		t.Run("accepts the entries of a counterexample", func(t *testing.T) {
			t.Parallel()

			record := fmt.Sprintf(failing, `[{"label": "n", "value": {"type": "int", "value": 10}, `+
				`"any-value-fails": false, "nearest-passing": {"type": "int", "value": 9}}, {"step": "put", "drain": false}]`)

			assert.NoError(t, spec.ValidateSuiteRecord([]byte(record)), "the record validates")
		})

		tests := []struct {
			name       string
			giveRecord string
			want       string
		}{
			{name: "refuses a record that is no JSON", giveRecord: `{"spec": `, want: "unexpected end of JSON input"},
			{name: "refuses a record without its checks", giveRecord: `{"spec": "2.0.0"}`, want: "checks"},
			{
				name:       "refuses a counterexample by label",
				giveRecord: fmt.Sprintf(failing, `{"n": {"type": "int", "value": 10}}`),
				want:       "counterexample",
			},
			{
				name:       "refuses a refusal without its gate",
				giveRecord: strings.Replace(example, `"gate": "input-samples",`, "", 1),
				want:       "gate",
			},
			{
				name:       "refuses points that are no integer",
				giveRecord: strings.Replace(example, `"shallow": false,`, `"shallow": false, "points": "82",`, 1),
				want:       "points",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				err := spec.ValidateSuiteRecord([]byte(tt.giveRecord))

				assert.ErrorIs(t, err, spec.ErrRecord, "the error is the error of a record")
				assert.Contains(t, err.Error(), tt.want, "the error names the fault of the record")
			})
		}
	})

	t.Run("SuiteRecord", func(t *testing.T) {
		t.Parallel()
		schema := schemaOf(t, "suite")
		items := func(property string) *jsonschema.Schema { return schema.Properties[property].Items }

		tests := []struct {
			name       string
			giveType   reflect.Type
			wantSchema *jsonschema.Schema
		}{
			{
				name:     "has a field for each property of the record",
				giveType: reflect.TypeFor[spec.SuiteRecord](), wantSchema: schema,
			},
			{
				name:     "has a field for each property of the suite",
				giveType: reflect.TypeFor[spec.RecordSuite](), wantSchema: schema.Properties["suite"],
			},
			{
				name:     "has a field for each property of the generator",
				giveType: reflect.TypeFor[spec.RecordTool](), wantSchema: schema.Properties["generator"],
			},
			{
				name:     "has a field for each property of the runtime",
				giveType: reflect.TypeFor[spec.RecordTool](), wantSchema: schema.Properties["runtime"],
			},
			{
				name:     "has a field for each property of a check",
				giveType: reflect.TypeFor[spec.CheckRecord](), wantSchema: items("checks"),
			},
			{
				name:     "has a field for each property of a refusal",
				giveType: reflect.TypeFor[spec.RefusalRecord](), wantSchema: items("refusals"),
			},
			{
				name:     "has a field for each property of an unclaimed classification",
				giveType: reflect.TypeFor[spec.UnclaimedRecord](), wantSchema: items("unclaimed"),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				assert.Equal(t, jsonFields(tt.giveType), slices.Sorted(maps.Keys(tt.wantSchema.Properties)),
					"the type reads each property of the schema of the copy")
			})
		}
	})
}
