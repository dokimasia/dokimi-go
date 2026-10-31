// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/spec"
)

// recordKind is the kind of a suite record in the artifact of go test.
const recordKind = "dokimi.suite"

// module is the path of the module of conform, which a suite record names as
// its runtime.
const module = "go.dokimi.dev/conform"

// The applications of a suite record.
const (
	falsification = "falsification"
	onSubject     = "subject"
)

// The results of a check in a suite record. A subject record has the results
// pass, fail, vacuous, excused, skipped and unchecked. A falsification record
// has the results proven, passed, wrong-reason, vacuous, skipped, unchecked,
// not-isolated, not-deeper and equivalent.
const (
	resultPass        = "pass"
	resultFail        = "fail"
	resultVacuous     = "vacuous"
	resultExcused     = "excused"
	resultSkipped     = "skipped"
	resultUnchecked   = "unchecked"
	resultProven      = "proven"
	resultPassed      = "passed"
	resultWrongReason = "wrong-reason"
	resultNotIsolated = "not-isolated"
	resultNotDeeper   = "not-deeper"
	resultEquivalent  = "equivalent"
)

// verdict is the result of one check in one application, with what its record
// states beside the result. A run of the check sets the result before it
// fails the test of the check, and the test sets a result that the run left
// empty when it ends, through [verdict.end].
type verdict struct {
	// failure is the first failure of an assertion of the check, and nil
	// before one. The failure of a property run with a failing case is the
	// failure of its smallest failing case.
	failure *assert.Failure

	result string

	// other is the identity of the other check of a result not-isolated,
	// not-deeper or equivalent.
	other string

	// why is the reason of a result skipped, excused or unchecked.
	why string

	// reason is the reason that the probe set for the first failure.
	reason spec.ReasonID

	// counterexample is the list of entries of the smallest failing case of a
	// property run, and cases is the number of valid cases of the run.
	counterexample []prop.Entry
	cases          int
}

// set sets the result of v, and what the run of kept kept, as [verdict.keep]
// states.
func (v *verdict) set(result string, kept *firsts) {
	v.result = result
	v.keep(kept)
}

// keep sets in v the first failure of the run of kept and the reason that the
// probe set for it. For a property run it sets the number of cases, the
// counterexample, and the failure of the smallest failing case in place of
// the first failure.
func (v *verdict) keep(kept *firsts) {
	v.failure = kept.failure.Load()
	if r := kept.reason.Load(); r != nil {
		v.reason = *r
	}
	if r := kept.result.Load(); r != nil {
		v.cases, v.counterexample, v.failure = r.Cases, r.Counterexample, r.Failure
	}
}

// end sets the result that the run of the check left empty, once the test t of
// the check has ended: excused for a check that an excusal covers, the result
// of a skip, and on a subject fail for a test that failed and pass for any
// other. A falsification whose run left no result ended with a fault, which
// gives it no verdict, so its result is unchecked.
func (v *verdict) end(t interface {
	Failed() bool
	Skipped() bool
}, application, excusal string, excused bool,
) {
	switch {
	case excused:
		v.result, v.why = resultExcused, excusal
	case v.result != "":
	case t.Skipped():
		v.result = resultSkipped
	case application == falsification:
		v.result, v.why = resultUnchecked, "the check ended with a fault before its verdict"
	case t.Failed():
		v.result = resultFail
	default:
		v.result = resultPass
	}
	if v.result != resultSkipped && v.result != resultExcused && v.result != resultUnchecked {
		v.why = ""
	}
}

// suiteRecord is the JSON form of a suite record, which
// schemas/suite.schema.json of the spec describes.
type suiteRecord struct {
	Suite       recordSuite       `json:"suite"`
	Generator   recordTool        `json:"generator"`
	Runtime     recordTool        `json:"runtime"`
	Spec        string            `json:"spec"`
	Seed        string            `json:"seed"`
	Application string            `json:"application"`
	Subject     string            `json:"subject,omitempty"`
	Ended       string            `json:"ended,omitempty"`
	Checks      []checkRecord     `json:"checks"`
	Refusals    []refusalRecord   `json:"refusals"`
	Unclaimed   []unclaimedRecord `json:"unclaimed"`
}

