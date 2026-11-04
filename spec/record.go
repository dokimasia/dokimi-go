// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// ErrRecord is the error of a suite record that does not validate against
// schemas/suite.schema.json.
var ErrRecord = errors.New("spec: the suite record does not validate")

// suiteSchema is the resolved schema of the suite record. The test of the
// package validates a record against it, so a schema that does not resolve
// fails the test.
var suiteSchema = sync.OnceValue(func() *jsonschema.Resolved {
	data, _ := files.ReadFile("schemas/suite.schema.json")
	var schema jsonschema.Schema
	_ = json.Unmarshal(data, &schema)
	resolved, _ := schema.Resolve(nil)
	return resolved
})

// ValidateSuiteRecord validates record, the JSON of one suite record, against
// schemas/suite.schema.json of the copy. It returns an error that wraps
// [ErrRecord] for a record that is no JSON or that the schema refuses.
func ValidateSuiteRecord(record []byte) error {
	var doc any
	if err := json.Unmarshal(record, &doc); err != nil {
		return fmt.Errorf("%w: %w", ErrRecord, err)
	}
	if err := suiteSchema().Validate(doc); err != nil {
		return fmt.Errorf("%w: %w", ErrRecord, err)
	}
	return nil
}

// SuiteRecord is the record of one application of a suite: the suite, the
// tools that wrote and ran it, the seed of the application, the result of
// each check, and the claims that the suite refused or left unclaimed. A
// runtime writes it into the artifact of its test runner, and
// schemas/suite.schema.json describes it.
type SuiteRecord struct {
	// Spec is the version of the spec that the runtime implements.
	Spec string `json:"spec"`

	Suite     RecordSuite `json:"suite"`
	Generator RecordTool  `json:"generator"`
	Runtime   RecordTool  `json:"runtime"`

	// Seed is the seed of the application.
	Seed Seed `json:"seed"`

	Application Application `json:"application"`

	// Subject is the type of the value that the factory of the application
	// builds, for the application subject: the import path of its package, a
	// dot and its name, such as example.com/store/storetest.InMemory.
	Subject string `json:"subject,omitempty"`

	// Ended lists the faults that ended the application before its last
	// check, one on each line, and is empty when every check ran.
	Ended string `json:"ended,omitempty"`

	Checks    []CheckRecord     `json:"checks"`
	Refusals  []RefusalRecord   `json:"refusals"`
	Unclaimed []UnclaimedRecord `json:"unclaimed"`
}

// RecordSuite is the suite of a record.
type RecordSuite struct {
	// Language is the language of the runtime, such as go.
	Language string `json:"language"`

	// Package is the package of the declaration, such as example.com/store.
	Package string `json:"package"`

	Declaration string `json:"declaration"`
}

// RecordTool is the name and the version of the generator or the runtime of
// a record.
type RecordTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Seed is a seed of 64 bits, written as 0x and 16 hexadecimal digits.
type Seed string

// Application is the application of a record: falsification or subject.
type Application string

// The applications of a record.
const (
	Falsification Application = "falsification"
	OnSubject     Application = "subject"
)

// Result is the result of a check in a record. A subject record has the
// results pass, fail, vacuous, excused, skipped and unchecked. A
// falsification record has the results proven, passed, wrong-reason,
// vacuous, unchecked, not-isolated, not-deeper and equivalent.
type Result string

// The results of a check.
const (
	Pass        Result = "pass"
	Fail        Result = "fail"
	Vacuous     Result = "vacuous"
	Excused     Result = "excused"
	Skipped     Result = "skipped"
	Unchecked   Result = "unchecked"
	Proven      Result = "proven"
	Passed      Result = "passed"
	WrongReason Result = "wrong-reason"
	NotIsolated Result = "not-isolated"
	NotDeeper   Result = "not-deeper"
	Equivalent  Result = "equivalent"
)

// Results are the results of a check, in the order of the record table of the
// spec.
var Results = []Result{
	Pass, Fail, Vacuous, Excused, Skipped, Unchecked, Proven, Passed, WrongReason, NotIsolated, NotDeeper, Equivalent,
}

// CheckRecord is the result of one check in a record.
type CheckRecord struct {
	// Counterexample is the list of entries of the smallest failing case of a
	// property check in request order, as assert-spec states the
	// counterexample of a run. A draw has its label and the typed literal of
	// its value, and a step of a machine has its action.
	Counterexample []map[string]any `json:"counterexample,omitempty"`

	// Failure is the failure record of assert-spec of the assertion that
	// failed, on the results fail and wrong-reason.
	Failure map[string]any `json:"failure,omitempty"`

	// Identity is the identity of the check, such as Store.Read/zero-on-error.
	Identity string `json:"identity"`

	// Key is the key form of the identity, such as store.read/zero-on-error.
	Key string `json:"key"`

	// Claim, Law, Machineries and Shallow describe a derived check, and are
	// empty for a check of the directive check.
	Claim ClaimID `json:"claim,omitempty"`
	Law   LawID   `json:"law,omitempty"`

	Result Result `json:"result"`

	// Other is the identity of the other check of a result not-isolated,
	// not-deeper or equivalent.
	Other string `json:"other,omitempty"`

	// Why is the reason of a result unchecked, skipped or excused.
	Why string `json:"why,omitempty"`

	// Seed is the seed of the run of a property check.
	Seed Seed `json:"seed,omitempty"`

	// Reason is the reason code that the probe reported.
	Reason ReasonID `json:"reason,omitempty"`

	Machineries []MachineryID `json:"machineries,omitempty"`

	// Cases is the number of valid cases of a property check.
	Cases int `json:"cases,omitempty"`

	// Points is the number of the points of a medium that a check enumerated,
	// such as the crash points of a durable probe.
	Points int `json:"points,omitempty"`

	Shallow bool `json:"shallow,omitempty"`

	// Authored is true for a check of the directive check.
	Authored bool `json:"authored,omitempty"`
}

// RefusalRecord is a licensed claim whose gate fails for a callable.
type RefusalRecord struct {
	Claim ClaimID `json:"claim"`

	// Callable is the declaration and the callable, such as Store.Scan.
	Callable string `json:"callable"`

	// Gate is the gate of the spec that fails for the callable.
	Gate GateID `json:"gate"`

	Why    string `json:"why"`
	Remedy string `json:"remedy"`
}

// UnclaimedRecord is a classification of a callable without a check.
type UnclaimedRecord struct {
	// Classification is shape:<name>, mixin:<name> or
	// contract:<name>/<role>, with the names of the shape catalog.
	Classification string `json:"classification"`

	// Callable is the declaration and the callable, such as Store.Stale.
	Callable string `json:"callable"`

	Stance StanceID `json:"stance"`
	Note   string   `json:"note,omitempty"`
}
