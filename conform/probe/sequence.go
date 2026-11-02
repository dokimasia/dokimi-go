// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"slices"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/spec"
)

// Actions returns the actions of a machine over the subject that subject
// returns. Each action calls one callable of a declaration with the inputs
// that it requests from the case, and completes the call in the history of
// the case through [conform.Complete]. The actions take their keys from pools
// that Actions draws from c.
type Actions[S any] func(c *prop.Case, subject func() S) []stateful.Action[struct{}]

// Apply calls a partner with the arguments of a call that the history of a
// case recorded, and returns the output of the partner. The generated check
// binds the arguments of the call to the inputs of the partner.
type Apply func(ctx context.Context, args []any) conform.Output

// Reference returns the function that applies an operation of the history of
// a case to the subject r by the name of its callable. The function returns
// the output of the call.
type Reference[S any] func(r S) func(ctx context.Context, operation string, args []any) conform.Output

// ResultsNeverFall runs generated sequences of the actions. It fails when a
// call of read returns a result below the result of the call of read with the
// same arguments before it.
func ResultsNeverFall[S any, O cmp.Ordered](h *conform.Handle[S], c *prop.Case, actions Actions[S], read string) {
	h.Helper()
	var last memo[completed]
	sequences(h, c, actions, func(calls []completed) {
		successive(calls, read, argsOf, &last, func(before, call completed) {
			h.Engage()
			h.Expect(spec.Regressed)
			assert.Pairwise(h, []O{value[O](before.out), value[O](call.out)}, atLeast[O], h.Statement())
		})
	})
}

// ReadVersionsNeverFall runs generated sequences of the actions. It fails when
// a call of read returns a value whose version is below the version of the
// value that the call of read with the same keys before it returned. A call
// without keys compares with the calls with the same arguments.
func ReadVersionsNeverFall[S, V any, O cmp.Ordered](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], read string, version func(v V) O,
) {
	h.Helper()
	var last memo[completed]
	sequences(h, c, actions, func(calls []completed) {
		successive(calls, read, groupOf, &last, func(before, call completed) {
			h.Engage()
			h.Expect(spec.Regressed)
			assert.Pairwise(h, []O{version(value[V](before.out)), version(value[V](call.out))}, atLeast[O],
				h.Statement())
		})
	})
}

// DrainsAgreeBetweenWrites runs generated sequences of the actions. It fails
// when two calls of drain with the same arguments and no call between them
// return two lists of elements.
func DrainsAgreeBetweenWrites[S any](h *conform.Handle[S], c *prop.Case, actions Actions[S], drain string) {
	h.Helper()
	var last memo[completed]
	sequences(h, c, actions, func(calls []completed) {
		successive(calls, drain, argsOf, &last, func(before, call completed) {
			if call.at != before.at+2 {
				return
			}
			h.Engage()
			h.Expect(spec.ReorderedDrain)
			assert.Equal(h, call.out, before.out, h.Statement())
		})
	})
}

// WrittenVersionsRise runs generated sequences of the actions, and applies
// read to the arguments of each call of write that succeeds. It fails when
// the version of the value that read returns is not above the version that
// read returned after the write of the same keys before it, and when read
// fails. A call without keys compares with the calls with the same arguments.
func WrittenVersionsRise[S, V any, O cmp.Ordered](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], write string, read Apply, version func(v V) O,
) {
	h.Helper()
	var last memo[O]
	sequences(h, c, actions, func(calls []completed) {
		for _, call := range calls {
			if call.operation != write || call.out.Failed {
				continue
			}
			written := readVersion(h, read, call.args, version, spec.UnobservedWrite)
			if before, found := last.at(groupOf(&call)); found {
				h.Engage()
				assert.Pairwise(h, []O{before, written}, above[O], h.Statement())
			}
			last.put(groupOf(&call), written)
		}
	})
}

