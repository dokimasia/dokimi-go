// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"testing"
	"time"

	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/sim"
)

// odd is an event with a field of each kind that the checksum encodes.
type odd struct {
	any   any
	m     map[string]int
	refs  map[string]*odd
	f     func()
	ch    chan int
	next  *odd
	slice []int
	items []any
	arr   [2]int
	z     complex128
	x     float64
	u     uint8
	b     bool
}

func (odd) event() {}

// quiet is a machine that returns no effect.
type quiet struct{}

func (quiet) Step(_ event, out []effect) []effect { return out }
func (quiet) Err() error                          { return nil }

// on returns the steps of a world whose store steps on its start and on ev.
func on(ev event) func(w *sim.World[event, effect]) {
	return func(w *sim.World[event, effect]) {
		w.Spawn("store", newStore)
		w.Step("store")
		w.Deliver("store", ev)
		w.Step("store")
	}
}

// cycle returns an odd whose field next points to itself.
func cycle() *odd {
	o := &odd{}
	o.next = o
	return o
}

// backTo returns an odd with a chain of two pointers whose second pointer
// points back to the pointer at index at of the chain.
func backTo(at int) odd {
	a, b := &odd{}, &odd{}
	a.next = b
	b.next = []*odd{a, b}[at]
	return odd{next: a}
}

// mapCycle returns an odd whose map refs contains the odd itself.
func mapCycle() *odd {
	o := &odd{}
	o.refs = map[string]*odd{"self": o}
	return o
}

// sliceCycle returns an odd whose slice items contains the slice itself.
func sliceCycle() odd {
	items := make([]any, 1)
	items[0] = items
	return odd{items: items}
}

// prefixCycle returns an odd whose slice items of two elements contains the
// first n elements of itself.
func prefixCycle(n int) odd {
	items := make([]any, 2)
	items[0] = items[:n]
	return odd{items: items}
}

