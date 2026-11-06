// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/host"
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

		t.Run("fails the case for a component, which takes no event", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, still, func(c *prop.Case) {
				w, _, _ := cells(c)
				w.Deliver("a", stopped{})
			})

			assert.Equal(t, got.Contract, `sim: the process "a" is a component, which takes no event`,
				"the failure names the component")
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

		t.Run("fails the case for a component, which takes no event", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, still, func(c *prop.Case) {
				w, _, _ := cells(c)
				w.Call(0, "a", "Put", nil, nil, func(r uint64) event { return put{key: "k", value: "v", reply: r} })
			})

			assert.Equal(t, got.Contract, `sim: the process "a" is a component, which takes no event`,
				"the failure names the component")
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

		t.Run("ends the open call of a component as unknown and ends its context", func(t *testing.T) {
			t.Parallel()
			var got []completion
			var ended bool
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				parked := a.Value()
				parked.parks = true
				returned := make(chan struct{})
				w.Invoke(4, "a", "Get", nil, nil, func(ctx context.Context) (any, error) {
					defer close(returned)
					return parked.Get(ctx)
				})
				assert.True(c, w.Cancel(4), "the client has an open call of a component")
				synctest.Wait()
				got = completions(w.History().Events())
				select {
				case <-returned:
					ended = true
				default:
				}
				w.Stop()
			})

			assert.Equal(t, got, []completion{{kind: history.Unknown, err: sim.ErrCancelled}},
				"the cancel ends the call of the client")
			assert.True(t, ended, "the cancel ends the context of the call before the stop")
		})

		t.Run("ends the open call of its client alone among the calls of the components", func(t *testing.T) {
			t.Parallel()
			var got []history.Event
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				parked := a.Value()
				parked.parks = true
				for _, client := range []int{4, 5} {
					w.Invoke(client, "a", "Get", nil, nil, func(ctx context.Context) (any, error) {
						return parked.Get(ctx)
					})
				}
				w.Cancel(5)
				synctest.Wait()
				got = w.History().Events()
				w.Stop()
			})

			assert.Length(t, got, 3, "the history has two invocations and one completion")
			expect.Equal(t, got[2].Client, 5, "the cancel ends the call of the client 5")
		})
	})

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		t.Run("advances the monotonic clock of the host of each component", func(t *testing.T) {
			t.Parallel()
			var got []time.Duration
			inBubble(t, still, func(c *prop.Case, first bool) {
				w, a, b := cells(c)
				w.Advance(time.Second)
				if first {
					got = []time.Duration{a.Value().env.Clock().Monotonic(), b.Value().env.Clock().Monotonic()}
				}
				w.Stop()
			})

			assert.Equal(t, got, []time.Duration{time.Second, time.Second}, "each host advances by the second")
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

		t.Run("fails the case for a component, which takes no event", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, still, func(c *prop.Case) {
				w, _, _ := cells(c)
				w.After(w.Process("a"), time.Second, ticked{token: 1})
			})

			assert.Equal(t, got.Contract, `sim: the process "a" is a component, which takes no event`,
				"the failure names the component")
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

		tests := []struct {
			name     string
			giveKill int
		}{
			{name: "crashes a component by a process crash, which keeps its disk", giveKill: 1},
			{name: "crashes a component by a power loss, which keeps the synced writes", giveKill: 2},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var got string
				draws := strings.TrimSuffix(still, "]") +
					`,{"label": "kill.a", "value": {"type": "int", "value": ` + strconv.Itoa(tt.giveKill) + `}}]`
				inBubble(t, draws, func(c *prop.Case, first bool) {
					w, a, _ := cells(c)
					assert.NoError(c, a.Value().Set(c.Context(), "v"), "the cell writes")
					w.Crash("a")
					w.Restart("a")
					v, err := a.Value().Get(c.Context())
					assert.NoError(c, err, "the cell reads")
					if first {
						got = v
					}
					w.Stop()
				})

				assert.Equal(t, got, "v", "the next incarnation reads the value that the cell synced")
			})
		}

		t.Run("ends the open calls of the crashed component alone", func(t *testing.T) {
			t.Parallel()
			var got []history.Event
			draws := strings.TrimSuffix(still, "]") + `,{"label": "kill.a", "value": {"type": "int", "value": 1}}]`
			inBubble(t, draws, func(c *prop.Case, first bool) {
				w, a, b := cells(c)
				for client, comp := range []*sim.Component[*cell]{a, b} {
					v := comp.Value()
					v.parks = true
					w.Invoke(client, v.env.Name(), "Get", nil, nil, func(ctx context.Context) (any, error) {
						return v.Get(ctx)
					})
				}
				w.Crash("a")
				if first {
					got = w.History().Events()
				}
				w.Stop()
			})

			assert.Length(t, got, 3, "the crash completes one of the two calls")
			expect.Equal(t, got[2].Client, 0, "the crash ends the call on the component a")
		})

		t.Run("fails the case for a goroutine of a crashed component that ignores its lifetime", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, lone, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				sim.SpawnComponent(w, "a", leakyCell(c))
				w.Crash("a")
			})

			assert.Contains(t, got.Contract, "sim: a goroutine of the incarnation 1 of a is alive after its crash",
				"the failure states the goroutine that ignored its lifetime")
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

		t.Run("opens a new incarnation of a crashed component in a new epoch", func(t *testing.T) {
			t.Parallel()
			var number int
			var epoch uint64
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				w.Crash("a")
				w.Restart("a")
				number, epoch = a.Value().env.Incarnation(), w.Process("a").Epoch()
				w.Stop()
			})

			assert.Equal(t, number, 2, "the restart opens the second incarnation")
			assert.Equal(t, epoch, uint64(2), "the restart raises the epoch of the process")
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

		t.Run("blocks each call of the environment of a component until Resume", func(t *testing.T) {
			t.Parallel()
			var during, after []completion
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				v := a.Value()
				w.Pause("a")
				w.Invoke(0, "a", "Set", nil, nil, func(ctx context.Context) (any, error) {
					return nil, v.Set(ctx, "v")
				})
				synctest.Wait()
				during = completions(w.History().Events())
				w.Resume("a")
				synctest.Wait()
				after = completions(w.History().Events())
				w.Stop()
			})

			assert.Empty(t, during, "the call waits while the component is paused")
			assert.Equal(t, after, []completion{{kind: history.OK}}, "the call completes once the pause ends")
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

		t.Run("ends the open calls of each component in the order of the components", func(t *testing.T) {
			t.Parallel()
			var clients []int
			inBubble(t, still, func(c *prop.Case, first bool) {
				w, a, b := cells(c)
				for client, comp := range []*sim.Component[*cell]{b, a} {
					v := comp.Value()
					v.parks = true
					w.Invoke(client, v.env.Name(), "Get", nil, nil, func(ctx context.Context) (any, error) {
						return v.Get(ctx)
					})
				}
				w.Stop()
				for _, e := range w.History().Events()[2:] {
					if first {
						clients = append(clients, e.Client)
					}
				}
			})

			assert.Equal(t, clients, []int{1, 0}, "the stop ends the call on a before the call on b")
		})

		t.Run("fails the case for a goroutine of a component that ignores its lifetime", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, lone, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				sim.SpawnComponent(w, "a", leakyCell(c))
				w.Stop()
			})

			assert.Contains(t, got.Contract, "sim: a goroutine of the incarnation 1 of a is alive after its crash",
				"the stop ends the incarnation and finds the goroutine")
		})
	})
}

// leakyCell returns the open of a cell that starts a goroutine of its
// incarnation, which waits for the end of the case c alone, so it ignores the
// lifetime of the incarnation.
func leakyCell(c *prop.Case) func(env *host.Env) *cell {
	return func(env *host.Env) *cell {
		env.Go(func() { <-c.Context().Done() })
		return openCell(env)
	}
}