// ReadsSeeTheLastWrite runs generated sequences of the actions. It fails when
// a call of read returns a value whose version is below the highest version
// of a call of write with the same keys before it, and when readback fails.
// The version of a write is the version of the value that it returns, or the
// version of the value that readback returns for its arguments where it
// returns none. A call without keys meets the calls with the same arguments.
func ReadsSeeTheLastWrite[S, V any, O cmp.Ordered](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], read, write string, readback Apply, version func(v V) O,
) {
	h.Helper()
	var written memo[O]
	sequences(h, c, actions, func(calls []completed) {
		for _, call := range calls {
			switch {
			case call.out.Failed:
			case call.operation == write:
				seen := writtenVersion(h, &call, readback, version, spec.UnobservedWrite)
				highest, _ := written.at(groupOf(&call))
				written.put(groupOf(&call), max(highest, seen))
			case call.operation == read:
				if highest, found := written.at(groupOf(&call)); found {
					h.Engage()
					assert.Pairwise(h, []O{highest, version(value[V](call.out))}, atLeast[O], h.Statement())
				}
			}
		}
	})
}

// WritesRiseAboveReads runs generated sequences of the actions. It fails when
// a call of write that succeeds after a call of read with the same keys has a
// version that is not above the version of the value that the last of those
// calls of read returned, and when readback fails. The version of a write is
// the version of the value that it returns, or the version of the value that
// readback returns for its arguments where it returns none. A call without
// keys meets the calls with the same arguments.
func WritesRiseAboveReads[S, V any, O cmp.Ordered](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], write, read string, readback Apply, version func(v V) O,
) {
	h.Helper()
	var seen memo[O]
	sequences(h, c, actions, func(calls []completed) {
		for _, call := range calls {
			switch {
			case call.out.Failed:
			case call.operation == read:
				seen.put(groupOf(&call), version(value[V](call.out)))
			case call.operation == write:
				if before, found := seen.at(groupOf(&call)); found {
					written := writtenVersion(h, &call, readback, version, spec.UnorderedWrite)
					h.Engage()
					assert.Pairwise(h, []O{before, written}, above[O], h.Statement())
				}
			}
		}
	})
}

// PositionsRise runs generated sequences of the actions. It fails when a call
// of appends returns a position that is not above the position that the call
// of appends with the same keys before it returned.
func PositionsRise[S any, O cmp.Ordered](h *conform.Handle[S], c *prop.Case, actions Actions[S], appends string) {
	h.Helper()
	var last memo[completed]
	sequences(h, c, actions, func(calls []completed) {
		successive(calls, appends, keysOf, &last, func(before, call completed) {
			h.Engage()
			h.Expect(spec.NotAscending)
			assert.Pairwise(h, []O{value[O](before.out), value[O](call.out)}, above[O], h.Statement())
		})
	})
}

// ReplaysFollowTheStamps runs generated sequences of the actions. It fails
// when a call of replay returns an entry whose stamp is below the stamp of the
// entry before it.
func ReplaysFollowTheStamps[S, E any, O cmp.Ordered](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], replay string, stamp func(e E) O,
) {
	h.Helper()
	sequences(h, c, actions, func(calls []completed) {
		for _, call := range calls {
			if call.operation != replay || call.out.Failed {
				continue
			}
			entries := value[[]E](call.out)
			if len(entries) < 2 {
				continue
			}
			stamps := make([]O, len(entries))
			for i, e := range entries {
				stamps[i] = stamp(e)
			}
			h.Engage()
			h.Expect(spec.UnorderedHistory)
			assert.Pairwise(h, stamps, atLeast[O], h.Statement())
		}
	})
}

// DrainsWithoutRepeats runs generated sequences of the actions. It fails when
// a call of drain returns an element twice.
func DrainsWithoutRepeats[S, E any](h *conform.Handle[S], c *prop.Case, actions Actions[S], drain string) {
	h.Helper()
	sequences(h, c, actions, func(calls []completed) {
		for _, call := range calls {
			if call.operation != drain || call.out.Failed {
				continue
			}
			elements := value[[]E](call.out)
			if len(elements) < 2 {
				continue
			}
			h.Engage()
			h.Expect(spec.RepeatedElement)
			assert.NoDuplicates(h, func() ([]E, error) { return elements, nil }, h.Statement())
		}
	})
}

