// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/files"
)

// entries decodes the section of the file name.json of the copy into
// entries of the model. It fails the test for a field or a section of the
// file that the model does not read, and for a section without entries.
func entries[E any](t *testing.T, name, section string) []E {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(files.Read(t, name+".json")))
	dec.DisallowUnknownFields()
	var doc map[string][]E
	assert.NoError(t, dec.Decode(&doc), "the model reads each field of "+name+".json")
	assert.Equal(t, slices.Sorted(maps.Keys(doc)), []string{section}, name+".json has the section "+section+" alone")
	assert.NotEmpty(t, doc[section], "the section "+section+" has entries")
	return doc[section]
}

// schemaOf returns the schema of the file name.json of the copy.
func schemaOf(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	var schema jsonschema.Schema
	assert.NoError(t, json.Unmarshal([]byte(files.Read(t, "schemas/"+name+".schema.json")), &schema),
		"the schema of "+name+" decodes")
	return &schema
}

// validates checks that the schema of the file name.json of the copy accepts
// the file.
func validates(t *testing.T, name string) {
	t.Helper()
	resolved, err := schemaOf(t, name).Resolve(nil)
	assert.NoError(t, err, "the schema of "+name+" resolves")
	var doc any
	assert.NoError(t, json.Unmarshal([]byte(files.Read(t, name+".json")), &doc), name+".json decodes")

	assert.NoError(t, resolved.Validate(doc), "the schema accepts "+name+".json")
}
