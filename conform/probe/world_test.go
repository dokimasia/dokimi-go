// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"runtime/coverage"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
	"go.dokimi.dev/spec"
)

// kv is the subject of the specs of the world: values by key over a medium.
// Put writes the record of the value to a file of its own, syncs it, renames
// it over the file of the key and syncs the directory, so a write that returns
// survives every crash, and the rename orders two writes of one key. A record
// is a reserved byte that Get ignores, the checksum of the rest, the wall time
// of the write and the value. Get fails with errCorrupt for a record whose
// checksum does not match.
//
// A kv over the environment of an incarnation calls the dependency audit at
// the start of each Put and writes the wall time of its clock, and an
// audit-last kv calls audit at the end of the write. A windowed kv sleeps for
// a millisecond of its clock before each rename, and an unsynced kv writes the
// file of the key in place without a sync. A waiting kv waits on its
// environment until a millisecond of its clock has passed before each Put. A
// kv blocks only through its medium, its dependency, its clock, its
// environment and host.Park, as a subject of the option Tasks does. A kv that
// blocks parks each call until its context ends, and a kv that lingers parks
// each Put until its context ends and then writes. A kv that invents returns
// the value z for a key without a value, and a kv with a counter returns the
// next number of the counter from each Get.
type kv struct {
	fs        medium.FS
	env       *host.Env
	clock     *host.Clock
	audit     *host.Dependency
	counter   *atomic.Int64
	writes    atomic.Int64
	blocks    bool
	lingers   bool
	invents   bool
	windowed  bool
	unsynced  bool
	waits     bool
	auditLast bool
}

// errInvalid is the error of a Create of a value that the store refuses.
var errInvalid = errors.New("probe_test: the store refuses the value")

// openStore opens a store over the medium m.
func openStore(_ *conform.Handle[*kv], m medium.FS) *kv { return &kv{fs: m} }

// openHosted opens a store over the disk, the clock and the dependency audit
// of the environment env.
func openHosted(h *conform.Handle[*kv], env *host.Env) *kv {
	s := openStore(h, env.Disk())
	s.env, s.clock, s.audit = env, env.Clock(), env.Dependency("audit")
	return s
}

// Put stores the value under the key.
func (s *kv) Put(ctx context.Context, key, value string) error {
	if s.blocks {
		return host.Park(ctx)
	}
	if s.lingers {
		_ = host.Park(ctx)
	}
	if s.waits {
		until := s.clock.Monotonic() + time.Millisecond
		s.env.Wait(func() bool { return s.clock.Monotonic() >= until || ctx.Err() != nil })
	}
	var at int64
	if s.audit != nil && !s.auditLast {
		if err := s.audit.Call("Put"); err != nil {
			return err
		}
		at = s.clock.Wall().UnixNano()
	}
	if err := s.write(key, value, at); err != nil || !s.auditLast {
		return err
	}
	return s.audit.Call("Put")
}