// DrainsContainTheWrites runs generated sequences of the actions. It fails
// when a call of drain gains over the call of drain with the same keys before
// it fewer than the elements that the calls of write since then wrote.
// written returns the element that a call of write with args writes.
func DrainsContainTheWrites[S, E any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], drain, write string, written func(args []any) E,
) {
	h.Helper()
	var log drainLog[E]
	sequences(h, c, actions, func(calls []completed) {
		log.judge(calls, drain, write, written, func(gained, wrote []E) {
			h.Engage()
			h.Expect(spec.ShrunkDrain)
			assert.Empty(h, without(wrote, gained), h.Statement())
		})
	})
}

// DrainsGainTheWrites runs generated sequences of the actions. It fails when
// a call of drain gains over the call of drain with the same keys before it
// other elements than the elements that the calls of write since then wrote,
// each as often as a call wrote it. written returns the element that a call
// of write with args writes.
func DrainsGainTheWrites[S, E any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], drain, write string, written func(args []any) E,
) {
	h.Helper()
	var log drainLog[E]
	sequences(h, c, actions, func(calls []completed) {
		log.judge(calls, drain, write, written, func(gained, wrote []E) {
			h.Engage()
			h.Expect(spec.MiscountedDrain)
			assert.Permutation(h, gained, wrote, h.Statement())
		})
	})
}

// EachCallMovesTheObservation runs generated sequences of the actions, and
// applies observe to the arguments of each call of call right before and
// right after the call. It fails when a call that succeeds leaves the output
// of observe as it was before the call.
func EachCallMovesTheObservation[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], call string, observe Apply,
) {
	h.Helper()
	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: observing(h, c, actions, call, observe, func(before, after, out conform.Output) {
			if out.Failed {
				return
			}
			h.Engage()
			h.Expect(spec.Uncompounded)
			assert.NotEqual(h, after, before, h.Statement())
		}),
	})
}

// observing returns the actions of a machine whose action of operation
// applies observe to the arguments of each call right before and right after
// the call. It passes judge the two outputs of observe and the output of the
// call. The input of an action is the list of the arguments of its call.
func observing[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], operation string, observe Apply,
	judge func(before, after, out conform.Output),
) []stateful.Action[struct{}] {
	listed := actions(c, h.Subject)
	for i := range listed {
		if listed[i].Name != operation {
			continue
		}
		run := listed[i].Run
		listed[i].Run = func(c *prop.Case, client int, input any) {
			args, _ := input.([]any)
			before := observe(c.Context(), args)
			run(c, client, input)
			events := c.History().Events()
			out, _ := events[len(events)-1].Output.(conform.Output)
			judge(before, observe(c.Context(), args), out)
		}
	}
	return listed
}

// ReadsReturnTheLastWrite runs generated sequences of the actions, and applies
// readback to the arguments of each call of write that succeeds. It fails when
// readback fails, and when it returns a value other than the value that
// written returns for the arguments of the write.
func ReadsReturnTheLastWrite[S, V any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], write string, readback Apply, written func(args []any) V,
) {
	h.Helper()
	sequences(h, c, actions, func(calls []completed) {
		for _, call := range calls {
			if call.operation != write || call.out.Failed {
				continue
			}
			out := readback(h.Context(), call.args)
			h.Engage()
			h.Expect(spec.UnobservedWrite)
			assert.False(h, out.Failed, h.Statement())
			assert.Equal(h, value[V](out), written(call.args), h.Statement())
		}
	})
}

// ReadsAgreeWithTheBacking runs generated sequences of the actions, and
// applies backing to the arguments of each call of read. It fails when the
// output of backing differs from the output of the call.
func ReadsAgreeWithTheBacking[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], read string, backing Apply,
) {
	h.Helper()
	sequences(h, c, actions, func(calls []completed) {
		for _, call := range calls {
			if call.operation != read {
				continue
			}
			h.Engage()
			h.Expect(spec.CacheDisagrees)
			assert.Equal(h, call.out, backing(h.Context(), call.args), h.Statement())
		}
	})
}

