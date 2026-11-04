// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/spec"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/medium"
)

// errAbsent is the error of a get of a key without a value, and errCorrupt the
// error of a get of a value whose checksum byte differs from its sum.
var (
	errAbsent  = errors.New("probe_test: the shelf has no value of the key")
	errCorrupt = errors.New("probe_test: the value of the key is corrupt")
)

// shelf is the durable subject of the specs of the durable probes. It keeps
// the value of each key in a file that the key names, with a checksum byte
// after the value: the sum of the bytes of the value. A put writes a
// temporary file, syncs it, renames it to the file of the key and syncs the
// directory of the file, so a crash leaves the old value or the new one. dir
// is the directory of the files, and the root for an empty dir.
//
// The other fields plant the behaviours of the specs:
//
//   - refused is the key whose puts fail, and failsAfter the value whose puts
//     fail after the shelf wrote the value. memory keeps the values in memory
//     alone.
//   - direct writes the file of a key in place, and nosync writes it without a
//     sync of the file or of its directory.
//   - trailing syncs the directory once more after each put, and lazy syncs it
//     for the first put of a key alone.
//   - unchecked reads a value without its checksum byte. For a value whose
//     checksum byte differs, misreports fails the get with another error, and
//     besides returns the value beside the error.
//   - swallows returns no error for a put that fails, and noisy returns each
//     value beside an error.
//   - keepsOld copies the old value of a key to a file of its own before an
//     overwrite, and clobbers removes the files of the other keys at each put.
//   - journals appends a byte to a file at each put, and touches at each get,
//     without a sync.
type shelf struct {
	fs         medium.FS
	memory     map[string]string
	dir        string
	refused    string
	failsAfter string
	direct     bool
	nosync     bool
	trailing   bool
	lazy       bool
	unchecked  bool
	misreports bool
	besides    bool
	swallows   bool
	noisy      bool
	keepsOld   bool
	clobbers   bool
	journals   bool
	touches    bool
}

// put writes value under key.
func (s *shelf) put(key, value string) error {
	switch {
	case key == s.refused:
		return errOther
	case s.memory != nil:
		s.memory[key] = value
		return nil
	}
	err := s.write(path.Join(s.dir, key), value)
	switch {
	case s.swallows:
		return nil
	case err == nil && value == s.failsAfter:
		return errOther
	}
	return err
}

// write writes value with its checksum byte to file, as put states.
func (s *shelf) write(file, value string) error {
	_, err := s.fs.Stat(file)
	fresh := errors.Is(err, fs.ErrNotExist)
	if err := s.prepare(file, fresh); err != nil {
		return err
	}
	name := file + ".tmp"
	if s.direct {
		name = file
	}
	if err := s.store(name, append([]byte(value), checksum(value)), !s.nosync); err != nil {
		return err
	}
	if !s.direct {
		if err := s.fs.Rename(name, file); err != nil {
			return err
		}
	}
	if s.nosync || s.lazy && !fresh {
		return nil
	}
	if err := s.fs.SyncDir(path.Dir(file)); err != nil || !s.trailing {
		return err
	}
	return s.fs.SyncDir(path.Dir(file))
}

// prepare runs the steps of a put before the write of the value to file:
// the copy of keepsOld, the byte of journals and the removals of clobbers.
// fresh reports that file does not exist.
func (s *shelf) prepare(file string, fresh bool) error {
	if s.keepsOld && !fresh {
		old, err := s.raw(file)
		if err != nil {
			return err
		}
		if err := s.store(file+".old", old, true); err != nil {
			return err
		}
	}
	if s.journals {
		if err := s.appendByte("journal"); err != nil {
			return err
		}
	}
	if s.clobbers {
		entries, _ := s.fs.ReadDir(path.Dir(file))
		for _, e := range entries {
			if e.Name() != path.Base(file) {
				_ = s.fs.Remove(path.Join(path.Dir(file), e.Name()))
			}
		}
	}
	return nil
}

