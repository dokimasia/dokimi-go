// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	"go.dokimi.dev/spec"
)

// Rule is the check of a claim of sim that binds to the machines of a
// simulation, such as replies-once. The generator writes it from the claim.
type Rule struct {
	// ID is the identity of the check, such as Machine/replies-once.
	ID string
	// Statement is the sentence that the check proves of the machines.
	Statement string
	// Claim is the claim, Probe its probe, Reason the reason of its defect
	// and Form the form of its defect.
	Claim  spec.ClaimID
	Probe  spec.ProbeID
	Reason spec.ReasonID
	Form   spec.DefectFormID
}

// errDefect is the error of a machine with the planted defect of the form
// steps-after-error.
var errDefect = errors.New("sim: the planted defect stops the machine")

// life is one machine of a process: the process and its epoch.
type life struct {
	process string
	epoch   uint64
}

// judge returns the problems that the judge of the probe finds in the log,
// and reports false for a probe without a judge.
func judge[E, F any](probe spec.ProbeID, a Anatomy[E, F], log []Entry[E, F]) ([]string, bool) {
	switch probe {
	case spec.RepliesAtMostOnce:
		return repliesTwice(a, log), true
	case spec.RepliesOnStop:
		return callsWithoutReply(a, log), true
	case spec.OnlyRepliesAfterStop:
		return effectsAfterStop(a, log), true
	case spec.NoEffectsAfterError:
		return effectsAfterError(log), true
	case spec.TokensDiffer:
		return reusedTokens(a, log), true
	default:
		return nil, false
	}
}

// repliesTwice returns a problem for each reply of a machine to an inbound
// call that it replied to before.
func repliesTwice[E, F any](a Anatomy[E, F], log []Entry[E, F]) []string {
	type reply struct {
		life
		to uint64
	}
	seen := map[reply]bool{}
	var out []string
	for i, e := range log {
		for _, f := range e.Effects {
			to, _, _, ok := a.Reply(f)
			if !ok {
				continue
			}
			key := reply{process: e.Process, epoch: e.Epoch, to: to}
			if seen[key] {
				out = append(out, fmt.Sprintf("step %d of %s replies to the call %d a second time", i, e.Process, to))
			}
			seen[key] = true
		}
	}
	return out
}

// callsWithoutReply returns a problem for each stop step of a machine that
// leaves an inbound call without a reply. A cancelled call needs no reply. A
// dropped event of an earlier epoch opens a call of a machine that crashed,
// which takes no stop event.
func callsWithoutReply[E, F any](a Anatomy[E, F], log []Entry[E, F]) []string {
	open := map[life]map[uint64]bool{}
	var out []string
	for i, e := range log {
		key := life{process: e.Process, epoch: e.Epoch}
		if open[key] == nil {
			open[key] = map[uint64]bool{}
		}
		calls := open[key]
		if reply, ok := a.Call(e.Event); ok {
			calls[reply] = true
		}
		for token := range calls {
			if a.Cancelled != nil && reflect.DeepEqual(e.Event, a.Cancelled(token)) {
				delete(calls, token)
			}
		}
		for _, f := range e.Effects {
			if to, _, _, ok := a.Reply(f); ok {
				delete(calls, to)
			}
		}
		if len(calls) > 0 && reflect.DeepEqual(e.Event, a.Stopped()) {
			out = append(out, fmt.Sprintf("step %d of %s takes the stop event and leaves %d calls without a reply",
				i, e.Process, len(calls)))
		}
	}
	return out
}

// effectsAfterStop returns a problem for each effect other than a reply that
// a machine returns on the stop event.
func effectsAfterStop[E, F any](a Anatomy[E, F], log []Entry[E, F]) []string {
	var out []string
	for i, e := range log {
		if !reflect.DeepEqual(e.Event, a.Stopped()) {
			continue
		}
		for _, f := range e.Effects {
			if _, _, _, ok := a.Reply(f); !ok {
				out = append(out, fmt.Sprintf("step %d of %s returns an effect other than a reply on the stop event",
					i, e.Process))
			}
		}
	}
	return out
}