// SequencesAgreeWithTheReference runs generated sequences of the actions, and
// applies each call to a reference from the door Reference of h through
// reference. It fails when the output of the reference differs from the
// output of the call.
func SequencesAgreeWithTheReference[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], reference Reference[S],
) {
	h.Helper()
	apply := reference(h.Reference())
	sequences(h, c, actions, func(calls []completed) {
		for _, call := range calls {
			h.Engage()
			h.Expect(spec.DivergesFromTheReference)
			assert.Equal(h, call.out, apply(h.Context(), call.operation, call.args), h.Statement())
		}
	})
}

// UseIsWithinTheBounds runs generated sequences of the actions, and calls
// observe after each step that completes a call. It fails when observe fails,
// and when the use that it returns is below least or above limit.
func UseIsWithinTheBounds[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], observe Call[int], least, limit int,
) {
	h.Helper()
	sequences(h, c, actions, func(calls []completed) {
		if len(calls) == 0 {
			return
		}
		use, err := observe(h.Context())
		h.Engage()
		h.Expect(spec.ExceededBound)
		assert.NoError(h, err, h.Statement())
		assert.InRange(h, use, float64(least), float64(limit), h.Statement())
	})
}

// MergesCommute writes to two replicas, the subject of h and a new subject,
// through generated sequences of the action of write. It merges the second
// replica into the first, applies the same writes to two more new replicas,
// and merges the first of those into the second. It fails when the action of
// read returns two outputs on the two merged replicas. apply calls merge on a
// replica with the other replica as its argument, and the guard of the probe
// fails a merge that fails.
func MergesCommute[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], apply Reference[S], merge, write, read string,
) {
	h.Helper()
	replicas, again := []S{h.Subject(), h.New()}, []S{h.New(), h.New()}
	current := replicas[0]
	var writes, reads stateful.Action[struct{}]
	for _, a := range actions(c, func() S { return current }) {
		switch a.Name {
		case write:
			writes = a
		case read:
			reads = a
		}
	}
	type step struct {
		input   any
		replica int
	}
	var steps []step
	writeOn := func(c *prop.Case, on []S, s step) {
		current = on[s.replica]
		writes.Run(c, s.replica, s.input)
	}
	var machine stateful.Machine[struct{}]
	for i, name := range []string{"the first replica", "the second replica"} {
		if writes.Run == nil {
			break
		}
		machine.Actions = append(machine.Actions, stateful.Action[struct{}]{
			Name: write + " on " + name, Weight: writes.Weight, Input: writes.Input,
			Run: func(c *prop.Case, _ int, input any) {
				steps = append(steps, step{input: input, replica: i})
				writeOn(c, replicas, steps[len(steps)-1])
			},
		})
	}
	stateful.Steps(c, machine)
	wrote := slices.ContainsFunc(completions(c.History().Events(), 0), func(call completed) bool {
		return !call.out.Failed
	})
	for _, s := range steps {
		writeOn(c, again, s)
	}
	assert.False(h, apply(replicas[0])(h.Context(), merge, []any{replicas[1]}).Failed, h.Statement())
	assert.False(h, apply(again[1])(h.Context(), merge, []any{again[0]}).Failed, h.Statement())
	if !wrote {
		return
	}
	var input any
	if reads.Input != nil {
		input = reads.Input(c, struct{}{})
	}
	readOn := func(replica S) conform.Output {
		current = replica
		reads.Run(c, 0, input)
		events := c.History().Events()
		out, _ := events[len(events)-1].Output.(conform.Output)
		return out
	}
	h.Engage()
	h.Expect(spec.OrderDependentMerge)
	assert.Equal(h, readOn(again[1]), readOn(replicas[0]), h.Statement())
}

