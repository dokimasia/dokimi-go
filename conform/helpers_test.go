// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"runtime/coverage"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// Store is the declaration of the specs: a store of values by key.
type Store interface {
	// Put stores the value under the key.
	Put(key, value string)
	// Get returns the value under the key, and whether the store has one.
	Get(key string) (string, bool)
}

// memory is a Store in memory, the subject of the specs. clock is the clock
// that its factory read, and nil where the factory read none.
type memory struct {
	clock  assert.Clock
	values map[string]string
	mu     sync.Mutex
}

// newMemory returns an empty memory.
func newMemory() *memory { return &memory{values: map[string]string{}} }

// Put stores the value under the key.
func (m *memory) Put(key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
}

// Get returns the value under the key, and whether the memory has one.
func (m *memory) Get(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, found := m.values[key]
	return value, found
}

// forgetful is a Store that drops every value, the planted defect of the
// check of the specs.
type forgetful struct{}

// Put drops the value.
func (forgetful) Put(string, string) {}

// Get finds no value.
func (forgetful) Get(string) (string, bool) { return "", false }

// The callables of Store.
const (
	storeGet conform.Callable[Store] = "Store.Get"
	storePut conform.Callable[Store] = "Store.Put"
)

// keepsTheValue is the statement of the check of keeps.
const keepsTheValue = "Get returns the value that Put stored"

// childEnv is the environment variable that names the test whose child
// process runs a check that fails or that a suite excuses.
const childEnv = "CONFORM_TEST_CHILD"

// keeps returns the check that Get returns the value that Put stored. Its
// planted defect is forgetful.
func keeps() conform.Check[Store] {
	return conform.Check[Store]{
		ID:        storeGet.Check("keeps-the-value"),
		Statement: keepsTheValue,
		Apply: func(h *conform.Handle[Store]) {
			s := h.Subject()
			s.Put("k", "v")
			got, _ := s.Get("k")
			assert.Equal(h, got, "v", h.Statement())
		},
		Defect: func(*conform.Handle[Store]) Store { return forgetful{} },
	}
}

// inventive is a Store in memory that finds a value under every key, the
// planted defect of the check of misses.
type inventive struct{ *memory }

// Get returns the value under the key, and an invented value under a key
// without one.
func (s inventive) Get(key string) (string, bool) {
	if value, found := s.memory.Get(key); found {
		return value, true
	}
	return "invented", true
}

// scrambled is a Store that finds one value under every key. It fails the
// checks of keeps and of misses.
type scrambled struct{}

// Put drops the value.
func (scrambled) Put(string, string) {}

// Get finds the value scrambled.
func (scrambled) Get(string) (string, bool) { return "scrambled", true }

// missesUnknownKeys is the statement of the check of misses.
const missesUnknownKeys = "Get finds no value under a key that Put did not store"

// misses returns the check that Get finds no value under a key that Put did
// not store. Its planted defect is inventive, which passes the check of keeps.
func misses() conform.Check[Store] {
	return conform.Check[Store]{
		ID:        storeGet.Check("misses-unknown-keys"),
		Statement: missesUnknownKeys,
		Apply: func(h *conform.Handle[Store]) {
			_, found := h.Subject().Get("absent")
			assert.False(h, found, h.Statement())
		},
		Defect: func(*conform.Handle[Store]) Store { return inventive{newMemory()} },
	}
}

// scrambles is the planted defect scrambled, which two checks can share.
func scrambles(*conform.Handle[Store]) Store { return scrambled{} }

// dropsPuts is a Store over a fresh subject whose Put drops the value.
type dropsPuts struct{ Store }

// Put drops the value.
func (dropsPuts) Put(string, string) {}

// invents is a Store over a fresh subject that finds a value under every key.
type invents struct{ Store }

// Get returns the value under the key, and an invented value under a key
// without one.
func (s invents) Get(key string) (string, bool) {
	if value, found := s.Store.Get(key); found {
		return value, true
	}
	return "invented", true
}

// derivedKeeps returns the check of keeps as a derived check of the claim
// read-after-write. It sets the reason unobserved-write before its decisive
// assertion, and its planted defect drops each Put of a fresh subject.
func derivedKeeps() conform.Check[Store] {
	c := keeps()
	c.Claim, c.Probe, c.Reason = spec.ReadAfterWrite, spec.WriteThenRead, spec.UnobservedWrite
	apply := c.Apply
	c.Apply = func(h *conform.Handle[Store]) {
		h.Expect(spec.UnobservedWrite)
		apply(h)
	}
	c.Defect = func(h *conform.Handle[Store]) Store { return dropsPuts{h.Fresh()} }
	return c
}

