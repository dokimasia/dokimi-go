// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

import (
	"fmt"
	"slices"
	"sync"

	"go.dokimi.dev/assert"
)

// OrderTracker checks the order of the calls of the methods of a double. A
// method that the double may call only after another method checks its
// prerequisite through [OrderTracker.AssertAfter]. A strict tracker stops the
// test for a call before its prerequisite, and a lenient tracker checks
// nothing.
type OrderTracker struct {
	tb     assert.TB
	called map[string]bool
	mu     sync.Mutex
	strict bool
}

// NewOrderTracker returns a tracker that reports to tb, strict where strict is
// true.
func NewOrderTracker(tb assert.TB, strict bool) *OrderTracker {
	return &OrderTracker{called: map[string]bool{}, strict: strict, tb: tb}
}

// Record marks the method as called. A lenient tracker marks nothing.
func (o *OrderTracker) Record(method string) {
	if !o.strict {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.called[method] = true
}

// AssertAfter stops the test where the prerequisite of the method has not been
// called. A lenient tracker and a tracker without a TB check nothing.
func (o *OrderTracker) AssertAfter(method, prerequisite string) {
	if !o.strict || o.tb == nil {
		return
	}
	if !o.Called(prerequisite) {
		o.tb.Fatalf("%s: the double refuses the call before a call of %s", method, prerequisite)
	}
}

// Reset clears the marks of the called methods.
func (o *OrderTracker) Reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.called = map[string]bool{}
}

// Called reports whether the method has been called.
func (o *OrderTracker) Called(method string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.called[method]
}

// String returns the called methods in order of their names, for the message
// of a failed check.
func (o *OrderTracker) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	methods := make([]string, 0, len(o.called))
	for m := range o.called {
		methods = append(methods, m)
	}
	slices.Sort(methods)
	return fmt.Sprintf("OrderTracker{called: %v}", methods)
}