// StatesMoveAlongTheGraph runs generated sequences of the actions, and applies
// observe to the arguments of each call of fn right before and right after
// the call. It fails when the call moves the state to a state other than the
// state before the call and its successors in transitions. A call whose state
// before or after it fails to read is not judged.
func StatesMoveAlongTheGraph[S any, T comparable](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], fn string, observe Apply, transitions map[T][]T,
) {
	h.Helper()
	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: observing(h, c, actions, fn, observe, func(before, after, _ conform.Output) {
			if before.Failed || after.Failed {
				return
			}
			from := value[T](before)
			h.Engage()
			h.Expect(spec.ForbiddenTransition)
			assert.Contains(h, append([]T{from}, transitions[from]...), value[T](after), h.Statement())
		}),
	})
}

// MovesKeepTheTotal runs generated sequences of the actions, and calls observe
// right before and right after each call of call. It fails when the total
// that observe returns after the call differs from the total before it, and
// when observe fails.
func MovesKeepTheTotal[S, T any](h *conform.Handle[S], c *prop.Case, actions Actions[S], call string, observe Call[T]) {
	h.Helper()
	listed := actions(c, h.Subject)
	for i := range listed {
		if listed[i].Name != call {
			continue
		}
		run := listed[i].Run
		listed[i].Run = func(c *prop.Case, client int, input any) {
			before, err := observe(c.Context())
			run(c, client, input)
			after, errAfter := observe(c.Context())
			h.Engage()
			h.Expect(spec.NotConserved)
			assert.NoError(h, errors.Join(err, errAfter), h.Statement())
			assert.Equal(h, after, before, h.Statement())
		}
	}
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: listed})
}

// FailedCallsLeaveNoTrace runs generated sequences of the actions with one
// more action: the action of call with every call of a dependency failing,
// which applies readback to the arguments of the call right before and right
// after it. It fails when a call of that action that fails after a failed
// call of a dependency changes the output of readback.
func FailedCallsLeaveNoTrace[S any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], call string, readback Apply,
) {
	h.Helper()
	listed := actions(c, h.Subject)
	at := slices.IndexFunc(listed, func(a stateful.Action[struct{}]) bool { return a.Name == call })
	if at >= 0 {
		failing, run := listed[at], listed[at].Run
		failing.Name = call + " with failing dependencies"
		failing.Run = func(c *prop.Case, client int, input any) {
			args, _ := input.([]any)
			before := readback(c.Context(), args)
			inj := h.Injector()
			failures := inj.Failures()
			inj.FailUntilHealed(conform.ErrInjected)
			run(c, client, input)
			inj.Heal()
			events := c.History().Events()
			out, _ := events[len(events)-1].Output.(conform.Output)
			if !out.Failed || inj.Failures() == failures {
				return
			}
			h.Engage()
			h.Expect(spec.PartialWrite)
			assert.Equal(h, readback(c.Context(), args), before, h.Statement())
		}
		listed = append(listed, failing)
	}
	stateful.Steps(c, stateful.Machine[struct{}]{Actions: listed})
}

// IterationsSeeTheMutations runs generated sequences of the actions. After the
// first step at which stream yields a value, it keeps that iteration, and
// after the last step it pulls the rest of the iteration. It fails when the
// rest misses a value that a call of mutate after that step wrote, unless the
// first value is that value or a later call of remove removed it. It also
// fails when the rest yields a value that a call of remove after that step
// removed and no later call of mutate wrote again. written and removed return
// the value that a call of mutate writes and a call of remove removes. After
// a step at which stream fails or yields no value, the probe starts the
// iteration again after the next step.
func IterationsSeeTheMutations[S, E any](
	h *conform.Handle[S], c *prop.Case, actions Actions[S], stream Pull[E], mutate, remove string,
	written, removed func(args []any) E,
) {
	h.Helper()
	var it *iteration[E]
	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: actions(c, h.Subject),
		Invariant: func(c *prop.Case, _ struct{}) {
			if it != nil {
				return
			}
			next, stop, err := stream(h.Context())
			if err != nil {
				return
			}
			first, yielded := next()
			if !yielded {
				stop()
				return
			}
			it = &iteration[E]{next: next, stop: stop, first: first, from: len(c.History().Events())}
		},
	})
	if it == nil {
		return
	}
	defer it.stop()
	var values []E
	var present []bool
	for _, call := range completions(c.History().Events(), it.from) {
		var v E
		switch {
		case call.out.Failed:
			continue
		case call.operation == mutate:
			v = written(call.args)
		case call.operation == remove:
			v = removed(call.args)
		default:
			continue
		}
		at := slices.IndexFunc(values, func(e E) bool { return reflect.DeepEqual(e, v) })
		if at < 0 {
			values, present, at = append(values, v), append(present, false), len(values)
		}
		present[at] = call.operation == mutate
	}
	if len(values) == 0 {
		return
	}
	var rest []E
	for v, more := it.next(); more; v, more = it.next() {
		rest = append(rest, v)
	}
	var missed, removedYet []E
	for i, v := range values {
		yielded := slices.ContainsFunc(rest, func(e E) bool { return reflect.DeepEqual(e, v) })
		switch {
		case present[i] && !yielded && !reflect.DeepEqual(it.first, v):
			missed = append(missed, v)
		case !present[i] && yielded:
			removedYet = append(removedYet, v)
		}
	}
	h.Engage()
	h.Expect(spec.StaleIteration)
	assert.Empty(h, missed, h.Statement())
	assert.Empty(h, removedYet, h.Statement())
}