// derivedMisses returns the check of misses as a derived check of the claim
// zero-on-error. It sets the reason value-beside-error before its decisive
// assertion, and its planted defect invents a value over a fresh subject.
func derivedMisses() conform.Check[Store] {
	c := misses()
	c.Claim, c.Probe, c.Reason = spec.ZeroOnError, spec.ZeroBesideError, spec.ValueBesideError
	apply := c.Apply
	c.Apply = func(h *conform.Handle[Store]) {
		h.Expect(spec.ValueBesideError)
		apply(h)
	}
	c.Defect = func(h *conform.Handle[Store]) Store { return invents{h.Fresh()} }
	return c
}

// keepsExactly is the statement of the check of derivedExact.
const keepsExactly = "Get returns the value that Put stored and nothing under another key"

// derivedExact returns a derived check that Get returns the value that Put
// stored and finds no value under another key, whose law implies the law of
// derivedMisses. Its planted defect drops each Put of a fresh subject.
func derivedExact() conform.Check[Store] {
	return conform.Check[Store]{
		ID:        storeGet.Check("keeps-exactly"),
		Statement: keepsExactly,
		Claim:     spec.ReadAfterWrite, Probe: spec.WriteThenRead, Reason: spec.UnobservedWrite,
		Entails: []conform.ID[Store]{storeGet.Check("misses-unknown-keys")},
		Apply: func(h *conform.Handle[Store]) {
			h.Expect(spec.UnobservedWrite)
			s := h.Subject()
			s.Put("k", "v")
			got, _ := s.Get("k")
			assert.Equal(h, got, "v", h.Statement())
			_, found := s.Get("absent")
			assert.False(h, found, h.Statement())
		},
		Defect: func(h *conform.Handle[Store]) Store { return dropsPuts{h.Fresh()} },
	}
}

// keepsEveryValue is the statement of the property check of keepsEvery.
const keepsEveryValue = "Get returns the value that Put stored under any key"

// keepsEvery returns the property check that Get returns the value that Put
// stored under a key of each case. Its planted defect is forgetful.
func keepsEvery() conform.Check[Store] {
	return conform.Check[Store]{
		ID:        storeGet.Check("keeps-every-value"),
		Statement: keepsEveryValue,
		Property: func(h *conform.Handle[Store], c *prop.Case) {
			key := c.Draw(prop.String(), "key")
			s := h.Subject()
			s.Put(key, "v")
			got, _ := s.Get(key)
			assert.Equal(h, got, "v", h.Statement())
		},
		Defect: func(*conform.Handle[Store]) Store { return forgetful{} },
	}
}

// unseen is the label of a share that no case of keepsEvery classifies.
const unseen = "a label that no case classifies"

// requiring returns the property check of keepsEvery with options that require
// half of its cases to classify unseen. An application of the check fails,
// because no case classifies the label.
func requiring() conform.Check[Store] {
	c := keepsEvery()
	c.Options = func(*conform.Handle[Store]) []prop.Option { return []prop.Option{prop.Require(unseen, 0.5)} }
	return c
}

// countedEvery returns the property check of keepsEvery as a check that counts
// allocations. Its planted defect forgetful also fails the check of keeps.
func countedEvery() conform.Check[Store] {
	c := keepsEvery()
	c.Counted = true
	return c
}

// kind is a type over int, of which a suite declares two values.
type kind int

// declared is the statement of the property check of declaredKinds.
const declared = "the kind is one of the declared kinds"

// declaredKinds returns a suite with a property check that every kind that its
// cases draw is 1 or 2. It overrides the generator of kind through Using.
func declaredKinds() conform.Suite[Store] {
	c := keepsEvery()
	property := c.Property
	c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
		k := pc.Draw(prop.Of[kind](), "kind")
		assert.True(h, k == 1 || k == 2, declared)
		property(h, pc)
	}
	return conform.Suite[Store]{
		Declaration: "Store",
		Checks:      []conform.Check[Store]{c},
		Using:       []prop.Option{prop.Using(prop.SampledFrom[kind](1, 2))},
	}
}

// stated is the kind that the option Using of the specs states. The overrides
// of declaredKinds, declaredBudget and statedKinds do not declare it.
const stated kind = 3

// statedKinds returns a suite with a property check that every kind that its
// cases draw is stated. It overrides the generator of kind with the kinds 1 and
// 2, so the check passes only where the option Using replaces the override.
func statedKinds() conform.Suite[Store] {
	c := keepsEvery()
	property := c.Property
	c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
		assert.Equal(h, pc.Draw(prop.Of[kind](), "kind"), stated, "the case draws the kind of the option Using")
		property(h, pc)
	}
	return conform.Suite[Store]{
		Declaration: "Store",
		Checks:      []conform.Check[Store]{c},
		Using:       []prop.Option{prop.Using(prop.SampledFrom[kind](1, 2))},
	}
}