// recordSuite is the suite of a record.
type recordSuite struct {
	Language    string `json:"language"`
	Package     string `json:"package"`
	Declaration string `json:"declaration"`
}

// recordTool is the generator or the runtime of a record.
type recordTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// checkRecord is the result of one check in a record. The counterexample has
// the JSON form of the counterexample of a run of assert-spec.
type checkRecord struct {
	Failure        *assert.Failure    `json:"failure,omitempty"`
	Identity       string             `json:"identity"`
	Key            string             `json:"key"`
	Claim          spec.ClaimID       `json:"claim,omitempty"`
	Law            spec.LawID         `json:"law,omitempty"`
	Result         string             `json:"result"`
	Other          string             `json:"other,omitempty"`
	Why            string             `json:"why,omitempty"`
	Seed           string             `json:"seed,omitempty"`
	Reason         spec.ReasonID      `json:"reason,omitempty"`
	Machineries    []spec.MachineryID `json:"machineries,omitempty"`
	Counterexample []prop.Entry       `json:"counterexample,omitempty"`
	Cases          int                `json:"cases,omitempty"`
	Shallow        bool               `json:"shallow,omitempty"`
	Authored       bool               `json:"authored,omitempty"`
}

// refusalRecord is a refusal of a record.
type refusalRecord struct {
	Claim    spec.ClaimID `json:"claim"`
	Callable string       `json:"callable"`
	Why      string       `json:"why"`
	Remedy   string       `json:"remedy"`
}

// unclaimedRecord is an unclaimed classification of a record.
type unclaimedRecord struct {
	Classification string        `json:"classification"`
	Callable       string        `json:"callable"`
	Stance         spec.StanceID `json:"stance"`
	Note           string        `json:"note,omitempty"`
}

// hex returns the seed as 0x and 16 hexadecimal digits.
func hex(seed uint64) string {
	return fmt.Sprintf("0x%016x", seed)
}

// runtimeVersion returns the version of the module of conform in the build of
// the test binary, and (devel) where the binary is a build of conform itself.
var runtimeVersion = sync.OnceValue(func() string {
	info, _ := debug.ReadBuildInfo()
	versions := map[string]string{info.Main.Path: info.Main.Version}
	//dokimi:mutate-skip all: the test binary of conform has conform as its main module, so no spec reads a dependency
	for _, d := range info.Deps {
		versions[d.Path] = d.Version //dokimi:mutate-skip all: no dependency of the test binary of conform is conform
	}
	return versions[module]
})

// application collects the verdicts of the checks of one application of a
// suite, and writes its suite records once every test of a check has ended.
//
// # Concurrency
//
// add is safe for concurrent use. write reads the verdicts once every call of
// add has returned.
type application[S any] struct {
	suite *Suite[S]

	// falsified and judged are the records of the checks of the
	// falsification and of the subject, by the position of each check.
	falsified, judged map[int]checkRecord

	// name is the name of the application, such as RunStoreSuite, and subject
	// the name of its factory for an application with a subject record.
	name, subject string

	// ended lists the problems that stopped the application before its first
	// check, one on each line, and is empty for an application that ran its
	// checks.
	ended string

	// kinds are the applications of the records that write writes.
	kinds []string

	// seed is the seed of the property runs of the application.
	seed uint64
	mu   sync.Mutex
}

// newApplication returns the collector of the application name of the suite,
// which writes a record of each of kinds. subject is the name of the factory
// of the subjects.
func newApplication[S any](suite *Suite[S], name, subject string, seed uint64, kinds ...string) *application[S] {
	return &application[S]{
		suite: suite, name: name, subject: subject, seed: seed, kinds: kinds,
		falsified: map[int]checkRecord{}, judged: map[int]checkRecord{},
	}
}

// begin makes the application write its records once every test of t has
// ended, and stops t with each problem of found, as stopAt does. The records
// of an application that problems stop have no check, and the field ended
// lists the problems, one on each line.
func (a *application[S]) begin(t T, found []string) {
	t.Helper()
	whenDone(t, func() { a.write(t) })
	a.ended = strings.Join(found, "\n")
	stopAt(t, found)
}

