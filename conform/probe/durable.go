// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"reflect"
	"slices"
	"syscall"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/medium"
)

// samples is the number of samples of each input of a derived check, and
// longest is the most writes of a sequence of a durable probe.
const (
	samples = 2
	longest = 3
)

// writeSequences are the 14 sequences of one to three writes of the samples 0
// and 1. The shorter sequences come first.
var writeSequences = func() [][]int {
	var out [][]int
	for n := 1; n <= longest; n++ {
		for code := range 1 << n {
			seq := make([]int, n)
			for j := range seq {
				seq[j] = code >> (n - 1 - j) & 1
			}
			out = append(out, seq)
		}
	}
	return out
}()

// Durable are the calls of a durable writer and of its reader. Write writes
// the sample i of the writer to the subject s, and Read reads the key of the
// sample i from s. Want returns the value that the sample i writes. Key
// returns the key of the sample i. Two samples with one key replace each
// other.
type Durable[S, V any] struct {
	Write func(ctx context.Context, s S, i int) error
	Read  func(ctx context.Context, s S, i int) (V, error)
	Want  func(i int) V
	Key   func(i int) any
}

// SurvivesEveryCrashPoint runs each sequence of one to three writes of the
// samples of d and counts the persistence points of its writes. For each count
// p up to that count, it runs the sequence again with a crash in place of the
// persistence point p+1 of the writes. A count p of every point crashes the
// medium after the last write. The probe then opens the subject again over the
// medium and reads each key that a write acknowledged. It fails unless each
// read returns the value of the last acknowledged write of its key, or the
// value of a later write of the key in which the crash fell.
//
// Each run opens its subject over a new medium.Memory through
// [conform.Handle.Open]. The probe adds the crash points of each sequence to
// the points of the check.
func SurvivesEveryCrashPoint[S, V any](h *conform.Handle[S], d Durable[S, V]) {
	h.Helper()
	slot := keyed(d)
	for _, seq := range writeSequences {
		after := pointsOf(h, d, seq)
		n := after[len(after)-1]
		h.AddPoints(n + 1)
		for p := range n + 1 {
			at := crashedIn(after, p)
			s, errs := crashed(h, d, seq, p, -1)
			var allowed [samples][]V
			for j, i := range seq {
				switch {
				case errs[j] == nil:
					allowed[slot[i]] = []V{d.Want(i)}
				case j == at && allowed[slot[i]] != nil:
					allowed[slot[i]] = append(allowed[slot[i]], d.Want(i))
				}
			}
			for k, values := range allowed {
				if values == nil {
					continue
				}
				h.Engage()
				got, err := d.Read(h.Context(), s, k)
				h.Expect(spec.LostAcknowledgedWrite)
				assert.NoError(h, err, h.Statement())
				assert.Contains(h, values, got, h.Statement())
			}
		}
	}
}

// WholeAtEveryCrashPoint runs the crash points of [SurvivesEveryCrashPoint].
// Before the write in which the crash falls, it makes the medium tear the next
// write of a file to one byte, through medium.Memory.Tear. A crash after the
// last write tears nothing. The probe then opens the subject again over the
// medium and reads each key that the sequence writes. It fails unless each
// read returns a value that a write of the sequence wrote to its key, or an
// error. The probe adds the crash points of each sequence to the points of the
// check.
func WholeAtEveryCrashPoint[S, V any](h *conform.Handle[S], d Durable[S, V]) {
	h.Helper()
	slot := keyed(d)
	for _, seq := range writeSequences {
		after := pointsOf(h, d, seq)
		n := after[len(after)-1]
		h.AddPoints(n + 1)
		values := writtenOf(d, seq, slot)
		for p := range n + 1 {
			s, _ := crashed(h, d, seq, p, crashedIn(after, p))
			for k, written := range values {
				if written == nil {
					continue
				}
				got, err := d.Read(h.Context(), s, k)
				if err != nil {
					continue
				}
				h.Engage()
				h.Expect(spec.TornRecord)
				assert.Contains(h, written, got, h.Statement())
			}
		}
	}
}

// ReportsEachFlippedBit runs each sequence of one to three writes of the
// samples of d, crashes the medium and lists the bytes of its files. For each
// byte, it runs the sequence again on a new medium and crashes it. It flips
// the lowest bit of the byte through medium.Memory.Corrupt, opens the subject
// again over the medium and reads each key that a write acknowledged. It fails
// unless each read returns the value of the last acknowledged write of its
// key, or an error that wraps corrupt beside the zero value. The probe adds the
// flipped bytes to the points of the check.
func ReportsEachFlippedBit[S, V any](h *conform.Handle[S], d Durable[S, V], corrupt error) {
	h.Helper()
	slot := keyed(d)
	var zero V
	for _, seq := range writeSequences {
		listed, _ := wrote(h, d, seq)
		for _, f := range filesOf(listed) {
			h.AddPoints(int(f.size))
			for offset := range f.size {
				m, errs := wrote(h, d, seq)
				m.Corrupt(f.name, offset)
				s := h.Open(m)
				for _, i := range lastOf(seq, errs, slot) {
					if i < 0 {
						continue
					}
					got, err := d.Read(h.Context(), s, i)
					if errors.Is(err, corrupt) {
						h.Engage()
					}
					h.Expect(spec.UnreportedCorruption)
					if err != nil {
						assert.ErrorIs(h, err, corrupt, h.Statement())
						assert.Equal(h, got, zero, h.Statement())
						continue
					}
					assert.Equal(h, got, d.Want(i), h.Statement())
				}
			}
		}
	}
}

