// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// outsideCases returns a factory of stores in memory, which counts each store
// that it builds on a handle outside the cases of a property run in built.
func outsideCases(built *atomic.Int32) func(*conform.Handle[Store]) Store {
	return func(h *conform.Handle[Store]) Store {
		if _, isCase := h.Unwrap().(*prop.Case); !isCase {
			built.Add(1)
		}
		return newMemory()
	}
}

// tested is a recorder of the test t. Its method Unwrap returns t, so a check
// with Around runs inside the environment of t, and the recorder keeps what
// conform reports on the seat.
type tested struct {
	*assert.Recorder

	t *testing.T
}

// Unwrap returns the test of the recorder.
func (s *tested) Unwrap() assert.TB { return s.t }

// onTest returns a recorder of the test t.
func onTest(t *testing.T) *tested {
	t.Helper()
	return &tested{Recorder: assert.NewRecorder(), t: t}
}

// strand starts a goroutine that blocks on a channel until the process ends.
// Inside a bubble, the bubble fails once its root goroutine returns.
func strand() {
	blocked := make(chan struct{})
	go func() { <-blocked }()
}

// stranding returns the check c, whose body first strands a goroutine.
func stranding(c conform.Check[Store]) conform.Check[Store] {
	if apply := c.Apply; apply != nil {
		c.Apply = func(h *conform.Handle[Store]) {
			strand()
			apply(h)
		}
		return c
	}
	property := c.Property
	c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
		strand()
		property(h, pc)
	}
	return c
}

// tracked returns the check c, whose function Apply first sets ran.
func tracked(c conform.Check[Store], ran *atomic.Bool) conform.Check[Store] {
	apply := c.Apply
	c.Apply = func(h *conform.Handle[Store]) {
		ran.Store(true)
		apply(h)
	}
	return c
}