// track returns the verdict of the check c on the test t, and adds the record
// of the check at the position i to the records of kind once t has ended. An
// excusal of the options of cfg covers the check. seed is the seed of the run
// of a property check.
func (a *application[S]) track(t *testing.T, i int, kind string, c *Check[S], cfg *config[S], seed uint64) *verdict {
	t.Helper()
	v := new(verdict)
	excusal, excused := cfg.excused(c.ID)
	t.Cleanup(func() {
		v.end(t, kind, excusal, excused)
		a.add(i, kind, c, v, seed)
	})
	return v
}

// add adds the record of the check c at the position i of the application,
// with the verdict v, to the records of kind. seed is the seed of the run of
// a property check.
func (a *application[S]) add(i int, kind string, c *Check[S], v *verdict, seed uint64) {
	r := checkRecord{
		Identity: string(c.ID), Key: spec.Key(string(c.ID)), Claim: c.Claim, Law: c.Law,
		Machineries: c.Machineries, Shallow: c.Shallow, Authored: c.Claim == "" && !c.ceiling,
		Result: v.result, Other: v.other, Why: v.why, Reason: v.reason, Failure: v.failure,
		Cases: v.cases, Counterexample: v.counterexample,
	}
	if c.Property != nil {
		r.Seed = hex(seed)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if kind == falsification {
		a.falsified[i] = r
		return
	}
	a.judged[i] = r
}

// write writes the record of each kind of the application into the output of
// t through [assert.WriteRecord], and logs the census of a subject record on
// t. A t that is no [assert.TB] gets no record, and a t without Logf gets no
// census.
func (a *application[S]) write(t T) {
	tb, writes := t.(assert.TB)
	for _, kind := range a.kinds {
		checks := a.judged
		if kind == falsification {
			checks = a.falsified
		}
		r := a.record(kind, checks)
		if kind == onSubject {
			if logger, logs := t.(interface {
				Logf(format string, args ...any)
			}); logs {
				logger.Logf("%s", census(a.name, r.Checks))
			}
		}
		if writes {
			data, _ := json.Marshal(r)
			assert.WriteRecord(tb, recordKind, string(data))
		}
	}
}

// record returns the record of kind with the checks in the order of their
// positions.
func (a *application[S]) record(kind string, checks map[int]checkRecord) suiteRecord {
	r := suiteRecord{
		Spec:        spec.Version(),
		Suite:       recordSuite{Language: "go", Package: a.suite.Package, Declaration: a.suite.Declaration},
		Generator:   recordTool{Name: a.suite.Generator.Name, Version: a.suite.Generator.Version},
		Runtime:     recordTool{Name: module, Version: runtimeVersion()},
		Seed:        hex(a.seed),
		Application: kind,
		Ended:       a.ended,
		Checks:      []checkRecord{},
		Refusals:    []refusalRecord{},
		Unclaimed:   []unclaimedRecord{},
	}
	if kind == onSubject {
		r.Subject = a.subject
	}
	positions := make([]int, 0, len(checks))
	for i := range checks {
		positions = append(positions, i)
	}
	slices.Sort(positions)
	for _, i := range positions {
		r.Checks = append(r.Checks, checks[i])
	}
	for _, f := range a.suite.Refusals {
		r.Refusals = append(r.Refusals, refusalRecord(f))
	}
	for _, u := range a.suite.Unclaimed {
		r.Unclaimed = append(r.Unclaimed, unclaimedRecord(u))
	}
	return r
}

// census returns the census of the checks of the subject record of the
// application name as one line, such as
// "RunStoreSuite: checks 5 pass 1 vacuous 1 fail 1 excused 1 skipped 1".
func census(name string, checks []checkRecord) string {
	by := map[string]int{}
	for i := range checks {
		by[checks[i].Result]++
	}
	line := fmt.Sprintf("%s: checks %d pass %d vacuous %d fail %d excused %d skipped %d", name, len(checks),
		by[resultPass], by[resultVacuous], by[resultFail], by[resultExcused], by[resultSkipped])
	if n := by[resultUnchecked]; n > 0 {
		line += fmt.Sprintf(" unchecked %d", n)
	}
	return line
}