// write writes the record of the value under the key, with the wall time at.
func (s *kv) write(key, value string, at int64) error {
	rest := append(binary.AppendVarint(nil, at), value...)
	record := append(binary.BigEndian.AppendUint32([]byte{0}, crc32.ChecksumIEEE(rest)), rest...)
	name := key
	if !s.unsynced {
		name = key + "." + strconv.FormatInt(s.writes.Add(1), 10)
	}
	f, err := s.fs.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.Write(record); err == nil && !s.unsynced {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil || s.unsynced {
		return err
	}
	if s.windowed {
		s.clock.Sleep(time.Millisecond)
	}
	if err := s.fs.Rename(name, key); err != nil {
		return err
	}
	return s.fs.SyncDir(".")
}

// Create stores the value under the key c.<value>, and returns the key. It
// fails with errInvalid for the value y.
func (s *kv) Create(ctx context.Context, value string) (string, error) {
	key := "c." + value
	if value == "y" {
		return key, errInvalid
	}
	return key, s.Put(ctx, key, value)
}

// Get returns the value of the key, and an empty value for a key without one.
func (s *kv) Get(ctx context.Context, key string) (string, error) {
	if s.blocks {
		return "", host.Park(ctx)
	}
	if s.counter != nil {
		return strconv.FormatInt(s.counter.Add(1), 10), nil
	}
	f, err := s.fs.OpenFile(key, os.O_RDONLY, 0)
	if errors.Is(err, fs.ErrNotExist) && s.invents {
		return "z", nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	record, err := io.ReadAll(f)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if len(record) < 5 || crc32.ChecksumIEEE(record[5:]) != binary.BigEndian.Uint32(record[1:]) {
		return "", errCorrupt
	}
	_, n := binary.Varint(record[5:])
	return string(record[5+n:]), nil
}

// Now returns the wall time and the monotonic time of the clock of the store.
func (s *kv) Now() (time.Time, time.Duration) {
	return s.clock.Wall(), s.clock.Monotonic()
}

// storeOperations returns the operations Put, Create and Get of the world of
// a store, over a pool of the keys a and b that takes each key that Create
// returns.
func storeOperations(c *prop.Case) []probe.Operation[*kv] {
	keys := probe.NewPool(c, prop.List(prop.SampledFrom("a", "b"), prop.Unique(), prop.MinSize(1), prop.MaxSize(2)),
		"keys")
	args := func(input any) []any { return input.([]any) }
	key := func(input any) []any { return input.([]any)[:1] }
	return []probe.Operation[*kv]{
		{
			Name: "Put",
			Input: func(c *prop.Case) any {
				return []any{keys.Pick(c, "put.key"), c.Draw(prop.SampledFrom("x", "y"), "put.value")}
			},
			Args: args, Keys: key,
			Call: func(ctx context.Context, s *kv, input any) (conform.Output, error) {
				in := input.([]any)
				err := s.Put(ctx, in[0].(string), in[1].(string))
				return conform.OutputOf(err), err
			},
		},
		{
			Name:  "Create",
			Input: func(c *prop.Case) any { return []any{c.Draw(prop.SampledFrom("x", "y"), "create.value")} },
			Args:  args,
			Keys:  func(input any) []any { return []any{"c." + input.([]any)[0].(string)} },
			Call: func(ctx context.Context, s *kv, input any) (conform.Output, error) {
				created, err := s.Create(ctx, input.([]any)[0].(string))
				return conform.OutputOf(err, created), err
			},
			Produced: func(out conform.Output) { keys.Add(out.Values[0].(string)) },
		},
		{
			Name:  "Get",
			Input: func(c *prop.Case) any { return []any{keys.Pick(c, "get.key")} },
			Args:  args, Keys: key,
			Call: func(ctx context.Context, s *kv, input any) (conform.Output, error) {
				v, err := s.Get(ctx, input.([]any)[0].(string))
				return conform.OutputOf(err, v), err
			},
		},
	}
}

// nowOperations returns the operation Now of the world of a store over the
// environment of an incarnation, whose output is the wall time and the
// monotonic time of the clock of the store.
func nowOperations(*prop.Case) []probe.Operation[*kv] {
	none := func(any) []any { return nil }
	return []probe.Operation[*kv]{{
		Name: "Now", Input: func(*prop.Case) any { return nil }, Args: none, Keys: none,
		Call: func(_ context.Context, s *kv, _ any) (conform.Output, error) {
			wall, mono := s.Now()
			return conform.OutputOf(nil, wall, mono), nil
		},
	}}
}

// storeReference applies an operation of the history to the reference r.
func storeReference(r *kv) func(ctx context.Context, operation string, args []any) conform.Output {
	return func(ctx context.Context, operation string, args []any) conform.Output {
		switch operation {
		case "Put":
			return conform.OutputOf(r.Put(ctx, args[0].(string), args[1].(string)))
		case "Create":
			created, err := r.Create(ctx, args[0].(string))
			return conform.OutputOf(err, created)
		}
		v, err := r.Get(ctx, args[0].(string))
		return conform.OutputOf(err, v)
	}
}

// storeWorld is the world of a store with two clients, sealedWorld the world
// of a store that reports a corrupted record with errCorrupt, and nowWorld
// the world of the clock of a store with one client.
var (
	storeWorld  = probe.World[*kv]{Operations: storeOperations, Reference: storeReference, Clients: 2}
	sealedWorld = probe.World[*kv]{
		Operations: storeOperations, Reference: storeReference, Clients: 2, Corrupt: errCorrupt,
	}
	nowWorld = probe.World[*kv]{Operations: nowOperations, Clients: 1}
)

// newStore returns a store over a new medium, and newReference the reference
// of a store.
func newStore(*conform.Handle[*kv]) *kv     { return &kv{fs: medium.NewMemory()} }
func newReference(*conform.Handle[*kv]) *kv { return &kv{fs: medium.NewMemory()} }

// worldCheck returns the check of a world of the probe p with the claim, the
// reason and the property, whose planted defect opens each incarnation
// through the doors.
func worldCheck(
	p spec.ProbeID, claim spec.ClaimID, reason spec.ReasonID, property func(h *conform.Handle[*kv], c *prop.Case),
	doors conform.Doors[*kv],
) conform.Check[*kv] {
	return conform.Check[*kv]{
		ID: "Store.Put/world", Statement: "the world of the store holds", Claim: claim, Probe: p, Reason: reason,
		Around: conform.Bubble, Options: probe.WorldOptions[*kv], Property: property, Defect: newStore,
		Doors: doors,
	}
}

// linearizableOver returns the property of FaultyWorldHistory over the world
// w with the options of its history checks, and completesOver the property of
// WorldRecovery over w.
func linearizableOver(w probe.World[*kv], opts ...history.Option) func(h *conform.Handle[*kv], c *prop.Case) {
	return func(h *conform.Handle[*kv], c *prop.Case) { probe.FaultyWorldHistory(h, c, w, opts...) }
}

func completesOver(w probe.World[*kv]) func(h *conform.Handle[*kv], c *prop.Case) {
	return func(h *conform.Handle[*kv], c *prop.Case) { probe.WorldRecovery(h, c, w) }
}

// linearizableCheck returns the check of FaultyWorldHistory over the world w,
// whose planted defect opens each incarnation through the doors.
func linearizableCheck(w probe.World[*kv], doors conform.Doors[*kv]) conform.Check[*kv] {
	return worldCheck(spec.FaultyWorldHistory, spec.LinearizableUnderFaults, spec.NotLinearizableUnderFaults,
		linearizableOver(w), doors)
}

// amnesiac is the door of the defect form amnesiac-recovery: each restart of
// a world opens the store over an empty disk.
var amnesiac = conform.Doors[*kv]{Host: probe.Amnesiac[*kv]}

// forgetting is the door of a planted defect that opens a store of its own
// over a new medium at each incarnation, apart from the door of the
// application. A goroutine that the door of the application leaks leaves the
// defect alone.
var forgetting = conform.Doors[*kv]{Host: func(h *conform.Handle[*kv], _ *host.Env) *kv {
	return openStore(h, medium.NewMemory())
}}

// syncedWrites is the durable check of the store that the isolation of a
// world check applies to the planted defect of the world. The durable probe
// opens the store again after each crash of its medium, without a world.
var syncedWrites = conform.Check[*kv]{
	ID: "Store.Put/synced-writes-survive", Statement: "Get returns each value that Put wrote before a crash",
	Claim: spec.SyncedWritesSurvive, Probe: spec.SurvivesEveryCrashPoint, Reason: spec.LostAcknowledgedWrite,
	Machineries: []spec.MachineryID{spec.KillableProcessOrBreakableMedium},
	Apply: func(h *conform.Handle[*kv]) {
		probe.SurvivesEveryCrashPoint(h, probe.Durable[*kv, string]{
			Write: func(ctx context.Context, s *kv, i int) error { return s.Put(ctx, "k"+strconv.Itoa(i), "v") },
			Read:  func(ctx context.Context, s *kv, i int) (string, error) { return s.Get(ctx, "k"+strconv.Itoa(i)) },
			Want:  func(int) string { return "v" },
			Key:   func(i int) any { return "k" + strconv.Itoa(i) },
		})
	},
	Doors:  conform.Doors[*kv]{Open: openUnsynced},
	Defect: func(h *conform.Handle[*kv]) *kv { return h.Open(medium.NewMemory()) },
}

// inventive is the door of a store that invents a value for a key without one,
// a defect that the check of the first call of a case finds.
var inventive = conform.Doors[*kv]{Open: func(h *conform.Handle[*kv], m medium.FS) *kv {
	s := openStore(h, m)
	s.invents = true
	return s
}}

// blocking is the door of the defect form blocks-after-recovery: a store whose
// calls park until their context ends from the second incarnation of its host
// on.
var blocking = conform.Doors[*kv]{Host: func(h *conform.Handle[*kv], env *host.Env) *kv {
	s := h.FreshHost(env)
	s.blocks = env.Incarnation() > 1
	return s
}}

// The doors of the stores of the scripted cases: a windowed store over the
// environment of an incarnation, an unsynced store, and a store whose calls
// park until their context ends.
func openWindowed(h *conform.Handle[*kv], env *host.Env) *kv {
	s := openHosted(h, env)
	s.windowed = true
	return s
}

func openUnsynced(h *conform.Handle[*kv], m medium.FS) *kv {
	s := openStore(h, m)
	s.unsynced = true
	return s
}

func openParked(h *conform.Handle[*kv], m medium.FS) *kv {
	s := openStore(h, m)
	s.blocks = true
	return s
}

// sleepy returns the door of a store over the environment of an incarnation
// whose open sleeps for d of the clock of the host.
func sleepy(d time.Duration) func(h *conform.Handle[*kv], env *host.Env) *kv {
	return func(h *conform.Handle[*kv], env *host.Env) *kv {
		env.Clock().Sleep(d)
		return openHosted(h, env)
	}
}

// leakyOpen opens a store over the medium m, and starts a goroutine that
// waits for the end of the check, so the goroutine ignores the lifetime of
// its incarnation.
func leakyOpen(h *conform.Handle[*kv], m medium.FS) *kv {
	stop := make(chan struct{})
	h.Cleanup(func() { close(stop) })
	go func() { <-stop }()
	return openStore(h, m)
}

// childEnv is the environment variable that names the test whose child
// process runs a suite of a world.
const childEnv = "PROBE_TEST_CHILD"

// attrLine matches a line of an attribute of a suite record in the output of
// go test -v: the name of the test, the key and the value.
var attrLine = regexp.MustCompile(`=== ATTR  (\S+) (dokimi\.suite\.\d+) (.*)$`)

// metaWritten writes the coverage meta-data of the test binary once, into the
// directory of its coverage, before the first child process runs.
var metaWritten sync.Once

// inChild runs the check over the store in a suite of its own, in the child
// process of the test t, through the options opts.
func inChild(t *testing.T, check conform.Check[*kv], opts ...conform.Option[*kv]) {
	t.Helper()
	suite := conform.Suite[*kv]{
		Declaration: "Store", Package: "example.com/store",
		Generator: conform.Generator{Name: "dokimi", Version: "0.1.0"}, Checks: []conform.Check[*kv]{check},
	}
	conform.Run(t, suite, newStore, append(opts, conform.Reference(newReference))...)
}

// worldOf runs the test of t alone in a child process of the test binary,
// with childEnv set to the name of the test and the seed 1. It returns the
// world of the check of the subject record that the child writes, and the
// number of the cases of the check. The child runs in a temporary directory,
// so the store of a failing property run writes no file into the package. In a
// run with coverage, the child writes its coverage where the parent writes its
// own.
func worldOf(t *testing.T) (*spec.WorldRecord, int) {
	t.Helper()
	binary, err := os.Executable()
	assert.NoError(t, err, "the test binary has a path")
	levels := strings.Split(t.Name(), "/")
	for i, level := range levels {
		levels[i] = "^" + regexp.QuoteMeta(level) + "$"
	}
	args := []string{"-test.run=" + strings.Join(levels, "/"), "-test.v", "-test.timeout=1m"}
	if dir := flag.Lookup("test.gocoverdir"); dir != nil && dir.Value.String() != "" {
		metaWritten.Do(func() { _ = coverage.WriteMetaDir(dir.Value.String()) })
		args = append(args, "-test.gocoverdir="+dir.Value.String())
	}
	child := exec.CommandContext(t.Context(), binary, args...)
	child.Dir = t.TempDir()
	child.Env = append(os.Environ(), childEnv+"="+t.Name(), "DOKIMI_SEED=1")
	out, _ := child.CombinedOutput()
	t.Logf("the output of the child process:\n%s", out)
	values := map[string]string{}
	for line := range strings.Lines(string(out)) {
		if m := attrLine.FindStringSubmatch(strings.TrimRight(line, "\n")); m != nil {
			values[m[1]+" "+m[2]] += m[3]
		}
	}
	type checkRecord struct {
		World *spec.WorldRecord `json:"world"`
		Cases int               `json:"cases"`
	}
	var subject []checkRecord
	for _, value := range values {
		var record struct {
			Application string        `json:"application"`
			Checks      []checkRecord `json:"checks"`
		}
		assert.NoError(t, json.Unmarshal([]byte(value), &record), "the suite record is JSON")
		if record.Application == "subject" {
			assert.Length(t, record.Checks, 1, "the subject record has the check of the world")
			subject = append(subject, record.Checks[0])
		}
	}
	assert.Length(t, subject, 1, "the child process writes one subject record")
	return subject[0].World, subject[0].Cases
}

// The faults of a world over a store in the order of their draws: in native
// mode, for a store with a corruption error, and with the blocking faults.
var (
	instantFaults = []string{
		"fail-before", "fail-after", "fail-part", "tear", "fill", "fail-next", "fail-until-healed", "bump", "strobe",
	}
	sealedFaults = []string{
		"fail-before", "fail-after", "fail-part", "tear", "fill", "flip", "fail-next", "fail-until-healed", "bump",
		"strobe",
	}
	blockingFaults = append(slices.Clone(instantFaults), "disk-hold", "dependency-hold", "pause")
)

// entry is an entry of the draws of a scripted case of a world, a draw or a
// step, as prop.Draws reads it.
type entry map[string]any

// drawn returns the entry of a draw of the value under the label, with the
// typed literal of a bool, an int, a float64, a string, a list of float64 or
// a list of strings.
func drawn(label string, value any) entry {
	literal := map[string]any{"value": value}
	switch value.(type) {
	case bool:
		literal["type"] = "bool"
	case int:
		literal["type"] = "int"
	case float64:
		literal["type"] = "float"
	case string:
		literal["type"] = "string"
	case []float64:
		literal["type"], literal["of"] = "list", "float"
	default:
		literal["type"], literal["of"] = "list", "string"
	}
	return entry{"label": label, "value": literal}
}

// worldOpening returns the draws that a world over storeWorld makes before
// its first step: a host without drift, a pool of the key a that is not hot,
// and one choice for each fault of kinds, which keeps the faults of kept.
// nowOpening returns the draws of a world over nowWorld on a host with the
// drift, which has no pool.
func worldOpening(kinds []string, kept ...string) []entry {
	return append([]entry{drawn("drift.subject", 0), drawn("keys", []string{"a"}), drawn("keys.hot", false)},
		faultsOf(kinds, kept)...)
}

func nowOpening(drift int, kinds []string, kept ...string) []entry {
	return append([]entry{drawn("drift.subject", drift)}, faultsOf(kinds, kept)...)
}

// faultsOf returns the choice of each fault of kinds, which keeps the faults
// of kept.
func faultsOf(kinds, kept []string) []entry {
	entries := make([]entry, 0, len(kinds))
	for _, kind := range kinds {
		entries = append(entries, drawn("faults."+kind, slices.Contains(kept, kind)))
	}
	return entries
}

// stepOf returns the entries of a step of the action with the draws of its
// input.
func stepOf(action string, draws ...entry) []entry {
	return append([]entry{{"step": action}}, draws...)
}

// put returns the step of a Put of the value under the first key of the pool,
// on the client at the index or on the first client without an open call
// after it. get returns the step of a Get of the first key, and now the step
// of a Now.
func put(client int, value string) []entry {
	return stepOf("Put", drawn("client", client), drawn("put.key", 0.0), drawn("put.value", value))
}

func get(client int) []entry {
	return stepOf("Get", drawn("client", client), drawn("get.key", 0.0))
}

// create returns the step of a Create of the value on the client at the index
// or on the first client without an open call after it.
func create(client int, value string) []entry {
	return stepOf("Create", drawn("client", client), drawn("create.value", value))
}

// cancel returns the step of a cancel of the open call of the client at the
// index or of the first client with an open call after it.
func cancel(client int) []entry {
	return stepOf("cancel", drawn("client", client))
}

func now() []entry {
	return stepOf("Now", drawn("client", 0))
}

// disk returns the step of the fault of the disk of the kind, with the draws
// of its input, and dependency the step of the fault of a dependency.
func disk(kind string, draws ...entry) []entry {
	return stepOf("disk", append([]entry{drawn("disk", kind)}, draws...)...)
}

func dependency(kind string) []entry {
	return stepOf("dependency", drawn("dependency", kind))
}

// bump returns the step of a bump of the wall clock by 1.5 to the power of
// 6 plus the exponent milliseconds, back or forward.
func bump(exponent float64, back bool) []entry {
	return stepOf("clock", drawn("clock", "bump"), drawn("bump", exponent), drawn("bump.back", back))
}

// The steps of a world without draws, and the steps of a crash by a process
// crash and of an advance by the microseconds.
var (
	restart = stepOf("restart")
	end     = stepOf("end")
	pause   = stepOf("pause")
	crash   = stepOf("crash", drawn("kill", int(host.ProcessCrash)))
)

func advance(microseconds int) []entry {
	return stepOf("advance", drawn("advance", microseconds))
}

// script returns the draws of a case that makes the draws of the opening and
// then the steps.
func script(start []entry, steps ...[]entry) string {
	entries := slices.Clone(start)
	for _, s := range steps {
		entries = append(entries, s...)
	}
	// A marshal of maps of bools, numbers, strings and lists does not fail.
	data, _ := json.Marshal(entries)
	return string(data)
}

// runCase runs the case of the draws first, in a check of the probe p with
// the property over the store on the seat through the options opts, and
// returns the events of the history of that case. The run keeps no failing
// case in the store of the package.
func runCase(
	seat assert.TB, p spec.ProbeID, property func(h *conform.Handle[*kv], c *prop.Case), draws string,
	opts ...conform.Option[*kv],
) []history.Event {
	seat.Helper()
	var first sync.Once
	var events []history.Event
	conform.Check[*kv]{
		ID: "Store.Put/world", Statement: "the world of the store holds", Probe: p, Around: conform.Bubble,
		Options: func(h *conform.Handle[*kv]) []prop.Option {
			return append(probe.WorldOptions(h), prop.Draws(draws), prop.Store(""))
		},
		Property: func(h *conform.Handle[*kv], c *prop.Case) {
			defer first.Do(func() { events = c.History().Events() })
			property(h, c)
		},
	}.Run(seat, newStore, append(opts, conform.Cases[*kv](1), conform.Reference(newReference))...)
	return events
}

// outcome is the outcome of a call of a history: its operation, its client,
// the kind of its completion, the cause of the error of an unknown completion
// and the values of the output of an OK completion. An open call has the kind
// Invoke.
type outcome struct {
	cause  error
	op     string
	values []any
	client int
	kind   history.Kind
}

// causes are the errors that outcomesOf names as the cause of an unknown
// completion.
var causes = []error{syscall.EIO, syscall.ENOSPC, host.ErrInjected, medium.ErrCrashed, context.Canceled, errCorrupt}

// outcomesOf returns the outcome of each call of the events, in the order of
// their invocations.
func outcomesOf(events []history.Event) []outcome {
	var out []outcome
	at := map[int]int{}
	for _, e := range events {
		if e.Kind == history.Invoke {
			at[e.Index] = len(out)
			out = append(out, outcome{op: e.Operation, client: e.Client, kind: history.Invoke})
			continue
		}
		o := &out[at[e.Call]]
		o.kind = e.Kind
		if output, ok := e.Output.(conform.Output); ok {
			o.values = output.Values
		}
		for _, cause := range causes {
			if errors.Is(e.Error, cause) {
				o.cause = cause
				break
			}
		}
	}
	return out
}

// clockOf returns the offset of the wall clock from the monotonic clock at
// each call of Now in the events, measured from the first call, and the time
// of the monotonic clock at each call.
func clockOf(events []history.Event) (offsets, monos []time.Duration) {
	var first time.Time
	for _, e := range events {
		output, ok := e.Output.(conform.Output)
		if !ok {
			continue
		}
		wall, _ := output.Values[0].(time.Time)
		mono, _ := output.Values[1].(time.Duration)
		if len(monos) == 0 {
			first = wall.Add(-mono)
		}
		offsets, monos = append(offsets, wall.Sub(first)-mono), append(monos, mono)
	}
	return offsets, monos
}

func TestWorld(t *testing.T) {
	t.Parallel()

	t.Run("FaultyWorldHistory", func(t *testing.T) {
		t.Parallel()

		t.Run("proves its claim on a durable store in native mode", func(t *testing.T) {
			t.Parallel()

			linearizableCheck(storeWorld, amnesiac).Run(t, newStore, conform.Durable(openStore),
				conform.Reference(newReference))
		})

		t.Run("proves its claim on a durable store in Tasks mode", func(t *testing.T) {
			t.Parallel()

			linearizableCheck(storeWorld, amnesiac).Run(t, newStore, conform.Durable(openStore),
				conform.Reference(newReference), conform.Tasks[*kv]())
		})

		t.Run("proves its claim on a hosted store that detects a corrupted record in native mode", func(t *testing.T) {
			t.Parallel()

			linearizableCheck(sealedWorld, amnesiac).Run(t, newStore, conform.Hosted(openHosted),
				conform.Reference(newReference))
		})

		t.Run("proves its claim on a hosted store that detects a corrupted record in Tasks mode", func(t *testing.T) {
			t.Parallel()

			linearizableCheck(sealedWorld, amnesiac).Run(t, newStore, conform.Hosted(openHosted),
				conform.Reference(newReference), conform.Tasks[*kv]())
		})

		t.Run("proves its claim with a planted defect that passes a durable check of the store", func(t *testing.T) {
			t.Parallel()

			linearizableCheck(storeWorld, amnesiac).Run(t, newStore, conform.Durable(openStore),
				conform.Reference(newReference), conform.Isolate[*kv](), conform.With(syncedWrites))
		})

		t.Run("strikes each instant fault of the disk at the next call of the disk", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults, "fail-before", "fail-part", "fill"),
				put(0, "x"),
				disk("fail-before"), put(0, "y"),
				disk("fail-part", drawn("disk.bytes", 2)), put(0, "y"),
				disk("fill", drawn("disk.bytes", 2)), put(0, "y"),
				end, put(0, "y"),
				disk("fail-before"), put(0, "y"),
			), conform.Durable(openStore))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.OK},
				{op: "Put", kind: history.Unknown, cause: syscall.EIO},
				{op: "Put", kind: history.Unknown, cause: syscall.EIO},
				{op: "Put", kind: history.Unknown, cause: syscall.ENOSPC},
				{op: "Put", kind: history.OK},
				{op: "Put", kind: history.Unknown, cause: syscall.EIO},
			}, "each fault fails the Put after it, and the end of the fill lets the next Put write")
		})

		t.Run("lets the writes within the bytes of a fill succeed", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults, "fill"), disk("fill", drawn("disk.bytes", 4096)), put(0, "x"),
			), conform.Durable(openStore))

			assert.Equal(t, outcomesOf(events), []outcome{{op: "Put", kind: history.OK}},
				"the Put writes within the bytes of the fill")
		})

		t.Run("takes the whole effect of a call that fails after its effect", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults, "fail-after"), put(0, "x"), disk("fail-after"), advance(1000), get(0),
			), conform.Hosted(openWindowed))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.Unknown, cause: syscall.EIO},
				{op: "Get", kind: history.OK, values: []any{"x"}},
			}, "the rename of the Put fails after it renamed the file")
		})

		t.Run("keeps the first bytes of a torn write after a power loss", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(sealedWorld), script(
				worldOpening(sealedFaults, "tear"), disk("tear", drawn("disk.bytes", 2)), put(0, "x"),
				stepOf("crash", drawn("kill", int(host.PowerLoss)), drawn("keep", []float64{1.0, 1.0})),
				restart, get(0),
			), conform.Durable(openUnsynced))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.OK},
				{op: "Get", kind: history.Unknown, cause: errCorrupt},
			}, "the Get reads the two bytes that the power loss kept")
		})

		t.Run("flips a bit inside the data of a file", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(sealedWorld), script(
				worldOpening(sealedFaults, "flip"), put(0, "x"),
				disk("flip", drawn("disk.file", 0.0), drawn("disk.offset", 0.93)), get(0),
			), conform.Durable(openStore))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.OK},
				{op: "Get", kind: history.Unknown, cause: errCorrupt},
			}, "the Get reads the flipped byte of the value")
		})

		t.Run("holds each call of the disk until the end of the hold", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(blockingFaults, "disk-hold"), disk("disk-hold"), put(0, "x"), cancel(0), end, get(0),
			), conform.Durable(openStore), conform.Blocking[*kv]())

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.Unknown, cause: context.Canceled},
				{op: "Get", kind: history.OK, values: []any{"x"}},
			}, "the Put waits for the end of the hold, and then writes")
		})

		t.Run("fails each call of a dependency until the end of its failure", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults, "fail-next", "fail-until-healed"), put(0, "x"),
				dependency("fail-next"), put(0, "y"), put(0, "y"),
				dependency("fail-until-healed"), put(0, "y"), put(0, "y"), end, put(0, "y"),
			), conform.Hosted(openHosted))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.OK},
				{op: "Put", kind: history.Unknown, cause: host.ErrInjected},
				{op: "Put", kind: history.OK},
				{op: "Put", kind: history.Unknown, cause: host.ErrInjected},
				{op: "Put", kind: history.Unknown, cause: host.ErrInjected},
				{op: "Put", kind: history.OK},
			}, "a failure of the next call fails one Put, and a failure until the end fails each Put before the end")
		})

		t.Run("holds each call of a dependency until the end of the hold", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(blockingFaults, "dependency-hold"), put(0, "x"), dependency("dependency-hold"),
				put(0, "y"), cancel(0), end, get(0),
			), conform.Hosted(openHosted), conform.Blocking[*kv]())

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.OK},
				{op: "Put", kind: history.Unknown, cause: context.Canceled},
				{op: "Get", kind: history.OK, values: []any{"y"}},
			}, "the second Put waits for the end of the hold, and then writes")
		})

		t.Run("pauses each call of the incarnation until the end of the pause", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(blockingFaults, "pause"), pause, put(0, "x"), cancel(0), end, get(0),
			), conform.Durable(openStore), conform.Blocking[*kv]())

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.Unknown, cause: context.Canceled},
				{op: "Get", kind: history.OK, values: []any{"x"}},
			}, "the Put waits for the end of the pause, and then writes")
		})

		t.Run("cancels the open call of the first client with one from the client that the case picks",
			func(t *testing.T) {
				t.Parallel()

				events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
					worldOpening(blockingFaults, "disk-hold"), disk("disk-hold"), put(1, "x"), cancel(0),
				), conform.Durable(openStore), conform.Blocking[*kv]())

				assert.Equal(t, outcomesOf(events), []outcome{
					{op: "Put", kind: history.Unknown, cause: context.Canceled, client: 1},
				}, "the cancel of client 0 ends the Put of client 1")
			})

		t.Run("calls the first client without an open call from the client that the case picks", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(blockingFaults, "disk-hold"), disk("disk-hold"), put(1, "x"), put(1, "y"),
			), conform.Durable(openStore), conform.Blocking[*kv]())

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.Unknown, cause: medium.ErrCrashed, client: 1},
				{op: "Put", kind: history.Unknown, cause: medium.ErrCrashed},
			}, "the second Put of client 1 goes to client 0")
		})

		t.Run("takes no effect for a call while every client has an open call", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(blockingFaults, "disk-hold"), disk("disk-hold"), put(0, "x"), put(0, "y"), put(1, "x"),
			), conform.Durable(openStore), conform.Blocking[*kv]())

			assert.Length(t, outcomesOf(events), 2, "the third Put finds no client without an open call")
		})

		t.Run("takes no effect for a cancel without an open call", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults), put(0, "x"), cancel(1), get(0),
			), conform.Durable(openStore))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.OK},
				{op: "Get", kind: history.OK, values: []any{"x"}},
			}, "the cancel finds no open call")
		})

		t.Run("strikes a fault of a dependency before a call of a dependency", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults, "fail-next"), dependency("fail-next"), put(0, "x"),
			), conform.Hosted(openHosted))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.Unknown, cause: host.ErrInjected},
			}, "the failure of the next call fails the first Put")
		})

		t.Run("takes no effect for a flip of a disk without a file with data", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(sealedWorld), script(
				worldOpening(sealedFaults, "flip"), disk("flip", drawn("disk.file", 0.0), drawn("disk.offset", 0.0)),
				put(0, "x"), get(0),
			), conform.Durable(openStore))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.OK},
				{op: "Get", kind: history.OK, values: []any{"x"}},
			}, "the flip finds no file, and the Get reads the value")
		})

		t.Run("completes a call that is open at a crash as unknown with medium.ErrCrashed", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults), put(0, "x"), crash,
			), conform.Durable(openParked))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.Unknown, cause: medium.ErrCrashed},
			}, "the crash completes the parked Put")
		})

		t.Run("completes a call that is open at the end of its case as unknown with medium.ErrCrashed",
			func(t *testing.T) {
				t.Parallel()

				events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
					worldOpening(instantFaults), put(0, "x"),
				), conform.Durable(openParked))

				assert.Equal(t, outcomesOf(events), []outcome{
					{op: "Put", kind: history.Unknown, cause: medium.ErrCrashed},
				}, "the end of the case completes the parked Put")
			})

		t.Run("fails a case without steps for a goroutine that ignores its lifetime", func(t *testing.T) {
			t.Parallel()
			r := &onTest{Recorder: assert.NewRecorder(), t: t}

			runCase(r, spec.FaultyWorldHistory, linearizableOver(storeWorld), "[]", conform.Durable(leakyOpen))

			failures := r.Failures()
			assert.NotEmpty(t, failures, "the check fails")
			inner, _ := failures[0].CaseFailure()
			expect.Contains(t, inner.Contract, "is alive after the crash of its incarnation",
				"the failure states the leak at the end of the case")
		})

		t.Run("makes the next call of a client after the step of its last call", func(t *testing.T) {
			t.Parallel()

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(blockingFaults), put(0, "x"), get(0),
			), conform.Durable(openStore), conform.Tasks[*kv]())

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.OK},
				{op: "Get", kind: history.OK, values: []any{"x"}},
			}, "client 0 makes the Get once its Put has returned")
		})

		t.Run("ends the context of a call that a cancel ends", func(t *testing.T) {
			t.Parallel()
			lingering := func(h *conform.Handle[*kv], m medium.FS) *kv {
				s := openStore(h, m)
				s.lingers = true
				return s
			}

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults), put(0, "x"), cancel(0), get(0),
			), conform.Durable(lingering))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.Unknown, cause: context.Canceled},
				{op: "Get", kind: history.OK, values: []any{"x"}},
			}, "the Put writes once the cancel has ended its context")
		})

		t.Run("wakes each wait of its subject after each step", func(t *testing.T) {
			t.Parallel()
			waiting := func(h *conform.Handle[*kv], env *host.Env) *kv {
				s := openHosted(h, env)
				s.waits = true
				return s
			}

			events := runCase(t, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults), put(0, "x"), advance(1000), get(0),
			), conform.Hosted(waiting))

			assert.Equal(t, outcomesOf(events), []outcome{
				{op: "Put", kind: history.OK},
				{op: "Get", kind: history.OK, values: []any{"x"}},
			}, "the Put waits until a millisecond has passed, and then writes")
		})

		t.Run("passes the output of each call that creates a key to its Produced once", func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var counts []*atomic.Int64
			counted := storeWorld
			counted.Operations = func(c *prop.Case) []probe.Operation[*kv] {
				produced := new(atomic.Int64)
				mu.Lock()
				counts = append(counts, produced)
				mu.Unlock()
				ops := storeOperations(c)
				for i := range ops {
					if add := ops[i].Produced; add != nil {
						ops[i].Produced = func(out conform.Output) {
							produced.Add(1)
							add(out)
						}
					}
				}
				return ops
			}

			runCase(t, spec.FaultyWorldHistory, linearizableOver(counted), script(
				worldOpening(instantFaults), create(0, "x"), create(0, "y"), advance(1),
			), conform.Durable(openStore))

			assert.Equal(t, counts[0].Load(), int64(1),
				"the Create of x produces its key once, and the refused Create of y produces none")
		})

		t.Run("ends a run in Tasks mode as flaky for a subject whose replay takes another effect", func(t *testing.T) {
			t.Parallel()
			r := &onTest{Recorder: assert.NewRecorder(), t: t}
			var runs atomic.Int64
			fickle := func(h *conform.Handle[*kv], env *host.Env) *kv {
				s := openHosted(h, env)
				if env.Incarnation() == 1 {
					runs.Add(1)
				}
				s.auditLast = runs.Load()%2 == 0
				return s
			}
			draws := script(worldOpening(blockingFaults, "fail-next"), put(0, "x"), dependency("fail-next"),
				put(0, "y"), get(0))

			conform.Check[*kv]{
				ID: "Store.Put/world", Statement: "the world of the store holds", Probe: spec.FaultyWorldHistory,
				Around: conform.Bubble,
				Options: func(h *conform.Handle[*kv]) []prop.Option {
					return append(probe.WorldOptions(h), prop.Draws(draws), prop.Store(""), prop.Confirm(1))
				},
				Property: linearizableOver(storeWorld),
			}.Run(r, newStore, conform.Hosted(fickle), conform.Cases[*kv](1), conform.Reference(newReference),
				conform.Tasks[*kv]())

			failures := r.Failures()
			assert.NotEmpty(t, failures, "the run fails")
			expect.Equal(t, failures[0].Detail["outcome"], any(prop.Flaky),
				"the replay of the case returns another value from the Get")
		})

		t.Run("picks the keys that a call of the world creates", func(t *testing.T) {
			t.Parallel()
			var picked atomic.Bool
			check := linearizableCheck(storeWorld, amnesiac)
			check.Property = func(h *conform.Handle[*kv], c *prop.Case) {
				probe.FaultyWorldHistory(h, c, storeWorld)
				for _, e := range c.History().Events() {
					created := e.Kind == history.Invoke && strings.HasPrefix(e.Args[0].(string), "c.")
					if created && e.Operation != "Create" {
						picked.Store(true)
					}
				}
			}

			check.Run(t, newStore, conform.Durable(openStore), conform.Reference(newReference))

			assert.True(t, picked.Load(), "a call of Put or Get picks a key that Create returned")
		})

		t.Run("refuses each step that its world cannot take", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name        string
				giveProbe   spec.ProbeID
				giveRun     func(h *conform.Handle[*kv], c *prop.Case)
				giveStart   []entry
				giveSteps   [][]entry
				giveRefused []entry
				giveOpts    []conform.Option[*kv]
			}{
				{
					name: "strikes no fault that its case does not keep", giveStart: worldOpening(instantFaults),
					giveRefused: disk("fail-before"),
				},
				{
					name:      "strikes no fault of the disk on a crashed subject",
					giveStart: worldOpening(instantFaults, "fail-before"), giveSteps: [][]entry{crash},
					giveRefused: disk("fail-before"),
				},
				{
					name:      "strikes no fault while a fault is in force",
					giveStart: worldOpening(instantFaults, "fail-before", "fill"),
					giveSteps: [][]entry{disk("fill", drawn("disk.bytes", 2))}, giveRefused: disk("fail-before"),
				},
				{
					name:      "calls no client past the clients of the world",
					giveStart: worldOpening(instantFaults), giveRefused: put(2, "x"),
				},
				{
					name:      "calls no operation of a crashed subject",
					giveStart: worldOpening(instantFaults), giveSteps: [][]entry{crash}, giveRefused: put(0, "x"),
				},
				{
					name:      "cancels no call of a crashed subject",
					giveStart: worldOpening(instantFaults), giveSteps: [][]entry{crash}, giveRefused: cancel(0),
				},
				{
					name:      "strikes no fault of a dependency that its case does not keep",
					giveStart: worldOpening(instantFaults), giveSteps: [][]entry{put(0, "x")},
					giveRefused: dependency("fail-next"), giveOpts: []conform.Option[*kv]{conform.Hosted(openHosted)},
				},
				{
					name:      "strikes no fault of a dependency on a crashed subject",
					giveStart: worldOpening(instantFaults, "fail-next"), giveSteps: [][]entry{put(0, "x"), crash},
					giveRefused: dependency("fail-next"), giveOpts: []conform.Option[*kv]{conform.Hosted(openHosted)},
				},
				{
					name: "strikes no fault of the clock that its case does not keep", giveProbe: spec.WorldRecovery,
					giveRun: completesOver(nowWorld), giveStart: nowOpening(0, instantFaults),
					giveSteps: [][]entry{now()}, giveRefused: bump(0, false),
					giveOpts: []conform.Option[*kv]{conform.Hosted(openHosted)},
				},
				{
					name:      "strikes no fault of the clock on a crashed subject",
					giveProbe: spec.WorldRecovery, giveRun: completesOver(nowWorld),
					giveStart: nowOpening(0, instantFaults, "bump"), giveSteps: [][]entry{now(), crash},
					giveRefused: bump(0, false), giveOpts: []conform.Option[*kv]{conform.Hosted(openHosted)},
				},
				{
					name: "pauses no subject in a case that does not keep the pause", giveStart: worldOpening(
						blockingFaults), giveRefused: pause,
					giveOpts: []conform.Option[*kv]{conform.Blocking[*kv]()},
				},
				{
					name:      "pauses no crashed subject",
					giveStart: worldOpening(blockingFaults, "pause"), giveSteps: [][]entry{crash}, giveRefused: pause,
					giveOpts: []conform.Option[*kv]{conform.Blocking[*kv]()},
				},
				{
					name: "ends no fault while none is in force", giveStart: worldOpening(instantFaults),
					giveRefused: end,
				},
				{
					name:      "ends a pause with the crash of its incarnation",
					giveStart: worldOpening(blockingFaults, "pause"), giveSteps: [][]entry{pause, crash},
					giveRefused: end, giveOpts: []conform.Option[*kv]{conform.Blocking[*kv]()},
				},
				{
					name:      "ends a hold of the disk with the crash of the disk",
					giveStart: worldOpening(blockingFaults, "disk-hold"),
					giveSteps: [][]entry{disk("disk-hold"), crash}, giveRefused: end,
					giveOpts: []conform.Option[*kv]{conform.Blocking[*kv]()},
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					p, run := spec.FaultyWorldHistory, linearizableOver(storeWorld)
					if tt.giveRun != nil {
						p, run = tt.giveProbe, tt.giveRun
					}
					opts := append([]conform.Option[*kv]{conform.Durable(openStore)}, tt.giveOpts...)
					before := &onTest{Recorder: assert.NewRecorder(), t: t}
					after := &onTest{Recorder: assert.NewRecorder(), t: t}

					runCase(before, p, run, script(tt.giveStart, tt.giveSteps...), opts...)
					runCase(after, p, run, script(tt.giveStart, append(slices.Clone(tt.giveSteps), tt.giveRefused)...),
						opts...)

					assert.False(t, before.Failed(), "the case runs without the refused step")
					assert.Length(t, after.Faults(), 1, "the refused step ends the run with a fault")
					assert.Empty(t, after.Failures(), "the refused step fails no case")
				})
			}
		})

		t.Run("keeps a fill in force across a crash", func(t *testing.T) {
			t.Parallel()
			r := &onTest{Recorder: assert.NewRecorder(), t: t}

			runCase(r, spec.FaultyWorldHistory, linearizableOver(storeWorld), script(
				worldOpening(instantFaults, "fill"), disk("fill", drawn("disk.bytes", 2)), crash, restart, end,
			), conform.Durable(openStore))

			assert.False(t, r.Failed(), "the end of the fill follows the restart")
		})

		t.Run("keeps no blocking fault in native mode without the option Blocking", func(t *testing.T) {
			t.Parallel()
			blocked := &onTest{Recorder: assert.NewRecorder(), t: t}
			unblocked := &onTest{Recorder: assert.NewRecorder(), t: t}
			draws := script(worldOpening(instantFaults), put(0, "x"))

			runCase(blocked, spec.FaultyWorldHistory, linearizableOver(storeWorld), draws, conform.Durable(openStore),
				conform.Blocking[*kv]())
			runCase(unblocked, spec.FaultyWorldHistory, linearizableOver(storeWorld), draws,
				conform.Durable(openStore))

			assert.Length(t, blocked.Faults(), 1, "the option Blocking adds the choices of the blocking faults")
			assert.False(t, unblocked.Failed(), "the world without the option chooses no blocking fault")
		})

		t.Run("engages no case without a call", func(t *testing.T) {
			t.Parallel()
			r := &onTest{Recorder: assert.NewRecorder(), t: t}
			silent := probe.World[*kv]{
				Operations: func(*prop.Case) []probe.Operation[*kv] { return nil }, Reference: storeReference,
			}

			conform.Check[*kv]{
				ID: "Subject.Call/claim", Statement: "the probe holds", Probe: spec.FaultyWorldHistory,
				Around: conform.Bubble, Property: linearizableOver(silent),
			}.Run(r, newStore, conform.Durable(openStore), conform.Reference(newReference))

			entry, _ := spec.ProbeOf(spec.FaultyWorldHistory)
			condition := strings.ToLower(entry.Vacuous[:1]) + strings.TrimSuffix(entry.Vacuous[1:], ".")
			assert.Equal(t, r.Messages(), []string{"Subject.Call/claim never engaged: " + condition + ". Pass a " +
				"subject that engages the check, or excuse the check with the directive excuse or conform.Excuse " +
				"and a reason."}, "the check fails for the vacuous condition of its probe")
		})

		t.Run("ends a failing case in native mode as a counterexample for outputs that its replays change",
			func(t *testing.T) {
				t.Parallel()
				r := &onTest{Recorder: assert.NewRecorder(), t: t}
				var counter atomic.Int64
				counting := func(h *conform.Handle[*kv], m medium.FS) *kv {
					s := openStore(h, m)
					s.counter = &counter
					return s
				}

				conform.Check[*kv]{
					ID: "Store.Put/world", Statement: "the world of the store holds", Probe: spec.FaultyWorldHistory,
					Around: conform.Bubble, Options: probe.WorldOptions[*kv], Property: linearizableOver(storeWorld),
				}.Run(r, newStore, conform.Durable(counting), conform.Reference(newReference))

				failures := r.Failures()
				assert.NotEmpty(t, failures, "the check fails")
				expect.Equal(t, failures[0].Detail["outcome"], any(prop.Counterexample),
					"the replays reproduce the failure, whatever the numbers that the Gets return")
			})

		t.Run("fails the check for a goroutine of a crashed incarnation that ignores its lifetime", func(t *testing.T) {
			t.Parallel()
			r := &onTest{Recorder: assert.NewRecorder(), t: t}

			linearizableCheck(storeWorld, forgetting).Run(r, newStore, conform.Durable(leakyOpen),
				conform.Reference(newReference))

			var contracts []string
			for _, f := range r.Failures() {
				if inner, ok := f.CaseFailure(); ok {
					contracts = append(contracts, inner.Contract)
				}
			}
			expect.Contains(t, strings.Join(contracts, "\n"), "is alive after the crash of its incarnation",
				"the failure of a case states the leak")
		})

		t.Run("ends the check with the fault of a reference that panics", func(t *testing.T) {
			t.Parallel()
			r := &onTest{Recorder: assert.NewRecorder(), t: t}
			panicking := storeWorld
			panicking.Reference = func(*kv) func(context.Context, string, []any) conform.Output {
				var gone *kv
				return storeReference(gone)
			}

			conform.Check[*kv]{
				ID: "Store.Put/world", Statement: "the world of the store holds", Probe: spec.FaultyWorldHistory,
				Around: conform.Bubble, Property: linearizableOver(panicking),
			}.Run(r, newStore, conform.Durable(openStore), conform.Reference(newReference))

			faults := r.Faults()
			assert.NotEmpty(t, faults, "the check ends with a fault")
			expect.ErrorIs(t, faults[0], history.ErrSpec, "the fault is the fault of the spec of the reference")
		})

		t.Run("fails the check for an open that does not return within the bound of its world", func(t *testing.T) {
			t.Parallel()
			r := &onTest{Recorder: assert.NewRecorder(), t: t}
			bounded := storeWorld
			bounded.Bound = 2 * time.Second
			stuck := func(h *conform.Handle[*kv], env *host.Env) *kv {
				_ = host.Park(env.Lifetime())
				return openHosted(h, env)
			}

			conform.Check[*kv]{
				ID: "Store.Put/world", Statement: "the world of the store holds", Probe: spec.FaultyWorldHistory,
				Around: conform.Bubble, Property: linearizableOver(bounded),
			}.Run(r, newStore, conform.Hosted(stuck), conform.Reference(newReference))

			failures := r.Failures()
			assert.NotEmpty(t, failures, "the check fails")
			inner, _ := failures[0].CaseFailure()
			expect.Equal(t, inner.Contract, "probe: the open of the incarnation 1 did not return within 2s of its host",
				"the failure states the open and the bound")
		})

		t.Run("records the world of its cases", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				inChild(t, linearizableCheck(sealedWorld, amnesiac), conform.Hosted(openHosted),
					conform.Tasks[*kv]())
				return
			}
			world, _ := worldOf(t)

			assert.NotNil(t, world, "the check has a world")
			expect.True(t, world.Steps > 0, "the world counts the steps of its cases")
			expect.Equal(t, positive(world.Crashes), []string{"power-loss", "process-crash"},
				"the world counts the crashes of each kind")
			expect.Equal(t, positive(world.Faults.Struck), []string{
				"bump", "dependency-hold", "disk-hold", "fail-after", "fail-before", "fail-next", "fail-part",
				"fail-until-healed", "fill", "flip", "pause", "strobe", "tear",
			}, "the world counts the strikes of each fault")
		})

		t.Run("records the faults of the clock and of the dependencies as vacuous for a store that uses neither",
			func(t *testing.T) {
				t.Parallel()

				if os.Getenv(childEnv) == t.Name() {
					inChild(t, linearizableCheck(storeWorld, amnesiac), conform.Durable(openStore))
					return
				}
				world, _ := worldOf(t)

				assert.NotNil(t, world, "the check has a world")
				expect.Equal(t, world.Faults.Vacuous, []string{"bump", "fail-next", "fail-until-healed", "strobe"},
					"the faults of the clock and of the dependencies are vacuous, and no case counts their strikes")
			})

		t.Run("records the partitions that its checker cannot decide within their budget", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				inChild(t, worldCheck(spec.FaultyWorldHistory, spec.LinearizableUnderFaults,
					spec.NotLinearizableUnderFaults, linearizableOver(storeWorld, history.Budget(1)), inventive),
					conform.Durable(openStore))
				return
			}
			world, cases := worldOf(t)

			assert.NotNil(t, world, "the check has a world")
			expect.InRange(t, world.Undecided, 1, float64(cases),
				"the world counts the first undecided partition of each case alone")
		})

		t.Run("records the stack of a goroutine that ignored its lifetime", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				inChild(t, linearizableCheck(storeWorld, forgetting), conform.Durable(leakyOpen))
				return
			}
			world, _ := worldOf(t)

			assert.NotNil(t, world, "the check has a world")
			assert.NotEmpty(t, world.Leaked, "the world lists the leaked goroutines")
			expect.Contains(t, world.Leaked[0], "probe_test.leakyOpen", "the stack shows the goroutine of the open")
		})
	})

	t.Run("WorldRecovery", func(t *testing.T) {
		t.Parallel()

		t.Run("proves its claim on a durable store in native mode", func(t *testing.T) {
			t.Parallel()

			worldCheck(spec.WorldRecovery, spec.CompletesAfterRecovery, spec.CallOpenAfterRecovery,
				completesOver(storeWorld), blocking).Run(t, newStore, conform.Durable(openStore),
				conform.Reference(newReference))
		})

		t.Run("proves its claim on a durable store in Tasks mode", func(t *testing.T) {
			t.Parallel()

			worldCheck(spec.WorldRecovery, spec.CompletesAfterRecovery, spec.CallOpenAfterRecovery,
				completesOver(storeWorld), blocking).Run(t, newStore, conform.Durable(openStore),
				conform.Reference(newReference), conform.Tasks[*kv]())
		})

		t.Run("proves its claim with a planted defect that passes a durable check of the store", func(t *testing.T) {
			t.Parallel()

			worldCheck(spec.WorldRecovery, spec.CompletesAfterRecovery, spec.CallOpenAfterRecovery,
				completesOver(storeWorld), blocking).Run(t, newStore, conform.Durable(openStore),
				conform.Isolate[*kv](), conform.With(syncedWrites))
		})

		t.Run("fails the check for a goroutine of the incarnation of its recovery that ignores its lifetime",
			func(t *testing.T) {
				t.Parallel()
				r := &onTest{Recorder: assert.NewRecorder(), t: t}

				runCase(r, spec.WorldRecovery, completesOver(storeWorld), "[]", conform.Durable(leakyOpen))

				failures := r.Failures()
				assert.NotEmpty(t, failures, "the check fails")
				inner, _ := failures[0].CaseFailure()
				expect.Contains(t, inner.Contract, "is alive after the crash of its incarnation",
					"the failure states the leak at the end of the case")
			})

		t.Run("bumps the wall clock until the end of the bump", func(t *testing.T) {
			t.Parallel()

			offsets, _ := clockOf(runCase(t, spec.WorldRecovery, completesOver(nowWorld), script(
				nowOpening(0, instantFaults, "bump"), now(), bump(1, true), now(), end, now(),
			), conform.Hosted(openHosted)))

			assert.Equal(t, offsets, []time.Duration{0, -17085937, 0},
				"the wall clock is 1.5 to the power of 7 ms behind until the end of the bump")
		})

		t.Run("bumps the wall clock before the first read of the wall clock", func(t *testing.T) {
			t.Parallel()

			offsets, _ := clockOf(runCase(t, spec.WorldRecovery, completesOver(nowWorld), script(
				nowOpening(0, instantFaults, "bump"), bump(1, true), now(), end, now(),
			), conform.Hosted(openHosted)))

			assert.Equal(t, offsets, []time.Duration{0, 17085937},
				"the wall clock is 1.5 to the power of 7 ms behind at the first read, until the end of the bump")
		})

		t.Run("strobes the wall clock every period until the end of the strobe", func(t *testing.T) {
			t.Parallel()

			offsets, _ := clockOf(runCase(t, spec.WorldRecovery, completesOver(nowWorld), script(
				nowOpening(0, instantFaults, "strobe"), now(),
				stepOf("clock", drawn("clock", "strobe"), drawn("strobe.delta", 1.0), drawn("strobe.period", 1.0)),
				now(), advance(2000), now(), advance(2000), now(), end, now(),
			), conform.Hosted(openHosted)))

			assert.Equal(t, offsets, []time.Duration{0, 8 * time.Millisecond, 0, 8 * time.Millisecond, 0},
				"the wall clock is 8 ms ahead in every second period of 2 ms until the end of the strobe")
		})

		t.Run("advances the monotonic clock of a slow host by each advance", func(t *testing.T) {
			t.Parallel()

			_, monos := clockOf(runCase(t, spec.WorldRecovery, completesOver(nowWorld), script(
				nowOpening(-500, instantFaults), now(), advance(1_000_000), now(),
			), conform.Hosted(openHosted)))

			assert.Length(t, monos, 2, "the case makes two calls of Now")
			expect.InRange(t, monos[1]-monos[0], float64(time.Second), float64(time.Second+time.Microsecond),
				"the advance moves the monotonic clock of the host by a second")
		})

		t.Run("opens a subject that opens at once without an advance of its clock", func(t *testing.T) {
			t.Parallel()

			_, monos := clockOf(runCase(t, spec.WorldRecovery, completesOver(nowWorld), script(
				nowOpening(0, instantFaults), now(),
			), conform.Hosted(openHosted)))

			assert.Equal(t, monos, []time.Duration{0}, "the first call runs at the start of the host")
		})

		t.Run("opens a subject that opens within the bound of its world", func(t *testing.T) {
			t.Parallel()

			_, monos := clockOf(runCase(t, spec.WorldRecovery, completesOver(nowWorld), script(
				nowOpening(0, instantFaults), now(),
			), conform.Hosted(sleepy(500*time.Millisecond))))

			assert.Equal(t, monos, []time.Duration{time.Second}, "the world waits for the open within the bound")
		})

		t.Run("opens a subject that opens at the bound of its world", func(t *testing.T) {
			t.Parallel()

			_, monos := clockOf(runCase(t, spec.WorldRecovery, completesOver(nowWorld), script(
				nowOpening(0, instantFaults), now(),
			), conform.Hosted(sleepy(time.Second))))

			assert.Equal(t, monos, []time.Duration{time.Second}, "the open that returns at the bound counts")
		})
	})

	t.Run("WorldOptions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			giveOpts []conform.Option[*kv]
			wantRuns int
		}{
			{name: "confirms every tenth valid case in Tasks mode", giveOpts: []conform.Option[*kv]{
				conform.Tasks[*kv](),
			}, wantRuns: 11},
			{name: "replays no valid case in native mode", wantRuns: 10},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var opts []prop.Option
				conform.Check[*kv]{
					ID: "Store.Get/options", Statement: "the options of a world", Defect: newStore,
					Apply: func(h *conform.Handle[*kv]) { opts = probe.WorldOptions(h) },
				}.Run(&onTest{Recorder: assert.NewRecorder(), t: t}, newStore, tt.giveOpts...)
				var runs atomic.Int64

				prop.ForAll(t, "each case runs", func(c *prop.Case) {
					c.Draw(prop.Integer(0, 9), "n")
					runs.Add(1)
				}, append(opts, prop.Cases(10), prop.Store(""))...)

				assert.Equal(t, int(runs.Load()), tt.wantRuns, "the run makes the cases and its replays")
			})
		}

		t.Run("replays a failing case ten times in native mode", func(t *testing.T) {
			t.Parallel()
			var opts []prop.Option
			conform.Check[*kv]{
				ID: "Store.Get/options", Statement: "the options of a world", Defect: newStore,
				Apply: func(h *conform.Handle[*kv]) { opts = probe.WorldOptions(h) },
			}.Run(&onTest{Recorder: assert.NewRecorder(), t: t}, newStore)
			var runs atomic.Int64

			assert.Rejects(t, "the property fails", func(tb assert.TB) {
				prop.ForAll(tb, "each case fails", func(c *prop.Case) {
					runs.Add(1)
					assert.True(c, false, "the case fails")
				}, append(opts, prop.Cases(1), prop.Store(""))...)
			})

			assert.Equal(t, int(runs.Load()), 11, "the run makes the failing case and its ten replays")
		})
	})

	t.Run("Amnesiac", func(t *testing.T) {
		t.Parallel()

		t.Run("opens the subject over the disk of the first incarnation as the disk is", func(t *testing.T) {
			t.Parallel()

			c := conform.Check[*kv]{
				ID: "Store.Put/amnesiac", Statement: "the subject finds the file of the first incarnation",
				Around: conform.Bubble,
				Property: func(h *conform.Handle[*kv], pc *prop.Case) {
					_, env := host.New(pc, "node-1", nil, nil).Start()
					f, err := env.Disk().OpenFile("a", os.O_CREATE|os.O_WRONLY, 0o644)
					assert.NoError(h, err, "the disk creates a file")
					assert.NoError(h, f.Close(), "the file closes")

					_, err = probe.Amnesiac(h, env).fs.Stat("a")

					assert.NoError(h, err, h.Statement())
				},
				Defect: newStore,
			}
			c.Run(t, newStore, conform.Cases[*kv](1), conform.Durable(openStore))
		})

		t.Run("erases every file and every directory of the disk from the second incarnation on", func(t *testing.T) {
			t.Parallel()

			c := conform.Check[*kv]{
				ID: "Store.Put/amnesiac", Statement: "the subject finds an empty disk after a restart",
				Around: conform.Bubble,
				Property: func(h *conform.Handle[*kv], pc *prop.Case) {
					node := host.New(pc, "node-1", nil, nil)
					i, env := node.Start()
					assert.NoError(h, env.Disk().MkdirAll("d", 0o755), "the disk creates a directory")
					for _, name := range []string{"a", "d/b"} {
						f, err := env.Disk().OpenFile(name, os.O_CREATE|os.O_WRONLY, 0o644)
						assert.NoError(h, err, "the disk creates the file "+name)
						assert.NoError(h, f.Close(), "the file "+name+" closes")
					}
					i.Crash(host.ProcessCrash)
					_, env = node.Start()

					entries, err := probe.Amnesiac(h, env).fs.ReadDir(".")

					assert.NoError(h, err, "the disk lists its root")
					assert.Empty(h, entries, h.Statement())
				},
				Defect: newStore,
			}
			c.Run(t, newStore, conform.Cases[*kv](1), conform.Durable(openStore))
		})

		t.Run("keeps the disk erased across a power loss after the erase", func(t *testing.T) {
			t.Parallel()

			c := conform.Check[*kv]{
				ID: "Store.Put/amnesiac", Statement: "the disk has no file after a power loss",
				Around: conform.Bubble,
				Property: func(h *conform.Handle[*kv], pc *prop.Case) {
					node := host.New(pc, "node-1", nil, nil)
					i, env := node.Start()
					f, err := env.Disk().OpenFile("a", os.O_CREATE|os.O_WRONLY, 0o644)
					assert.NoError(h, err, "the disk creates a file")
					assert.NoError(h, f.Sync(), "the file syncs")
					assert.NoError(h, f.Close(), "the file closes")
					assert.NoError(h, env.Disk().SyncDir("."), "the disk syncs its root")
					i.Crash(host.ProcessCrash)
					i, env = node.Start()
					probe.Amnesiac(h, env)
					i.Crash(host.PowerLoss)
					_, env = node.Start()

					entries, err := env.Disk().ReadDir(".")

					assert.NoError(h, err, "the disk lists its root")
					assert.Empty(h, entries, h.Statement())
				},
				Defect: newStore,
			}
			c.Run(t, newStore, conform.Cases[*kv](1), conform.Durable(openStore))
		})
	})
}

// positive returns the keys of the counts that are above 0, in order.
func positive(counts map[string]int) []string {
	var keys []string
	for key, n := range counts {
		if n > 0 {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}
