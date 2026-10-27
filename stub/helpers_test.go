// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/stub"
)

// The name of the method of the specs, which a failure contains.
const getName = "Store.Get"

// suiteEnv is the environment variable that names the test whose child
// process runs a suite against a broken double.
const suiteEnv = "STUB_TEST_SUITE"

// call is a call of Store.Get: its key and the error that the double
// returned.
type call struct {
	Err error
	Key string
}

// ret are the results of Store.Get.
type ret struct {
	Err   error
	Value string
}

// errTest is the error that the specs arm.
var errTest = errors.New("stub_test: the armed error")

// origin is the first instant of the controlled clocks of the specs.
var origin = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// arms returns the arms of Store.Get with the fault and the stamp, as a
// generated double writes them.
func arms() stub.Arms[call, ret] {
	return stub.Arms[call, ret]{
		Fault: func(err error) ret { return ret{Err: err} },
		Stamp: func(c call, r ret) call {
			c.Err = r.Err
			return c
		},
	}
}

// fake is a double of Store.Get as a generated double writes it: the stub of
// the method, a function, the default results, and a method that returns its
// results through stub.Answer.
type fake struct {
	on       *stub.MethodStub[call]
	fn       func(key string) error
	defaults func() ret
}

// newFake returns a fake that reports to tb.
func newFake(tb assert.TB) *fake {
	return &fake{on: stub.NewMethodStub[call](tb, getName)}
}

// Get returns the value and the error of the results of the call of key.
func (f *fake) Get(key string) (string, error) {
	a := arms()
	if f.fn != nil {
		a.Invoke = func() ret { return ret{Err: f.fn(key)} }
	}
	a.Default = f.defaults
	r := stub.Answer(f.on, call{Key: key}, a)
	return r.Value, r.Err
}

// get calls Store.Get with key through s and the arms of a double without a
// function and a fixed result, and returns the error of the results.
func get(s *stub.MethodStub[call], key string) error {
	return stub.Answer(s, call{Key: key}, arms()).Err
}

// assertFailedChecks runs the test of t alone in a child process of the test
// binary, with suiteEnv set to the name of the test. The test runs a suite
// against a broken double there. assertFailedChecks checks that exactly the
// checks that want names fail in the child process.
func assertFailedChecks(t *testing.T, want ...string) {
	t.Helper()
	levels := strings.Split(t.Name(), "/")
	for i, level := range levels {
		levels[i] = "^" + regexp.QuoteMeta(level) + "$"
	}
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run="+strings.Join(levels, "/"), "-test.timeout=1m")
	child.Env = append(os.Environ(), suiteEnv+"="+t.Name())
	out, err := child.CombinedOutput()
	t.Logf("the output of the child process:\n%s", out)
	exit := assert.ErrorAs[*exec.ExitError](t, err, "the suite fails the broken double")
	assert.Equal(t, exit.ExitCode(), 1, "the child process ends with the code of a failed test")
	var failed []string
	prefix := "--- FAIL: " + t.Name() + "/"
	for line := range strings.Lines(string(out)) {
		rest, found := strings.CutPrefix(strings.TrimSpace(line), prefix)
		check, _, _ := strings.Cut(rest, " (")
		if found && !strings.Contains(check, "/") {
			failed = append(failed, check)
		}
	}
	assert.Permutation(t, failed, want, "the suite fails the checks that the broken double breaks")
}
