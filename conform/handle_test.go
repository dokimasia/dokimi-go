// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/files"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// TempDir of a case of a property check makes its directory in the directory
// of temporary files. The test sets the variables of that directory, so
// neither it nor its subtests run in parallel.
func TestHandleTempDir(t *testing.T) {
	t.Run("fails the property run where the directory of temporary files is missing", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		for _, env := range []string{"TMPDIR", "TMP", "TEMP"} {
			t.Setenv(env, missing)
		}
		c := keepsEvery()
		c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
			_ = pc.Draw(prop.String(), "key")
			_ = h.TempDir()
		}
		r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
		assert.True(t, r.Failed(), "the property run fails")
		failure, isRecord := detailOf(t, r, "failure").(map[string]any)
		assert.True(t, isRecord, "the record of the property run has the failure of its case")
		assert.Matches(t, failure["contract"],
			`^conform: the handle cannot make a temporary directory for Store\.Get/keeps-every-value: .*missing`,
			"the failure of the case is the fault of TempDir with the missing directory")
	})
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

				var continued atomic.Bool
				c := keeps()
				c.Apply = func(h *conform.Handle[Store]) {
					tt.giveCall(h)
					continued.Store(true)
				}
				r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
				faults := r.Faults()
				assert.Length(t, faults, 1, "the door ends the check with one fault")
				expect.ErrorIs(t, faults[0], conform.ErrOption, "the fault is the fault of a door without its option")
				expect.Equal(t, faults[0].Error(), tt.wantMessage, "the fault contains the option of the door")
				expect.False(t, continued.Load(), "the check ends at the door")
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
			assert.Equal(t, moved, time.Duration(0), "the clock does not move")
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

	t.Run("Cleanup", func(t *testing.T) {
		t.Parallel()

		t.Run("runs the function when the test of the check ends", func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool
			// The cleanup of t runs once its subtest has ended and run the
			// function.
			t.Cleanup(func() { assert.True(t, ran.Load(), "the test of the check runs the function") })
			t.Run("check", func(t *testing.T) {
				t.Parallel()

				c := keeps()
				c.Apply = func(h *conform.Handle[Store]) { h.Cleanup(func() { ran.Store(true) }) }
				c.Run(t, newStore)
			})
		})

		t.Run("runs the function when each case of a property check ends", func(t *testing.T) {
			t.Parallel()

			var cases, cleanups atomic.Int64
			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				cases.Add(1)
				h.Cleanup(func() { cleanups.Add(1) })
				expect.Equal(t, cleanups.Load(), cases.Load()-1, "the earlier cases have run their functions")
			}
			c.Run(t, newStore)
			assert.True(t, cases.Load() > 0, "the property check runs cases")
			expect.Equal(t, cleanups.Load(), cases.Load(), "each case runs its function when it ends")
		})

		t.Run("ends the check with a fault on a seat without Cleanup", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { h.Cleanup(func() {}) }
			faults := recorded(func(tb assert.TB) { c.Run(tb, newStore) }).Faults()
			assert.Length(t, faults, 1, "Cleanup ends the check with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrCleanup, "the fault is the fault of Cleanup")
			expect.Equal(t, faults[0].Error(), "conform: the handle cannot register a cleanup: the seat of "+
				"Store.Get/keeps-the-value has no Cleanup", "the fault contains the check")
		})

		t.Run("returns to a seat that goes on after a fatal failure", func(t *testing.T) {
			t.Parallel()

			var returned atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				h.Cleanup(func() {})
				returned.Store(true)
			}
			c.Run(assert.NewRecorder(), newStore)
			assert.True(t, returned.Load(), "Cleanup returns to the check")
		})
	})

	t.Run("Skipf", func(t *testing.T) {
		t.Parallel()

		t.Run("skips the test of the check", func(t *testing.T) {
			t.Parallel()

			var check *testing.T
			var continued atomic.Bool
			// The cleanup of t runs once its subtest has ended.
			t.Cleanup(func() {
				expect.True(t, check.Skipped(), "the test of the check skips")
				expect.False(t, continued.Load(), "the check ends at Skipf")
			})
			t.Run("check", func(t *testing.T) {
				t.Parallel()

				check = t
				c := keeps()
				c.Apply = func(h *conform.Handle[Store]) {
					h.Skipf("the store needs %s", "a disk")
					continued.Store(true)
				}
				c.Run(t, newStore)
			})
		})

		t.Run("skips the property run at the case that calls it", func(t *testing.T) {
			t.Parallel()

			var check *testing.T
			var cases atomic.Int64
			// The cleanup of t runs once its subtest has ended.
			t.Cleanup(func() {
				expect.True(t, check.Skipped(), "the property run skips the test of the check")
				expect.Equal(t, cases.Load(), int64(1), "the property run ends at the first case")
			})
			t.Run("check", func(t *testing.T) {
				t.Parallel()

				check = t
				c := keepsEvery()
				c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
					_ = pc.Draw(prop.String(), "key")
					cases.Add(1)
					h.Skipf("the store needs %s", "a disk")
				}
				c.Run(t, newStore)
			})
		})

		t.Run("ends the check with a fault on a seat without Skipf", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { h.Skipf("the store needs a disk") }
			faults := recorded(func(tb assert.TB) { c.Run(tb, newStore) }).Faults()
			assert.Length(t, faults, 1, "Skipf ends the check with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrSkip, "the fault is the fault of Skipf")
			expect.Equal(t, faults[0].Error(), "conform: the handle cannot skip the check: the seat of "+
				"Store.Get/keeps-the-value has no Skipf", "the fault contains the check")
		})

		t.Run("returns to a seat that goes on after a fatal failure", func(t *testing.T) {
			t.Parallel()

			var returned atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				h.Skipf("the store needs a disk")
				returned.Store(true)
			}
			c.Run(assert.NewRecorder(), newStore)
			assert.True(t, returned.Load(), "Skipf returns to the check")
		})
	})

	t.Run("TempDir", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a directory of the test that the test removes", func(t *testing.T) {
			t.Parallel()

			var dir string
			// The cleanup of t runs once its subtest has ended and removed
			// the directory.
			t.Cleanup(func() { files.Absent(t, dir, "the test removes the directory when it ends") })
			t.Run("check", func(t *testing.T) {
				t.Parallel()

				c := keeps()
				c.Apply = func(h *conform.Handle[Store]) {
					dir = h.TempDir()
					files.IsDir(h, dir, "TempDir returns a directory")
				}
				c.Run(t, newStore)
			})
		})

		t.Run("returns a directory of each case that the case removes", func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			var dirs []string
			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				dir := h.TempDir()
				files.IsDir(h, dir, "TempDir returns a directory of the case")
				mu.Lock()
				dirs = append(dirs, dir)
				mu.Unlock()
			}
			c.Run(t, newStore)
			assert.NotEmpty(t, dirs, "the cases call TempDir")
			distinct := slices.Clone(dirs)
			slices.Sort(distinct)
			expect.Length(t, slices.Compact(distinct), len(dirs), "each case has a directory of its own")
			for _, dir := range dirs {
				files.Absent(t, dir, "the case removes its directory when it ends")
			}
		})

		t.Run("ends the check with a fault on a seat that cannot remove a directory", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { _ = h.TempDir() }
			faults := recorded(func(tb assert.TB) { c.Run(tb, newStore) }).Faults()
			assert.Length(t, faults, 1, "TempDir ends the check with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrTempDir, "the fault is the fault of TempDir")
			expect.Equal(t, faults[0].Error(), "conform: the handle cannot make a temporary directory: the seat "+
				"of Store.Get/keeps-the-value has no Cleanup", "the fault contains the check")
		})

		t.Run("returns an empty name to a seat that goes on after a fatal failure", func(t *testing.T) {
			t.Parallel()

			got := "unset"
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { got = h.TempDir() }
			c.Run(assert.NewRecorder(), newStore)
			assert.Equal(t, got, "", "TempDir returns no directory")
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

		t.Run("returns to the check after a failure outside an assertion", func(t *testing.T) {
			t.Parallel()

			var continued atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				h.Errorf("the store is %s", "slow")
				continued.Store(true)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
			expect.Equal(t, r.Message(), "the store is slow", "the seat takes the message of the failure")
			expect.True(t, continued.Load(), "Errorf returns to the check")
		})
	})

	t.Run("Fatalf", func(t *testing.T) {
		t.Parallel()

		t.Run("stops the check after a failure outside an assertion", func(t *testing.T) {
			t.Parallel()

			var continued atomic.Bool
			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				h.Fatalf("the store is %s", "closed")
				continued.Store(true)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
			expect.Equal(t, r.Message(), "the store is closed", "the seat takes the message of the failure")
			expect.False(t, continued.Load(), "Fatalf stops the check")
		})
	})

	t.Run("Report", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the record of a failure to a seat that takes records", func(t *testing.T) {
			t.Parallel()

			var continued atomic.Bool
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				apply(h)
				continued.Store(true)
			}
			r := recorded(func(tb assert.TB) { c.Run(tb, forget) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "the seat takes the record of the failure")
			assert.Equal(t, failures[0].Assertion, "equal", "the record is the record of the assertion")
			assert.False(t, continued.Load(), "the fatal failure stops the check")
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
