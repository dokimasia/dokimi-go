// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"runtime/debug"
	"strconv"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// suiteOf returns a suite of Store with the checks, and with the package, the
// generator, a refusal and an unclaimed classification that a generated suite
// states.
func suiteOf(checks ...conform.Check[Store]) conform.Suite[Store] {
	return conform.Suite[Store]{
		Declaration: "Store",
		Package:     "example.com/store",
		Generator:   conform.Generator{Name: "dokimi", Version: "0.1.0"},
		Checks:      checks,
		Refusals: []conform.Refusal{{
			Claim: spec.ZeroOnError, Callable: "Store.Put", Why: "Put cannot fail", Remedy: "return an error",
		}},
		Unclaimed: []conform.Unclaimed{{
			Classification: "mixin:concurrent", Callable: "Store.Get", Stance: spec.Needs, Note: "needs callers",
		}},
	}
}

// resultOf returns the result of the check id in the record r, and the empty
// string for a record without the check.
func resultOf(r record, id string) string {
	for _, c := range r.Checks {
		if c.Identity == id {
			return c.Result
		}
	}
	return ""
}

// identities returns the identity of each check of the record r, in its
// order.
func identities(r record) []string {
	ids := make([]string, len(r.Checks))
	for i, c := range r.Checks {
		ids[i] = c.Identity
	}
	return ids
}

// failingSeed matches the seed that the failure of a property run reports.
var failingSeed = regexp.MustCompile(`seed (\d+)`)

// meanAlone is the problem of a budget of Store.Get with a mean and no p99.
const meanAlone = "the budget of Store.Get states a mean and no p99, and a mean alone passes a tail that grows"