// RefusesOnAFullMedium runs each sequence of one to three writes of the
// samples of d and counts the bytes of the writes of its last write, through
// medium.Memory.Written. For each count below those bytes, it runs the
// sequence again on a new medium and reads the key of each sample before the
// last write. It fills the medium after that count of bytes through
// medium.Memory.Fill, writes the last sample, lifts the fill and reads the key
// of each sample again. It fails unless the last write fails and each read
// returns the same value as before the last write. The probe adds the counts
// to the points of the check.
func RefusesOnAFullMedium[S, V any](h *conform.Handle[S], d Durable[S, V]) {
	h.Helper()
	for _, seq := range writeSequences {
		first, last := seq[:len(seq)-1], seq[len(seq)-1]
		m, s := opened(h)
		writeEach(h, d, s, first)
		before := m.Written()
		_ = d.Write(h.Context(), s, last)
		size := m.Written() - before
		if size == 0 {
			continue
		}
		h.Engage()
		h.AddPoints(int(size))
		for limit := range size {
			m, s := opened(h)
			writeEach(h, d, s, first)
			was := readEach(h, d, s)
			m.Fill(limit)
			err := d.Write(h.Context(), s, last)
			m.Fill(-1)
			is := readEach(h, d, s)
			h.Expect(spec.HalfWrittenOnFull)
			assert.HasError(h, err, h.Statement())
			assert.Equal(h, is, was, h.Statement())
		}
	}
}

// Unsynced returns a medium over m whose syncs do not persist anything.
// SyncDir and the Sync of each file that it opens return nil. Every other call
// goes to m. The planted defect of the claim synced-writes-survive opens its
// subject over it.
func Unsynced(m medium.FS) medium.FS { return unsynced{FS: m} }

// unsynced is the medium of [Unsynced].
type unsynced struct{ medium.FS }

// OpenFile opens the file name of the medium and returns it with a Sync that
// does not persist anything.
func (u unsynced) OpenFile(name string, flag int, perm fs.FileMode) (medium.File, error) {
	return openWith(u.FS, name, flag, perm, func(f medium.File) medium.File { return unsyncedFile{File: f} })
}

// SyncDir returns nil and does not persist anything.
func (unsynced) SyncDir(string) error { return nil }

// unsyncedFile is a file of an [Unsynced] medium.
type unsyncedFile struct{ medium.File }

// Sync returns nil and does not persist anything.
func (unsyncedFile) Sync() error { return nil }

// Unfilled returns a medium over m that hides a full medium. When m refuses a
// write of a file that it opens with an error that wraps syscall.ENOSPC, the
// write returns the length of its data and no error. Every other call goes to
// m. The planted defect of the claim full-medium-is-reported opens its subject
// over it.
func Unfilled(m medium.FS) medium.FS { return unfilled{FS: m} }

// unfilled is the medium of [Unfilled].
type unfilled struct{ medium.FS }

// OpenFile opens the file name of the medium and returns it with writes that
// report every byte as written when the medium is full.
func (u unfilled) OpenFile(name string, flag int, perm fs.FileMode) (medium.File, error) {
	return openWith(u.FS, name, flag, perm, func(f medium.File) medium.File { return unfilledFile{File: f} })
}

// unfilledFile is a file of an [Unfilled] medium.
type unfilledFile struct{ medium.File }

// Write writes p and reports every byte of p as written when the medium is
// full.
func (f unfilledFile) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	return unrefused(n, err, len(p))
}

// WriteAt writes p at the offset off and reports every byte of p as written
// when the medium is full.
func (f unfilledFile) WriteAt(p []byte, off int64) (int, error) {
	n, err := f.File.WriteAt(p, off)
	return unrefused(n, err, len(p))
}

// unrefused returns the count n and the error err of a write of size bytes.
// It returns size and no error when err wraps syscall.ENOSPC.
func unrefused(n int, err error, size int) (int, error) {
	if errors.Is(err, syscall.ENOSPC) {
		return size, nil
	}
	return n, err
}