// iteration is the iteration of a stream that a probe keeps across the steps
// of a sequence: its pull functions, its first value, and the index of the
// history event after the step at which it started.
type iteration[E any] struct {
	next  func() (E, bool)
	stop  func()
	first E
	from  int
}

// completed is a call of a sequence that completed with an output.
type completed struct {
	// operation is the name of the callable of the call.
	operation string
	// args and keys are the arguments and the keys of the call.
	args, keys []any
	// out is the output of the call.
	out conform.Output
	// at is the index of the invocation of the call in the history.
	at int
}

// completions returns the calls of the history events that completed with an
// output at the index from or later, in the order of their completions.
func completions(events []history.Event, from int) []completed {
	var calls []completed
	for i := from; i < len(events); i++ {
		if events[i].Kind != history.OK {
			continue
		}
		invocation := &events[events[i].Call]
		out, _ := events[i].Output.(conform.Output)
		calls = append(calls, completed{
			operation: invocation.Operation, args: invocation.Args, keys: invocation.Keys, out: out,
			at: invocation.Index,
		})
	}
	return calls
}

// sequences runs generated sequences of the actions on the subject of h in
// the case c. After each step it passes judge the calls that completed with
// an output since the step before.
func sequences[S any](h *conform.Handle[S], c *prop.Case, actions Actions[S], judge func(calls []completed)) {
	judged := 0
	stateful.Steps(c, stateful.Machine[struct{}]{
		Actions: actions(c, h.Subject),
		Invariant: func(c *prop.Case, _ struct{}) {
			events := c.History().Events()
			calls := completions(events, judged)
			judged = len(events)
			judge(calls)
		},
	})
}

// memo keeps a value for each list of arguments or keys of the calls of a
// sequence. It compares two lists as reflect.DeepEqual compares them.
type memo[T any] struct {
	lists  [][]any
	values []T
}

// index returns the index of list in the memo, and -1 for a list that the
// memo lacks.
func (m *memo[T]) index(list []any) int {
	return slices.IndexFunc(m.lists, func(l []any) bool { return reflect.DeepEqual(l, list) })
}

// at returns the value of list, and false for a list that the memo lacks.
func (m *memo[T]) at(list []any) (T, bool) {
	i := m.index(list)
	if i < 0 {
		var zero T
		return zero, false
	}
	return m.values[i], true
}

// put sets the value of list.
func (m *memo[T]) put(list []any, v T) {
	if i := m.index(list); i >= 0 {
		m.values[i] = v
		return
	}
	m.lists = append(m.lists, list)
	m.values = append(m.values, v)
}