func TestRecord(t *testing.T) {
	t.Parallel()

	t.Run("Run", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the falsification record and the subject record of the application", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, suiteOf(derivedKeeps(), keepsEvery()), newStore, conform.With(misses()))
				return
			}
			records := recordsOf(t, childOutput(t))

			assert.Length(t, records, 2, "the application writes two records")
			falsified, judged := records[0], records[1]
			assert.Equal(t, []string{falsified.Application, judged.Application}, []string{"falsification", "subject"},
				"the falsification record comes first")
			assert.Equal(t, judged.Spec, spec.Version(), "the record names the version of the spec")
			assert.Equal(t, judged.Suite.Language+" "+judged.Suite.Package+" "+judged.Suite.Declaration,
				"go example.com/store Store", "the record names the suite")
			assert.Equal(t, judged.Generator.Name+" "+judged.Generator.Version, "dokimi 0.1.0",
				"the record names the generator of the suite")
			assert.Equal(t, judged.Runtime.Name, "go.dokimi.dev/conform", "the record names the runtime")
			info, _ := debug.ReadBuildInfo()
			assert.Equal(t, judged.Runtime.Version, info.Main.Version,
				"the runtime has the version of the module of conform in the build")
			assert.Matches(t, judged.Seed, `^0x[0-9a-f]{16}$`, "the seed has 16 hexadecimal digits")
			assert.Equal(t, judged.Subject, "conform_test.newStore", "the subject is the name of the factory")
			assert.Empty(t, falsified.Subject, "a falsification record names no subject")
			assert.Equal(t, judged.Refusals, []map[string]string{
				{
					"claim":    "zero-on-error",
					"callable": "Store.Put",
					"why":      "Put cannot fail",
					"remedy":   "return an error",
				},
			}, "the record has the refusals of the suite")
			assert.Equal(t, judged.Unclaimed, []map[string]string{
				{
					"classification": "mixin:concurrent",
					"callable":       "Store.Get",
					"stance":         "needs",
					"note":           "needs callers",
				},
			}, "the record has the unclaimed classifications of the suite")

			assert.Equal(
				t,
				identities(falsified),
				[]string{"Store.Get/keeps-the-value", "Store.Get/misses-unknown-keys"},
				"falsification has the derived check and the check that the options add",
			)
			assert.Equal(t, []string{falsified.Checks[0].Result, falsified.Checks[1].Result},
				[]string{"proven", "proven"}, "each defect proves its check")
			assert.Equal(t, falsified.Checks[0].Claim, "read-after-write", "the record has the claim of the check")
			assert.Equal(
				t,
				falsified.Checks[0].Key,
				"store.get/keeps-the-value",
				"the key is the key form of the identity",
			)
			assert.Equal(t, identities(judged), []string{
				"Store.Get/keeps-the-value", "Store.Get/keeps-every-value", "Store.Get/misses-unknown-keys",
			}, "the subject record has each check in the order of the application")
			assert.Equal(t, resultOf(judged, "Store.Get/keeps-the-value"), "pass", "the derived check passes")
			assert.False(t, judged.Checks[0].Authored, "a derived check is not authored")
			assert.False(t, judged.Checks[0].Shallow, "the derived check witnesses the depth of its law")
			assert.Empty(t, judged.Checks[0].Seed, "a check that runs once has no seed")
			assert.True(t, judged.Checks[1].Authored, "a check of the consumer is authored")
			assert.Equal(t, judged.Checks[1].Seed, judged.Seed, "a property check runs at the seed of the application")
		})

		t.Run("records the result of each check on the subject", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				fails := keeps()
				fails.ID = storeGet.Check("keeps-another-value")
				fails.Apply = func(h *conform.Handle[Store]) {
					got, _ := h.Subject().Get("k")
					assert.Equal(h, got, "w", h.Statement())
				}
				excused := keeps()
				excused.ID = storePut.Check("keeps-the-value")
				reasoned := derivedKeeps()
				reasoned.ID = storeGet.Check("keeps-with-a-reason")
				reasoned.Apply = func(h *conform.Handle[Store]) {
					h.Engage()
					h.Expect(spec.UnobservedWrite)
					assert.True(h, false, h.Statement())
				}
				conform.Run(t, suiteOf(vacuous(), fails, excused, skipping(), reasoned), newStore,
					conform.Excuse(excused.ID, "Put has no check of its own"))
				return
			}
			judged := recordsOf(t, childOutput(t))[1]

			assert.Equal(t, resultOf(judged, "Store.Get/misses-unknown-keys"), "vacuous", "the check never engages")
			assert.Equal(t, resultOf(judged, "Store.Get/keeps-another-value"), "fail", "the check fails")
			assert.Equal(t, judged.Checks[1].Failure["contract"], any(keepsTheValue),
				"a failed check has its failed assertion")
			assert.Equal(t, resultOf(judged, "Store.Put/keeps-the-value"), "excused", "the check is excused")
			assert.Equal(t, judged.Checks[2].Why, "Put has no check of its own", "an excusal has its reason")
			assert.Equal(t, resultOf(judged, "Store.Get/needs-direct-io"), "skipped", "the check skips")
			assert.Equal(t, judged.Checks[3].Why, "the file system has no direct I/O", "a skip has its reason")
			assert.Equal(t, resultOf(judged, "Store.Get/keeps-with-a-reason"), "fail", "the derived check fails")
			assert.Equal(t, judged.Checks[4].Reason, "unobserved-write", "a failed derived check has its reason")
		})

		t.Run("records the result of each falsification", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				passes := derivedKeeps()
				passes.Defect = func(h *conform.Handle[Store]) Store { return h.Fresh() }
				unengaged := vacuous()
				unengaged.Defect = newStore
				wrong := derivedMisses()
				wrong.ID, wrong.Reason = storeGet.Check("misses-with-another-reason"), spec.UnobservedWrite
				faulted := derivedKeeps()
				faulted.ID = storeGet.Check("keeps-without-a-door")
				faulted.Defect = func(h *conform.Handle[Store]) Store { return h.Reference() }
				conform.Run(t, suiteOf(passes, unengaged, wrong, faulted), newStore)
				return
			}
			falsified := recordsOf(t, childOutput(t))[0]

			assert.Equal(t, resultOf(falsified, "Store.Get/keeps-the-value"), "passed", "the defect passes the check")
			assert.Equal(t, resultOf(falsified, "Store.Get/misses-unknown-keys"), "vacuous",
				"the defect never engages the check")
			assert.Equal(t, resultOf(falsified, "Store.Get/misses-with-another-reason"), "wrong-reason",
				"the defect fails the check with another reason")
			assert.Equal(t, falsified.Checks[2].Reason, "value-beside-error", "the record has the reason of the probe")
			assert.Equal(t, falsified.Checks[2].Failure["contract"], any(missesUnknownKeys),
				"the record has the failed assertion")
			assert.Equal(t, resultOf(falsified, "Store.Get/keeps-without-a-door"), "unchecked",
				"a fault ends the falsification without a verdict")
			assert.Equal(t, falsified.Checks[3].Why, "the check ended with a fault before its verdict",
				"the record has the reason")
		})

		t.Run("records the first other check of an isolation", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				scrambling := derivedMisses()
				scrambling.Defect = scrambles
				again := derivedKeeps()
				again.ID = storeGet.Check("keeps-the-value-again")
				again.Defect = func(*conform.Handle[Store]) Store { return forgetful{} }
				conform.Run(t, suiteOf(derivedKeeps(), scrambling, again), newStore, conform.Isolate[Store]())
				return
			}
			falsified := recordsOf(t, childOutput(t))[0]

			assert.Equal(t, resultOf(falsified, "Store.Get/misses-unknown-keys"), "not-isolated",
				"the defect fails other checks")
			assert.Equal(t, falsified.Checks[1].Other, "Store.Get/keeps-the-value",
				"the record names the first check that fails")
		})

		t.Run("records the first shallower check of an edge", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				again := derivedMisses()
				again.ID = storeGet.Check("misses-again")
				again.Defect = func(h *conform.Handle[Store]) Store { return invents{h.Fresh()} }
				deep := derivedExact()
				deep.Entails = []conform.ID[Store]{storeGet.Check("misses-unknown-keys"), again.ID}
				deep.Defect = func(h *conform.Handle[Store]) Store { return invents{dropsPuts{h.Fresh()}} }
				conform.Run(t, suiteOf(derivedMisses(), again, deep), newStore, conform.Isolate[Store]())
				return
			}
			falsified := recordsOf(t, childOutput(t))[0]

			assert.Equal(t, resultOf(falsified, "Store.Get/keeps-exactly"), "equivalent",
				"the shallower checks fail on the defect of the deeper check")
			assert.Equal(t, falsified.Checks[2].Other, "Store.Get/misses-unknown-keys",
				"the record names the first shallower check")
		})

		t.Run("records a hexadecimal seed of DOKIMI_SEED", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, suiteOf(keepsEvery()), newStore)
				return
			}
			judged := recordsOf(t, childOutputWith(t, "DOKIMI_SEED=0x1f"))[1]

			assert.Equal(t, judged.Seed, "0x000000000000001f", "the record has the seed of DOKIMI_SEED")
			assert.Equal(t, judged.Checks[0].Seed, "0x000000000000001f", "the property check runs at that seed")
		})

		t.Run("records the seed that the property engine chooses for the application", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, suiteOf(keepsEvery()), newStore)
				return
			}
			judged := recordsOf(t, childOutputWith(t, "DOKIMI_ASSERT_PROP_SEED=7"))[1]

			assert.Equal(t, judged.Seed, "0x0000000000000007", "the record has the seed of the property engine")
		})

		t.Run("runs each property check at the seed of its record", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, suiteOf(keepsEvery()), forget)
				return
			}
			out := childOutput(t)
			judged := recordsOf(t, out)[1]
			m := failingSeed.FindStringSubmatch(out)
			assert.NotEmpty(t, m, "the failure of the property run reports its seed")
			seed, err := strconv.ParseUint(m[1], 10, 64)
			assert.NoError(t, err, "the seed of the failure is a number")

			assert.Equal(t, fmt.Sprintf("0x%016x", seed), judged.Seed, "the property run takes the seed of the record")
			assert.Equal(t, resultOf(judged, "Store.Get/keeps-every-value"), "fail", "the property check fails")
		})

		t.Run("records the key form of a name of several words", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				all := keeps()
				all.ID = conform.Callable[Store]("Store.GetAll").Check("keeps-the-value")
				conform.Run(t, suiteOf(all), newStore)
				return
			}
			judged := recordsOf(t, childOutput(t))[1]

			assert.Equal(t, judged.Checks[0].Key, "store.get-all/keeps-the-value",
				"the key splits the name of the callable into its words")
		})

		t.Run("records the number of cases of a property check", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, suiteOf(keepsEvery()), newStore, conform.Cases[Store](7))
				return
			}
			judged := recordsOf(t, childOutput(t))[1]

			assert.Equal(t, judged.Checks[0].Cases, 7, "the record has the number of cases of the run")
			assert.Empty(t, judged.Checks[0].Counterexample, "a property check that passes has no counterexample")
		})

		t.Run("records the counterexample and the failure of the smallest failing case", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				below := keepsEvery()
				below.ID, below.Statement = storeGet.Check("draws-a-small-number"), "the number is below 10"
				below.Property = func(h *conform.Handle[Store], c *prop.Case) {
					assert.InRange(h, c.Draw(prop.Integer(0, 1000), "n"), 0, 9, h.Statement())
				}
				conform.Run(t, suiteOf(below), newStore)
				return
			}
			judged := recordsOf(t, childOutputWith(t, "DOKIMI_SEED=1"))[1]
			data, err := json.Marshal(prop.Drawn{Label: "n", Value: 10})
			assert.NoError(t, err, "a draw has a JSON form")
			var ten map[string]any
			assert.NoError(t, json.Unmarshal(data, &ten), "the JSON form of a draw decodes")

			assert.Length(t, judged.Checks[0].Counterexample, 1, "the counterexample has the one draw of its case")
			assert.Equal(t, judged.Checks[0].Counterexample[0]["label"], any("n"), "the draw has its label")
			assert.Equal(t, judged.Checks[0].Counterexample[0]["value"], ten["value"],
				"the draw has the typed literal of the smallest failing value")
			detail, _ := judged.Checks[0].Failure["detail"].(map[string]any)
			assert.Equal(t, detail["got"], ten["value"], "the failure is the failure of the smallest failing case")
		})

		t.Run("records each draw of a label that two draws share", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				twice := keepsEvery()
				twice.ID, twice.Statement = storeGet.Check("draws-a-flag-twice"), "the check fails on every case"
				twice.Property = func(h *conform.Handle[Store], c *prop.Case) {
					c.Draw(prop.Of[bool](), "flag")
					c.Draw(prop.Of[bool](), "flag")
					assert.True(h, false, h.Statement())
				}
				conform.Run(t, suiteOf(twice), newStore)
				return
			}
			counterexample := recordsOf(t, childOutput(t))[1].Checks[0].Counterexample

			assert.Length(t, counterexample, 2, "the counterexample has both draws")
			assert.Equal(t, []any{counterexample[0]["label"], counterexample[1]["label"]}, []any{"flag", "flag"},
				"each draw has the label")
		})

		t.Run("records the problems that end the application", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, suiteOf(keeps()), newStore, conform.Cases[Store](0))
				return
			}
			records := recordsOf(t, childOutputWith(t, "DOKIMI_SEED=seven"))

			assert.Length(t, records, 2, "the application writes both records")
			assert.Equal(t, records[0].Ended, `DOKIMI_SEED is "seven", and a seed is a decimal or a 0x hexadecimal `+
				"number below 2^64\nthe option Cases asks for 0 cases, and a property run needs at least 1",
				"the falsification record has each problem")
			assert.Equal(t, records[1].Ended, records[0].Ended, "the subject record has the same problems")
			assert.Empty(t, records[1].Checks, "no check runs")
		})

		t.Run("records the fault of the seed that ends the application", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, suiteOf(keeps()), newStore)
				return
			}
			out := childOutputWith(t, "DOKIMI_ASSERT_PROP_SEED=seven")
			records := recordsOf(t, out)

			assert.Length(t, records, 2, "the application writes both records")
			assert.Contains(t, records[1].Ended, "DOKIMI_ASSERT_PROP_SEED", "the record has the fault of the seed")
			assert.Contains(t, out, records[1].Ended, "the test reports the same fault")
			assert.Empty(t, records[1].Checks, "no check runs")
		})
	})

	t.Run("Falsify", func(t *testing.T) {
		t.Parallel()

		t.Run("writes the falsification record of the checks and of the ceilings", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				suite := suiteOf(keeps())
				suite.Calls = budgeted(conform.Budget{Latency: time.Millisecond, Mean: time.Millisecond}).Calls
				conform.Falsify(t, suite)
				return
			}
			out := childOutput(t)
			records := recordsOf(t, out)

			assert.Length(t, records, 1, "falsification writes one record")
			assert.Equal(t, records[0].Application, "falsification", "the record is a falsification record")
			assert.Equal(t, identities(records[0]), []string{
				"Store.Get/keeps-the-value", "Store.Get/latency-budget", "Store.Get/mean-latency-budget",
			}, "the record has the checks and then the ceilings")
			assert.Equal(t, resultOf(records[0], "Store.Get/keeps-the-value"), "proven", "the defect proves the check")
			assert.Equal(t, resultOf(records[0], "Store.Get/latency-budget"), "proven", "the defect breaks the ceiling")
			assert.False(t, records[0].Checks[1].Authored, "a ceiling is no check of the consumer")
			assert.NotContains(t, out, "StoreSuite: checks", "falsification logs no census")
		})

		t.Run("records a ceiling whose defect passes", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				suite := suiteOf()
				suite.Calls = budgeted(conform.Budget{Latency: time.Millisecond}).Calls
				suite.Calls[0].Over = func(*conform.Handle[Store], func()) Store { return newMemory() }
				conform.Falsify(t, suite)
				return
			}
			falsified := recordsOf(t, childOutput(t))[0]

			assert.Equal(t, resultOf(falsified, "Store.Get/latency-budget"), "passed",
				"a defect that does not spend passes the ceiling")
		})

		t.Run("records why a check of the consumer fails on its defect", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				errorf := keeps()
				errorf.ID, errorf.Defect = storeGet.Check("fails-through-errorf"), forget
				errorf.Apply = func(h *conform.Handle[Store]) { h.Errorf("the store fails") }
				stops := keepsEvery()
				stops.ID, stops.Defect = storeGet.Check("stops-a-case"), forget
				stops.Property = func(h *conform.Handle[Store], _ *prop.Case) { h.Fatalf("the case stops") }
				other := keeps()
				other.ID, other.Defect = storeGet.Check("fails-another-assertion"), forget
				other.Apply = func(h *conform.Handle[Store]) { assert.Equal(h, "x", "y", "another contract") }
				skips := skipping()
				skips.Defect = forget
				failsThenSkips := keeps()
				failsThenSkips.ID, failsThenSkips.Defect = storeGet.Check("fails-then-skips"), forget
				failsThenSkips.Apply = func(h *conform.Handle[Store]) {
					got, _ := h.Subject().Get("k")
					expect.Equal(h, got, "v", h.Statement())
					h.Skipf("the check skips after it fails")
				}
				conform.Falsify(t, suiteOf(errorf, stops, other, skips, failsThenSkips))
				return
			}
			falsified := recordsOf(t, childOutput(t))[0]

			assert.Equal(t,
				[]string{falsified.Checks[0].Result, falsified.Checks[1].Result, falsified.Checks[2].Result},
				[]string{"wrong-reason", "wrong-reason", "wrong-reason"},
				"a failure outside an assertion and a failure at another assertion are wrong reasons")
			assert.Equal(
				t,
				resultOf(falsified, "Store.Get/needs-direct-io"),
				"skipped",
				"the check skips on its defect",
			)
			assert.Equal(t, falsified.Checks[3].Why, "the file system has no direct I/O", "a skip has its reason")
			assert.Equal(t, resultOf(falsified, "Store.Get/fails-then-skips"), "proven",
				"a check that fails and then skips is proven")
			assert.Empty(t, falsified.Checks[4].Why, "a proven check has no reason of a skip")
		})

		t.Run("records the counterexample of a property check on its defect", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Falsify(t, suiteOf(keepsEvery()))
				return
			}
			falsified := recordsOf(t, childOutput(t))[0]

			assert.Length(t, falsified.Checks[0].Counterexample, 1, "the counterexample has the draw of the case")
			assert.Equal(t, falsified.Checks[0].Counterexample[0]["label"], any("key"), "the draw has its label")
		})

		t.Run("records the problems that end the application", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				unstated := keeps()
				unstated.Statement = ""
				conform.Falsify(t, suiteOf(unstated))
				return
			}
			records := recordsOf(t, childOutput(t))

			assert.Length(t, records, 1, "falsification writes its record")
			assert.Equal(t, records[0].Ended, "the check Store.Get/keeps-the-value has no statement",
				"the record has the problem")
			assert.Empty(t, records[0].Checks, "no check runs")
		})
	})
}

