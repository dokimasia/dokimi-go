// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"io"
	"testing"
	"testing/iotest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/conform"
)

// content is the content of the values of NewBytes of the specs.
const content = "conform é"

// lost is the input of the writes of the specs, which a Bytes discards.
var lost = []byte{'l', 'o', 's', 't'}

// A value of Bytes keeps the contracts of the interfaces of package io: its
// reads cover its bytes, and its writes discard their input.
func TestBytes(t *testing.T) {
	t.Parallel()

	t.Run("Bytes", func(t *testing.T) {
		t.Parallel()

		t.Run("reads no byte as the zero value", func(t *testing.T) {
			t.Parallel()

			var b conform.Bytes
			n, err := b.Read(make([]byte, 4))
			expect.Equal(t, n, 0, "the read reads no byte")
			expect.ErrorIs(t, err, io.EOF, "the read reports the end of the bytes")
		})
	})

	t.Run("NewBytes", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a reader that keeps the contract of io.Reader", func(t *testing.T) {
			t.Parallel()

			assert.NoError(t, iotest.TestReader(conform.NewBytes([]byte(content)), []byte(content)),
				"the reader passes the checks of iotest")
		})
	})

	t.Run("Write", func(t *testing.T) {
		t.Parallel()

		t.Run("discards its input and reports it as written", func(t *testing.T) {
			t.Parallel()

			b := conform.NewBytes([]byte(content))
			n, err := b.Write(lost)
			assert.NoError(t, err, "the write succeeds")
			assert.Equal(t, n, len(lost), "the write reports each byte as written")
			read, err := io.ReadAll(b)
			assert.NoError(t, err, "the read succeeds")
			assert.Equal(t, string(read), content, "the reads do not return the written bytes")
		})
	})

	t.Run("WriteString", func(t *testing.T) {
		t.Parallel()

		t.Run("discards its input and reports it as written", func(t *testing.T) {
			t.Parallel()

			b := conform.NewBytes([]byte(content))
			n, err := b.WriteString(string(lost))
			assert.NoError(t, err, "the write succeeds")
			assert.Equal(t, n, len(lost), "the write reports each byte as written")
			read, err := io.ReadAll(b)
			assert.NoError(t, err, "the read succeeds")
			assert.Equal(t, string(read), content, "the reads do not return the written bytes")
		})
	})

	t.Run("WriteByte", func(t *testing.T) {
		t.Parallel()

		t.Run("discards the byte", func(t *testing.T) {
			t.Parallel()

			b := conform.NewBytes([]byte(content))
			assert.NoError(t, b.WriteByte('x'), "the write succeeds")
			read, err := io.ReadAll(b)
			assert.NoError(t, err, "the read succeeds")
			assert.Equal(t, string(read), content, "the reads do not return the written byte")
		})
	})

	t.Run("Close", func(t *testing.T) {
		t.Parallel()

		t.Run("returns nil", func(t *testing.T) {
			t.Parallel()

			assert.NoError(t, conform.NewBytes([]byte(content)).Close(), "the close succeeds")
		})
	})
}