func TestLog(t *testing.T) {
	t.Parallel()

	t.Run("Log", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a log whose extension leaves the next step of the world out", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Spawn("other", newStore)
				w.Step("store")
				extended := append(w.Log(), sim.Entry[event, effect]{Process: "extension"})
				w.Step("other")
				expect.Equal(c, extended[1].Process, "extension", "the extension keeps its own entry")
				expect.Equal(c, w.Log()[1].Process, "other", "the world keeps its own entry")
			})
		})
	})

	t.Run("Checksum", func(t *testing.T) {
		t.Parallel()

		t.Run("changes with each step", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				before := w.Checksum()
				w.Step("store")
				expect.NotEqual(c, w.Checksum(), before, "the checksum changes with the step")
			})
		})

		t.Run("returns the checksum of the encoding of every kind of value", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				on(odd{
					any: int8(1), m: map[string]int{"a": 1, "b": 2}, refs: mapCycle().refs, f: func() {},
					next: cycle(), slice: []int{1}, items: sliceCycle().items, arr: [2]int{1, 2}, z: 1 + 2i,
					x: 0.5, u: 3, b: true,
				})(w)
				expect.Equal(c, w.Checksum(), uint64(13947939472731489202),
					"the encoding of each kind is the same in every build")
			})
		})

		tests := []struct {
			name      string
			give      func(w *sim.World[event, effect])
			giveOther func(w *sim.World[event, effect])
			wantSame  bool
		}{
			{
				name: "chains the checksum of each step to the checksum before it",
				give: on(ticked{token: 1}),
				giveOther: func(w *sim.World[event, effect]) {
					on(ticked{token: 2})(w)
					w.Deliver("store", ticked{token: 1})
					w.Step("store")
				},
			},
			{
				name: "hashes the process of a step",
				give: func(w *sim.World[event, effect]) {
					w.Spawn("store", newStore)
					w.Step("store")
				},
				giveOther: func(w *sim.World[event, effect]) {
					w.Spawn("other", newStore)
					w.Step("other")
				},
			},
			{
				name: "hashes the epoch of a step",
				give: func(w *sim.World[event, effect]) {
					w.Spawn("store", newStore)
					w.Crash("store")
					w.Restart("store")
					w.Step("store")
				},
				giveOther: func(w *sim.World[event, effect]) {
					w.Spawn("other", newStore)
					w.Spawn("store", newStore)
					w.Step("store")
				},
			},
			{
				name: "hashes the effects of a step",
				give: func(w *sim.World[event, effect]) {
					w.Spawn("store", newStore)
					w.Step("store")
				},
				giveOther: func(w *sim.World[event, effect]) {
					w.Spawn("store", func(*sim.Clock) sim.Machine[event, effect] { return quiet{} })
					w.Step("store")
				},
			},
			{
				name: "hashes the time of a step",
				give: func(w *sim.World[event, effect]) {
					w.Spawn("store", newStore)
					w.Step("store")
				},
				giveOther: func(w *sim.World[event, effect]) {
					w.Spawn("store", newStore)
					w.Advance(time.Second)
					w.Step("store")
				},
			},
			{
				name:      "hashes a map by its entries in any order",
				give:      on(odd{m: map[string]int{"a": 1, "b": 2, "c": 3}}),
				giveOther: on(odd{m: map[string]int{"c": 3, "a": 1, "b": 2}}),
				wantSame:  true,
			},
			{
				name:      "hashes a pointer by the value that it points to",
				give:      on(odd{next: &odd{u: 1}}),
				giveOther: on(odd{next: &odd{u: 1}}),
				wantSame:  true,
			},
			{name: "ends a cycle of pointers", give: on(*cycle()), giveOther: on(*cycle()), wantSame: true},
			{name: "ends a cycle through a map", give: on(*mapCycle()), giveOther: on(*mapCycle()), wantSame: true},
			{name: "ends a cycle through a slice", give: on(sliceCycle()), giveOther: on(sliceCycle()), wantSame: true},
			{
				name:      "hashes a function and a channel by whether they are nil",
				give:      on(odd{f: func() {}, ch: make(chan int)}),
				giveOther: on(odd{f: func() { _ = 0 }, ch: make(chan int, 1)}),
				wantSame:  true,
			},
			{
				name:      "tells two maps apart by a value",
				give:      on(odd{m: map[string]int{"a": 1}}),
				giveOther: on(odd{m: map[string]int{"a": 2}}),
			},
			{name: "tells a nil map from an empty map", give: on(odd{}), giveOther: on(odd{m: map[string]int{}})},
			{name: "tells a nil slice from an empty slice", give: on(odd{}), giveOther: on(odd{slice: []int{}})},
			{
				name:      "tells two slices apart by an element",
				give:      on(odd{slice: []int{1}}),
				giveOther: on(odd{slice: []int{2}}),
			},
			{
				name:      "tells two arrays apart by an element",
				give:      on(odd{arr: [2]int{1, 2}}),
				giveOther: on(odd{arr: [2]int{1, 3}}),
			},
			{
				name:      "tells two complex numbers apart by their imaginary part",
				give:      on(odd{z: 1 + 1i}),
				giveOther: on(odd{z: 1 + 2i}),
			},
			{
				name:      "tells two complex numbers apart by their real part",
				give:      on(odd{z: 1 + 1i}),
				giveOther: on(odd{z: 2 + 1i}),
			},
			{name: "tells two floats apart", give: on(odd{x: 0.5}), giveOther: on(odd{x: 0.25})},
			{name: "tells two unsigned integers apart", give: on(odd{u: 1}), giveOther: on(odd{u: 2})},
			{name: "tells true from false", give: on(odd{b: true}), giveOther: on(odd{b: false})},
			{name: "tells a nil interface from a value in it", give: on(odd{}), giveOther: on(odd{any: 0})},
			{
				name:      "tells two values of an interface apart by their type",
				give:      on(odd{any: int8(1)}),
				giveOther: on(odd{any: int16(1)}),
			},
			{name: "tells a nil function from a function", give: on(odd{}), giveOther: on(odd{f: func() {}})},
			{name: "tells a nil pointer from a pointer", give: on(odd{}), giveOther: on(odd{next: &odd{}})},
			{
				name:      "tells a cycle from a chain of two",
				give:      on(*cycle()),
				giveOther: on(odd{next: &odd{next: &odd{}}}),
			},
			{
				name:      "tells two references back into a chain apart by their target",
				give:      on(backTo(0)),
				giveOther: on(backTo(1)),
			},
			{
				name:      "tells a cycle through a map from a chain",
				give:      on(*mapCycle()),
				giveOther: on(odd{refs: map[string]*odd{"self": {}}}),
			},
			{
				name:      "tells a slice that contains itself from one that contains a shorter slice of its array",
				give:      on(prefixCycle(2)),
				giveOther: on(prefixCycle(1)),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				inCase(t, func(c *prop.Case) {
					w, other := sim.New(c, anatomy), sim.New(c, anatomy)
					tt.give(w)
					tt.giveOther(other)
					expect.Equal(c, w.Checksum() == other.Checksum(), tt.wantSame,
						"the two checksums are equal exactly when the two worlds make the same steps")
				})
			})
		}
	})
}