// get returns the value under key, as read states.
func (s *shelf) get(key string) (string, error) {
	if s.memory != nil {
		value, ok := s.memory[key]
		if !ok {
			return "", errAbsent
		}
		return value, nil
	}
	if s.touches {
		if err := s.appendByte("reads"); err != nil {
			return "", err
		}
	}
	value, err := s.read(path.Join(s.dir, key))
	if s.noisy {
		return value, errOther
	}
	return value, err
}

// read returns the value in file: errAbsent for a file that does not exist,
// and errCorrupt for an empty file and for a value whose checksum byte
// differs from its sum.
func (s *shelf) read(file string) (string, error) {
	data, err := s.raw(file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", errAbsent
	case err != nil:
		return "", err
	case len(data) == 0:
		return "", errCorrupt
	}
	value := string(data[:len(data)-1])
	switch {
	case s.unchecked || data[len(data)-1] == checksum(value):
		return value, nil
	case s.misreports:
		return "", errOther
	case s.besides:
		return value, errCorrupt
	}
	return "", errCorrupt
}

// raw returns the content of the file name.
func (s *shelf) raw(name string) ([]byte, error) {
	f, err := s.fs.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// store writes data to the file name, and syncs the file where sync is true.
func (s *shelf) store(name string, data []byte, sync bool) error {
	f, err := s.fs.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if !sync {
		return nil
	}
	return f.Sync()
}

// appendByte appends a byte to the file name without a sync.
func (s *shelf) appendByte(name string) error {
	f, err := s.fs.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write([]byte{0})
	return err
}

// checksum returns the sum of the bytes of value, which overflows.
func checksum(value string) byte {
	var total byte
	for i := range len(value) {
		total += value[i]
	}
	return total
}

// openShelf is the door Open of the shelves of the specs. It syncs the root of
// the medium, as a store syncs its directory when it opens.
func openShelf(_ *conform.Handle[*shelf], m medium.FS) *shelf {
	_ = m.SyncDir(".")
	return &shelf{fs: m}
}

// opening returns the door Open of the shelves that change sets.
func opening(change func(s *shelf)) func(*conform.Handle[*shelf], medium.FS) *shelf {
	return func(h *conform.Handle[*shelf], m medium.FS) *shelf {
		s := openShelf(h, m)
		change(s)
		return s
	}
}

// counting returns the door open, which counts each open in opens.
func counting(
	opens *atomic.Int64, open func(*conform.Handle[*shelf], medium.FS) *shelf,
) func(*conform.Handle[*shelf], medium.FS) *shelf {
	return func(h *conform.Handle[*shelf], m medium.FS) *shelf {
		opens.Add(1)
		return open(h, m)
	}
}

// onShelf returns the calls of the durable probes on a shelf, whose sample i
// puts values[i] under keys[i].
func onShelf(keys, values [2]string) probe.Durable[*shelf, string] {
	return probe.Durable[*shelf, string]{
		Write: func(_ context.Context, s *shelf, i int) error { return s.put(keys[i], values[i]) },
		Read:  func(_ context.Context, s *shelf, i int) (string, error) { return s.get(keys[i]) },
		Want:  func(i int) string { return values[i] },
		Key:   func(i int) any { return keys[i] },
	}
}

// The samples of the specs: twoKeys writes x under a and y under b, and
// sameKey writes x and y under a.
var (
	twoKeys = onShelf([2]string{"a", "b"}, [2]string{"x", "y"})
	sameKey = onShelf([2]string{"a", "a"}, [2]string{"x", "y"})
)

// survives returns the application of SurvivesEveryCrashPoint to the calls d.
func survives(d probe.Durable[*shelf, string]) func(h *conform.Handle[*shelf]) {
	return func(h *conform.Handle[*shelf]) { probe.SurvivesEveryCrashPoint(h, d) }
}

// whole returns the application of WholeAtEveryCrashPoint to the calls d.
func whole(d probe.Durable[*shelf, string]) func(h *conform.Handle[*shelf]) {
	return func(h *conform.Handle[*shelf]) { probe.WholeAtEveryCrashPoint(h, d) }
}

// flipped returns the application of ReportsEachFlippedBit to the calls d,
// with errCorrupt as the error of a corrupted value.
func flipped(d probe.Durable[*shelf, string]) func(h *conform.Handle[*shelf]) {
	return func(h *conform.Handle[*shelf]) { probe.ReportsEachFlippedBit(h, d, errCorrupt) }
}

// refuses returns the application of RefusesOnAFullMedium to the calls d.
func refuses(d probe.Durable[*shelf, string]) func(h *conform.Handle[*shelf]) {
	return func(h *conform.Handle[*shelf]) { probe.RefusesOnAFullMedium(h, d) }
}

// breakable is the machinery of the durable checks of the specs.
var breakable = []spec.MachineryID{spec.KillableProcessOrBreakableMedium}

// durableCheck returns a derived check of the probe p with apply. Its planted
// defect opens its shelves through the door bad, and fails the check with
// reason.
func durableCheck(
	p spec.ProbeID, reason spec.ReasonID, apply func(h *conform.Handle[*shelf]),
	bad func(*conform.Handle[*shelf], medium.FS) *shelf,
) conform.Check[*shelf] {
	return conform.Check[*shelf]{
		ID: "Shelf.Put/claim", Statement: "the probe holds", Claim: "claim", Probe: p, Reason: reason,
		Machineries: breakable, Apply: apply, Doors: conform.Doors[*shelf]{Open: bad},
		Defect: func(h *conform.Handle[*shelf]) *shelf { return h.Open(medium.NewMemory()) },
	}
}

// recording is a test that keeps the records that conform writes into its
// output. records joins the values of the attributes of each key, in order.
type recording struct {
	*testing.T

	mu      sync.Mutex
	records map[string]string
}

// Attr keeps the value of the attribute key, a part of the text of a record.
func (r *recording) Attr(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[key] += value
}

// pointsOf returns the points of the first check of the record of the
// application on the subject among the records of r.
func pointsOf(t *testing.T, r *recording) int {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, text := range r.records {
		var record struct {
			Application string `json:"application"`
			Checks      []struct {
				Points int `json:"points"`
			} `json:"checks"`
		}
		assert.True(t, strings.HasPrefix(key, "dokimi.suite."), "conform writes records of suites alone")
		assert.NoError(t, json.Unmarshal([]byte(text), &record), "a record is a JSON object")
		if record.Application == "subject" {
			return record.Checks[0].Points
		}
	}
	return -1
}

func TestDurable(t *testing.T) {
	t.Parallel()

	// The planted defects of the probes: a shelf over an unsynced medium, a
	// shelf that trusts a torn value, a shelf that ignores its checksum, and a
	// shelf that truncates the value of a key before it writes the new one.
	unsynced := func(h *conform.Handle[*shelf], m medium.FS) *shelf { return h.FreshOpen(probe.Unsynced(m)) }
	trusting := opening(func(s *shelf) { s.direct, s.unchecked = true, true })
	ignoring := opening(func(s *shelf) { s.unchecked = true })
	truncating := opening(func(s *shelf) { s.direct = true })
	tests := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*shelf])
		giveBad   func(*conform.Handle[*shelf], medium.FS) *shelf
		want      spec.ReasonID
	}{
		{
			name: "SurvivesEveryCrashPoint fails a shelf over an unsynced medium", giveBad: unsynced,
			giveProbe: spec.SurvivesEveryCrashPoint, giveApply: survives(twoKeys), want: spec.LostAcknowledgedWrite,
		},
		{
			name:      "SurvivesEveryCrashPoint fails a shelf that returns each value beside an error",
			giveProbe: spec.SurvivesEveryCrashPoint, giveApply: survives(twoKeys),
			giveBad: opening(func(s *shelf) { s.noisy = true }), want: spec.LostAcknowledgedWrite,
		},
		{
			name:      "SurvivesEveryCrashPoint fails a shelf that syncs the directory for a new key alone",
			giveProbe: spec.SurvivesEveryCrashPoint, giveApply: survives(sameKey),
			giveBad: opening(func(s *shelf) { s.lazy = true }), want: spec.LostAcknowledgedWrite,
		},
		{
			name:      "SurvivesEveryCrashPoint fails a shelf whose put removes the other keys",
			giveProbe: spec.SurvivesEveryCrashPoint, giveApply: survives(twoKeys),
			giveBad: opening(func(s *shelf) { s.clobbers = true }), want: spec.LostAcknowledgedWrite,
		},
		{
			name:      "SurvivesEveryCrashPoint fails a shelf that reports an error for a value that it wrote",
			giveProbe: spec.SurvivesEveryCrashPoint, giveApply: survives(sameKey),
			giveBad: opening(func(s *shelf) { s.failsAfter = "y" }), want: spec.LostAcknowledgedWrite,
		},
		{
			name: "WholeAtEveryCrashPoint fails a shelf that trusts a torn value", giveBad: trusting,
			giveProbe: spec.WholeAtEveryCrashPoint, giveApply: whole(twoKeys), want: spec.TornRecord,
		},
		{
			name: "ReportsEachFlippedBit fails a shelf that ignores its checksum", giveBad: ignoring,
			giveProbe: spec.ReportsEachFlippedBit, giveApply: flipped(twoKeys), want: spec.UnreportedCorruption,
		},
		{
			name:      "ReportsEachFlippedBit fails a shelf that reports another error for a corrupt value",
			giveProbe: spec.ReportsEachFlippedBit, giveApply: flipped(twoKeys),
			giveBad: opening(func(s *shelf) { s.misreports = true }), want: spec.UnreportedCorruption,
		},
		{
			name:      "ReportsEachFlippedBit fails a shelf that returns a corrupt value beside the error",
			giveProbe: spec.ReportsEachFlippedBit, giveApply: flipped(twoKeys),
			giveBad: opening(func(s *shelf) { s.besides = true }), want: spec.UnreportedCorruption,
		},
		{
			name: "RefusesOnAFullMedium fails a shelf that truncates a value before it writes", giveBad: truncating,
			giveProbe: spec.RefusesOnAFullMedium, giveApply: refuses(twoKeys), want: spec.HalfWrittenOnFull,
		},
		{
			name:      "RefusesOnAFullMedium fails a shelf that swallows the error of a full medium",
			giveProbe: spec.RefusesOnAFullMedium, giveApply: refuses(twoKeys),
			giveBad: opening(func(s *shelf) { s.swallows = true }), want: spec.HalfWrittenOnFull,
		},
		{
			name:      "RefusesOnAFullMedium fails a shelf over a medium that hides a full medium",
			giveProbe: spec.RefusesOnAFullMedium, giveApply: refuses(twoKeys), want: spec.HalfWrittenOnFull,
			giveBad: func(h *conform.Handle[*shelf], m medium.FS) *shelf { return h.FreshOpen(probe.Unfilled(m)) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			durableCheck(tt.giveProbe, tt.want, tt.giveApply, tt.giveBad).Run(t, nil, conform.Durable(openShelf))
		})
	}

	passes := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*shelf])
		giveOpen  func(*conform.Handle[*shelf], medium.FS) *shelf
	}{
		{
			name: "SurvivesEveryCrashPoint passes a shelf whose interrupted put persists", giveApply: survives(sameKey),
			giveProbe: spec.SurvivesEveryCrashPoint, giveOpen: opening(func(s *shelf) { s.trailing = true }),
		},
		{
			name:      "SurvivesEveryCrashPoint passes a shelf that refuses the puts of the second sample",
			giveApply: survives(twoKeys), giveProbe: spec.SurvivesEveryCrashPoint,
			giveOpen: opening(func(s *shelf) { s.refused = "b" }),
		},
		{
			name: "WholeAtEveryCrashPoint passes a shelf whose two samples put one key", giveApply: whole(sameKey),
			giveProbe: spec.WholeAtEveryCrashPoint, giveOpen: openShelf,
		},
		{
			name: "ReportsEachFlippedBit passes a shelf whose two samples put one key", giveApply: flipped(sameKey),
			giveProbe: spec.ReportsEachFlippedBit, giveOpen: openShelf,
		},
		{
			name:      "ReportsEachFlippedBit passes a shelf that refuses the puts of the second sample",
			giveApply: flipped(twoKeys), giveProbe: spec.ReportsEachFlippedBit,
			giveOpen: opening(func(s *shelf) { s.refused = "b" }),
		},
		{
			name:      "ReportsEachFlippedBit passes a shelf whose files are in a directory",
			giveApply: flipped(twoKeys), giveProbe: spec.ReportsEachFlippedBit,
			giveOpen: opening(func(s *shelf) {
				_ = s.fs.MkdirAll("data", 0o700)
				_ = s.fs.SyncDir(".")
				s.dir = "data"
			}),
		},
		{
			name: "RefusesOnAFullMedium passes a shelf whose two samples put one key", giveApply: refuses(sameKey),
			giveProbe: spec.RefusesOnAFullMedium, giveOpen: openShelf,
		},
		{
			name: "RefusesOnAFullMedium passes a shelf that writes a file at each get", giveApply: refuses(twoKeys),
			giveProbe: spec.RefusesOnAFullMedium, giveOpen: opening(func(s *shelf) { s.touches = true }),
		},
	}
	for _, tt := range passes {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conform.Check[*shelf]{
				ID: "Shelf.Put/claim", Statement: "the probe holds", Probe: tt.giveProbe, Apply: tt.giveApply,
			}.Run(t, nil, conform.Durable(tt.giveOpen))
		})
	}

	ends := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*shelf])
		give      func(*conform.Handle[*shelf], medium.FS) *shelf
	}{
		{
			name: "SurvivesEveryCrashPoint is vacuous on puts that fail", giveProbe: spec.SurvivesEveryCrashPoint,
			giveApply: survives(sameKey), give: opening(func(s *shelf) { s.refused = "a" }),
		},
		{
			name: "WholeAtEveryCrashPoint is vacuous on gets that fail", giveProbe: spec.WholeAtEveryCrashPoint,
			giveApply: whole(twoKeys), give: opening(func(s *shelf) { s.noisy = true }),
		},
		{
			name:      "WholeAtEveryCrashPoint is vacuous on a shelf that never syncs",
			giveProbe: spec.WholeAtEveryCrashPoint, giveApply: whole(twoKeys), give: opening(func(s *shelf) {
				for _, key := range []string{"a", "b"} {
					if f, err := s.fs.OpenFile(key, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
						_ = f.Close()
					}
				}
				_ = s.fs.SyncDir(".")
				s.direct, s.nosync, s.unchecked = true, true, true
			}),
		},
		{
			name: "ReportsEachFlippedBit is vacuous on a shelf in memory", giveProbe: spec.ReportsEachFlippedBit,
			giveApply: flipped(twoKeys), give: opening(func(s *shelf) { s.memory = map[string]string{} }),
		},
		{
			name: "RefusesOnAFullMedium is vacuous on a shelf in memory", giveProbe: spec.RefusesOnAFullMedium,
			giveApply: refuses(twoKeys), give: opening(func(s *shelf) { s.memory = map[string]string{} }),
		},
	}
	for _, tt := range ends {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			vacuous(t, tt.giveProbe, tt.giveApply, nil, nil, conform.Durable(tt.give))
		})
	}

	t.Run("opens a subject over a new medium for each run of a sequence", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveProbe spec.ProbeID
			giveApply func(h *conform.Handle[*shelf])
			giveOpen  func(*conform.Handle[*shelf], medium.FS) *shelf
			wantOpens int64
		}{
			{
				name: "and again after each crash of SurvivesEveryCrashPoint", giveProbe: spec.SurvivesEveryCrashPoint,
				giveApply: survives(twoKeys), giveOpen: openShelf, wantOpens: 14 + 2*82,
			},
			{
				name: "and again after each crash of WholeAtEveryCrashPoint", giveProbe: spec.WholeAtEveryCrashPoint,
				giveApply: whole(twoKeys), giveOpen: openShelf, wantOpens: 14 + 2*82,
			},
			{
				name: "and again after each flip of ReportsEachFlippedBit", giveProbe: spec.ReportsEachFlippedBit,
				giveApply: flipped(twoKeys), giveOpen: openShelf, wantOpens: 14 + 2*44,
			},
			{
				name:      "and again after each flip of a durable byte of a shelf with an unsynced journal",
				giveProbe: spec.ReportsEachFlippedBit, giveApply: flipped(twoKeys),
				giveOpen: opening(func(s *shelf) { s.journals = true }), wantOpens: 14 + 2*44,
			},
			{
				name: "of RefusesOnAFullMedium", giveProbe: spec.RefusesOnAFullMedium,
				giveApply: refuses(twoKeys), giveOpen: openShelf, wantOpens: 14 + 28,
			},
			{
				name:      "of RefusesOnAFullMedium for each byte of a last put that keeps the old value",
				giveProbe: spec.RefusesOnAFullMedium, giveApply: refuses(twoKeys),
				giveOpen: opening(func(s *shelf) { s.keepsOld = true }), wantOpens: 14 + 44,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var opens atomic.Int64
				conform.Check[*shelf]{
					ID: "Shelf.Put/claim", Statement: "the probe holds", Probe: tt.giveProbe,
					Apply: func(h *conform.Handle[*shelf]) {
						tt.giveApply(h)
						assert.Equal(h, opens.Load(), tt.wantOpens, h.Statement())
					},
				}.Run(t, nil, conform.Durable(counting(&opens, tt.giveOpen)))
			})
		}
	})

	t.Run("adds the points that it enumerates to the record of the check", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			giveProbe  spec.ProbeID
			giveApply  func(h *conform.Handle[*shelf])
			giveBad    func(*conform.Handle[*shelf], medium.FS) *shelf
			giveReason spec.ReasonID
			want       int
		}{
			{
				name:      "82 crash points of SurvivesEveryCrashPoint for two syncs of each put",
				giveProbe: spec.SurvivesEveryCrashPoint, giveApply: survives(twoKeys), giveBad: unsynced,
				giveReason: spec.LostAcknowledgedWrite, want: 82,
			},
			{
				name:      "82 crash points of WholeAtEveryCrashPoint for two syncs of each put",
				giveProbe: spec.WholeAtEveryCrashPoint, giveApply: whole(twoKeys), giveBad: trusting,
				giveReason: spec.TornRecord, want: 82,
			},
			{
				name:      "44 flipped bytes of ReportsEachFlippedBit for files of two bytes",
				giveProbe: spec.ReportsEachFlippedBit, giveApply: flipped(twoKeys), giveBad: ignoring,
				giveReason: spec.UnreportedCorruption, want: 44,
			},
			{
				name:      "28 limits of RefusesOnAFullMedium for puts of two bytes",
				giveProbe: spec.RefusesOnAFullMedium, giveApply: refuses(twoKeys), giveBad: truncating,
				giveReason: spec.HalfWrittenOnFull, want: 28,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				r := &recording{T: t, records: map[string]string{}}
				t.Cleanup(func() {
					assert.Equal(t, pointsOf(t, r), tt.want, "the record of the check has the points of the probe")
				})
				conform.Run(r, conform.Suite[*shelf]{
					Declaration: "Shelf",
					Checks: []conform.Check[*shelf]{
						durableCheck(tt.giveProbe, tt.giveReason, tt.giveApply, tt.giveBad),
					},
				}, nil, conform.Durable(openShelf))
			})
		}
	})

	t.Run("Unsynced", func(t *testing.T) {
		t.Parallel()

		t.Run("persists nothing at a sync of a file", func(t *testing.T) {
			t.Parallel()

			m := medium.NewMemory()
			created, err := m.OpenFile("f", os.O_CREATE|os.O_WRONLY, 0o600)
			assert.NoError(t, err, "the medium creates the file")
			assert.NoError(t, created.Close(), "the medium closes the file")
			assert.NoError(t, m.SyncDir("."), "the medium syncs the entry of the file")
			f, err := probe.Unsynced(m).OpenFile("f", os.O_WRONLY, 0)
			assert.NoError(t, err, "the unsynced medium opens the file of the medium")
			_, err = f.Write([]byte("data"))
			assert.NoError(t, err, "the unsynced medium writes to the file")

			assert.NoError(t, f.Sync(), "the sync of a file of the unsynced medium returns nil")
			assert.Equal(t, m.Points(), 1, "the sync of the file is no persistence point of the medium")
			m.Crash()
			info, err := m.Stat("f")
			assert.NoError(t, err, "the file survives the crash")
			assert.Equal(t, info.Size(), int64(0), "the crash drops the data that the sync did not persist")
		})

		t.Run("persists nothing at a sync of a directory", func(t *testing.T) {
			t.Parallel()

			m := medium.NewMemory()
			u := probe.Unsynced(m)
			f, err := u.OpenFile("f", os.O_CREATE|os.O_WRONLY, 0o600)
			assert.NoError(t, err, "the unsynced medium creates the file")
			assert.NoError(t, f.Close(), "the unsynced medium closes the file")

			assert.NoError(t, u.SyncDir("."), "the sync of a directory of the unsynced medium returns nil")
			assert.Equal(t, m.Points(), 0, "the sync of the directory is no persistence point of the medium")
			m.Crash()
			_, err = m.Stat("f")
			assert.ErrorIs(t, err, fs.ErrNotExist, "the crash drops the entry that the sync did not persist")
		})

		t.Run("returns the error of an open that the medium refuses", func(t *testing.T) {
			t.Parallel()

			f, err := probe.Unsynced(medium.NewMemory()).OpenFile("missing", os.O_RDONLY, 0)

			assert.ErrorIs(t, err, fs.ErrNotExist, "the unsynced medium returns the error of the medium")
			assert.Nil(t, f, "the unsynced medium returns no file beside the error")
		})
	})

	t.Run("Unfilled", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			write func(f medium.File, data []byte) (int, error)
		}{
			{name: "Write", write: medium.File.Write},
			{name: "WriteAt", write: func(f medium.File, data []byte) (int, error) { return f.WriteAt(data, 0) }},
		}
		for _, tt := range tests {
			t.Run("reports each byte of a "+tt.name+" as written where the medium is full", func(t *testing.T) {
				t.Parallel()

				m := medium.NewMemory()
				f, err := probe.Unfilled(m).OpenFile("f", os.O_CREATE|os.O_WRONLY, 0o600)
				assert.NoError(t, err, "the unfilled medium creates the file")
				m.Fill(2)

				n, err := tt.write(f, []byte("data"))

				assert.NoError(t, err, "the write of the unfilled medium returns no error")
				assert.Equal(t, n, 4, "the write of the unfilled medium reports each byte as written")
				assert.Equal(t, m.Written(), int64(2), "the medium writes the bytes that fit")
			})

			t.Run("returns the error of any other "+tt.name+" that the medium refuses", func(t *testing.T) {
				t.Parallel()

				f, err := probe.Unfilled(medium.NewMemory()).OpenFile("f", os.O_CREATE|os.O_WRONLY, 0o600)
				assert.NoError(t, err, "the unfilled medium creates the file")
				assert.NoError(t, f.Close(), "the unfilled medium closes the file")

				n, err := tt.write(f, []byte("data"))

				assert.ErrorIs(t, err, fs.ErrClosed, "the write returns the error of the medium")
				assert.Equal(t, n, 0, "the write returns the count of the medium")
			})
		}

		t.Run("returns the error of an open that the medium refuses", func(t *testing.T) {
			t.Parallel()

			f, err := probe.Unfilled(medium.NewMemory()).OpenFile("missing", os.O_RDONLY, 0)

			assert.ErrorIs(t, err, fs.ErrNotExist, "the unfilled medium returns the error of the medium")
			assert.Nil(t, f, "the unfilled medium returns no file beside the error")
		})
	})
}