// spender is a Store whose Get calls spend and finds no value, the planted
// defect of a ceiling of Get.
type spender struct{ spend func() }

// Put drops the value.
func (spender) Put(string, string) {}

// Get calls spend and finds no value.
func (s spender) Get(string) (string, bool) {
	s.spend()
	return "", false
}

// getCalls generates calls of Get with a key of each case.
var getCalls = prop.Composite(func(c *prop.Case) func(context.Context, Store) error {
	key := c.Draw(prop.String(), "key")
	return func(_ context.Context, s Store) error {
		_, _ = s.Get(key)
		return nil
	}
})

// declaredBudget returns a suite of Store with an allocation ceiling of Get,
// whose calls draw a kind. It sets undeclared when a call draws a kind other
// than 1 and 2, the kinds of its override of the generator of kind.
func declaredBudget(undeclared *atomic.Bool) conform.Suite[Store] {
	suite := budgeted(conform.Budget{Allocs: new(uint64(0))})
	suite.Calls[0].Calls = prop.Composite(func(c *prop.Case) func(context.Context, Store) error {
		if k := c.Draw(prop.Of[kind](), "kind"); k != 1 && k != 2 {
			undeclared.Store(true)
		}
		return func(_ context.Context, s Store) error {
			_, _ = s.Get("k")
			return nil
		}
	})
	suite.Using = []prop.Option{prop.Using(prop.SampledFrom[kind](1, 2))}
	return suite
}

// counted returns a call generator of Get that counts each call in calls.
func counted(calls *atomic.Int32) prop.Generator[func(context.Context, Store) error] {
	return prop.Composite(func(c *prop.Case) func(context.Context, Store) error {
		key := c.Draw(prop.String(), "key")
		return func(_ context.Context, s Store) error {
			calls.Add(1)
			_, _ = s.Get(key)
			return nil
		}
	})
}

// skipUnchecked skips t in a run that does not check allocation ceilings,
// where every subject passes a ceiling.
func skipUnchecked(t *testing.T) {
	t.Helper()
	if checked, reason := assert.AllocsChecked(); !checked {
		t.Skipf("the run does not check allocation ceilings: %s", reason)
	}
}

// wasteful is a Store whose Get allocates three times per call.
type wasteful struct{}

// Put drops the value.
func (wasteful) Put(string, string) {}

// Get allocates three copies of the key, and finds the key as its value.
func (wasteful) Get(key string) (string, bool) {
	for range 3 {
		kept.Store(new(key))
	}
	return key, true
}

// kept keeps each allocation of wasteful on the heap.
var kept atomic.Pointer[string]

// squander is a factory of wasteful subjects.
func squander(*conform.Handle[Store]) Store { return wasteful{} }

// budgeted returns a suite of Store with the call of Get under budget.
func budgeted(budget conform.Budget) conform.Suite[Store] {
	return conform.Suite[Store]{
		Declaration: "Store",
		Calls: []conform.Call[Store]{{
			Callable: storeGet,
			Calls:    getCalls,
			Budget:   budget,
			Over:     func(_ *conform.Handle[Store], spend func()) Store { return spender{spend: spend} },
		}},
	}
}

// everySuite returns a suite of Store with the property check of keepsEvery.
func everySuite() conform.Suite[Store] {
	return conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{keepsEvery()}}
}

// sendsPerCase is the number of values that keepsAfterSends and sendCalls send
// to the send channel of each case.
const sendsPerCase = 100

// keepsAfterSends returns the property check that Get returns the value that
// Put stored, after the case sends sendsPerCase values to a send channel that
// it draws. The draw fails without the sink of the channel, and a send blocks
// without a goroutine that receives it. Its planted defect is forgetful.
func keepsAfterSends() conform.Check[Store] {
	return conform.Check[Store]{
		ID:        storeGet.Check("keeps-after-sends"),
		Statement: keepsTheValue,
		Property: func(h *conform.Handle[Store], pc *prop.Case) {
			out := pc.Draw(prop.Of[chan<- int](), "out")
			for i := range sendsPerCase {
				out <- i
			}
			s := h.Subject()
			s.Put("k", "v")
			got, _ := s.Get("k")
			assert.Equal(h, got, "v", h.Statement())
		},
		Defect: forget,
	}
}

// sinkSuite returns a suite of Store with the check of keepsAfterSends and the
// sink of chan<- int.
func sinkSuite() conform.Suite[Store] {
	return conform.Suite[Store]{
		Declaration: "Store",
		Checks:      []conform.Check[Store]{keepsAfterSends()},
		Sinks:       []conform.Sink{conform.SinkOf[chan<- int]()},
	}
}

