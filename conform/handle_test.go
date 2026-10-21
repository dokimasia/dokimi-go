// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/conform"
)

// keyOf is the type of the key of a value that a spec puts in the context of
// a seat.
type keyOf struct{}

// texts is a seat that takes no records. It keeps the text of each failure,
// and ends the goroutine of a check at Fatalf. fatal is true once Fatalf ran.
type texts struct {
	messages []string
	mu       sync.Mutex
	fatal    bool
}

// Helper does nothing.
func (*texts) Helper() {}

// Errorf keeps the text of the failure.
func (s *texts) Errorf(format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, fmt.Sprintf(format, args...))
}

// Fatalf keeps the text of the failure and ends the goroutine.
func (s *texts) Fatalf(format string, args ...any) {
	s.Errorf(format, args...)
	s.mu.Lock()
	s.fatal = true
	s.mu.Unlock()
	runtime.Goexit()
}

// onClock is a factory of subjects that read the clock of their handle.
func onClock(h *conform.Handle[Store]) Store {
	m := newMemory()
	m.clock = h.Clock()
	return m
}

// The handle of a check is the seat of its assertions. It builds the subjects
// of the check, supplies its machinery, and stops a check that calls a door
// without its option.
func TestHandle(t *testing.T) {
	t.Parallel()

	t.Run("Subject", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the one subject of the check", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				assert.True(h, h.Subject() == h.Subject(), "Subject returns the subject that it built for the check")
			}
			c.Run(t, newStore)
		})

		t.Run("returns the planted defect under falsification", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				_, isDefect := h.Subject().(forgetful)
				assert.True(h, isDefect, "Subject returns the planted defect")
				apply(h)
			}
			c.Falsify(t)
		})
	})

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a fresh subject of the factory for each call", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				_, isMemory := h.New().(*memory)
				assert.True(h, isMemory, "New builds a subject of the factory")
				assert.True(h, h.New() != h.New(), "New returns a fresh subject")
			}
			c.Run(t, newStore)
		})
	})

	tests := []struct {
		name        string
		giveCall    func(h *conform.Handle[Store]) (got, want Store)
		wantMessage string
	}{
		{
			name:     "Reference",
			giveCall: func(h *conform.Handle[Store]) (got, want Store) { return h.Reference(), nil },
			wantMessage: "Store.Get/keeps-the-value calls Reference without the option conform.Reference. " +
				"Pass the option to the application:\n" +
				"\tconform.Reference(func(h *conform.Handle[conform_test.Store]) conform_test.Store { … })",
		},
		{
			name:     "Second",
			giveCall: func(h *conform.Handle[Store]) (got, want Store) { return h.Second(), nil },
			wantMessage: "Store.Get/keeps-the-value calls Second without the option conform.Second. " +
				"Pass the option to the application:\n" +
				"\tconform.Second(func(h *conform.Handle[conform_test.Store]) conform_test.Store { … })",
		},
		{
			name: "Restart",
			giveCall: func(h *conform.Handle[Store]) (got, want Store) {
				s := h.Subject()
				return h.Restart(s), s
			},
			wantMessage: "Store.Get/keeps-the-value calls Restart without the option conform.Restartable. " +
				"Pass the option to the application:\n" +
				"\tconform.Restartable(func(h *conform.Handle[conform_test.Store], s conform_test.Store) " +
				"conform_test.Store { … })",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("stops the check without its option", func(t *testing.T) {
				t.Parallel()

				var reached atomic.Bool
				c := keeps()
				c.Apply = func(h *conform.Handle[Store]) {
					tt.giveCall(h)
					reached.Store(true)
				}
				r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
				assert.Equal(t, r.Message(), tt.wantMessage, "the message contains the option of the door")
				assert.False(t, reached.Load(), "the check ends at the door")
			})

			t.Run("returns to a seat that goes on after a fatal failure", func(t *testing.T) {
				t.Parallel()

				var got, want Store
				c := keeps()
				c.Apply = func(h *conform.Handle[Store]) { got, want = tt.giveCall(h) }
				c.Run(assert.NewRecorder(), newStore)
				assert.Equal(t, got, want, "the door returns its fallback")
			})

			t.Run("reports the line of the call of the door", func(t *testing.T) {
				t.Parallel()

				if os.Getenv(childEnv) == t.Name() {
					c := keeps()
					c.Apply = func(h *conform.Handle[Store]) { tt.giveCall(h) }
					c.Run(t, newStore)
					return
				}
				assert.Matches(t, childOutput(t), `handle_test\.go:\d+: Store\.Get/keeps-the-value calls `+tt.name,
					"the failure has the line of the call of the door")
			})
		})
	}

	t.Run("Clock", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the clock that the subject read", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				read := h.Subject().(*memory).clock
				assert.True(h, read == h.Clock(), "the subject read the clock of the handle")
			}
			c.Run(t, onClock)
		})
	})

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		t.Run("moves the clock that the subject read", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				clock := h.Subject().(*memory).clock
				start := clock.Now()
				h.Advance(time.Minute)
				assert.Equal(h, clock.Now().Sub(start), time.Minute, "the clock moves by the advance")
			}
			c.Run(t, onClock)
		})

		t.Run("stops the check when only the check read the clock", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				_ = h.Subject()
				_ = h.Clock()
				h.Advance(time.Minute)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
			assert.Equal(t, r.Message(),
				"Store.Get/keeps-the-value advances a clock that the subject never read. "+
					"Build the subject on the clock of the handle:\n"+
					"\tRunStoreOn(t, func(h *conform.Handle[conform_test.Store]) conform_test.Store { … h.Clock() … })",
				"the message contains the call that builds the subject on the clock")
		})

		t.Run("leaves the clock at its instant for a seat that goes on after a fatal failure", func(t *testing.T) {
			t.Parallel()

			var moved time.Duration
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				start := h.Clock().Now()
				h.Advance(time.Minute)
				moved = h.Clock().Now().Sub(start)
			}
			c.Run(assert.NewRecorder(), newStore)
			assert.Equal(t, moved, time.Duration(0), "the clock stays at its instant")
		})
	})

	t.Run("Statement", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the statement of the check", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				assert.Equal(h, h.Statement(), keepsTheValue, "the handle has the statement of its check")
			}
			c.Run(t, newStore)
		})
	})

	t.Run("Context", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the context of the seat", func(t *testing.T) {
			t.Parallel()

			ctx := context.WithValue(t.Context(), keyOf{}, "the context of the seat")
			var got any
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { got = h.Context().Value(keyOf{}) }
			c.Run(assert.NewRecorder().WithContext(ctx), newStore)
			assert.Equal(t, got, any("the context of the seat"), "the handle returns the context of its seat")
		})
	})

	t.Run("Report", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the record of a failure to a seat that takes records", func(t *testing.T) {
			t.Parallel()

			var reached atomic.Bool
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				apply(h)
				reached.Store(true)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, forget) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "the seat takes the record of the failure")
			assert.Equal(t, failures[0].Assertion, "equal", "the record is the record of the assertion")
			assert.False(t, reached.Load(), "the fatal failure stops the check")
		})

		t.Run("writes the text of a fatal failure to a seat that takes no records", func(t *testing.T) {
			t.Parallel()

			s := &texts{}
			done := make(chan struct{})
			go func() {
				defer close(done)
				keeps().Run(s, forget)
			}()
			<-done
			assert.Length(t, s.messages, 1, "the seat takes the text of the failure")
			assert.HasPrefix(t, s.messages[0], keepsTheValue, "the text starts with the statement")
			assert.True(t, s.fatal, "the handle writes the text through Fatalf")
		})

		t.Run("writes the text of a failure that goes on to a seat that takes no records", func(t *testing.T) {
			t.Parallel()

			s := &texts{}
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				got, _ := h.Subject().Get("k")
				expect.Equal(h, got, "v", h.Statement())
			}
			c.Run(s, forget)
			assert.Length(t, s.messages, 1, "the seat takes the text of the failure")
			assert.False(t, s.fatal, "the handle writes the text through Errorf")
		})

		t.Run("reports the line of the assertion of the check", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				keeps().Run(t, forget)
				return
			}
			assert.Matches(t, childOutput(t), `helpers_test\.go:\d+: `+regexp.QuoteMeta(keepsTheValue),
				"the failure has the line of the assertion in the check")
		})
	})
}