// successive passes compare each call of operation in calls that succeeded,
// with the call of operation before it that has the same list in group. last
// keeps that call of each list across the steps of a sequence.
func successive(
	calls []completed, operation string, group func(c *completed) []any, last *memo[completed],
	compare func(before, call completed),
) {
	for _, call := range calls {
		if call.operation != operation || call.out.Failed {
			continue
		}
		if before, found := last.at(group(&call)); found {
			compare(before, call)
		}
		last.put(group(&call), call)
	}
}

// argsOf returns the arguments of a call.
func argsOf(c *completed) []any { return c.args }

// keysOf returns the keys of a call.
func keysOf(c *completed) []any { return c.keys }

// value returns the first value of an output, and the zero V where the value
// is not a V.
func value[V any](out conform.Output) V {
	v, _ := out.Values[0].(V)
	return v
}

// atLeast reports whether later is at least earlier.
func atLeast[O cmp.Ordered](earlier, later O) bool { return later >= earlier }

// above reports whether later is above earlier.
func above[O cmp.Ordered](earlier, later O) bool { return later > earlier }

// readVersion applies read to args, and returns the version of the value that
// read returns. It sets reason as the reason of the assertions after it, and
// fails h when read fails.
func readVersion[S, V any, O cmp.Ordered](
	h *conform.Handle[S], read Apply, args []any, version func(v V) O, reason spec.ReasonID,
) O {
	h.Helper()
	h.Expect(reason)
	out := read(h.Context(), args)
	assert.False(h, out.Failed, h.Statement())
	return version(value[V](out))
}

// writtenVersion returns the version of the call of a write that succeeded:
// the version of the value that the write returned, and for a write that
// returned no value of the type V, the version of the value that read returns
// for the arguments of the write. It sets reason as the reason of the
// assertions after it, and fails h when read fails.
func writtenVersion[S, V any, O cmp.Ordered](
	h *conform.Handle[S], call *completed, read Apply, version func(v V) O, reason spec.ReasonID,
) O {
	h.Helper()
	if len(call.out.Values) > 0 {
		if v, returned := call.out.Values[0].(V); returned {
			h.Expect(reason)
			return version(v)
		}
	}
	return readVersion(h, read, call.args, version, reason)
}

// groupOf returns the keys of a call, and its arguments for a call without
// keys.
func groupOf(c *completed) []any {
	if len(c.keys) > 0 {
		return c.keys
	}
	return c.args
}

// drainLog is the state of a probe of drains across the steps of a sequence:
// each call of write that succeeded, and the last call of drain that
// succeeded for each list of keys.
type drainLog[E any] struct {
	writes []completed
	last   memo[drainRecord[E]]
}

// drainRecord is a call of drain that succeeded: the elements that it
// returned, and the number of the calls of write that succeeded before it.
type drainRecord[E any] struct {
	elements []E
	writes   int
}

// judge passes judge each call of drain in calls that succeeded after a call
// of write since the call of drain with the same keys before it. It passes the
// elements that the drain gains over that call, and the elements that written
// returns for the calls of write since it. A call of write and a call of
// drain meet where their keys are the same, and a call without keys meets
// every call.
//
// A drain that consumes what it returns gains each element written since the
// drain before it, and so does a drain that returns a snapshot. Where an
// element written since equals an element of the drain before, the two drains
// gain different elements. judge passes no such call of drain.
func (d *drainLog[E]) judge(
	calls []completed, drain, write string, written func(args []any) E, judge func(gained, wrote []E),
) {
	for _, call := range calls {
		switch {
		case call.out.Failed:
		case call.operation == write:
			d.writes = append(d.writes, call)
		case call.operation == drain:
			before, _ := d.last.at(call.keys)
			var wrote []E
			for _, w := range d.writes[before.writes:] {
				if len(w.keys) == 0 || len(call.keys) == 0 || reflect.DeepEqual(w.keys, call.keys) {
					wrote = append(wrote, written(w.args))
				}
			}
			elements := value[[]E](call.out)
			if len(wrote) > 0 && len(without(wrote, before.elements)) == len(wrote) {
				judge(without(elements, before.elements), wrote)
			}
			d.last.put(call.keys, drainRecord[E]{elements: elements, writes: len(d.writes)})
		}
	}
}
