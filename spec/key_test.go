// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec_test

import (
	"encoding/json"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/files"

	"go.dokimi.dev/spec"
)

func TestKey(t *testing.T) {
	t.Parallel()

	t.Run("Key", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the key of each identity of keys.json", func(t *testing.T) {
			t.Parallel()
			var doc struct {
				Keys []struct {
					Identity string `json:"identity"`
					Key      string `json:"key"`
				} `json:"keys"`
			}
			assert.NoError(t, json.Unmarshal([]byte(files.Read(t, "keys.json")), &doc), "keys.json decodes")
			assert.NotEmpty(t, doc.Keys, "the spec has identities with their keys")

			for _, k := range doc.Keys {
				expect.Equal(t, spec.Key(k.Identity), k.Key, "the key of "+k.Identity+" is the key of the spec")
			}
		})
	})
}
