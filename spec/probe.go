// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package spec

// ProbeID identifies a probe.
type ProbeID string

// InputKind is the kind of an input of a probe. A suite passes each kind in
// one form.
type InputKind string

// The kinds of the inputs of a probe.
const (
	// KindCall is a function that calls the subject with bound arguments
	// and returns the results of the subject.
	KindCall InputKind = "call"

	// KindValue is a sample value of a type.
	KindValue InputKind = "value"

	// KindError is an error value or an error type that the probe matches
	// by identity.
	KindError InputKind = "error"

	// KindDuration is a length of time, such as the lifetime of an entry.
	KindDuration InputKind = "duration"

	// KindCount is a whole number, such as the attempts of a retry.
	KindCount InputKind = "count"

	// KindProjection is a function that returns one part of a value, such
	// as its version.
	KindProjection InputKind = "projection"

	// KindScope is a function that runs a body inside a scope of the
	// subject, such as a transaction.
	KindScope InputKind = "scope"

	// KindHook is a function that installs a hook through the subject.
	KindHook InputKind = "hook"

	// KindPredicate is a function that returns whether the subject is in a
	// state, such as leadership.
	KindPredicate InputKind = "predicate"

	// KindClock is the controlled clock of the run.
	KindClock InputKind = "clock"

	// KindValues are named values that the check covers as one set, such as
	// the members of an enumeration or the error values of a package.
	KindValues InputKind = "values"

	// KindActions are the actions of a machine over the subject. Each action
	// calls one callable, or runs one transaction, and records the call in
	// the history of the case.
	KindActions InputKind = "actions"

	// KindOperation is the name of a callable as the history of a case
	// records its calls.
	KindOperation InputKind = "operation"

	// KindApply is a function that calls a partner with the arguments of a
	// call that the history recorded, and returns its output.
	KindApply InputKind = "apply"

	// KindReference is a function that applies an operation of the history
	// to a subject by the name of its callable.
	KindReference InputKind = "reference"

	// KindPull is a function that starts a pull iteration of the stream of
	// the subject, and returns the function that yields its next value and
	// the function that ends it.
	KindPull InputKind = "pull"

	// KindChannel is a function that subscribes to the subject and returns
	// the channel of the subscription.
	KindChannel InputKind = "channel"

	// KindHandleMethod is a method of the handle that a callable returns,
	// such as the read of the next update of a subscription.
	KindHandleMethod InputKind = "handle-method"

	// KindDurable are the writes of the licensed callable and the reads of
	// its partner over a subject that the probe opens, by the index of a
	// sample, with the value and the key that each sample writes.
	KindDurable InputKind = "durable"
)

// Input is one input of a probe.
type Input struct {
	Name string    `json:"name"`
	Kind InputKind `json:"kind"`
}

// Probe is the procedure of a check, with the reason codes that it reports.
// The package probe of the module conform implements each probe of the
// spec.
type Probe struct {
	ID ProbeID `json:"id"`

	Summary string `json:"summary"`

	// Vacuous is the condition under which the probe does not exercise its
	// claim, as a sentence. A run names it when it fails a vacuous check.
	Vacuous string `json:"vacuous"`

	Since string `json:"since"`

	Inputs []Input `json:"inputs"`

	// Reasons lists the reason codes that the probe reports. The handle
	// fails a check whose probe sets another code.
	Reasons []ReasonID `json:"reasons"`
}

func (p *Probe) key() ProbeID { return p.ID }

var probes = section[ProbeID, Probe]("probes.json", "probes")

// Probes returns every probe of the copy, in the order of probes.json. The
// slice is shared with every other call, so a caller does not modify it.
func Probes() []Probe { return probes().entries }

// ProbeOf returns the probe with the identifier id, and false for an
// identifier that the copy does not contain.
func ProbeOf(id ProbeID) (Probe, bool) { return probes().of(id) }
