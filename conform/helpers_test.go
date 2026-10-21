// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
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

// childOutput runs the test of t alone in a child process of the test
// binary, with childEnv set to the name of the test, and returns the output
// of the child process.
func childOutput(t *testing.T) string {
	t.Helper()
	levels := strings.Split(t.Name(), "/")
	for i, level := range levels {
		levels[i] = "^" + regexp.QuoteMeta(level) + "$"
	}
	child := exec.CommandContext(t.Context(), os.Args[0],
		"-test.run="+strings.Join(levels, "/"), "-test.v", "-test.timeout=1m")
	child.Env = append(os.Environ(), childEnv+"="+t.Name())
	out, _ := child.CombinedOutput()
	t.Logf("the output of the child process:\n%s", out)
	return string(out)
}
