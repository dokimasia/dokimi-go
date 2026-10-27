// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"os"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// fuzzMode is the variable of the environment that names the mode of
// FuzzChild. It is empty outside a child process.
const fuzzMode = "CONFORM_TEST_FUZZ_MODE"

// noPropertyCheck is the reason of the skip of a fuzz target of Get without a
// property check to fuzz.
const noPropertyCheck = "Store.Get has no property check that the application fuzzes"

// TestFuzzProcess checks the fuzz target that Fuzz registers. Fuzz takes a
// *testing.F, which only the testing package constructs, so each case runs
// FuzzChild in a child process of the test binary. The children run one at a
// time, because two children of a coverage run that exit in the same
// nanosecond write one coverage file.
func TestFuzzProcess(t *testing.T) {
	t.Run("Fuzz", func(t *testing.T) {
		t.Run("runs a property check of the callable on each input", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=failing")
			expect.Contains(t, out, "--- FAIL: FuzzChild/seed#0", "the input fails")
			expect.Contains(t, out, `check: "Store.Get/keeps-every-value"`, "the case draws the identity of the check")
			expect.Contains(t, out, keepsEveryValue, "the failure is the failure of the check")
		})

		t.Run("runs a property check that the option With adds", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=added")
			expect.Contains(t, out, "--- FAIL: FuzzChild/seed#0", "the input fails")
			expect.Contains(t, out, keepsEveryValue, "the failure is the failure of the added check")
		})

		t.Run("builds a fresh subject for each input", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=counting")
			expect.Contains(t, out, "--- PASS: FuzzChild/seed#2", "the target runs the third input")
			expect.Contains(t, out, "the factory built 3 subjects", "the factory builds one subject for each input")
		})

		t.Run("passes the overrides of the suite to each input", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=using")
			expect.Contains(t, out, "--- PASS: FuzzChild/seed#0", "the input draws a declared kind")
		})

		t.Run("passes the generators of the option Using after the overrides of the suite", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=stated")
			expect.Contains(t, out, "--- PASS: FuzzChild/seed#0", "the input draws the kind of the option")
		})

		t.Run("passes the sinks of the suite to each input", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=sinks")
			expect.Contains(t, out, "--- PASS: FuzzChild/seed#0", "the input sends to the sink of the suite")
		})

		t.Run("skips a callable without a property check", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=apply")
			expect.Contains(t, out, "--- SKIP: FuzzChild", "the fuzz test skips")
			expect.Contains(t, out, noPropertyCheck, "the skip states the reason")
		})

		t.Run("skips a callable whose property checks the options excuse", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=excused")
			expect.Contains(t, out, "--- SKIP: FuzzChild", "the fuzz test skips")
			expect.Contains(t, out, noPropertyCheck, "the skip states the reason")
		})

		t.Run("leaves out a property check that counts allocations", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=counted")
			expect.Contains(t, out, "--- SKIP: FuzzChild", "the fuzz test skips")
			expect.Contains(t, out, noPropertyCheck, "the skip states the reason")
		})

		t.Run("leaves out the property checks of another callable", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=other")
			expect.Contains(t, out, "--- SKIP: FuzzChild", "the fuzz test skips")
			expect.Contains(t, out, noPropertyCheck, "the skip states the reason")
		})

		t.Run("reports each problem of the application", func(t *testing.T) {
			out := childRun(t, "FuzzChild", fuzzMode+"=problem")
			expect.Contains(t, out, "--- FAIL: FuzzChild", "the fuzz test fails")
			expect.Contains(t, out, "the excusal of Store.Put/keeps-the-value matches no check of FuzzStoreSuite.",
				"the failure states the problem")
		})
	})
}

// FuzzChild runs Fuzz in a child process of TestFuzzProcess, in the mode that
// CONFORM_TEST_FUZZ_MODE names. It skips outside a child process.
func FuzzChild(f *testing.F) {
	mode := os.Getenv(fuzzMode)
	if mode == "" {
		f.Skip("runs in a child process of TestFuzzProcess")
	}
	fuzzModes[mode](f)
}

// fuzzModes are the modes of FuzzChild. Each registers a fuzz target of
// Store.Get on f.
var fuzzModes = map[string]func(f *testing.F){
	// failing fuzzes keepsEvery on forgetful subjects from one seed.
	"failing": func(f *testing.F) {
		f.Helper()
		f.Add([]byte{})
		conform.Fuzz(f, everySuite(), storeGet, forget)
	},
	// added fuzzes keepsEvery, which the option With adds, on forgetful
	// subjects from one seed.
	"added": func(f *testing.F) {
		f.Helper()
		f.Add([]byte{})
		conform.Fuzz(f, conform.Suite[Store]{Declaration: "Store"}, storeGet, forget, conform.With(keepsEvery()))
	},
	// counting fuzzes keepsEvery from three seeds, and logs the number of
	// subjects that the factory builds.
	"counting": func(f *testing.F) {
		f.Helper()
		f.Add([]byte{})
		f.Add([]byte{1})
		f.Add([]byte{2})
		var built atomic.Int32
		conform.Fuzz(f, everySuite(), storeGet, func(*conform.Handle[Store]) Store {
			built.Add(1)
			return newMemory()
		})
		f.Logf("the factory built %d subjects", built.Load())
	},
	// using fuzzes the property check of declaredKinds from one seed. The check
	// fails for a kind that the override of the suite does not declare.
	"using": func(f *testing.F) {
		f.Helper()
		f.Add([]byte{})
		conform.Fuzz(f, declaredKinds(), storeGet, newStore)
	},
	// stated fuzzes the property check of statedKinds from one seed under the
	// option Using. The check fails for a kind other than the kind of the
	// option.
	"stated": func(f *testing.F) {
		f.Helper()
		f.Add([]byte{})
		conform.Fuzz(f, statedKinds(), storeGet, newStore, conform.Using[Store](prop.Just(stated)))
	},
	// sinks fuzzes the property check of sinkSuite from one seed. The draw of
	// the send channel of the check fails without the sink of the suite.
	"sinks": func(f *testing.F) {
		f.Helper()
		f.Add([]byte{})
		conform.Fuzz(f, sinkSuite(), storeGet, newStore)
	},
	// apply fuzzes a suite whose check of Get has a function Apply.
	"apply": func(f *testing.F) {
		f.Helper()
		suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{keeps()}}
		conform.Fuzz(f, suite, storeGet, newStore)
	},
	// excused fuzzes keepsEvery under its excusal.
	"excused": func(f *testing.F) {
		f.Helper()
		conform.Fuzz(f, everySuite(), storeGet, newStore,
			conform.Excuse(storeGet.Check("keeps-every-value"), "the store keeps no values"))
	},
	// counted fuzzes a suite whose property check counts allocations.
	"counted": func(f *testing.F) {
		f.Helper()
		suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{countedEvery()}}
		conform.Fuzz(f, suite, storeGet, newStore)
	},
	// other fuzzes a suite whose property check is a check of Put.
	"other": func(f *testing.F) {
		f.Helper()
		c := keepsEvery()
		c.ID = storePut.Check("keeps-every-value")
		suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{c}}
		conform.Fuzz(f, suite, storeGet, newStore)
	},
	// problem fuzzes keepsEvery under an excusal that matches no check.
	"problem": func(f *testing.F) {
		f.Helper()
		conform.Fuzz(f, everySuite(), storeGet, newStore,
			conform.Excuse(storePut.Check("keeps-the-value"), "the store keeps no values"))
	},
}
