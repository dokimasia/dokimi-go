// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"os"
	"sync/atomic"
	"testing"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
)

// The options of an application add checks, excuse checks, and supply the
// doors of the handle that only the consumer can build.
func TestSuite(t *testing.T) {
	t.Parallel()

	empty := conform.Suite[Store]{Declaration: "Store"}

	t.Run("Option", func(t *testing.T) {
		t.Parallel()

		t.Run("configures nothing as the zero value", func(t *testing.T) {
			t.Parallel()

			keeps().Run(t, newStore, conform.Option[Store]{})
		})
	})

	t.Run("With", func(t *testing.T) {
		t.Parallel()

		t.Run("applies the added check to its planted defect before the subject", func(t *testing.T) {
			t.Parallel()

			var applied atomic.Int32
			c := keeps()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				applied.Add(1)
				apply(h)
			}
			t.Cleanup(func() {
				assert.Equal(t, applied.Load(), int32(2),
					"the application applies the check to its defect and to the subject")
			})
			conform.Run(t, empty, newStore, conform.With(c))
		})
	})

	t.Run("Excuse", func(t *testing.T) {
		t.Parallel()

		t.Run("skips the excused check", func(t *testing.T) {
			t.Parallel()

			var applied atomic.Int32
			c := keeps()
			c.Apply = func(*conform.Handle[Store]) { applied.Add(1) }
			t.Cleanup(func() {
				assert.Equal(t, applied.Load(), int32(0), "the application does not apply an excused check")
			})
			conform.Run(t, empty, newStore, conform.With(c),
				conform.Excuse(storeGet.Check("keeps-the-value"), "the store keeps no values"))
		})

		t.Run("applies each check that the options do not excuse", func(t *testing.T) {
			t.Parallel()

			var applied atomic.Int32
			c := misses()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				applied.Add(1)
				apply(h)
			}
			t.Cleanup(func() {
				assert.Equal(t, applied.Load(), int32(2),
					"the application applies the check to its defect and to the subject")
			})
			conform.Run(t, empty, newStore, conform.With(keeps(), c),
				conform.Excuse(storeGet.Check("keeps-the-value"), "the store keeps no values"))
		})

		t.Run("reports the reason of the excusal in the skip of the check", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Run(t, empty, newStore, conform.With(keeps()),
					conform.Excuse(storeGet.Check("keeps-the-value"), "the store keeps no values"))
				return
			}
			assert.Contains(t, childOutput(t), "Store.Get/keeps-the-value is excused: the store keeps no values",
				"the skip of the check contains the reason of the excusal")
		})
	})

	t.Run("Reference", func(t *testing.T) {
		t.Parallel()

		t.Run("supplies a fresh reference implementation for each call", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				assert.True(h, h.Reference() != h.Reference(), "Reference returns a fresh reference implementation")
			}
			c.Run(t, newStore, conform.Reference(newStore))
		})
	})

	t.Run("Second", func(t *testing.T) {
		t.Parallel()

		t.Run("supplies a fresh second subject for each call", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				assert.True(h, h.Second() != h.Second(), "Second returns a fresh second subject")
			}
			c.Run(t, newStore, conform.Second(newStore))
		})
	})

	t.Run("Cases", func(t *testing.T) {
		t.Parallel()

		t.Run("runs a property check over the number of cases of the option", func(t *testing.T) {
			t.Parallel()

			r := assert.NewRecorder()
			keepsEvery().Run(r, newStore, conform.Cases[Store](5))
			cases, counted := detailOf(t, r, "cases").(float64)
			assert.True(t, counted, "the record of the run counts its cases")
			expect.True(t, cases < 10, "the run aims for 5 valid cases")
		})

		t.Run("runs a property check over one case", func(t *testing.T) {
			t.Parallel()

			keepsEvery().Run(t, newStore, conform.Cases[Store](1))
		})

		t.Run("ends a check with a fault for fewer than one case", func(t *testing.T) {
			t.Parallel()

			r := recorded(func(tb assert.TB) { keepsEvery().Run(tb, newStore, conform.Cases[Store](0)) })
			faults := r.Faults()
			assert.Length(t, faults, 1, "the check ends with one fault")
			expect.ErrorIs(t, faults[0], conform.ErrConfig, "the fault is the fault of a setting")
			expect.Equal(t, faults[0].Error(), "conform: the application has an invalid setting: the option "+
				"Cases asks for 0 cases, and a property run needs at least 1", "the fault contains the setting")
		})
	})

	t.Run("Using", func(t *testing.T) {
		t.Parallel()

		option := conform.Using[Store](prop.Just(stated))

		t.Run("replaces the override of the suite in each property run of an application", func(t *testing.T) {
			t.Parallel()

			conform.Run(t, statedKinds(), newStore, option)
		})

		t.Run("states the generator of a type in the property run of a check", func(t *testing.T) {
			t.Parallel()

			statedKinds().Checks[0].Run(t, newStore, option)
		})

		t.Run("states the generator of a type in the falsification of an added check", func(t *testing.T) {
			t.Parallel()

			conform.Run(t, empty, newStore, conform.With(statedKinds().Checks...), option)
		})

		t.Run("replaces the override of the suite in the calls of a benchmark", func(t *testing.T) {
			t.Parallel()

			var undeclared atomic.Bool
			suite := declaredBudget(&undeclared)
			suite.Calls[0].Budget = conform.Budget{}
			measured(t, 10, func(b conform.B) { conform.Bench(b, suite, storeGet, newStore, option) })
			assert.True(t, undeclared.Load(), "the calls draw the kind of the option")
		})
	})

	t.Run("Restartable", func(t *testing.T) {
		t.Parallel()

		t.Run("supplies the restart of a subject", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				s := h.Subject()
				s.Put("k", "v")
				got, _ := h.Restart(s).Get("k")
				assert.Equal(h, got, "v", "the restarted subject has the values of the subject")
			}
			restart := func(_ *conform.Handle[Store], s Store) Store {
				restarted := newMemory()
				value, _ := s.Get("k")
				restarted.Put("k", value)
				return restarted
			}
			c.Run(t, newStore, conform.Restartable(restart))
		})
	})

	t.Run("Crashable", func(t *testing.T) {
		t.Parallel()

		t.Run("supplies the crash of a subject during a call", func(t *testing.T) {
			t.Parallel()

			c := keeps()
			c.Apply = func(h *conform.Handle[Store]) {
				s := h.Subject()
				got, _ := h.Crash(s, func() { s.Put("k", "v") }).Get("k")
				assert.Equal(h, got, "v-torn", "the crashed subject has the value that the crash left")
			}
			crash := func(_ *conform.Handle[Store], s Store, call func()) Store {
				call()
				crashed := newMemory()
				value, _ := s.Get("k")
				crashed.Put("k", value+"-torn")
				return crashed
			}
			c.Run(t, newStore, conform.Crashable(crash))
		})
	})

	t.Run("Isolate", func(t *testing.T) {
		t.Parallel()

		isolate := conform.Isolate[Store]()

		t.Run("passes a planted defect of a derived check that the other checks pass", func(t *testing.T) {
			t.Parallel()

			derivedKeeps().Run(t, newStore, isolate, conform.With(derivedMisses()))
		})

		t.Run("fails a planted defect of a derived check that fails another check", func(t *testing.T) {
			t.Parallel()

			c := derivedMisses()
			c.Defect = scrambles
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore, isolate, conform.With(derivedKeeps())) })
			assert.Equal(t, r.Message(),
				"the planted defect conform_test.scrambles of Store.Get/misses-unknown-keys also fails these "+
					"checks:\n"+
					"\tStore.Get/keeps-the-value fails at the assertion equal with the message "+
					`"Get returns the value that Put stored"`+"\n"+
					"A planted defect fails the check that it proves and no other. Change the defect so that it "+
					"passes these checks, or give each check that it fails the same defect.",
				"the message lists the other check that the defect fails")
		})

		t.Run("exempts a deeper check that entails the derived check from its isolation", func(t *testing.T) {
			t.Parallel()

			derivedMisses().Run(t, newStore, isolate, conform.With(derivedExact()))
		})

		t.Run("passes an edge whose deeper check fails on the defect of its shallower check", func(t *testing.T) {
			t.Parallel()

			var onSubject atomic.Bool
			c := derivedExact()
			apply := c.Apply
			c.Apply = func(h *conform.Handle[Store]) {
				if _, isSubject := h.Subject().(*memory); isSubject {
					onSubject.Store(true)
				}
				apply(h)
			}
			c.Run(t, newStore, isolate, conform.With(derivedMisses()))
			expect.True(t, onSubject.Load(), "the check applies to the subject once its edges hold")
		})

		t.Run("fails an edge whose deeper check passes on the defect of its shallower check", func(t *testing.T) {
			t.Parallel()

			c := derivedKeeps()
			c.Entails = []conform.ID[Store]{storeGet.Check("misses-unknown-keys")}
			r := recorded(func(tb assert.TB) { c.Run(tb, newStore, isolate, conform.With(derivedMisses())) })
			assert.Equal(t, r.Message(),
				"the edges of Store.Get/keeps-the-value fail:\n"+
					"\tStore.Get/keeps-the-value passes on the planted defect of Store.Get/misses-unknown-keys, "+
					"which it entails, so it is not deeper\n"+
					"A deeper check fails on the planted defect of each check that it entails, and each of those "+
					"checks passes on the planted defect of the deeper check.",
				"the message names the edge that is not deeper")
		})

		t.Run("fails an edge whose shallower check fails on the defect of its deeper check", func(t *testing.T) {
			t.Parallel()

			c := derivedExact()
			c.Entails = []conform.ID[Store]{storeGet.Check("keeps-the-value")}
			r := recorded(func(tb assert.TB) {
				c.Run(tb, newStore, isolate, conform.With(derivedMisses(), derivedKeeps()))
			})
			assert.Equal(t, r.Message(),
				"the edges of Store.Get/keeps-exactly fail:\n"+
					"\tStore.Get/keeps-the-value fails on the planted defect of Store.Get/keeps-exactly, which "+
					"entails it, so the two checks are equivalent\n"+
					"A deeper check fails on the planted defect of each check that it entails, and each of those "+
					"checks passes on the planted defect of the deeper check.",
				"the message names the equivalent edge")
		})

		t.Run("ends the falsification with a fault for a shallower check inside Around on a seat without a test",
			func(t *testing.T) {
				t.Parallel()

				var onSubject atomic.Bool
				c := derivedExact()
				apply := c.Apply
				c.Apply = func(h *conform.Handle[Store]) {
					if _, isSubject := h.Subject().(*memory); isSubject {
						onSubject.Store(true)
					}
					apply(h)
				}
				var onDeeper atomic.Bool
				shallower := bubbled(derivedMisses())
				missesApply := shallower.Apply
				shallower.Apply = func(h *conform.Handle[Store]) {
					if _, onDefect := h.Subject().(dropsPuts); onDefect {
						onDeeper.Store(true)
					}
					missesApply(h)
				}
				r := assert.NewRecorder()
				c.Run(r, newStore, isolate, conform.With(shallower))
				faults := r.Faults()
				assert.Length(t, faults, 1, "the environment ends the falsification with one fault")
				expect.ErrorIs(t, faults[0], conform.ErrConfig, "the fault is the fault of a setting")
				expect.False(t, onDeeper.Load(), "the shallower check does not run on the defect of the deeper check")
				expect.False(t, onSubject.Load(), "the check does not apply to the subject")
			})

		t.Run("ends the falsification with a fault for an edge to a check that the application lacks",
			func(t *testing.T) {
				t.Parallel()

				r := recorded(func(tb assert.TB) { derivedExact().Run(tb, newStore, isolate) })
				faults := r.Faults()
				assert.Length(t, faults, 1, "the edge ends the falsification with one fault")
				expect.ErrorIs(t, faults[0], conform.ErrConfig, "the fault is the fault of a setting")
				expect.Equal(t, faults[0].Error(), "conform: the application has an invalid setting: "+
					"Store.Get/keeps-exactly entails Store.Get/misses-unknown-keys, which the application lacks",
					"the fault names the missing check")
			})
	})
}

