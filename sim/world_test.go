// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/sim"
)

// loud is a machine that commits to its disk at each event, the stop event
// too.
type loud struct{ disk disk }

func (l loud) Step(_ event, out []effect) []effect {
	return append(out, commit{disk: l.disk, key: "k", value: "v", token: 1})
}

func (loud) Err() error { return nil }

func TestWorld(t *testing.T) {
	t.Parallel()

	t.Run("New", func(t *testing.T) {
		t.Parallel()

		t.Run("returns a world without processes and steps", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				expect.Empty(c, w.Pending(), "a new world has no process with an event")
				expect.Empty(c, w.Log(), "a new world has stepped no machine")
				expect.Nil(c, w.Process("store"), "a new world has no process")
			})
		})
	})

	t.Run("Spawn", func(t *testing.T) {
		t.Parallel()

		t.Run("queues the start of the process with a new reply token", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Spawn("other", newStore)
				expect.Equal(c, w.Pending(), []string{"store", "other"}, "each process waits for its start")
				assert.True(c, w.Step("other"), "the process steps on its start")
				assert.Length(c, w.Log(), 1, "the world records the step")
				expect.Equal(c, w.Log()[0].Event, event(started{reply: 2}), "the second start has the second token")
			})
		})
	})

	t.Run("Context", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the context of the case", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				expect.Equal(c, w.Context(), c.Context(), "the world passes on the context of its case",
					assert.ByIdentity())
			})
		})
	})

	t.Run("Deliver", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the case for a name that no process has", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { sim.New(c, anatomy).Deliver("missing", stopped{}) })
			expect.Equal(t, got.Contract, `sim: no process of the world is named "missing"`,
				"the failure names the process")
		})
	})

	t.Run("Step", func(t *testing.T) {
		t.Parallel()

		t.Run("returns false for a process without an event", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				expect.False(c, w.Step("store"), "the process has no event after its start")
				expect.False(c, w.Step("missing"), "no process has the name")
			})
		})

		t.Run("records the process, the event, the effects and the epoch of a step", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				entry := w.Log()[0]
				expect.Equal(c, entry.Process, "store", "the entry names the process")
				expect.Equal(c, entry.Epoch, uint64(1), "the first epoch of a process is 1")
				expect.Equal(c, entry.Effects, []effect{reply{to: 1}}, "the store replies to its start")
				expect.Nil(c, entry.Err, "the store runs")
			})
		})

		t.Run("queues the result of an effect for a later step of its process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.Deliver("store", put{key: "k", value: "v", reply: 9})
				w.Step("store")
				expect.Equal(c, w.Pending(), []string{"store"}, "the commit has a result to step on")
				w.Step("store")
				expect.Equal(c, w.Log()[2].Event, event(committed{token: 1}), "the store steps on the result")
			})
		})

		t.Run("records the error of a machine that stops itself, and stops its process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.Deliver("store", put{key: "k", reply: 9})
				w.Step("store")
				w.Deliver("store", put{key: "k", value: "v", reply: 10})
				expect.ErrorIs(c, w.Log()[1].Err, errEmpty, "the entry records the error of the machine")
				expect.Equal(c, w.Log()[2].Event, event(stopped{}), "the world stops a machine that stopped itself")
				expect.Empty(c, w.Pending(), "a stopped process takes no event")
				expect.False(c, w.Step("store"), "a stopped process does not step")
			})
		})

		t.Run("queues the next event of each wait after every step until the wait ends", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				f := &feed{}
				w.Deliver("a", subscribe{feed: f, token: 5})
				w.Step("a")
				f.items = []event{fed{item: "x", token: 5}, fed{item: "y", token: 5}}
				expect.Empty(c, w.Pending(), "the world takes the events of a wait after a step")
				w.Deliver("b", ticked{token: 1})
				w.Step("b")
				expect.Equal(c, w.Pending(), []string{"a"}, "the step makes the world take one event of the wait")
				w.Step("a")
				f.ended = true
				w.Step("a")
				f.items = []event{fed{item: "z", token: 5}}
				w.Deliver("b", ticked{token: 2})
				w.Step("b")
				expect.Equal(c, events(w, "a")[2:], []event{fed{item: "x", token: 5}, fed{item: "y", token: 5}},
					"the process takes each event of the wait until the wait ends")
				expect.Empty(c, w.Pending(), "an ended wait passes no event")
			})
		})

		t.Run("takes the events of the waits in the order of their starts", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				first, second := &feed{}, &feed{}
				w.Deliver("a", subscribe{feed: first, token: 5})
				w.Deliver("a", subscribe{feed: second, token: 6})
				w.Step("a")
				w.Step("a")
				second.items = []event{fed{item: "y", token: 6}}
				first.items = []event{fed{item: "x", token: 5}}
				w.Deliver("b", ticked{token: 1})
				w.Step("b")
				drain(w)
				expect.Equal(c, events(w, "a")[3:], []event{fed{item: "x", token: 5}, fed{item: "y", token: 6}},
					"the event of the first wait arrives first")
			})
		})

		t.Run("ends the waits of a crashed process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				f, other := &feed{}, &feed{}
				w.Deliver("a", subscribe{feed: f, token: 5})
				w.Deliver("b", subscribe{feed: other, token: 6})
				w.Step("a")
				w.Step("b")
				w.Crash("a")
				w.Restart("a")
				f.items = []event{fed{item: "x", token: 5}}
				other.items = []event{fed{item: "y", token: 6}}
				drain(w)
				w.Step("a")
				expect.Equal(c, events(w, "a")[2:], []event{started{reply: 3}},
					"the restarted process takes no event of the wait")
				expect.False(c, slices.ContainsFunc(w.Log(), func(e sim.Entry[event, effect]) bool {
					return e.Dropped
				}), "the world queues no event of the ended wait")
				expect.Equal(c, events(w, "b")[2:], []event{fed{item: "y", token: 6}},
					"the wait of the other process goes on")
			})
		})
	})

	t.Run("Call", func(t *testing.T) {
		t.Parallel()

		t.Run("records the call by its client and completes it with the reply", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.Call(3, "store", "Put", []any{"k", "v"}, []any{"k"}, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				w.Step("store")
				w.Step("store")
				events := w.History().Events()
				assert.Length(c, events, 2, "the history has the invocation and the completion")
				expect.Equal(c, events[0].Client, 3, "the client of the call is 3")
				expect.Equal(c, events[0].Operation, "Put", "the operation is Put")
				expect.Equal(c, events[0].Keys, []any{"k"}, "the call touches k")
				expect.Equal(c, events[1].Kind, history.OK, "the reply completes the call")
				expect.Equal(c, events[1].Output, any("stored"), "the call returns the value of the reply")
			})
		})

		t.Run("fails the call of a reply with an error", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.Call(0, "store", "Put", nil, nil, func(r uint64) event { return put{key: "k", reply: r} })
				w.Step("store")
				events := w.History().Events()
				assert.Length(c, events, 2, "the history has the invocation and the completion")
				expect.Equal(c, events[1].Kind, history.Fail, "the reply with an error fails the call")
				expect.ErrorIs(c, events[1].Error, errEmpty, "the call fails with the error of the reply")
			})
		})

		unknowns := []struct {
			name        string
			giveUnknown func(err error) bool
			wantKind    history.Kind
		}{
			{
				name:        "ends the call of a reply with an error of an unknown outcome as unknown",
				giveUnknown: func(err error) bool { return errors.Is(err, errEmpty) },
				wantKind:    history.Unknown,
			},
			{
				name:        "fails the call of a reply with an error of a known outcome",
				giveUnknown: func(error) bool { return false },
				wantKind:    history.Fail,
			},
		}
		for _, tt := range unknowns {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				inCase(t, func(c *prop.Case) {
					a := anatomy
					a.Unknown = tt.giveUnknown
					w := sim.New(c, a)
					w.Spawn("store", newStore)
					w.Step("store")
					w.Call(0, "store", "Put", nil, nil, func(r uint64) event { return put{key: "k", reply: r} })
					w.Step("store")
					got := w.History().Events()
					assert.Length(c, got, 2, "the history has the invocation and the completion")
					expect.Equal(c, got[1].Kind, tt.wantKind, "the error of the reply decides the outcome of the call")
					expect.ErrorIs(c, got[1].Error, errEmpty, "the call ends with the error of the reply")
				})
			})
		}

		t.Run("completes the call of a reply without an error as OK", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				a := anatomy
				a.Unknown = func(error) bool { return true }
				w := sim.New(c, a)
				w.Spawn("store", newStore)
				w.Step("store")
				w.Call(0, "store", "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				drain(w)
				got := w.History().Events()
				assert.Length(c, got, 2, "the history has the invocation and the completion")
				expect.Equal(c, got[1].Kind, history.OK, "Unknown decides the outcome of an error alone")
			})
		})

		t.Run("ignores a reply of another process to the call", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Spawn("other", newStore)
				w.Call(0, "store", "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				w.Deliver("other", put{key: "k", reply: 3})
				w.Step("other")
				w.Step("other")
				assert.Length(c, w.History().Events(), 1, "the reply of other leaves the call of store open")
			})
		})

		t.Run("ends a call to a crashed process as unknown", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.Crash("store")
				w.Call(0, "store", "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				w.Restart("store")
				drain(w)
				got := w.History().Events()
				assert.Length(c, got, 2, "the history has the invocation and the completion")
				expect.Equal(c, got[1].Kind, history.Unknown, "the crashed process never takes the call")
				expect.ErrorIs(c, got[1].Error, sim.ErrCrashed, "the call ends with the crash of its process")
				expect.Equal(c, events(w, "store"), []event{started{reply: 1}, started{reply: 2}},
					"the store takes its start and the start of its restart alone")
			})
		})

		t.Run("ends a call to a stopped process as unknown", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.Stop()
				w.Call(0, "store", "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				got := w.History().Events()
				assert.Length(c, got, 2, "the history has the invocation and the completion")
				expect.Equal(c, got[1].Kind, history.Unknown, "the stopped process never takes the call")
				expect.ErrorIs(c, got[1].Error, sim.ErrRunEnded, "the call ends with the end of the run")
			})
		})
	})

	t.Run("Cancel", func(t *testing.T) {
		t.Parallel()

		t.Run("ends the open call of the client and cancels it at its process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				for _, client := range []int{4, 5} {
					w.Call(client, "store", "Put", nil, nil, func(r uint64) event {
						return put{key: "k", value: "v", reply: r}
					})
				}
				assert.True(c, w.Cancel(5), "the client has an open call")
				drain(w)
				got := w.History().Events()
				assert.Length(c, got, 4, "the history has two invocations and two completions")
				expect.Equal(c, []history.Kind{got[2].Kind, got[3].Kind}, []history.Kind{history.Unknown, history.OK},
					"the cancel ends the call of the client as unknown, and the store completes the other call")
				expect.Equal(c, got[2].Call, 1, "the cancel ends the call of the client")
				expect.ErrorIs(c, got[2].Error, sim.ErrCancelled, "the cancelled call ends with the cancel")
				expect.Contains(c, events(w, "store"), event(cancelled{reply: 3}),
					"the store takes the cancel of the call")
				w.Stop()
				expect.Length(c, w.History().Events(), 4, "the stop finds the cancelled call closed")
			})
		})

		t.Run("returns false for a client without an open call", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Call(5, "b", "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				expect.False(c, w.Cancel(4), "the call of another client is not the call of client 4")
				expect.False(c, w.Cancel(0), "a message is not the call of a client")
			})
		})

		t.Run("queues no cancel for an anatomy without a cancel event", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				bare := anatomy
				bare.Cancelled = nil
				w := sim.New(c, bare)
				w.Spawn("store", newStore)
				w.Call(4, "store", "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				assert.True(c, w.Cancel(4), "the client has an open call")
				drain(w)
				expect.NotContains(c, events(w, "store"), event(cancelled{reply: 2}), "the store takes no cancel")
			})
		})
	})

	t.Run("After", func(t *testing.T) {
		t.Parallel()

		t.Run("queues the timers that one advance makes due in the order of their calls", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				p := w.Process("store")
				w.After(p, 2*time.Second, ticked{token: 1})
				w.After(p, time.Second, ticked{token: 2})
				w.Advance(10 * time.Second)
				w.Step("store")
				w.Step("store")
				log := w.Log()
				expect.Equal(c, []event{log[1].Event, log[2].Event}, []event{ticked{token: 1}, ticked{token: 2}},
					"the timers take their places in the order of their calls")
			})
		})

		t.Run("queues the event of a timer once", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.After(w.Process("store"), time.Second, ticked{token: 1})
				w.Advance(time.Hour)
				w.Step("store")
				w.Advance(time.Hour)
				expect.Empty(c, w.Pending(), "a timer that is due once queues its event once")
			})
		})
	})

	t.Run("Pending", func(t *testing.T) {
		t.Parallel()

		t.Run("leaves out a process whose events are all of an earlier epoch", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.After(w.Process("store"), time.Second, ticked{token: 1})
				w.Crash("store")
				w.Restart("store")
				w.Step("store")
				w.Advance(time.Hour)
				expect.Empty(c, w.Pending(), "the timer of the old epoch gives the store no event to step on")
				expect.False(c, w.Step("store"), "the store does not step on the timer of the old epoch")
				expect.True(c, w.Log()[2].Dropped, "the world drops the timer of the old epoch")
			})
		})
	})

	t.Run("Next", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the time until the earliest timer", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				p := w.Process("store")
				w.After(p, 2*time.Second, ticked{token: 1})
				w.After(p, time.Second, ticked{token: 2})
				w.After(p, 3*time.Second, ticked{token: 3})
				gap, pending := w.Next()
				assert.True(c, pending, "three timers are pending")
				w.Advance(gap)
				w.Step("store")
				expect.Equal(c, w.Log()[1].Event, event(ticked{token: 2}), "the earliest timer is due first")
				expect.Empty(c, w.Pending(), "the later timers are not due yet")
			})
		})

		t.Run("returns false without a timer of a process that is not paused", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				_, pending := w.Next()
				expect.False(c, pending, "no timer is pending")
				w.After(w.Process("store"), time.Second, ticked{token: 1})
				w.Pause("store")
				_, pending = w.Next()
				expect.False(c, pending, "the timer of a paused process is not due")
			})
		})
	})

	t.Run("Crash", func(t *testing.T) {
		t.Parallel()

		t.Run("ends each open call of the process as unknown and stops its steps", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.Call(0, "store", "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				w.Crash("store")
				events := w.History().Events()
				assert.Length(c, events, 2, "the crash completes the open call")
				expect.Equal(c, events[1].Kind, history.Unknown, "the call of a crashed process has an unknown outcome")
				expect.ErrorIs(c, events[1].Error, sim.ErrCrashed, "the call ends with the crash")
				expect.Empty(c, w.Pending(), "a crashed process takes no event")
				expect.False(c, w.Step("store"), "a crashed process does not step")
			})
		})

		t.Run("crashes each medium among the dependencies of the process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				m := mediumWith("data")
				w.Spawn("store", newStore, disk{}, m)
				assert.NoError(c, write(c, m, "lost"), "the medium takes the write")
				w.Crash("store")
				expect.Equal(c, contentOf(c, m), "data", "the crash drops the write that no sync covered")
			})
		})

		t.Run("fails the case for a process that does not run", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := single(c)
				w.Crash("store")
				w.Crash("store")
			})
			expect.Equal(t, got.Contract, `sim: the process "store" does not run`, "the failure names the process")
		})

		t.Run("ends the pause and the strobe of the process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.Pause("store")
				w.Strobe("store", time.Second, time.Second, 3)
				w.Crash("store")
				expect.Equal(c, w.Faults(), []string{"crash:store"}, "the crash alone is in force")
				w.Restart("store")
				expect.Equal(c, w.Pending(), []string{"store"}, "the restarted process takes its start")
			})
		})

		t.Run("ends the open calls of the process in the order of their calls", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				const calls = 32
				for client := range calls {
					w.Call(client, "store", "Put", nil, nil, func(r uint64) event {
						return put{key: "k", value: "v", reply: r}
					})
				}
				w.Crash("store")
				ended := make([]int, 0, calls)
				for _, e := range w.History().Events()[calls:] {
					ended = append(ended, e.Client)
				}
				assert.Length(c, ended, calls, "the crash ends each open call")
				expect.Pairwise(c, ended, func(earlier, later int) bool { return earlier < later },
					"the crash ends the calls in the order of their calls")
			})
		})

		t.Run("closes the calls of the process to the replies of its next epoch", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.Call(0, "store", "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				w.Crash("store")
				w.Restart("store")
				w.Step("store")
				w.Deliver("store", put{key: "k", value: "v", reply: 2})
				w.Step("store")
				w.Step("store")
				expect.Equal(c, w.Log()[3].Effects, []effect{reply{to: 2, value: "stored"}},
					"the restarted store replies to the token of the crashed call")
				assert.Length(c, w.History().Events(), 2, "the reply leaves the call that the crash ended alone")
			})
		})
	})

	t.Run("Restart", func(t *testing.T) {
		t.Parallel()

		t.Run("starts a new machine in a new epoch after the events of the old epoch", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Crash("store")
				w.Deliver("store", ticked{token: 1})
				w.Restart("store")
				assert.True(c, w.Step("store"), "the restarted process steps")
				log := w.Log()
				assert.Length(c, log, 2, "the world drops the event of the old epoch and steps on the start")
				expect.True(c, log[0].Dropped, "the event of the old epoch is dropped")
				expect.Equal(c, log[0].Epoch, uint64(1), "the dropped event has the old epoch")
				expect.Equal(c, log[1].Event, event(started{reply: 2}), "the new machine takes a new start")
				expect.Equal(c, log[1].Epoch, uint64(2), "the restart raises the epoch")
			})
		})

		t.Run("fails the case for a process that has not crashed", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Restart("store")
			})
			expect.Equal(t, got.Contract, `sim: the process "store" has not crashed`, "the failure names the process")
		})
	})

	t.Run("Pause", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the process from its events and its clock from the time of the run", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Pause("store")
				w.Advance(time.Second)
				expect.Empty(c, w.Pending(), "a paused process takes no event")
				expect.False(c, w.Step("store"), "a paused process does not step")
				expect.Equal(c, w.Process("store").Clock().Now(), sim.Start,
					"the clock of a paused process does not advance")
			})
		})

		t.Run("fails the case for a paused process", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := single(c)
				w.Pause("store")
				w.Pause("store")
			})
			expect.Equal(t, got.Contract, `sim: the process "store" does not run unpaused`,
				"the failure names the process")
		})

		t.Run("fails the case for a crashed process", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := single(c)
				w.Crash("store")
				w.Pause("store")
			})
			expect.Equal(t, got.Contract, `sim: the process "store" does not run unpaused`,
				"the failure names the process")
		})
	})

	t.Run("Resume", func(t *testing.T) {
		t.Parallel()

		t.Run("gives the process its events and its clock the time of the run again", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Pause("store")
				w.Resume("store")
				w.Advance(time.Second)
				expect.Equal(c, w.Pending(), []string{"store"}, "a resumed process takes its events")
				expect.True(c, w.Process("store").Clock().Now().After(sim.Start),
					"the clock of a resumed process advances")
			})
		})

		t.Run("fails the case for a process that is not paused", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).Resume("store") })
			expect.Equal(t, got.Contract, `sim: the process "store" is not paused`, "the failure names the process")
		})
	})

	t.Run("Stop", func(t *testing.T) {
		t.Parallel()

		t.Run("passes the stop event to each process and ends each open call as unknown", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.Call(0, "store", "Put", nil, nil, func(r uint64) event {
					return put{key: "k", value: "v", reply: r}
				})
				w.Step("store")
				w.Call(1, "store", "Put", nil, nil, func(r uint64) event {
					return put{key: "j", value: "w", reply: r}
				})
				w.Stop()
				log := w.Log()
				expect.Equal(c, log[len(log)-1].Event, event(stopped{}), "the last step of the store is its stop")
				expect.Equal(c, log[len(log)-1].Effects, []effect{reply{to: 2, err: errStopped}},
					"the store replies to its open put on the stop event")
				events := w.History().Events()
				assert.Length(c, events, 4, "the history has two invocations and two completions")
				expect.Equal(c, events[2].Kind, history.Fail, "the reply of the stop event fails the first put")
				expect.Equal(c, events[3].Kind, history.Unknown, "the stop ends the put that the store never took")
				expect.ErrorIs(c, events[3].Error, sim.ErrRunEnded, "the unknown call ends with the end of the run")
				expect.Empty(c, w.Pending(), "a stopped world has no process with an event")
			})
		})

		t.Run("performs the replies of the stop event alone", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				d := disk{}
				w.Spawn("loud", func(*sim.Clock) sim.Machine[event, effect] { return loud{disk: d} })
				w.Stop()
				expect.Equal(c, w.Log()[0].Effects, []effect{commit{disk: d, key: "k", value: "v", token: 1}},
					"the log records the commit of the stop event")
				expect.Empty(c, d, "the world does not perform the commit of the stop event")
			})
		})

		t.Run("leaves a crashed process alone", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore)
				w.Step("store")
				w.Crash("store")
				w.Stop()
				assert.Length(c, w.Log(), 1, "a crashed process takes no stop event")
			})
		})
	})
}
