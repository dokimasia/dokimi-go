// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/dokimi-go/stub"
)

// member records which settings of a group reached it.
type member struct {
	strict, reset, verified, bench, clocked, sourced, delayed bool
}

// Strict records the setting.
func (m *member) Strict() { m.strict = true }

// Reset records the setting.
func (m *member) Reset() { m.reset = true }

// Verify records the setting.
func (m *member) Verify() { m.verified = true }

// BenchMode records the setting.
func (m *member) BenchMode() { m.bench = true }

// SetClock records the setting.
func (m *member) SetClock(assert.Clock) { m.clocked = true }

// SetRandSource records the setting.
func (m *member) SetRandSource(rand.Source) { m.sourced = true }

// SetLatency records the setting.
func (m *member) SetLatency(time.Duration) { m.delayed = true }

// A group applies each setting to each of its members, and Bind checks the
// expected counts of the members at the end of the test.
func TestGroup(t *testing.T) {
	t.Parallel()

	t.Run("NewGroup", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a group that applies each setting to each member", func(t *testing.T) {
			t.Parallel()

			a, b := &member{}, &member{}
			g := stub.NewGroup(a, b)
			g.StrictAll()
			g.Reset()
			g.BenchMode()
			g.SetClock(assert.System{})
			g.SetRandSource(quarter{})
			g.SetLatency(time.Millisecond)
			want := member{strict: true, reset: true, bench: true, clocked: true, sourced: true, delayed: true}
			assert.Equal(t, []member{*a, *b}, []member{want, want}, "each member has each setting")
		})
	})

	t.Run("Bind", func(t *testing.T) {
		t.Parallel()

		t.Run("checks the members at the end of the test", func(t *testing.T) {
			t.Parallel()

			m := &member{}
			//nolint:paralleltest // the subtest and its cleanups end before the check reads the member
			t.Run("bound", func(t *testing.T) { stub.NewGroup(m).Bind(t) })
			assert.True(t, m.verified, "the cleanup of the test checks the member")
		})

		tests := []struct {
			name string
			give assert.TB
		}{
			{name: "registers nothing for a nil TB"},
			{name: "registers nothing for a TB without Cleanup", give: assert.NewRecorder()},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				m := &member{}
				stub.NewGroup(m).Bind(tt.give)
				assert.False(t, m.verified, "nothing checks the member")
			})
		}
	})
}

// Bind registers no check for a fuzz target, which runs its body many times.
// The check of the member is a cleanup registered before Bind, and cleanups
// run in reverse order, so a cleanup of Bind would run first.
func FuzzGroup(f *testing.F) {
	m := &member{}
	f.Cleanup(func() {
		if m.verified {
			f.Error("Bind checks no member of a fuzz target")
		}
	})
	stub.NewGroup(m).Bind(f)
	f.Add(0)
	f.Fuzz(func(*testing.T, int) {})
}
