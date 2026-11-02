// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
	"math"
	"reflect"
	"slices"
	"strconv"
	"time"
)

// Entry is one step of a machine: the process and its epoch, the event, the
// effects that the machine returned, the error of the machine after the
// step, the time of its clock and the faults in force. Dropped reports an
// event of an earlier epoch that the world dropped instead of a step, which
// has no effects.
type Entry[E, F any] struct {
	Event   E
	Err     error
	Time    time.Time
	Process string
	Effects []F
	Faults  []string
	Epoch   uint64
	Dropped bool
}

// Log returns the steps of the machines so far, in their order.
func (w *World[E, F]) Log() []Entry[E, F] { return slices.Clip(w.log) }

// Checksum returns the checksum of the log so far. Two runs that make the
// same steps have the same checksum after each step.
func (w *World[E, F]) Checksum() uint64 { return w.sum }

// record appends e to the log, and hashes it into the checksum: the
// checksum before it, the process, the epoch, the typed value of the event
// and of each effect, and the time.
func (w *World[E, F]) record(e Entry[E, F]) {
	w.log = append(w.log, e)
	var enc encoder
	enc.uint(w.sum)
	enc.text(e.Process)
	enc.uint(e.Epoch)
	enc.value(reflect.ValueOf(&e.Event).Elem())
	enc.value(reflect.ValueOf(e.Effects))
	enc.int(e.Time.UnixNano())
	h := fnv.New64a()
	_, _ = h.Write(enc.buf.Bytes())
	w.sum = h.Sum64()
}

// encoder writes the typed value of a Go value: its type, and its value with
// each pointer followed, the entries of a map in the order of their keys, and
// a function, a channel and an unsafe pointer by whether they are nil. The
// address of a pointer differs between two runs, so the encoding contains no
// address. A pointer, a map or a slice that contains itself is written once,
// and as a reference to that place within itself.
type encoder struct {
	// path is the pointers, the maps and the slices around the value that the
	// encoder writes, from the outermost.
	path []place
	buf  bytes.Buffer
}

// place is a pointer, a map or a slice of a path: its kind, its address and,
// for a slice, its length.
type place struct {
	at   uintptr
	len  int
	kind reflect.Kind
}

// The tags of the encoding of a value.
const (
	tagNil   byte = 'n'
	tagValue byte = 'v'
	tagSeen  byte = 's'
)

// value writes the typed value of v. Each value that the encoding visits is
// valid, because it writes a nil interface as nil and does not enter it.
func (e *encoder) value(v reflect.Value) {
	e.text(v.Type().String())
	switch v.Kind() {
	case reflect.Bool:
		e.text(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.int(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.uint(v.Uint())
	case reflect.Float32, reflect.Float64:
		e.uint(math.Float64bits(v.Float()))
	case reflect.Complex64, reflect.Complex128:
		e.uint(math.Float64bits(real(v.Complex())))
		e.uint(math.Float64bits(imag(v.Complex())))
	case reflect.String:
		e.text(v.String())
	case reflect.Array:
		e.elements(v)
	case reflect.Slice:
		e.nilOr(v, func() { e.within(placeOf(v), func() { e.elements(v) }) })
	case reflect.Struct:
		for _, field := range v.Fields() {
			e.value(field)
		}
	case reflect.Pointer:
		e.nilOr(v, func() { e.within(placeOf(v), func() { e.value(v.Elem()) }) })
	case reflect.Interface:
		e.nilOr(v, func() { e.value(v.Elem()) })
	case reflect.Map:
		e.nilOr(v, func() { e.within(placeOf(v), func() { e.entries(v) }) })
	default:
		// A function, a channel and an unsafe pointer have no value that two
		// runs share.
		e.nilOr(v, func() {})
	}
}

// elements writes the length and each element of the array or the slice v.
func (e *encoder) elements(v reflect.Value) {
	e.int(int64(v.Len()))
	for i := range v.Len() {
		e.value(v.Index(i))
	}
}

// placeOf returns the place of the pointer, the map or the slice v.
func placeOf(v reflect.Value) place {
	p := place{kind: v.Kind(), at: v.Pointer()}
	if p.kind == reflect.Slice {
		p.len = v.Len()
	}
	return p
}

// within calls write for the value at p, and writes a reference to p in its
// place when the path around the value already contains p.
func (e *encoder) within(p place, write func()) {
	if at := slices.Index(e.path, p); at >= 0 {
		e.buf.WriteByte(tagSeen)
		e.int(int64(at))
		return
	}
	e.path = append(e.path, p)
	write()
	e.path = e.path[:len(e.path)-1]
}

// entries writes the entries of the map v, each key and value in an encoding
// of its own, in the order of the encodings of the keys.
func (e *encoder) entries(v reflect.Value) {
	type entry struct{ key, value []byte }
	entries := make([]entry, 0, v.Len())
	for it := v.MapRange(); it.Next(); {
		key, value := encoder{path: e.path}, encoder{path: e.path}
		key.value(it.Key())
		value.value(it.Value())
		entries = append(entries, entry{key: key.buf.Bytes(), value: value.buf.Bytes()})
	}
	slices.SortFunc(entries, func(a, b entry) int { return bytes.Compare(a.key, b.key) })
	e.int(int64(len(entries)))
	for _, en := range entries {
		e.bytes(en.key)
		e.bytes(en.value)
	}
}

// nilOr writes nil for the nil v, and calls write after the tag of a value
// otherwise.
func (e *encoder) nilOr(v reflect.Value, write func()) {
	if v.IsNil() {
		e.buf.WriteByte(tagNil)
		return
	}
	e.buf.WriteByte(tagValue)
	write()
}

// int writes the signed integer n as a varint.
func (e *encoder) int(n int64) { e.buf.Write(binary.AppendVarint(e.buf.AvailableBuffer(), n)) }

// uint writes the unsigned integer n as a varint.
func (e *encoder) uint(n uint64) { e.buf.Write(binary.AppendUvarint(e.buf.AvailableBuffer(), n)) }

// text writes the length and the bytes of s.
func (e *encoder) text(s string) {
	e.int(int64(len(s)))
	e.buf.WriteString(s)
}

// bytes writes the length and the bytes of b.
func (e *encoder) bytes(b []byte) {
	e.int(int64(len(b)))
	e.buf.Write(b)
}
