// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe

import "context"

// Call calls a callable and returns its value, and the error of a call that
// fails. A callable without an error result returns a nil error. A callable
// with two values returns them as a [Pair]. A callable whose value is a
// stream returns the elements that the stream yields, collected into a
// slice, and the error that ends the stream.
type Call[V any] func(ctx context.Context) (V, error)

// Act calls a callable for its effect, and returns the error of a call that
// fails. A probe of an Act does not read the values of the callable.
type Act func(ctx context.Context) error

// Lookup calls a callable with a presence flag, and returns its value and
// the flag. A callable with two values beside the flag returns them as a
// [Pair].
type Lookup[V any] func(ctx context.Context) (V, bool)

// Pair contains the two values of a callable that returns two values.
type Pair[A, B any] struct {
	First  A
	Second B
}