// The records of the allocation ceilings come from tests that do not run in
// parallel, because a count of allocations covers the whole process.
func TestRecordAllocs(t *testing.T) {
	again := countedEvery()
	again.ID = storeGet.Check("keeps-every-value-again")

	t.Run("RunBudgets", func(t *testing.T) {
		t.Run("writes the subject record of the allocation ceilings and the counted checks", func(t *testing.T) {
			if os.Getenv(childEnv) == t.Name() {
				suite := budgeted(conform.Budget{Allocs: new(uint64(0))})
				suite.Checks = []conform.Check[Store]{countedEvery(), again}
				conform.RunBudgets(t, suite, newStore)
				return
			}
			out := childOutput(t)
			records := recordsOf(t, out)

			assert.Length(t, records, 1, "the budgets write one record")
			assert.Equal(t, records[0].Application, "subject", "the record is a subject record")
			assert.Equal(t, identities(records[0]), []string{
				"Store.Get/allocation-budget", "Store.Get/keeps-every-value", "Store.Get/keeps-every-value-again",
			}, "the record has the ceilings and then the counted checks")
			want, why, census := "pass", "", "RunStoreBudgets: checks 3 pass 3 vacuous 0 fail 0 excused 0 skipped 0\n"
			if checked, reason := assert.AllocsChecked(); !checked {
				want, why = "unchecked", "the run does not check allocation ceilings: "+reason
				census = "RunStoreBudgets: checks 3 pass 0 vacuous 0 fail 0 excused 0 skipped 0 unchecked 3\n"
			}
			assert.Equal(t, resultOf(records[0], "Store.Get/allocation-budget"), want, "the ceiling has its result")
			assert.Equal(t, records[0].Checks[0].Why, why, "an unchecked ceiling has the reason")
			assert.Contains(t, out, census, "the census counts each result")
		})

		t.Run("records the problems that end the application", func(t *testing.T) {
			if os.Getenv(childEnv) == t.Name() {
				conform.RunBudgets(t, budgeted(conform.Budget{Mean: time.Millisecond}), newStore)
				return
			}
			records := recordsOf(t, childOutput(t))

			assert.Length(t, records, 1, "the budgets write their record")
			assert.Equal(t, records[0].Ended, meanAlone, "the record has the problem")
		})
	})

	t.Run("FalsifyAllocs", func(t *testing.T) {
		t.Run("writes the falsification record of the allocation ceilings and the counted checks", func(t *testing.T) {
			if os.Getenv(childEnv) == t.Name() {
				suite := budgeted(conform.Budget{Allocs: new(uint64(0))})
				suite.Checks = []conform.Check[Store]{countedEvery(), again}
				conform.FalsifyAllocs(t, suite)
				return
			}
			records := recordsOf(t, childOutput(t))

			assert.Length(t, records, 1, "falsification writes one record")
			assert.Equal(t, identities(records[0]), []string{
				"Store.Get/allocation-budget", "Store.Get/keeps-every-value", "Store.Get/keeps-every-value-again",
			}, "the record has the ceilings and then the counted checks")
			want := "proven"
			if checked, _ := assert.AllocsChecked(); !checked {
				want = "unchecked"
			}
			assert.Equal(t, resultOf(records[0], "Store.Get/allocation-budget"), want, "the ceiling has its result")
		})

		t.Run("records the problems that end the application", func(t *testing.T) {
			if os.Getenv(childEnv) == t.Name() {
				conform.FalsifyAllocs(t, budgeted(conform.Budget{Mean: time.Millisecond}))
				return
			}
			records := recordsOf(t, childOutput(t))

			assert.Length(t, records, 1, "falsification writes its record")
			assert.Equal(t, records[0].Ended, meanAlone, "the record has the problem")
		})
	})
}
