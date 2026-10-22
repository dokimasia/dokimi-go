// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

import (
	"math/rand/v2"
	"testing"
	"time"

	"go.dokimi.dev/assert"
)

// Group applies a setting of a whole double to the stub of each method of the
// double. A generated constructor builds one group and binds it to the test.
type Group struct {
	members []Configurable
}

// NewGroup returns the group of the stubs of the methods of one double.
func NewGroup(members ...Configurable) *Group {
	return &Group{members: members}
}

// Bind registers the check of the expected counts of each member for the end
// of the test of tb, through the method Cleanup of tb. A nil tb, a tb without
// Cleanup and a fuzz target register nothing. A fuzz target runs its body
// many times and registers a cleanup for each run, so a check there would
// report against another run.
func (g *Group) Bind(tb assert.TB) {
	if _, fuzz := tb.(*testing.F); fuzz {
		return
	}
	c, cleans := tb.(interface{ Cleanup(func()) })
	if !cleans {
		return
	}
	c.Cleanup(func() {
		for _, m := range g.members {
			m.Verify()
		}
	})
}

// StrictAll turns strict mode on for each member.
func (g *Group) StrictAll() {
	for _, m := range g.members {
		m.Strict()
	}
}

// SetClock sets the clock of each member.
func (g *Group) SetClock(clk assert.Clock) {
	for _, m := range g.members {
		m.SetClock(clk)
	}
}

// SetLatency sets the latency of each member, for a test of a slow
// dependency.
func (g *Group) SetLatency(d time.Duration) {
	for _, m := range g.members {
		m.SetLatency(d)
	}
}

// SetRandSource sets the source of the random numbers of each member.
func (g *Group) SetRandSource(src rand.Source) {
	for _, m := range g.members {
		m.SetRandSource(src)
	}
}

// BenchMode turns recording off for each member.
func (g *Group) BenchMode() {
	for _, m := range g.members {
		m.BenchMode()
	}
}

// Reset resets each member. Each member keeps the configuration of its
// results.
func (g *Group) Reset() {
	for _, m := range g.members {
		m.Reset()
	}
}