// sendCalls generates calls of Get that send sendsPerCase values to a send
// channel of each case before they call Get.
var sendCalls = prop.Composite(func(c *prop.Case) func(context.Context, Store) error {
	out := c.Draw(prop.Of[chan<- int](), "out")
	return func(_ context.Context, s Store) error {
		for i := range sendsPerCase {
			out <- i
		}
		_, _ = s.Get("k")
		return nil
	}
})

// sinkBudget returns the suite of budgeted with the calls of sendCalls and the
// sink of chan<- int.
func sinkBudget(budget conform.Budget) conform.Suite[Store] {
	suite := budgeted(budget)
	suite.Calls[0].Calls = sendCalls
	suite.Sinks = []conform.Sink{conform.SinkOf[chan<- int]()}
	return suite
}

// detailOf returns the field of the detail of the call record of the first
// assertion that r recorded.
func detailOf(t *testing.T, r *assert.Recorder, field string) any {
	t.Helper()
	records := r.Records()
	assert.NotEmpty(t, records, "the recorder keeps the call record of the property run")
	var record struct {
		Detail map[string]any `json:"detail"`
	}
	assert.NoError(t, json.Unmarshal([]byte(records[0]), &record), "the call record is JSON")
	return record.Detail[field]
}

// newStore is the factory of the subjects of the specs.
func newStore(*conform.Handle[Store]) Store { return newMemory() }

// broken returns a suite of Store with the check of keeps after change.
func broken(change func(c *conform.Check[Store])) conform.Suite[Store] {
	c := keeps()
	change(&c)
	return conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}}
}

// forget is a factory of forgetful subjects, on which keeps fails.
func forget(*conform.Handle[Store]) Store { return forgetful{} }

// recorded runs fn on a recorder that ends fn at its first fatal failure,
// and returns the recorder once fn has ended.
func recorded(fn func(tb assert.TB)) *assert.Recorder {
	r := assert.NewRecorder().WithGoexit()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(r)
	}()
	<-done
	return r
}

// bubbled returns the check c inside the bubble of synctest.Test. Its body
// first asserts that synctest.Wait returns, which panics outside a bubble.
func bubbled(c conform.Check[Store]) conform.Check[Store] {
	c.Around = synctest.Test
	if apply := c.Apply; apply != nil {
		c.Apply = func(h *conform.Handle[Store]) {
			assert.NotPanics(h, synctest.Wait, "the check runs inside a bubble")
			apply(h)
		}
		return c
	}
	property := c.Property
	c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
		assert.NotPanics(h, synctest.Wait, "the case runs inside a bubble")
		property(h, pc)
	}
	return c
}

// applying is a test of an application that records the errors and the
// tests of the application, and ends the goroutine of the application at
// FailNow. Its Run records the name of the test and does not run it.
type applying struct {
	errors []string
	tests  []string
	mu     sync.Mutex
}

// Helper does nothing.
func (*applying) Helper() {}

// Errorf records the error.
func (a *applying) Errorf(format string, args ...any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errors = append(a.errors, fmt.Sprintf(format, args...))
}

// FailNow ends the goroutine of the application.
func (*applying) FailNow() { runtime.Goexit() }

// Run records the name of the test.
func (a *applying) Run(name string, _ func(t *testing.T)) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tests = append(a.tests, name)
	return true
}

// application runs apply under an applying test, and returns the test once
// apply has ended.
func application(apply func(t conform.T)) *applying {
	a := &applying{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		apply(a)
	}()
	<-done
	return a
}

// metaWritten writes the coverage meta-data of the test binary once, into the
// directory of its coverage, before the first child process runs. A child
// that finds the meta-data there writes none of its own. Two children that
// write it in one nanosecond write one temporary file, because the runtime
// names that file by the time alone.
var metaWritten sync.Once

// childOutput runs the test of t alone in a child process of the test
// binary, with childEnv set to the name of the test, and returns the output
// of the child process. The child process runs in a temporary directory, so
// the store of a failing property run writes no file into the package.
func childOutput(t *testing.T) string {
	t.Helper()
	return childOutputWith(t)
}

// childOutputWith runs the test of t as childOutput does, with the variables
// env added to the environment of the child process.
func childOutputWith(t *testing.T, env ...string) string {
	t.Helper()
	return childRun(t, t.Name(), env...)
}

// childRun runs the test name alone in a child process of the test binary,
// with childEnv set to name and the variables env added to the environment
// of the child process, and returns the output of the child process. The
// child process runs in a temporary directory, as childOutput states. In a
// run with coverage, the child writes its coverage where the parent writes
// its own.
func childRun(t *testing.T, name string, env ...string) string {
	t.Helper()
	binary, err := os.Executable()
	assert.NoError(t, err, "the test binary has a path")
	levels := strings.Split(name, "/")
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
	child.Env = append(append(os.Environ(), childEnv+"="+name), env...)
	out, _ := child.CombinedOutput()
	t.Logf("the output of the child process:\n%s", out)
	return string(out)
}