// The variable DOKIMI_SEED of the process seeds each property run of an
// application and the random source of each check that runs once. The test
// sets the variable, so neither it nor its subtests run in parallel.
func TestSuiteSeed(t *testing.T) {
	const seedEnv = "DOKIMI_SEED"
	const invalid = `DOKIMI_SEED is "seven", and a seed is a decimal number below 2^64`

	t.Run("seeds each property run of an application", func(t *testing.T) {
		t.Setenv(seedEnv, "7")
		r := assert.NewRecorder()
		keepsEvery().Run(r, newStore)
		assert.Equal(t, detailOf(t, r, "seed"), any("7"), "the property run takes the seed of DOKIMI_SEED")
	})

	t.Run("seeds the random source of a check that runs once", func(t *testing.T) {
		t.Setenv(seedEnv, "7")
		var values []uint64
		for _, c := range []conform.Check[Store]{keeps(), misses()} {
			c.Apply = func(h *conform.Handle[Store]) { values = append(values, h.Rand().Uint64()) }
			c.Run(t, newStore)
		}
		assert.Length(t, values, 2, "each check draws one value")
		expect.Equal(t, values[0], values[1], "two checks draw the same value under one seed")
	})

	t.Run("ends a check with a fault for a seed that is no number", func(t *testing.T) {
		t.Setenv(seedEnv, "seven")
		r := recorded(func(tb assert.TB) { keepsEvery().Run(tb, newStore) })
		faults := r.Faults()
		assert.Length(t, faults, 1, "the check ends with one fault")
		expect.ErrorIs(t, faults[0], conform.ErrConfig, "the fault is the fault of a setting")
		expect.Equal(t, faults[0].Error(), "conform: the application has an invalid setting: "+invalid,
			"the fault contains the variable and its value")
	})

	t.Run("reports a seed that is no number as a problem of an application", func(t *testing.T) {
		t.Setenv(seedEnv, "seven")
		suite := conform.Suite[Store]{Declaration: "Store", Checks: []conform.Check[Store]{keeps()}}
		a := application(func(t conform.T) { conform.Run(t, suite, newStore) })
		assert.Equal(t, a.errors, []string{invalid}, "the application reports the seed")
		assert.Empty(t, a.tests, "the application runs no check")
	})
}