// A check applies to a subject on a seat, and falsification proves that it
// fails on its planted defect at the assertion of its statement.
func TestCheck(t *testing.T) {
	t.Parallel()

	t.Run("Run", func(t *testing.T) {
		t.Parallel()

		t.Run("applies the check to a subject of the factory", func(t *testing.T) {
			t.Parallel()

			keeps().Run(t, newStore)
		})

		t.Run("stops the seat at the first failed assertion of the check", func(t *testing.T) {
			t.Parallel()

			r := recorded(func(tb assert.TB) { keeps().Run(tb, forget) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "the check stops at its first failure")
			assert.Equal(t, failures[0].Contract, keepsTheValue, "the check fails at the assertion of its statement")
		})

		t.Run("applies a property check to a fresh subject of the factory in each case", func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			subjects := map[Store]bool{}
			cases := 0
			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				s := h.Subject()
				mu.Lock()
				defer mu.Unlock()
				subjects[s] = true
				cases++
			}
			c.Run(t, newStore)
			expect.True(t, cases >= 100, "the property engine runs 100 valid cases by default")
			expect.Equal(t, len(subjects), cases, "no two cases share a subject")
		})

		t.Run("fails the seat with the record of the property", func(t *testing.T) {
			t.Parallel()

			r := recorded(func(tb assert.TB) { keepsEvery().Run(tb, forget) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "the property fails once")
			expect.Equal(t, failures[0].Assertion, "prop-for-all", "the record is the record of the property")
			expect.Equal(t, failures[0].Contract, keepsEveryValue, "the contract of the property is the statement")
			inner, isCase := failures[0].CaseFailure()
			assert.True(t, isCase, "the record contains the failure of the smallest case")
			expect.Equal(t, inner.Contract, keepsEveryValue, "the case fails at the assertion of the statement")
		})

		t.Run("passes the options of a property check after the options of the application", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			c := keepsEvery()
			c.Options = func(*conform.Handle[Store]) []prop.Option { return []prop.Option{prop.Cases(50)} }
			c.Run(r, newStore, conform.Cases[Store](3))
			cases, counted := detailOf(t, r, "cases").(float64)
			assert.True(t, counted, "the record of the run counts its cases")
			expect.True(t, cases >= 50, "the cases of the check replace the 3 cases of the application")
		})

		t.Run("fails a run that leaves a share of the options unmet", func(t *testing.T) {
			t.Parallel()

			r := recorded(func(tb assert.TB) { requiring().Run(tb, newStore) })
			expect.True(t, r.Failed(), "the run fails the share of the options of the check")
		})

		t.Run("builds the subject of the handle of the options from the factory", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			var subject Store
			c := keepsEvery()
			c.Options = func(h *conform.Handle[Store]) []prop.Option {
				subject = h.Subject()
				return nil
			}
			c.Run(t, outsideCases(&built), conform.Cases[Store](1))
			_, isMemory := subject.(*memory)
			expect.True(t, isMemory, "the handle of the options returns a subject of the factory")
			expect.Equal(t, built.Load(), int32(1), "the factory builds one subject outside the cases")
		})

		t.Run("builds no subject outside the cases for options that do not read it", func(t *testing.T) {
			t.Parallel()

			var built atomic.Int32
			c := keepsEvery()
			c.Options = func(*conform.Handle[Store]) []prop.Option { return nil }
			c.Run(t, outsideCases(&built), conform.Cases[Store](1))
			expect.Equal(t, built.Load(), int32(0), "the factory builds the subjects of the cases alone")
		})

		t.Run("ends the handle of the options before the first case", func(t *testing.T) {
			t.Parallel()

			var ended, early atomic.Bool
			c := keepsEvery()
			property := c.Property
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				if !ended.Load() {
					early.Store(true)
				}
				property(h, pc)
			}
			c.Options = func(h *conform.Handle[Store]) []prop.Option {
				h.Cleanup(func() { ended.Store(true) })
				return nil
			}
			c.Run(assert.NewRecorder(), newStore)
			expect.True(t, ended.Load(), "the handle of the options runs its cleanups")
			expect.False(t, early.Load(), "the cleanups of the options run before the first case")
		})

		t.Run("leaves the options of a check without a property uncalled", func(t *testing.T) {
			t.Parallel()

			var called atomic.Bool
			c := keeps()
			c.Options = func(*conform.Handle[Store]) []prop.Option {
				called.Store(true)
				return nil
			}
			c.Run(t, newStore)
			expect.False(t, called.Load(), "a check without a property has no run for its options")
		})

		t.Run("leaves out the doors of the planted defect", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Doors = conform.Doors[Store]{Reference: newStore}
			c.Apply = func(h *conform.Handle[Store]) { _ = h.Reference() }
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore) })
			faults := r.Faults()
			assert.Length(t, faults, 1, "the subject of the factory has no door of the planted defect")
			expect.ErrorIs(t, faults[0], conform.ErrOption, "the fault is the fault of a door that the handle lacks")
		})

		t.Run("runs the check inside its environment", func(t *testing.T) {
			t.Parallel()

			bubbled(keeps()).Run(t, newStore)
		})

		t.Run("runs each case of a property check inside its environment", func(t *testing.T) {
			t.Parallel()

			bubbled(keepsEvery()).Run(t, newStore)
		})

		t.Run("finds the test of a recorder of WithTest", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder().WithTest(t)
			bubbled(keeps()).Run(r, newStore)
			expect.Empty(t, r.Faults(), "the check finds the test of its environment")
		})

		t.Run("fails the seat when the environment of the check fails", func(t *testing.T) {
			t.Parallel()

			s := onTest(t)
			bubbled(stranding(keeps())).Run(s, newStore)
			assert.Equal(t, s.Messages(), []string{"the environment of Store.Get/keeps-the-value fails: " +
				"deadlock: main bubble goroutine has exited but blocked goroutines remain"},
				"the seat fails with the failure of the environment")
		})

		t.Run("fails the seat when the environment of a case fails", func(t *testing.T) {
			t.Parallel()

			s := onTest(t)
			bubbled(stranding(keepsEvery())).Run(s, newStore, conform.Cases[Store](1))
			assert.Equal(t, s.Messages(), []string{"the environment of Store.Get/keeps-every-value fails: " +
				"deadlock: main bubble goroutine has exited but blocked goroutines remain"},
				"the seat fails with the failure of the environment")
		})

		t.Run("ends with a fault on a seat without a test", func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool
			c := tracked(bubbled(keeps()), &ran)
			r := assert.NewRecorder()
			c.Run(r, newStore)
			faults := r.Faults()
			assert.Length(t, faults, 1, "the check ends with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrConfig, "the fault is the fault of an invalid setting")
			expect.Equal(t, faults[0].Error(), "conform: the application has an invalid setting: the check "+
				"Store.Get/keeps-the-value runs inside Around, and the seat is no *testing.T and wraps none",
				"the fault names the check")
			expect.False(t, ran.Load(), "the check does not run")
		})

		t.Run("ends with a fault when Around returns without calling run", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Around = func(*testing.T, func(*testing.T)) {}
			s := onTest(t)
			c.Run(s, newStore)
			faults := s.Faults()
			assert.Length(t, faults, 1, "the check ends with one fault")
			expect.Equal(t, faults[0].Error(), "conform: Around of Store.Get/keeps-the-value: the wrapper returned "+
				"without calling run", "the fault states the misuse of run")
		})

		t.Run("ends with a fault when Around calls run a second time", func(t *testing.T) {
			t.Parallel()

			var runs atomic.Int32
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				runs.Add(1)
				apply(h)
			}
			c.Around = func(t *testing.T, run func(*testing.T)) {
				t.Helper()
				run(t)
				run(t)
			}
			s := onTest(t)
			c.Run(s, newStore)
			faults := s.Faults()
			assert.Length(t, faults, 1, "the check ends with one fault")
			expect.Equal(t, faults[0].Error(), "conform: Around of Store.Get/keeps-the-value: the wrapper called "+
				"run a second time", "the fault states the misuse of run")
			expect.Equal(t, runs.Load(), int32(1), "the second call of run runs no check")
		})

		t.Run("runs no check when Around calls run after it returned", func(t *testing.T) {
			t.Parallel()

			var late func(*testing.T)
			var ran atomic.Bool
			c := tracked(keeps(), &ran)
			c.Around = func(t *testing.T, run func(*testing.T)) {
				t.Helper()
				late = run
			}
			c.Run(onTest(t), newStore)
			late(t)
			expect.False(t, ran.Load(), "a call of run after Around returned runs no check")
		})

		t.Run("fails the seat when Around panics before it calls run", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Around = func(t *testing.T, _ func(*testing.T)) {
				t.Helper()
				var gone *memory
				gone.Put("k", "v")
			}
			s := onTest(t)
			c.Run(s, newStore)
			expect.Empty(t, s.Faults(), "Around that panics misuses no run")
			assert.Equal(t, s.Messages(), []string{"the environment of Store.Get/keeps-the-value fails: " +
				"runtime error: invalid memory address or nil pointer dereference"},
				"the seat fails with the failure of the environment")
		})
	})

	t.Run("Falsify", func(t *testing.T) {
		t.Parallel()

		t.Run("passes when the defect fails the assertion of the statement", func(t *testing.T) {
			t.Parallel()

			keeps().Falsify(t)
		})

		t.Run("fails when the defect passes the check", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Defect = newStore
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "falsification fails once")
			assert.Equal(t, failures[0].Contract, "the planted defect of Store.Get/keeps-the-value fails the check",
				"falsification fails because the defect passes the check")
		})

		t.Run("reports one failure to a seat that goes on after a fatal failure", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Defect = newStore
			r := assert.NewRecorder()
			c.Falsify(r)
			assert.Length(t, r.Failures(), 1, "falsification fails once")
		})

		t.Run("fails when the defect fails another assertion first", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				s := h.Subject()
				s.Put("k", "v")
				_, found := s.Get("k")
				assert.True(h, found, "Get finds the key that Put stored")
				got, _ := s.Get("k")
				assert.Equal(h, got, "v", h.Statement())
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-the-value fails the assertion true with the message `+
					`"Get finds the key that Put stored". Falsification needs a failure of the assertion whose `+
					`message is the statement "Get returns the value that Put stored". Pass h.Statement() as the `+
					`message of the decisive assertion, or change the defect so that it passes the other `+
					`assertions of the check.`,
				"the message contains the assertion that failed first")
		})

		t.Run("ends with the fault of a door that the check calls without its option", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { _ = h.Reference() }
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			faults := r.Faults()
			assert.Length(t, faults, 1, "falsification ends with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrOption, "the fault is the fault of the door")
			expect.Empty(t, r.Failures(), "falsification reports no verdict on the defect")
		})

		t.Run("fails when the check stops through Fatalf before an assertion fails", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { h.Fatalf("the store is %s", "closed") }
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-the-value fails the check before an assertion fails. `+
					`Falsification needs a failure of the assertion whose message is the statement `+
					`"Get returns the value that Put stored". The check failed with this message:`+"\n"+
					`the store is closed`,
				"the message contains the failure of the check")
		})

		t.Run("fails when the check reports a failure through Errorf", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) { h.Errorf("the store is %s", "empty") }
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-the-value fails the check before an assertion fails. `+
					`Falsification needs a failure of the assertion whose message is the statement `+
					`"Get returns the value that Put stored". The check failed with this message:`+"\n"+
					`the store is empty`,
				"the message contains the failure of the check")
		})

		t.Run("passes a planted defect that the checks of the options pass", func(t *testing.T) {
			t.Parallel()

			misses().Falsify(t, conform.With(keeps()))
		})

		t.Run("fails a planted defect that fails a check of the options", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			r := recorded(func(tb assert.TB) { c.Falsify(tb, conform.With(keeps())) })
			assert.Equal(t, r.Message(),
				"the planted defect conform_test.scrambles of Store.Get/misses-unknown-keys also fails these "+
					"checks:\n"+
					"\tStore.Get/keeps-the-value fails at the assertion equal with the message "+
					`"Get returns the value that Put stored"`+"\n"+
					"A planted defect fails the check that it proves and no other. Change the defect so that it "+
					"passes these checks, or give each check that it fails the same defect.",
				"the message lists the defect, its check and each other check that the defect fails")
		})

		t.Run("lists a check of the options that fails the defect through Errorf", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			complaining := keeps()
			complaining.Apply = func(h *conform.Handle[Store]) {
				if _, isScrambled := h.Subject().(scrambled); isScrambled {
					h.Errorf("the store scrambles %s", "values")
				}
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb, conform.With(complaining)) })
			assert.Contains(t, r.Message(),
				"\n\tStore.Get/keeps-the-value fails with the message \"the store scrambles values\"\n",
				"the message lists the check with the text of its failure")
		})

		t.Run("lists a check of the options under which the defect panics", func(t *testing.T) {
			t.Parallel()

			panicking := keeps()
			panicking.Apply = func(h *conform.Handle[Store]) {
				if _, isInventive := h.Subject().(inventive); isInventive {
					var gone *memory
					gone.Put("k", "v")
				}
				keeps().Apply(h)
			}
			r := recorded(func(tb assert.TB) { misses().Falsify(tb, conform.With(panicking)) })
			assert.Contains(t, r.Message(), "\n\tStore.Get/keeps-the-value panics with runtime error: invalid memory "+
				"address or nil pointer dereference\n", "the message lists the check with the value of the panic")
		})

		t.Run("accepts a check of the options that shares the planted defect", func(t *testing.T) {
			t.Parallel()

			c, other := misses(), keeps()
			c.Defect, other.Defect = scrambles, scrambles
			c.Falsify(t, conform.With(other))
		})

		t.Run("ignores a check of the options that ends with a fault on the planted defect", func(t *testing.T) {
			t.Parallel()

			faulting := keeps()
			faulting.Apply = func(h *conform.Handle[Store]) {
				if _, isInventive := h.Subject().(inventive); isInventive {
					_ = h.Reference()
				}
				keeps().Apply(h)
			}
			misses().Falsify(t, conform.With(faulting))
		})

		t.Run("passes when the defect fails the property at the assertion of the statement", func(t *testing.T) {
			t.Parallel()

			keepsEvery().Falsify(t)
		})

		t.Run("fails when the defect passes the property", func(t *testing.T) {
			t.Parallel()

			c := keepsEvery()
			c.Defect = newStore
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			failures := r.Failures()
			assert.Length(t, failures, 1, "falsification fails once")
			expect.Equal(t, failures[0].Contract, "the planted defect of Store.Get/keeps-every-value fails the check",
				"falsification fails because the defect passes the property")
		})

		t.Run("fails when the defect fails the property at another assertion first", func(t *testing.T) {
			t.Parallel()

			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				key := pc.Draw(prop.String(), "key")
				s := h.Subject()
				s.Put(key, "v")
				_, found := s.Get(key)
				assert.True(h, found, "Get finds the key that Put stored")
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-every-value fails the assertion true with the message `+
					`"Get finds the key that Put stored". Falsification needs a failure of the assertion whose `+
					`message is the statement "Get returns the value that Put stored under any key". Pass `+
					`h.Statement() as the message of the decisive assertion, or change the defect so that it `+
					`passes the other assertions of the check.`,
				"the message contains the assertion of the case that failed first")
		})

		t.Run("fails when the defect stops a case of the property through Fatalf", func(t *testing.T) {
			t.Parallel()

			c := keepsEvery()
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				h.Fatalf("the store is %s", "closed")
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb) })
			assert.Equal(t, r.Message(),
				`the planted defect of Store.Get/keeps-every-value fails the check before an assertion fails. `+
					`Falsification needs a failure of the assertion whose message is the statement `+
					`"Get returns the value that Put stored under any key". The check failed with this message:`+
					"\n"+`the store is closed`,
				"the message contains the failure of the case")
		})

		t.Run("runs the property of the planted defect without shrinking", func(t *testing.T) {
			t.Parallel()

			var runs atomic.Int32
			c := keepsEvery()
			property := c.Property
			c.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				runs.Add(1)
				property(h, pc)
			}
			c.Falsify(t)
			expect.Equal(t, runs.Load(), int32(1), "the first failing case ends the run of the defect")
		})

		t.Run("lists a property check of the options that the defect fails", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			r := recorded(func(tb assert.TB) { c.Falsify(tb, conform.With(keepsEvery())) })
			assert.Contains(t, r.Message(), "\n\tStore.Get/keeps-every-value fails at the assertion equal with the "+
				`message "Get returns the value that Put stored under any key"`+"\n",
				"the message lists the property check with the assertion of its smallest case")
		})

		t.Run("lists a property check of the options that the defect stops through Fatalf", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			stopping := keepsEvery()
			stopping.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				if _, isScrambled := h.Subject().(scrambled); isScrambled {
					h.Fatalf("the store is %s", "closed")
				}
			}
			r := recorded(func(tb assert.TB) { c.Falsify(tb, conform.With(stopping)) })
			assert.Contains(t, r.Message(), "\n\tStore.Get/keeps-every-value fails with the message \"the store is "+
				"closed\"\n", "the message lists the property check with the failure of its case")
		})

		t.Run("applies the check to its defect with the doors of the options", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				_, isMemory := h.Reference().(*memory)
				assert.True(h, isMemory, "Reference returns the reference implementation of the option")
				apply(h)
			}
			c.Falsify(t, conform.Reference(newStore))
		})

		t.Run("prefers the door of the check to the door of the options", func(t *testing.T) {
			t.Parallel()

			door := Store(newMemory())
			c := keeps()
			apply := c.Apply
			c.Doors = conform.Doors[Store]{Reference: func(*conform.Handle[Store]) Store { return door }}
			c.Apply = func(h *conform.Handle[Store]) {
				assert.True(h, h.Reference() == door, "Reference returns the door of the check")
				apply(h)
			}
			c.Falsify(t, conform.Reference(newStore))
		})

		t.Run("applies a check of the options to the defect without the options of that check", func(t *testing.T) {
			t.Parallel()

			misses().Falsify(t, conform.With(requiring()))
		})

		t.Run("applies a check of the options to the defect with the doors of the defect", func(t *testing.T) {
			t.Parallel()

			var restarted atomic.Bool
			c := keeps()
			c.Doors = conform.Doors[Store]{Restart: func(*conform.Handle[Store], Store) Store {
				restarted.Store(true)
				return forgetful{}
			}}
			other := misses()
			apply := other.Apply
			other.Apply = func(h *conform.Handle[Store]) {
				_ = h.Restart(h.Subject())
				apply(h)
			}
			c.Falsify(t, conform.With(other))
			assert.True(t, restarted.Load(), "the check of the options restarts the defect with the door of its check")
		})

		t.Run("passes when the defect fails the statement inside the environment of the check", func(t *testing.T) {
			t.Parallel()

			bubbled(keeps()).Falsify(t)
		})

		t.Run("passes when the defect fails a case of the property inside its environment", func(t *testing.T) {
			t.Parallel()

			bubbled(keepsEvery()).Falsify(t)
		})

		t.Run("accepts a failure of the environment after the defect fails the statement", func(t *testing.T) {
			t.Parallel()

			bubbled(stranding(keeps())).Falsify(t)
		})

		t.Run("accepts a failure of the environment after a case fails the statement", func(t *testing.T) {
			t.Parallel()

			bubbled(stranding(keepsEvery())).Falsify(t)
		})

		t.Run("fails when the environment fails before an assertion fails", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Around = synctest.Test
			c.Apply = func(*conform.Handle[Store]) { <-make(chan struct{}) }
			s := onTest(t)
			c.Falsify(s)
			assert.Equal(t, s.Message(),
				`the planted defect of Store.Get/keeps-the-value fails the check before an assertion fails. `+
					`Falsification needs a failure of the assertion whose message is the statement `+
					`"Get returns the value that Put stored". The check failed with this message:`+"\n"+
					`deadlock: all goroutines in bubble are blocked`,
				"the message contains the failure of the environment")
		})

		t.Run("skips when the check skips inside its environment", func(t *testing.T) {
			t.Parallel()

			var falsification *testing.T
			// The cleanup of t runs once its subtest has ended.
			t.Cleanup(func() {
				expect.True(t, falsification.Skipped(), "the test of the falsification skips")
			})
			t.Run("falsification", func(t *testing.T) {
				t.Parallel()

				falsification = t
				c := keeps()
				c.Around = synctest.Test
				c.Apply = func(h *conform.Handle[Store]) { h.Skipf("the store needs %s", "a disk") }
				c.Falsify(t)
			})
		})

		t.Run("ends with a fault on a seat without a test", func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool
			c := tracked(bubbled(keeps()), &ran)
			r := assert.NewRecorder()
			c.Falsify(r)
			faults := r.Faults()
			assert.Length(t, faults, 1, "falsification ends with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrConfig, "the fault is the fault of an invalid setting")
			expect.False(t, ran.Load(), "the check does not run")
		})

		t.Run("ends with a fault when Around returns without calling run", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Around = func(*testing.T, func(*testing.T)) {}
			s := onTest(t)
			c.Falsify(s)
			faults := s.Faults()
			assert.Length(t, faults, 1, "falsification ends with one fault")
			expect.Equal(t, faults[0].Error(), "conform: Around of Store.Get/keeps-the-value: the wrapper returned "+
				"without calling run", "the fault states the misuse of run")
		})

		t.Run("applies a check of the options to the defect inside its environment", func(t *testing.T) {
			t.Parallel()

			misses().Falsify(t, conform.With(bubbled(keeps())))
		})

		t.Run("applies a property check of the options to the defect inside its environment", func(t *testing.T) {
			t.Parallel()

			misses().Falsify(t, conform.With(bubbled(keepsEvery())))
		})

		t.Run("lists a check of the options that fails its environment on the defect", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			other := keeps()
			other.Around = synctest.Test
			other.Apply = func(h *conform.Handle[Store]) {
				if _, isScrambled := h.Subject().(scrambled); isScrambled {
					strand()
				}
			}
			s := onTest(t)
			c.Falsify(s, conform.With(other))
			assert.Contains(t, s.Message(), "\n\tStore.Get/keeps-the-value fails its environment with \"deadlock: "+
				"main bubble goroutine has exited but blocked goroutines remain\"\n",
				"the message lists the check with the failure of its environment")
		})

		t.Run("lists a property check of the options that fails its environment on the defect", func(t *testing.T) {
			t.Parallel()

			c := misses()
			c.Defect = scrambles
			other := keepsEvery()
			other.Around = synctest.Test
			other.Property = func(h *conform.Handle[Store], pc *prop.Case) {
				_ = pc.Draw(prop.String(), "key")
				if _, isScrambled := h.Subject().(scrambled); isScrambled {
					strand()
				}
			}
			s := onTest(t)
			c.Falsify(s, conform.With(other))
			assert.Contains(t, s.Message(), "\n\tStore.Get/keeps-every-value fails its environment with \"deadlock: "+
				"main bubble goroutine has exited but blocked goroutines remain\"\n",
				"the message lists the property check with the failure of its environment")
		})

		t.Run("ends with a fault on a seat without a test for a check of the options", func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool
			r := assert.NewRecorder()
			misses().Falsify(r, conform.With(tracked(bubbled(keeps()), &ran)))
			faults := r.Faults()
			assert.Length(t, faults, 1, "falsification ends with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrConfig, "the fault is the fault of an invalid setting")
			expect.False(t, ran.Load(), "the check of the options does not run")
		})

		t.Run("ignores a check of the options whose Around calls run a second time", func(t *testing.T) {
			t.Parallel()

			complaining := keeps()
			complaining.Around = func(t *testing.T, run func(*testing.T)) {
				t.Helper()
				run(t)
				run(t)
			}
			complaining.Apply = func(h *conform.Handle[Store]) { h.Errorf("the check ran") }
			misses().Falsify(t, conform.With(complaining))
		})

		t.Run("applies a check of the options to the defect with the context of its environment", func(t *testing.T) {
			t.Parallel()

			var same atomic.Bool
			other := keeps()
			other.Around = func(t *testing.T, run func(*testing.T)) {
				t.Helper()
				run(t)
			}
			other.Apply = func(h *conform.Handle[Store]) { same.Store(h.Context() == t.Context()) }
			misses().Falsify(t, conform.With(other))
			assert.True(t, same.Load(), "the check reads the context of the test that Around hands run")
		})
	})
}
