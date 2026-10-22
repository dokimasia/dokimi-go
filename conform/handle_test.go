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
	"go.dokimi.dev/assert/prop"

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
			wantMessage: "conform: the application does not pass the option of a door: " +
				"Store.Get/keeps-the-value calls Reference without the option conform.Reference. " +
				"Pass the option to the application:\n" +
				"\tconform.Reference(func(h *conform.Handle[conform_test.Store]) conform_test.Store { … })",
		},
		{
			name:     "Second",
			giveCall: func(h *conform.Handle[Store]) (got, want Store) { return h.Second(), nil },
			wantMessage: "conform: the application does not pass the option of a door: " +
				"Store.Get/keeps-the-value calls Second without the option conform.Second. " +
				"Pass the option to the application:\n" +
				"\tconform.Second(func(h *conform.Handle[conform_test.Store]) conform_test.Store { … })",
		},
		{
			name: "Restart",
			giveCall: func(h *conform.Handle[Store]) (got, want Store) {
				s := h.Subject()
				return h.Restart(s), s
			},
			wantMessage: "conform: the application does not pass the option of a door: " +
				"Store.Get/keeps-the-value calls Restart without the option conform.Restartable. " +
				"Pass the option to the application:\n" +
				"\tconform.Restartable(func(h *conform.Handle[conform_test.Store], s conform_test.Store) " +
				"conform_test.Store { … })",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("ends the check with a fault without its option", func(t *testing.T) {
				t.Parallel()

				var reached atomic.Bool
				c := keeps()
				c.Apply = func(h *conform.Handle[Store]) {
					tt.giveCall(h)
					reached.Store(true)
				}
				r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
				faults := r.Faults()
				assert.Length(t, faults, 1, "the door ends the check with one fault")
				expect.ErrorIs(t, faults[0], conform.ErrOption, "the fault is the fault of a door without its option")
				expect.Equal(t, faults[0].Error(), tt.wantMessage, "the fault contains the option of the door")
				expect.False(t, reached.Load(), "the check ends at the door")
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
				assert.Matches(t, childOutput(t),
					`handle_test\.go:\d+: conform: the application does not pass the option of a door: `+
						`Store\.Get/keeps-the-value calls `+tt.name,
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

		t.Run("gives each case of a property check a clock of its own", func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			clocks := map[assert.Clock]bool{}
			cases := 0
			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				mu.Lock()
				defer mu.Unlock()
				clocks[h.Clock()] = true
				cases++
			}
			c.Run(t, newStore)
			expect.Equal(t, len(clocks), cases, "no two cases share a clock")
		})
	})

	t.Run("Rand", func(t *testing.T) {
		t.Parallel()

		// first returns the first value of the random source of each run of
		// the check c.
		first := func(t *testing.T, c conform.Check[Store]) []uint64 {
			t.Helper()

			var mu sync.Mutex
			var values []uint64
			record := func(h *conform.Handle[Store]) {
				mu.Lock()
				defer mu.Unlock()
				values = append(values, h.Rand().Uint64())
			}
			if c.Property != nil {
				c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
					_ = pc.Draw(prop.String(), "key")
					record(h)
				}
			} else {
				c.Apply = record
			}
			c.Run(t, newStore)
			return values
		}

		t.Run("draws the values of a property check from each case", func(t *testing.T) {
			t.Parallel()

			distinct := map[uint64]bool{}
			for _, v := range first(t, keepsEvery()) {
				distinct[v] = true
			}
			expect.True(t, len(distinct) > 1, "the cases of a property check draw other values")
		})

		t.Run("returns the same values in each run of a check", func(t *testing.T) {
			t.Parallel()

			expect.Equal(t, first(t, keeps()), first(t, keeps()), "two runs of one check draw the same values")
		})

		t.Run("returns other values for another check", func(t *testing.T) {
			t.Parallel()

			expect.NotEqual(t, first(t, keeps()), first(t, misses()), "two checks draw other values")
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

		t.Run("ends the check with a fault when only the check read the clock", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				_ = h.Subject()
				_ = h.Clock()
				h.Advance(time.Minute)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
			faults := r.Faults()
			assert.Length(t, faults, 1, "Advance ends the check with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrClock, "the fault is the fault of a clock that no subject read")
			expect.Equal(t, faults[0].Error(),
				"conform: no subject reads the clock of the handle: "+
					"Store.Get/keeps-the-value advances a clock that the subject never read. "+
					"Build the subject on the clock of the handle:\n"+
					"\tRunStoreSuiteFromHandle(t, func(h *conform.Handle[conform_test.Store]) conform_test.Store "+
					"{ … h.Clock() … })",
				"the fault contains the call that builds the subject on the clock")
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

		t.Run("returns the context of the case in a property check", func(t *testing.T) {
			t.Parallel()

			var mismatched atomic.Bool
			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				if h.Context() != pc.Context() {
					mismatched.Store(true)
				}
			}
			c.Run(t, newStore)
			expect.False(t, mismatched.Load(), "the handle of each case returns the context of the case")
		})
	})

	t.Run("Unwrap", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the seat of the check", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			var got assert.TB
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { got = h.Unwrap() }
			c.Run(r, newStore)
			assert.Equal(t, got, assert.TB(r), "the handle wraps the seat of the check", assert.ByIdentity())
		})

		t.Run("writes the call record of an assertion of the check to the seat", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			keeps().Run(r, newStore)
			assert.Length(t, r.Records(), 1, "the seat keeps the call record of the one assertion of the check")
		})

		t.Run("returns the case in a property check", func(t *testing.T) {
			t.Parallel()

			var mismatched atomic.Bool
			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				if h.Unwrap() != assert.TB(pc) {
					mismatched.Store(true)
				}
			}
			c.Run(t, newStore)
			expect.False(t, mismatched.Load(), "the handle of each case wraps the case")
		})
	})

	t.Run("Errorf", func(t *testing.T) {
		t.Parallel()

		t.Run("reports a failure outside an assertion and returns", func(t *testing.T) {
			t.Parallel()

			var reached atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				h.Errorf("the store is %s", "slow")
				reached.Store(true)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
			expect.Equal(t, r.Message(), "the store is slow", "the seat takes the message of the failure")
			expect.True(t, reached.Load(), "Errorf returns to the check")
		})
	})

	t.Run("Fatalf", func(t *testing.T) {
		t.Parallel()

		t.Run("reports a failure outside an assertion and stops the check", func(t *testing.T) {
			t.Parallel()

			var reached atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				h.Fatalf("the store is %s", "closed")
				reached.Store(true)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
			expect.Equal(t, r.Message(), "the store is closed", "the seat takes the message of the failure")
			expect.False(t, reached.Load(), "Fatalf stops the check")
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