// openWith opens the file name of m with flag and perm and returns the file
// that wrap returns for it. It returns the error of m for an open that m
// refuses.
func openWith(
	m medium.FS, name string, flag int, perm fs.FileMode, wrap func(f medium.File) medium.File,
) (medium.File, error) {
	f, err := m.OpenFile(name, flag, perm)
	if err != nil {
		//nolint:wrapcheck // the subject matches the error of the medium with errors.Is as m returns it
		return nil, err
	}
	return wrap(f), nil
}

// file is a file of a medium: its name and its size in bytes.
type file struct {
	name string
	size int64
}

// keyed returns the first sample with the key of each sample of d. Two
// samples with one key share one index.
func keyed[S, V any](d Durable[S, V]) [samples]int {
	slot := [samples]int{0, 1}
	if reflect.DeepEqual(d.Key(0), d.Key(1)) {
		slot[1] = 0
	}
	return slot
}

// opened returns a new medium.Memory and a subject that h opens over it.
func opened[S any](h *conform.Handle[S]) (*medium.Memory, S) {
	m := medium.NewMemory()
	return m, h.Open(m)
}

// writeEach writes the samples of seq to s in their order and returns the
// error of each write.
func writeEach[S, V any](h *conform.Handle[S], d Durable[S, V], s S, seq []int) []error {
	errs := make([]error, len(seq))
	for j, i := range seq {
		errs[j] = d.Write(h.Context(), s, i)
	}
	return errs
}

// pointsOf writes seq to a subject that h opens over a new medium and returns
// the count of the persistence points of the writes after each write. The
// points of the open do not count.
func pointsOf[S, V any](h *conform.Handle[S], d Durable[S, V], seq []int) []int {
	m, s := opened(h)
	base := m.Points()
	after := make([]int, len(seq))
	for j, i := range seq {
		_ = d.Write(h.Context(), s, i)
		after[j] = m.Points() - base
	}
	return after
}

// crashedIn returns the position of the write in which a crash in place of the
// persistence point p+1 of the writes falls. The slice after counts the points
// of the writes after each write. crashedIn returns -1 for a crash after the
// last write.
func crashedIn(after []int, p int) int {
	return slices.IndexFunc(after, func(points int) bool { return points > p })
}

// crashed writes seq to a subject that h opens over a new medium. The medium
// crashes in place of the persistence point p+1 of the writes. Before the
// write at the position torn, crashed makes the medium tear the next write of
// a file to one byte. A negative torn tears nothing. crashed then crashes the
// medium and returns the error of each write and a subject that h opens again
// over the medium.
func crashed[S, V any](h *conform.Handle[S], d Durable[S, V], seq []int, p, torn int) (S, []error) {
	m, s := opened(h)
	m.CrashAt(m.Points() + p)
	errs := make([]error, len(seq))
	for j, i := range seq {
		if j == torn {
			m.Tear(1)
		}
		errs[j] = d.Write(h.Context(), s, i)
	}
	m.Crash()
	return h.Open(m), errs
}

// wrote writes seq to a subject that h opens over a new medium, crashes the
// medium, and returns it with the error of each write.
func wrote[S, V any](h *conform.Handle[S], d Durable[S, V], seq []int) (*medium.Memory, []error) {
	m, s := opened(h)
	errs := writeEach(h, d, s, seq)
	m.Crash()
	return m, errs
}

// lastOf returns the last sample of seq whose write returned no error for each
// key of slot, and -1 for a key without one.
func lastOf(seq []int, errs []error, slot [samples]int) [samples]int {
	last := [samples]int{-1, -1}
	for j, i := range seq {
		if errs[j] == nil {
			last[slot[i]] = i
		}
	}
	return last
}

// writtenOf returns the values that the writes of seq write under each key of
// slot, and nil for a key that seq does not write.
func writtenOf[S, V any](d Durable[S, V], seq []int, slot [samples]int) [samples][]V {
	var out [samples][]V
	for _, i := range seq {
		out[slot[i]] = append(out[slot[i]], d.Want(i))
	}
	return out
}

// readEach reads the key of each sample from s and returns the values of the
// reads in the order of the samples.
func readEach[S, V any](h *conform.Handle[S], d Durable[S, V], s S) []V {
	out := make([]V, samples)
	for i := range out {
		out[i], _ = d.Read(h.Context(), s, i)
	}
	return out
}

// filesOf returns each file of m with its size. It walks the directories from
// the root down and lists the entries of each directory in the order of their
// names.
func filesOf(m medium.FS) []file {
	var out []file
	for dirs := []string{"."}; len(dirs) > 0; dirs = dirs[1:] {
		entries, _ := m.ReadDir(dirs[0])
		for _, e := range entries {
			name := path.Join(dirs[0], e.Name())
			if e.IsDir() {
				dirs = append(dirs, name)
				continue
			}
			info, _ := e.Info()
			out = append(out, file{name: name, size: info.Size()})
		}
	}
	return out
}