// effectsAfterError returns a problem for each step of a machine with an
// effect after the step in which its error method returned an error.
func effectsAfterError[E, F any](log []Entry[E, F]) []string {
	failed := map[life]bool{}
	var out []string
	for i, e := range log {
		key := life{process: e.Process, epoch: e.Epoch}
		if failed[key] && len(e.Effects) > 0 {
			out = append(out, fmt.Sprintf("step %d of %s returns effects after the error of its machine", i, e.Process))
		}
		if e.Err != nil {
			failed[key] = true
		}
	}
	return out
}

// reusedTokens returns a problem for each effect of a machine whose token an
// earlier effect of the machine has.
func reusedTokens[E, F any](a Anatomy[E, F], log []Entry[E, F]) []string {
	type token struct {
		life
		token uint64
	}
	seen := map[token]bool{}
	var out []string
	for i, e := range log {
		for _, f := range e.Effects {
			t, ok := a.Token(f)
			if !ok {
				continue
			}
			key := token{process: e.Process, epoch: e.Epoch, token: t}
			if seen[key] {
				out = append(out, fmt.Sprintf("step %d of %s reuses the token %d", i, e.Process, t))
			}
			seen[key] = true
		}
	}
	return out
}

// Plant returns the machine m with the planted defect of the form, which
// reads the effects of m through the anatomy a. The forms are the defect
// forms of the rules of a runtime:
//
//   - replies-twice returns each reply of a step twice.
//   - forgets-a-call-on-stop drops the replies of the stop event.
//   - writes-after-stop returns, on the stop event, the last effect with a
//     token that the machine returned before it.
//   - steps-after-error makes the error method return an error from the
//     second step on, and keeps the effects of every step.
//   - reuses-a-token returns, in each step that starts a call or a wait, the
//     first such effect of the machine once more.
//
// Another form leaves the effects and the error of m as they are.
func Plant[E, F any](m Machine[E, F], a Anatomy[E, F], form spec.DefectFormID) Machine[E, F] {
	return &defect[E, F]{Machine: m, a: a, form: form}
}

// defect is a machine with the planted defect of the form of a rule. It
// reads the effects of its machine through the anatomy, and keeps the first
// and the last effect with a token that the machine returned.
type defect[E, F any] struct {
	Machine[E, F]

	a     Anatomy[E, F]
	form  spec.DefectFormID
	first []F
	last  []F
	steps int
}

// Step applies ev to the machine and changes its effects by the form of the
// defect.
func (d *defect[E, F]) Step(ev E, out []F) []F {
	start := len(out)
	out = d.Machine.Step(ev, out)
	own := slices.Clone(out[start:])
	d.steps++
	stop := reflect.DeepEqual(ev, d.a.Stopped())
	switch d.form {
	case spec.RepliesTwice:
		for _, f := range own {
			if d.reply(f) {
				out = append(out, f)
			}
		}
	case spec.ForgetsACallOnStop:
		if stop {
			out = append(out[:start], slices.DeleteFunc(slices.Clone(own), d.reply)...)
		}
	case spec.WritesAfterStop:
		if stop {
			out = append(out, d.last...)
		}
	case spec.ReusesAToken:
		if slices.ContainsFunc(own, d.token) {
			out = append(out, d.first...)
		}
	default:
		// A machine of the form steps-after-error keeps its effects, and
		// changes its error alone.
	}
	for _, f := range own {
		if d.token(f) {
			d.last = []F{f}
			if len(d.first) == 0 {
				d.first = d.last
			}
		}
	}
	return out
}

// Err returns errDefect from the second step on for the form
// steps-after-error, and the error of the machine otherwise.
func (d *defect[E, F]) Err() error {
	if d.form == spec.StepsAfterError && d.steps > 1 {
		return errDefect
	}
	return d.Machine.Err()
}

// reply reports whether f is a reply.
func (d *defect[E, F]) reply(f F) bool {
	_, _, _, ok := d.a.Reply(f)
	return ok
}

// token reports whether f has a token.
func (d *defect[E, F]) token(f F) bool {
	_, ok := d.a.Token(f)
	return ok
}
