// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"bytes"
	"io"
)

// Bytes is a value of the interfaces of package io over a byte slice. The
// methods of its embedded [bytes.Reader] read the bytes: Read, ReadAt,
// ReadByte, ReadRune, Seek and WriteTo. Write, WriteString and WriteByte
// discard their input and report it as written, and Close returns nil. The
// zero Bytes has no bytes.
//
// A generated suite states a *Bytes over drawn bytes as the generator of each
// interface of io that *Bytes implements:
//
//	prop.Using(prop.Composite(func(c *prop.Case) io.ReadCloser {
//		return conform.NewBytes(c.Draw(prop.Of[[]byte](), "body"))
//	}))
type Bytes struct {
	bytes.Reader
}

// The interfaces of package io that *Bytes implements, and each interface
// that they embed.
var (
	_ io.ReadWriteSeeker = (*Bytes)(nil)
	_ io.ReadSeekCloser  = (*Bytes)(nil)
	_ io.ReadWriteCloser = (*Bytes)(nil)
	_ io.ReaderAt        = (*Bytes)(nil)
	_ io.ByteScanner     = (*Bytes)(nil)
	_ io.RuneScanner     = (*Bytes)(nil)
	_ io.ByteWriter      = (*Bytes)(nil)
	_ io.StringWriter    = (*Bytes)(nil)
	_ io.WriterTo        = (*Bytes)(nil)
)

// NewBytes returns a *Bytes over p. Its reads return the bytes of p, so the
// caller does not change p while the value reads it.
func NewBytes(p []byte) *Bytes {
	b := &Bytes{}
	b.Reset(p)
	return b
}

// Write discards p and reports every byte of p as written.
func (*Bytes) Write(p []byte) (int, error) {
	return len(p), nil
}

// WriteString discards s and reports every byte of s as written.
func (*Bytes) WriteString(s string) (int, error) {
	return len(s), nil
}

// WriteByte discards c.
func (*Bytes) WriteByte(byte) error {
	return nil
}

// Close returns nil.
func (*Bytes) Close() error {
	return nil
}
