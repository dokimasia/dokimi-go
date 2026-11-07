// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import (
	"context"
	"reflect"
	"slices"
	"sync"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/history"

	"go.dokimi.dev/conform"
)

// linearizability checks the history of a case against fresh references of a
// handle with history.Linearizable.
type linearizability interface {
	// check checks hist on tb under contract.
	check(tb assert.TB, hist *history.History, contract string)
}

// checker is a linearizability against a spec of the states T under the
// options opts. The options keep the searches of each check in cp, so the
// next check of a growing history continues them.
type checker[T any] struct {
	spec history.Spec[T]
	cp   history.Checkpoint[T]
	opts []history.Option
}

// check checks hist against the spec of c under the options of c.
func (c *checker[T]) check(tb assert.TB, hist *history.History, contract string) {
	history.Linearizable(tb, hist, c.spec, contract, c.opts...)
}

// newChecker returns the checker of spec under opts.
func newChecker[T any](spec history.Spec[T], opts []history.Option) *checker[T] {
	c := &checker[T]{spec: spec}
	c.opts = append(slices.Clip(opts), history.Resume(&c.cp))
	return c
}

// newLinearizability returns the check of the histories of the cases of h
// against fresh references of h through reference, under opts. Each check
// continues the searches of the check before it. For a subject of an
// interface or a pointer type, the states of the spec are the references
// themselves, as references builds them. A subject of any other type keeps the
// state of a reference in the closure of reference, so the states of its spec
// are the lists of the calls of history.SpecFrom.
func newLinearizability[S any](h *conform.Handle[S], reference Reference[S], opts []history.Option) linearizability {
	if k := reflect.TypeFor[S]().Kind(); k == reflect.Interface || k == reflect.Pointer {
		return newChecker(references(h, reference), opts)
	}
	return newChecker(history.SpecFrom(func() history.Subject {
		apply := reference(h.Reference())
		return func(operation string, args []any) any { return apply(h.Context(), operation, args) }
	}), opts)
}

// references returns the spec whose states are fresh references of h, each
// after the calls of an order of a partition. Next builds a reference,
// applies the calls of the state to it through reference and then the
// operation, and accepts the operation when the call may have returned the
// output of the reference. Two states compare as history.Spec compares states
// without Equal and Hash, so the search merges two orders of the calls that
// leave equal references. Each reference has its whole state in its value: two
// references that compare equal return the same outputs for the same calls.
//
// A reference that is no pointer is a value of an interface type S, and the
// spec takes a pointer to a copy of it, which S accepts too. Each state is then
// a pointer that no other state shares.
//
// # Concurrency
//
// Next is safe for concurrent use, as history.Workers requires.
func references[S any](h *conform.Handle[S], reference Reference[S]) history.Spec[S] {
	r := &replayed[S]{h: h, reference: reference, calls: map[any]*applied{}}
	return history.Spec[S]{Initial: r.initial, Next: r.next}
}

// replayed builds the references of a spec over the references of h. calls
// maps each reference after a call to that call, and mu guards calls. A
// reference without an entry has no call.
type replayed[S any] struct {
	h         *conform.Handle[S]
	reference Reference[S]
	calls     map[any]*applied
	mu        sync.Mutex
}

// applied is a call that a reference applied after the call before it, and nil
// for no call.
type applied struct {
	before    *applied
	operation string
	args      []any
}

// replay applies each call up to c to a reference through apply, the first
// call first.
func (c *applied) replay(ctx context.Context, apply func(context.Context, string, []any) conform.Output) {
	if c == nil {
		return
	}
	c.before.replay(ctx, apply)
	apply(ctx, c.operation, c.args)
}

// initial returns a fresh reference without a call.
func (r *replayed[S]) initial() S {
	return pointed(r.h.Reference())
}

// next returns a fresh reference after the calls of state and op, and none
// when the call may not have returned the output of the reference.
func (r *replayed[S]) next(state S, op history.Operation) []S {
	r.mu.Lock()
	last := r.calls[state]
	r.mu.Unlock()
	s := pointed(r.h.Reference())
	apply := r.reference(s)
	last.replay(r.h.Context(), apply)
	if !op.Returned(apply(r.h.Context(), op.Name, op.Args)) {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[s] = &applied{before: last, operation: op.Name, args: op.Args}
	return []S{s}
}

// pointed returns the reference s where it is a pointer, and otherwise a
// pointer to a copy of it. A reference of the second kind is a value of the
// interface type S, and S accepts the pointer too, because the method set of a
// pointer contains the methods of its target.
func pointed[S any](s S) S {
	v := reflect.ValueOf(s)
	if v.Kind() == reflect.Pointer {
		return s
	}
	p := reflect.New(v.Type())
	p.Elem().Set(v)
	boxed, _ := reflect.TypeAssert[S](p)
	return boxed
}
