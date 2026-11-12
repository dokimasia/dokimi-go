// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"context"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/history"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/host"
	"go.dokimi.dev/medium"
	"go.dokimi.dev/sim"
)

// trio returns a world with the started processes a, b and c.
func trio(c *prop.Case) *sim.World[event, effect] {
	w := pair(c)
	w.Spawn("c", newStore)
	w.Step("c")
	return w
}

// stored returns the steps of a world whose store puts key on d through an
// inbound call with the reply token 9.
func stored(d disk, key string) func(w *sim.World[event, effect]) {
	return func(w *sim.World[event, effect]) {
		w.Deliver("store", put{disk: d, key: key, value: "v", reply: 9})
		drain(w)
	}
}

// scoped returns the anatomy of the stores whose disk and feed have the scope
// scope.
func scoped(scope sim.Scope) sim.Anatomy[event, effect] {
	a := anatomy
	a.Scope = func(string) sim.Scope { return scope }
	return a
}

// losing returns the anatomy of the agent whose launches and halts have the
// dependency host, and whose calls have the dependency peer. The failure of a
// launch or a halt is the event called.
func losing() sim.Anatomy[event, effect] {
	a := starting()
	a.Dependency = func(f effect) (string, bool) {
		switch f.(type) {
		case launch, halt:
			return "host", true
		case call:
			return "peer", true
		default:
			return anatomy.Dependency(f)
		}
	}
	a.Fail = func(f effect, err error) (event, bool) {
		switch f := f.(type) {
		case launch:
			return called{err: err, token: f.token}, true
		case halt:
			return called{err: err, token: f.token}, true
		default:
			return anatomy.Fail(f, err)
		}
	}
	return a
}

func TestFault(t *testing.T) {
	t.Parallel()

	t.Run("Partition", func(t *testing.T) {
		t.Parallel()

		t.Run("drops a message to a process on another side", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Partition([]string{"a"}, []string{"b"})
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				expect.Empty(c, w.Pending(), "the message to the other side does not arrive")
				expect.Equal(c, w.Faults(), []string{"partition"}, "the partition is in force")
			})
		})

		t.Run("drops a reply to a process on another side", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				w.Step("b")
				w.Partition([]string{"a"}, []string{"b"})
				drain(w)
				expect.Length(c, events(w, "a"), 2, "the reply from the other side does not arrive")
			})
		})

		t.Run("passes a message between two processes that one side contains", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := trio(c)
				w.Partition([]string{"a", "b"}, []string{"b", "c"})
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Deliver("a", forward{to: "c", key: "k", token: 8})
				drain(w)
				expect.Equal(c, events(w, "a")[3:], []event{called{value: "stored", token: 7}},
					"the message within a side arrives, and the message across the sides does not")
			})
		})

		t.Run("passes a message between two processes of one host under every partition", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := node(c)
				w.Partition([]string{"other"})
				w.Deliver("store", forward{to: "cache", key: "k", token: 7})
				w.Deliver("store", forward{to: "other", key: "k", token: 8})
				drain(w)
				expect.Equal(c, events(w, "store")[3:], []event{called{value: "stored", token: 7}},
					"the message within the host arrives, and the message to the isolated host does not")
			})
		})

		t.Run("fails the case for a name that no host has", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { pair(c).Partition([]string{"a"}, []string{"missing"}) })
			expect.Equal(t, got.Contract, `sim: no host of the world is named "missing"`,
				"the failure names the host")
		})

		t.Run("fails the case while a partition is in force", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := pair(c)
				w.Partition([]string{"a"}, []string{"b"})
				w.Partition([]string{"a", "b"})
			})
			expect.Equal(t, got.Contract, "sim: a partition is in force", "the failure names the partition")
		})

		t.Run("cuts the network of the components across the cut until the repair", func(t *testing.T) {
			t.Parallel()
			var cut, repaired error
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, b := cells(c)
				_, err := a.Value().env.Listen(7000)
				assert.NoError(c, err, "a listens")
				w.Partition([]string{"a"}, []string{"b"})
				cut = dial(c, b.Value().env, "a:7000")
				w.Repair()
				repaired = dial(c, b.Value().env, "a:7000")
				w.Stop()
			})

			assert.ErrorIs(t, cut, syscall.ECONNREFUSED, "the network refuses a dial across the cut")
			assert.NoError(t, repaired, "the network connects the components after the repair")
		})
	})

	t.Run("Sides", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the sides of the partition in force", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				expect.Nil(c, w.Sides(), "a world without a partition has no sides")
				w.Partition([]string{"a"}, []string{"b"})
				expect.Equal(c, w.Sides(), [][]string{{"a"}, {"b"}}, "the partition has its sides")
			})
		})
	})

	t.Run("Repair", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the case without a partition in force", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { pair(c).Repair() })
			expect.Equal(t, got.Contract, "sim: no partition is in force", "the failure names the missing partition")
		})

		t.Run("passes the messages across the cut again", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Partition([]string{"a"}, []string{"b"})
				w.Repair()
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				drain(w)
				expect.Equal(c, events(w, "a")[2:], []event{called{value: "stored", token: 7}},
					"the message arrives after the repair")
				expect.Empty(c, w.Faults(), "no fault is in force after the repair")
			})
		})
	})

	t.Run("Drop", func(t *testing.T) {
		t.Parallel()

		t.Run("drops the oldest message in flight to the process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "j", token: 7})
				w.Deliver("a", forward{to: "b", key: "k", token: 8})
				w.Step("a")
				w.Step("a")
				assert.True(c, w.Drop("b"), "a message is in flight to b")
				drain(w)
				expect.Equal(c, events(w, "b")[1], event(put{key: "k", value: "v", reply: 4}),
					"the second message arrives")
				expect.Equal(c, events(w, "a")[3:], []event{called{value: "stored", token: 8}},
					"the sender of the dropped message takes no result")
			})
		})

		t.Run("returns false without a message in flight", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("b", ticked{token: 1})
				expect.False(c, w.Drop("b"), "an event without a sender is not a message")
			})
		})

		t.Run("closes the call of the dropped message", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "k", token: 7, timeout: time.Second})
				w.Step("a")
				w.Drop("b")
				w.Advance(time.Hour)
				drain(w)
				expect.Equal(c, events(w, "a")[2:], []event{called{err: context.DeadlineExceeded, token: 7}},
					"the sender takes the timeout of the dropped message")
				expect.Length(c, events(w, "b"), 1, "the receiver takes no cancel of a call that it never took")
			})
		})
	})

	t.Run("Delay", func(t *testing.T) {
		t.Parallel()

		t.Run("moves the oldest message in flight behind the next event of the process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				w.Deliver("b", ticked{token: 1})
				assert.True(c, w.Delay("b"), "a message is in flight to b with an event behind it")
				w.Step("b")
				expect.Equal(c, events(w, "b")[1], event(ticked{token: 1}),
					"the event behind the message arrives first")
			})
		})

		t.Run("returns false for a message without an event behind it", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				expect.False(c, w.Delay("b"), "the message has no later event to arrive after")
			})
		})

		t.Run("returns false without a message in flight", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("b", ticked{token: 1})
				expect.False(c, w.Delay("b"), "an event without a sender is not a message")
			})
		})
	})

	t.Run("Duplicate", func(t *testing.T) {
		t.Parallel()

		t.Run("queues the message once more as a second call", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				assert.True(c, w.Duplicate("b"), "a message is in flight to b")
				drain(w)
				expect.Equal(c, events(w, "b")[1:3],
					[]event{put{key: "k", value: "v", reply: 3}, put{key: "k", value: "v", reply: 4}},
					"the receiver takes the call twice, each with its own reply token")
				expect.Equal(c, events(w, "a")[2:], []event{called{value: "stored", token: 7}},
					"the sender takes the first reply alone")
			})
		})

		t.Run("returns false without a message in flight", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) { expect.False(c, pair(c).Duplicate("b"), "no message is in flight to b") })
		})

		t.Run("returns false for a message whose call its timeout ended", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Pause("b")
				w.Deliver("a", forward{to: "b", key: "k", token: 7, timeout: time.Second})
				w.Step("a")
				w.Advance(time.Hour)
				expect.False(c, w.Duplicate("b"), "the message of an ended call is not in flight")
				expect.False(c, w.Drop("b"), "the message of an ended call is not in flight")
			})
		})
	})

	t.Run("LoseBefore", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the next effect of the dependency before the dependency applies it", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				d := disk{}
				w.LoseBefore("disk")
				expect.Equal(c, w.Faults(), []string{"lose-before:disk"}, "the armed loss is in force")
				stored(d, "k")(w)
				expect.Empty(c, d, "the disk does not apply the lost commit")
				expect.Equal(c, events(w, "store")[2], event(committed{err: errDisk, token: 1}),
					"the store takes the failure of its commit")
				stored(d, "j")(w)
				expect.Equal(c, d, disk{"j": "v"}, "the loss strikes one effect")
			})
		})

		t.Run("fails an effect of a dependency without errors with ErrLost", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.LoseBefore("feed")
				w.Deliver("store", subscribe{feed: &feed{items: []event{fed{item: "x", token: 5}}}, token: 5})
				drain(w)
				expect.Equal(c, events(w, "store")[2:], []event{fed{err: sim.ErrLost, token: 5}},
					"the store takes the loss of its wait, and no item of it")
			})
		})
	})

	t.Run("LoseAfter", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the case for a dependency without a name", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).LoseAfter("") })
			expect.Equal(t, got.Contract, "sim: a dependency has no name", "the failure names the missing name")
		})

		t.Run("fails the next effect of the dependency after the dependency applies it", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				d := disk{}
				w.LoseAfter("disk")
				stored(d, "k")(w)
				expect.Equal(c, d, disk{"k": "v"}, "the disk applies the lost commit")
				expect.Equal(c, events(w, "store")[2], event(committed{err: errDisk, token: 1}),
					"the store takes the failure of its commit")
				expect.Empty(c, w.Faults(), "the loss is not in force after it strikes")
			})
		})

		t.Run("cancels the outcome of the effect that it loses", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, ending())
				w.Spawn("store", newStore)
				f := &feed{items: []event{fed{item: "x", token: 5}}}
				w.LoseAfter("feed")
				w.Deliver("store", subscribe{feed: f, token: 5})
				drain(w)
				expect.Equal(c, f.cancels, 1, "the loss cancels the wait that the feed registered")
				expect.Equal(c, events(w, "store")[2:], []event{fed{err: sim.ErrLost, token: 5}},
					"the store takes the loss of its wait, and no item of it")
			})
		})

		t.Run("stops the process of the stop of the effect that it loses before the failure", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, losing())
				w.Spawn("agent", newAgent)
				w.Deliver("agent", place{name: "cell", token: 7, parks: true})
				drain(w)
				w.LoseAfter("host")
				w.Deliver("agent", unplace{name: "cell", token: 8})
				drain(w)
				expect.Equal(c, events(w, "cell"), []event{park{reply: 2}, stopped{}},
					"the cell takes the stop event of the lost halt")
				expect.Equal(c, events(w, "agent")[3:],
					[]event{called{err: errStopped, token: 7}, called{err: sim.ErrLost, token: 8}},
					"the agent takes the reply of the stop event before the failure of the halt")
			})
		})

		t.Run("starts the machine of the launch of the effect that it loses without a reply to the effect",
			func(t *testing.T) {
				t.Parallel()

				inCase(t, func(c *prop.Case) {
					w := sim.New(c, losing())
					w.Spawn("agent", newAgent)
					w.LoseAfter("host")
					w.Deliver("agent", place{name: "cell", token: 7})
					drain(w)
					expect.Equal(c, events(w, "cell"),
						[]event{put{key: "k", value: "v", reply: 2}, committed{token: 1}},
						"the cell takes the call of the lost launch")
					expect.Equal(c, events(w, "agent")[2:], []event{called{err: sim.ErrLost, token: 7}},
						"the agent takes the failure of the launch and no reply of the cell")
				})
			})

		t.Run("passes the message of the effect that it loses to its receiver without a reply to the effect",
			func(t *testing.T) {
				t.Parallel()

				inCase(t, func(c *prop.Case) {
					w := sim.New(c, losing())
					w.Spawn("a", newStore)
					w.Spawn("b", newStore)
					w.LoseAfter("peer")
					w.Deliver("a", forward{to: "b", key: "k", token: 7, timeout: time.Second})
					drain(w)
					expect.Equal(c, events(w, "b")[1:],
						[]event{put{key: "k", value: "v", reply: 3}, committed{token: 1}},
						"b takes the call of the lost message")
					expect.Equal(c, events(w, "a")[2:], []event{called{err: sim.ErrLost, token: 7}},
						"a takes the failure of the call and no reply of b")
				})
			})
	})

	t.Run("Outage", func(t *testing.T) {
		t.Parallel()

		t.Run("fails each effect of the dependency until the restore", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				d := disk{}
				w.Outage("disk")
				stored(d, "k")(w)
				stored(d, "j")(w)
				expect.Empty(c, d, "the disk applies no commit during the outage")
				expect.Equal(c, events(w, "store")[2], event(committed{err: errDisk, token: 1}),
					"the store takes the failure of its commit")
				w.Restore("disk")
				stored(d, "i")(w)
				expect.Equal(c, d, disk{"i": "v"}, "the disk applies the commit after the restore")
			})
		})

		t.Run("fails the case for a dependency with an outage", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := single(c)
				w.Outage("disk")
				w.Outage("disk")
			})
			expect.Equal(t, got.Contract, `sim: the dependency "disk" has an outage`,
				"the failure names the dependency")
		})

		t.Run("ends each wait of the dependency with the event of the failure of its effect", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				a := anatomy
				a.Dependency = func(f effect) (string, bool) {
					if wt, ok := f.(watch); ok && wt.token == 6 {
						return "other", true
					}
					return anatomy.Dependency(f)
				}
				w := sim.New(c, a)
				w.Spawn("store", newStore)
				first, other, last := &feed{}, &feed{}, &feed{}
				w.Deliver("store", subscribe{feed: first, token: 5})
				w.Deliver("store", subscribe{feed: other, token: 6})
				w.Deliver("store", subscribe{feed: last, token: 7})
				drain(w)
				w.Outage("feed")
				first.items = []event{fed{item: "x", token: 5}}
				other.items = []event{fed{item: "y", token: 6}}
				drain(w)
				expect.Equal(c, events(w, "store")[4:], []event{
					fed{err: sim.ErrLost, token: 5}, fed{err: sim.ErrLost, token: 7}, fed{item: "y", token: 6},
				}, "the waits of the feed end in the order of their starts, and the wait of the other dependency goes on")
			})
		})

		t.Run("ends a wait that has delivered an event with its end event, and cancels each wait", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, ending())
				w.Spawn("store", newStore)
				f, quiet := &feed{items: []event{fed{item: "x", token: 5}}}, &feed{}
				w.Deliver("store", subscribe{feed: f, token: 5})
				w.Deliver("store", subscribe{feed: quiet, token: 6})
				drain(w)
				w.Outage("feed")
				drain(w)
				expect.Equal(c, events(w, "store")[3:], []event{
					fed{item: "x", token: 5}, ended{err: sim.ErrLost, token: 5}, fed{err: sim.ErrLost, token: 6},
				}, "the wait that delivered an item takes its end event, and the other wait the event of its failure")
				expect.Equal(c, []int{f.cancels, quiet.cancels}, []int{1, 1}, "the outage cancels both waits")
			})
		})

		t.Run("counts the event of the outcome of a wait as an event that the wait delivered", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				a := ending()
				a.Perform = func(w *sim.World[event, effect], p *sim.Process[event, effect], f effect,
				) sim.Outcome[event, effect] {
					o := ending().Perform(w, p, f)
					if wt, ok := f.(watch); ok {
						o.Event = fed{item: "first", token: wt.token}
					}
					return o
				}
				w := sim.New(c, a)
				w.Spawn("store", newStore)
				w.Deliver("store", subscribe{feed: &feed{}, token: 5})
				drain(w)
				w.Outage("feed")
				drain(w)
				expect.Equal(c, events(w, "store")[2:], []event{
					fed{item: "first", token: 5}, ended{err: sim.ErrLost, token: 5},
				}, "the wait whose outcome delivered an event takes its end event")
			})
		})

		t.Run("ends a wait without an end event with the event of its failure", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.Deliver("store", subscribe{feed: &feed{items: []event{fed{item: "x", token: 5}}}, token: 5})
				drain(w)
				w.Outage("feed")
				drain(w)
				expect.Equal(c, events(w, "store")[2:], []event{
					fed{item: "x", token: 5}, fed{err: sim.ErrLost, token: 5},
				}, "the anatomy has no end event, so the wait takes the event of its failure after its item")
			})
		})

		t.Run(
			"fails the effects of the instance of a dependency of the scope host on its host alone",
			func(t *testing.T) {
				t.Parallel()

				inCase(t, func(c *prop.Case) {
					w := sim.New(c, scoped(sim.ScopeHost))
					w.Spawn("a", newStore)
					w.Spawn("b", newStore)
					ofA, ofB := disk{}, disk{}
					w.Outage("a/disk")
					w.Deliver("a", put{disk: ofA, key: "k", value: "v", reply: 9})
					w.Deliver("b", put{disk: ofB, key: "k", value: "v", reply: 9})
					drain(w)
					expect.Empty(c, ofA, "the outage of the instance of a fails the commit of a")
					expect.Equal(c, ofB, disk{"k": "v"}, "the instance of b applies the commit of b")
					expect.Equal(c, w.Faults(), []string{"outage:a/disk"}, "the faults list the instance of a")
				})
			},
		)

		t.Run("ends the waits of the instance of a dependency of the scope host on its host alone", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, scoped(sim.ScopeHost))
				w.Spawn("a", newStore)
				w.Spawn("b", newStore)
				ofA, ofB := &feed{}, &feed{}
				w.Deliver("a", subscribe{feed: ofA, token: 5})
				w.Deliver("b", subscribe{feed: ofB, token: 5})
				drain(w)
				w.Outage("a/feed")
				ofB.items = []event{fed{item: "y", token: 5}}
				drain(w)
				got := []event{events(w, "a")[len(events(w, "a"))-1], events(w, "b")[len(events(w, "b"))-1]}
				expect.Equal(c, got, []event{fed{err: sim.ErrLost, token: 5}, fed{item: "y", token: 5}},
					"the outage of the instance of a ends the wait of a, and the wait of b goes on")
			})
		})

		t.Run("fails the effects of every host for a dependency of the scope world", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, scoped(sim.ScopeWorld))
				w.Spawn("a", newStore)
				w.Spawn("b", newStore)
				ofA, ofB := disk{}, disk{}
				w.Outage("disk")
				w.Deliver("a", put{disk: ofA, key: "k", value: "v", reply: 9})
				w.Deliver("b", put{disk: ofB, key: "k", value: "v", reply: 9})
				drain(w)
				expect.Equal(c, []disk{ofA, ofB}, []disk{{}, {}}, "the one instance of the disk fails both commits")
			})
		})
	})

	t.Run("Restore", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the case for a dependency without a fault", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).Restore("disk") })
			expect.Equal(t, got.Contract, `sim: the dependency "disk" has no fault to restore`,
				"the failure names the dependency")
		})
	})

	t.Run("Slow", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps an effect without a timeout in flight until the restore", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				d := disk{}
				w.Slow("disk")
				stored(d, "k")(w)
				w.Advance(time.Hour)
				expect.Empty(c, w.Pending(), "the slow commit has no result")
				w.Restore("disk")
				drain(w)
				expect.Equal(c, d, disk{"k": "v"}, "the restore performs the slow commit")
				expect.Equal(c, events(w, "store")[2], event(committed{token: 1}), "the store takes the result")
			})
		})

		t.Run("fails an effect with a timeout once its timeout has passed", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				d := disk{}
				w.Slow("disk")
				w.Deliver("store", put{disk: d, key: "k", value: "v", reply: 9, timeout: time.Second})
				w.Step("store")
				w.Advance(time.Hour)
				w.Restore("disk")
				drain(w)
				expect.Empty(c, d, "the restore leaves the commit that timed out alone")
				expect.Equal(c, events(w, "store")[2], event(committed{err: context.DeadlineExceeded, token: 1}),
					"the store takes the timeout of its commit")
			})
		})

		t.Run("drops the effects that it keeps for a crashed process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				d, other := disk{}, disk{}
				w.Slow("disk")
				w.Deliver("a", put{disk: d, key: "k", value: "v", reply: 9, timeout: time.Second})
				w.Deliver("b", put{disk: other, key: "k", value: "v", reply: 9})
				w.Step("a")
				w.Step("b")
				w.Crash("a")
				w.Restore("disk")
				w.Advance(time.Hour)
				expect.Empty(c, d, "the restore does not perform the commit of the crashed process")
				expect.Equal(c, other, disk{"k": "v"}, "the restore performs the commit of the other process")
				expect.Equal(c, w.Pending(), []string{"b"},
					"the timeout of the commit of the crashed process passes nothing")
			})
		})

		t.Run("fails the case for a slow dependency", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := single(c)
				w.Slow("disk")
				w.Slow("disk")
			})
			expect.Equal(t, got.Contract, `sim: the dependency "disk" is slow`, "the failure names the dependency")
		})

		t.Run("performs a stalled effect once", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.Slow("disk")
				w.Deliver("store", put{disk: disk{}, key: "k", value: "v", reply: 9, timeout: time.Second})
				w.Step("store")
				w.Restore("disk")
				w.Advance(time.Hour)
				drain(w)
				expect.Equal(c, events(w, "store")[2:], []event{committed{token: 1}},
					"the store takes the result of its commit, and no timeout")
			})
		})
	})

	t.Run("Jump", func(t *testing.T) {
		t.Parallel()

		t.Run("moves the clock of the host forwards and backwards", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				clock := w.Process("store").Clock()
				w.Jump("store", time.Minute)
				expect.Equal(c, clock.Now(), sim.Start.Add(time.Minute), "the clock jumps forwards")
				w.Jump("store", -2*time.Minute)
				expect.Equal(c, clock.Now(), sim.Start.Add(-time.Minute), "the clock jumps backwards")
			})
		})

		t.Run("leaves the timers of the process to the advance of its clock", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.After(w.Process("store"), time.Minute, ticked{token: 1})
				want, _ := w.Next()
				for _, d := range []time.Duration{time.Hour, -2 * time.Hour} {
					w.Jump("store", d)
					gap, pending := w.Next()
					assert.True(c, pending, "the timer is pending")
					expect.Equal(c, gap, want, "a jump of the clock leaves the gap of the timer")
				}
				w.Advance(want)
				expect.Equal(c, w.Pending(), []string{"store"},
					"the timer is due once the clock has advanced by its gap")
			})
		})

		t.Run("moves the wall clock of the host of a component", func(t *testing.T) {
			t.Parallel()
			var offset time.Duration
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				clock := a.Value().env.Clock()
				before := clock.Wall()
				w.Jump("a", -time.Hour)
				offset = clock.Wall().Sub(before)
				w.Stop()
			})

			assert.Equal(t, offset, -time.Hour, "the wall clock of the host moves back by the hour")
		})
	})

	t.Run("Strobe", func(t *testing.T) {
		t.Parallel()

		t.Run("moves the clock between two offsets at each of the next steps", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.Strobe("store", time.Second, -time.Second, 3)
				expect.Equal(c, w.Faults(), []string{"clock-strobe:store"}, "the strobe is in force")
				for token := range uint64(4) {
					w.Deliver("store", ticked{token: token})
				}
				drain(w)
				times := make([]time.Time, 0, len(w.Log())-1)
				for _, e := range w.Log()[1:] {
					times = append(times, e.Time)
				}
				expect.Equal(c, times, []time.Time{
					sim.Start.Add(time.Second), sim.Start.Add(-time.Second), sim.Start.Add(time.Second), sim.Start,
				}, "the clock alternates for three steps and returns to its time")
				expect.Empty(c, w.Faults(), "the strobe is not in force after its steps")
			})
		})

		t.Run("counts the steps of every process of the host", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := node(c)
				w.Strobe("node", time.Second, -time.Second, 2)
				for _, name := range []string{"store", "cache", "store"} {
					w.Deliver(name, ticked{token: 1})
					w.Step(name)
				}
				times := make([]time.Time, 0, 3)
				for _, e := range w.Log()[3:] {
					times = append(times, e.Time)
				}
				want := []time.Time{sim.Start.Add(time.Second), sim.Start.Add(-time.Second), sim.Start}
				expect.Equal(c, times, want,
					"the processes take the two offsets in turn, and the third step the time of the clock")
			})
		})

		t.Run("fails the case for the host of a component", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, still, func(c *prop.Case) {
				w, _, _ := cells(c)
				w.Strobe("a", time.Second, -time.Second, 3)
			})

			assert.Equal(t, got.Contract,
				`sim: the host "a" runs a component, which takes no step that a strobe counts`,
				"the failure names the host")
		})
	})

	t.Run("Tear", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the first bytes of the next write of each medium of the host at a crash", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				m := mediumWith("")
				w.Spawn("store", newStore, disk{}, m)
				w.Tear("store", 2)
				assert.NoError(c, write(c, m, "data"), "the medium takes the write")
				w.Crash("store")
				expect.Equal(c, contentOf(c, m), "da", "the crash keeps the first two bytes of the torn write")
			})
		})

		t.Run("fails the case for a host that does not run", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore, mediumWith(""))
				w.Crash("store")
				w.Tear("store", 1)
			})
			expect.Equal(t, got.Contract, `sim: the host "store" does not run`, "the failure names the host")
		})

		t.Run("fails the case for a host without a medium", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).Tear("store", 1) })
			expect.Equal(t, got.Contract, `sim: the host "store" has no medium`, "the failure names the host")
		})

		t.Run("tears the next write to the disk of the host of a component", func(t *testing.T) {
			t.Parallel()
			var got string
			draws := strings.TrimSuffix(still, "]") +
				`,{"label": "kill.a", "value": {"type": "int", "value": 2}},` +
				`{"label": "keep", "value": {"type": "list", "of": "float", "value": [1.0]}}]`
			inBubble(t, draws, func(c *prop.Case, first bool) {
				w, a, _ := cells(c)
				assert.NoError(c, a.Value().Set(c.Context(), "x"), "the cell writes x")
				w.Tear("a", 1)
				f, err := a.Value().env.Disk().OpenFile("cell", os.O_WRONLY|os.O_TRUNC, 0)
				assert.NoError(c, err, "the cell opens")
				_, err = f.Write([]byte("abc"))
				assert.NoError(c, err, "the cell writes abc without a sync")
				w.Crash("a")
				w.Restart("a")
				if v := contents(c, a.Value().env, "cell"); first {
					got = v
				}
				w.Stop()
			})

			assert.Equal(t, got, "a", "the power loss keeps the first byte of the torn write")
		})
	})

	t.Run("Corrupt", func(t *testing.T) {
		t.Parallel()

		t.Run("flips the lowest bit of the durable byte of the file of each medium of the host", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				m := mediumWith("data")
				w.Spawn("store", newStore, m)
				w.Corrupt("store", "f", 1)
				expect.Equal(c, contentOf(c, m), "d`ta", "the corruption flips the lowest bit of the second byte")
			})
		})

		tests := []struct {
			name       string
			giveFile   string
			giveOffset int64
			want       string
		}{
			{
				name: "fails the case for an offset past the end of the file", giveFile: "f", giveOffset: 4,
				want: `sim: no medium of the host "store" has the byte 4 of the file "f"`,
			},
			{
				name: "fails the case for a negative offset", giveFile: "f", giveOffset: -1,
				want: `sim: no medium of the host "store" has the byte -1 of the file "f"`,
			},
			{
				name: "fails the case for a file that the media lack", giveFile: "g", giveOffset: 0,
				want: `sim: no medium of the host "store" has the byte 0 of the file "g"`,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got := failure(t, func(c *prop.Case) {
					w := sim.New(c, anatomy)
					w.Spawn("store", newStore, mediumWith("data"))
					w.Corrupt("store", tt.giveFile, tt.giveOffset)
				})
				expect.Equal(t, got.Contract, tt.want, "the failure names the host, the byte and the file")
			})
		}

		t.Run("flips a bit of a file on the disk of the host of a component", func(t *testing.T) {
			t.Parallel()
			var got string
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				assert.NoError(c, a.Value().Set(c.Context(), "a"), "the cell writes a after its checksum")
				w.Corrupt("a", "cell", 4)
				got = contents(c, a.Value().env, "cell")[4:]
				w.Stop()
			})

			assert.Equal(t, got, "`", "the lowest bit of the byte a flips")
		})
	})

	t.Run("Fill", func(t *testing.T) {
		t.Parallel()

		t.Run("refuses each write of the media of the host after the bytes that they take", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				m := mediumWith("")
				w.Spawn("store", newStore, m)
				w.Fill("store", 2)
				expect.ErrorIs(c, write(c, m, "data"), syscall.ENOSPC, "the medium refuses the bytes past the fill")
				expect.Equal(c, contentOf(c, m), "da", "the medium takes the bytes before the fill")
				expect.Equal(c, w.Faults(), []string{"full-disk:store"}, "the full media are in force")
			})
		})

		t.Run("fills the media of each process of the host", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				m, n := mediumWith(""), mediumWith("")
				h := w.AddHost("node")
				h.Spawn("store", newStore, m)
				h.Spawn("cache", newStore, n)
				w.Fill("node", 0)
				expect.ErrorIs(c, write(c, m, "data"), syscall.ENOSPC, "the medium of the store refuses the write")
				expect.ErrorIs(c, write(c, n, "data"), syscall.ENOSPC, "the medium of the cache refuses the write")
				expect.Equal(c, w.Faults(), []string{"full-disk:node"}, "the full media are one fault of the host")
			})
		})

		t.Run("fails the case for a host without a medium", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).Fill("store", 1) })
			expect.Equal(t, got.Contract, `sim: the host "store" has no medium`, "the failure names the host")
		})

		t.Run("fails the case for a host whose media are full", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore, mediumWith(""))
				w.Fill("store", 1)
				w.Fill("store", 1)
			})
			expect.Equal(t, got.Contract, `sim: the media of the host "store" are full`,
				"the failure names the host")
		})

		t.Run("fills the disk of the host of a component until RestoreDisk", func(t *testing.T) {
			t.Parallel()
			var full, restored error
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				w.Fill("a", 0)
				full = a.Value().Set(c.Context(), "x")
				w.RestoreDisk("a")
				restored = a.Value().Set(c.Context(), "x")
				w.Stop()
			})

			assert.ErrorIs(t, full, syscall.ENOSPC, "the full disk refuses the write")
			assert.NoError(t, restored, "the disk takes the write once its fill ends")
		})
	})

	t.Run("RestoreDisk", func(t *testing.T) {
		t.Parallel()

		t.Run("lifts the fill of the media of the host", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				m := mediumWith("")
				w.Spawn("store", newStore, m)
				w.Fill("store", 0)
				w.RestoreDisk("store")
				expect.NoError(c, write(c, m, "data"), "the medium takes each write after the restore")
				expect.Empty(c, w.Faults(), "the full media are no longer in force")
			})
		})

		t.Run("fails the case for a host whose media are not full", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).RestoreDisk("store") })
			expect.Equal(t, got.Contract, `sim: the media of the host "store" are not full`,
				"the failure names the host")
		})
	})

	t.Run("Recover", func(t *testing.T) {
		t.Parallel()

		t.Run("lifts the fill of the media of each host", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				m := mediumWith("")
				w.Spawn("store", newStore, m)
				w.Fill("store", 0)
				w.Recover()
				expect.NoError(c, write(c, m, "data"), "the medium takes each write after the recovery")
				expect.Empty(c, w.Faults(), "no fault is in force after the recovery")
			})
		})

		t.Run("ends each fault in force and restarts each crashed host", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := trio(c)
				d := disk{}
				w.Crash("a")
				w.Pause("b")
				w.Strobe("c", time.Second, time.Second, 5)
				w.Partition([]string{"a"}, []string{"b", "c"})
				w.Outage("disk")
				w.Slow("disk")
				w.LoseBefore("disk")
				w.LoseAfter("disk")
				expect.Equal(c, w.Faults(), []string{
					"crash:a", "pause:b", "clock-strobe:c", "partition",
					"outage:disk", "slow:disk", "lose-before:disk", "lose-after:disk",
				}, "the faults are in force in the order of their targets")
				w.Recover()
				expect.Empty(c, w.Faults(), "no fault is in force after the recovery")
				expect.Equal(c, w.Process("a").Epoch(), uint64(2), "the crashed process restarts")
				expect.Equal(c, w.Process("b").Epoch(), uint64(1), "a process that did not crash keeps its machine")
				w.Deliver("c", put{disk: d, key: "k", value: "v", reply: 9})
				drain(w)
				expect.Equal(c, d, disk{"k": "v"}, "the dependency applies the commit after the recovery")
			})
		})

		t.Run("restarts each exited host", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.Deliver("store", put{key: "k", reply: 9})
				w.Step("store")
				w.Recover()
				expect.Equal(c, w.Process("store").Epoch(), uint64(2), "the process that the exit stopped restarts")
				expect.Empty(c, w.Faults(), "no exit remains after the recovery")
			})
		})

		t.Run("leaves the hosts of a stopped world stopped", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := single(c)
				w.Stop()
				w.Recover()
				w.Crash("store")
			})
			expect.Equal(t, got.Contract, `sim: the host "store" does not run`,
				"the recovery leaves the stopped host stopped")
		})

		t.Run("performs the effects that a slow dependency kept in flight", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				d := disk{}
				w.Slow("disk")
				stored(d, "k")(w)
				w.Recover()
				expect.Equal(c, d, disk{"k": "v"}, "the recovery performs the slow commit")
			})
		})

		t.Run("resumes a paused component and restarts a crashed one", func(t *testing.T) {
			t.Parallel()
			var numbers []int
			var set error
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, b := cells(c)
				w.Pause("a")
				w.Crash("b")
				w.Recover()
				numbers = []int{a.Value().env.Incarnation(), b.Value().env.Incarnation()}
				set = a.Value().Set(c.Context(), "x")
				w.Stop()
			})

			assert.Equal(t, numbers, []int{1, 2}, "the recovery keeps a and restarts b")
			assert.NoError(t, set, "the resumed component writes")
		})

		t.Run("ends each slow link of the network of the components", func(t *testing.T) {
			t.Parallel()
			var faults []string

			got := slowThenSend(t, func(w *sim.World[sim.None, sim.None]) {
				w.Recover()
				faults = w.Faults()
			})

			assert.Equal(t, got, []string{"1"}, "the datagram arrives at the latency of the link before its slowness")
			assert.Empty(t, faults, "no slow link is in force after the recovery")
		})
	})

	t.Run("Faults", func(t *testing.T) {
		t.Parallel()

		t.Run("lists the faults of each dependency apart", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.Outage("disk")
				w.Slow("feed")
				expect.Equal(c, w.Faults(), []string{"outage:disk", "slow:feed"}, "each dependency has its own faults")
			})
		})

		t.Run("lists an exited host under the kind exit", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := single(c)
				w.Deliver("store", put{key: "k", reply: 9})
				w.Step("store")
				expect.Equal(c, w.Faults(), []string{"exit:store"}, "the exit of the host is apart from a crash")
			})
		})

		t.Run("records the faults in force in each entry of the log", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Pause("b")
				w.Deliver("a", ticked{token: 1})
				w.Step("a")
				expect.Equal(c, w.Log()[2].Faults, []string{"pause:b"}, "the entry records the pause")
				expect.Nil(c, w.Log()[0].Faults, "an entry without a fault in force records none")
			})
		})

		t.Run("classifies each fault that strikes by its kind", func(t *testing.T) {
			t.Parallel()

			kinds := []string{
				"crash", "restart", "pause", "partition", "repair", "drop", "delay", "duplicate",
				"lose-before", "lose-after", "outage", "slow", "clock-jump", "clock-strobe", "torn-write", "corruption",
				"full-disk",
			}
			opts := make([]prop.Option, 0, 3+len(kinds))
			opts = append(opts, prop.Cases(1), prop.Hermetic(), prop.Seed(1), prop.Store(""))
			for _, kind := range kinds {
				opts = append(opts, prop.Require(kind, 1))
			}
			prop.ForAll(t, "each fault strikes in each case", func(c *prop.Case) {
				w := pair(c)
				d := disk{}
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				w.Deliver("b", ticked{token: 1})
				w.Drop("b")
				w.Deliver("a", forward{to: "b", key: "k", token: 8})
				w.Step("a")
				w.Deliver("b", ticked{token: 2})
				w.Delay("b")
				w.Duplicate("b")
				w.Partition([]string{"a"}, []string{"b"})
				w.Repair()
				w.Pause("a")
				w.Jump("a", time.Second)
				w.Strobe("a", time.Second, time.Second, 1)
				w.Crash("a")
				w.Restart("a")
				w.Spawn("store", newStore)
				w.LoseBefore("disk")
				w.LoseAfter("disk")
				stored(d, "k")(w)
				stored(d, "j")(w)
				w.Outage("disk")
				w.Slow("disk")
				w.Spawn("durable", newStore, mediumWith("data"))
				w.Tear("durable", 1)
				w.Corrupt("durable", "f", 0)
				w.Fill("durable", 1)
			}, opts...)
		})

		t.Run("classifies each fault of the network of the components that strikes by its kind", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Blocking = true
			s.Tolerate = map[sim.Fault]int{sim.FaultReset: 1, sim.FaultSlowLink: 1}
			script := scriptOf(opening(true), stepOf("reset", drawn("link", 0)), slowLink(0),
				stepOf("restore-link", drawn("link", 0)))

			prop.ForAll(t, "the faults of the network strike", func(c *prop.Case) { sim.Run(c, s) },
				prop.Around(s.Around), prop.Draws(script), prop.Cases(1), prop.Store(""),
				prop.Require(string(sim.FaultReset), 0.01), prop.Require(string(sim.FaultSlowLink), 0.01),
				prop.Require(string(sim.FaultRestoreLink), 0.01))
		})
	})

	t.Run("ReplaceDisk", func(t *testing.T) {
		t.Parallel()

		t.Run("crashes a component until a restart that starts over an empty disk", func(t *testing.T) {
			t.Parallel()
			var faults [][]string
			var got string
			inBubble(t, still, func(c *prop.Case, first bool) {
				w, a, _ := cells(c)
				assert.NoError(c, a.Value().Set(c.Context(), "v"), "the cell writes")
				w.ReplaceDisk("a")
				replaced := w.Faults()
				w.Restart("a")
				v, err := a.Value().Get(c.Context())
				assert.NoError(c, err, "the cell reads")
				if first {
					faults, got = [][]string{replaced, w.Faults()}, v
				}
				w.Stop()
			})

			assert.Equal(t, faults, [][]string{{"disk-replacement:a"}, nil},
				"the replacement is in force until the restart")
			assert.Equal(t, got, "", "the next incarnation starts over an empty disk")
		})

		t.Run("ends each open call of the component as unknown with medium.ErrCrashed", func(t *testing.T) {
			t.Parallel()
			var got []completion
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, _ := cells(c)
				parked := a.Value()
				parked.parks = true
				w.Invoke(0, "a", "Get", nil, nil, func(ctx context.Context) (any, error) { return parked.Get(ctx) })
				w.ReplaceDisk("a")
				got = completions(w.History().Events())
				w.Stop()
			})

			assert.Equal(t, got, []completion{{kind: history.Unknown, err: medium.ErrCrashed}},
				"the replacement ends the parked call")
		})

		t.Run("lists a later crash of the restarted component as a crash", func(t *testing.T) {
			t.Parallel()
			var faults []string
			draws := strings.TrimSuffix(still, "]") + `,{"label": "kill.a", "value": {"type": "int", "value": 1}}]`
			inBubble(t, draws, func(c *prop.Case, first bool) {
				w, _, _ := cells(c)
				w.ReplaceDisk("a")
				w.Restart("a")
				w.Crash("a")
				if first {
					faults = w.Faults()
				}
				w.Stop()
			})

			assert.Equal(t, faults, []string{"crash:a"}, "the restart ends the disk replacement")
		})

		t.Run("passes the kind disk-replacement to the case as a label", func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				prop.ForAll(t, "the replacement strikes in each case", func(c *prop.Case) {
					w, _, _ := cells(c)
					w.ReplaceDisk("a")
					w.Stop()
				}, prop.Cases(1), prop.Draws(still), prop.Store(""), prop.Require(string(sim.FaultDiskReplacement), 1))
			})
		})

		t.Run("fails the case for a host without a component", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).ReplaceDisk("store") })
			expect.Equal(t, got.Contract, `sim: the host "store" runs no component`, "the failure names the host")
		})

		t.Run("fails the case for the host of a component that does not run", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, still, func(c *prop.Case) {
				w, _, _ := cells(c)
				w.Stop()
				w.ReplaceDisk("a")
			})

			assert.Equal(t, got.Contract, `sim: the host "a" does not run`, "the failure names the host")
		})
	})

	t.Run("Reset", func(t *testing.T) {
		t.Parallel()

		t.Run("fails each call on the streams between two components", func(t *testing.T) {
			t.Parallel()
			var readErr error
			inBubble(t, still, func(c *prop.Case, _ bool) {
				w, a, b := cells(c)
				accepted := serve(c, a.Value().env, 7000)
				env := b.Value().env
				done := make(chan struct{})
				env.Go(func() {
					defer close(done)
					conn, err := env.Dial(c.Context(), "a:7000")
					assert.NoError(c, err, "b dials a")
					<-accepted
					w.Reset("a", "b")
					_, readErr = conn.Read(make([]byte, 1))
				})
				<-done
				w.Stop()
			})

			assert.ErrorIs(t, readErr, host.ErrReset, "the read of the reset stream fails")
		})

		t.Run("fails the case for a process that is no component", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, `[{"label": "drift.a", "value": {"type": "int", "value": 0}},`+
				`{"label": "drift.m", "value": {"type": "int", "value": 0}}]`, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				sim.SpawnComponent(w, "a", openCell)
				w.Spawn("m", newStore)
				w.Reset("a", "m")
			})

			assert.Equal(t, got.Contract, `sim: the process "m" is no component`, "the failure names the process")
		})
	})

	t.Run("SlowLink", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the case for a world without the blocking faults", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, still, func(c *prop.Case) {
				w, _, _ := cells(c)
				w.SlowLink("b", "a", time.Second)
			})

			assert.Equal(t, got.Contract, "host: Slow(b, a) of a network without blocking",
				"the network refuses the slow link")
		})

		t.Run("fails the case for a link that is slow", func(t *testing.T) {
			t.Parallel()
			var cs cellSet
			s := cs.simulation()
			s.Blocking = true
			s.Actions = []sim.Action[sim.None, sim.None]{{
				Name: "slow twice",
				Run: func(w *sim.World[sim.None, sim.None], _ int, _ any) {
					w.SlowLink("a", "b", time.Second)
					w.SlowLink("b", "a", time.Second)
				},
			}}
			r := &recorded{Recorder: assert.NewRecorder(), t: t}

			cs.runCase(r, s, scriptOf(opening(true), stepOf("slow twice")))

			failures := r.Failures()
			assert.NotEmpty(t, failures, "the second slow link fails the case")
			inner, _ := failures[0].CaseFailure()
			assert.Equal(t, inner.Contract, `sim: the link between "b" and "a" is slow`, "the failure names the link")
		})
	})

	t.Run("RestoreLink", func(t *testing.T) {
		t.Parallel()

		t.Run("fails the case for a link that is not slow", func(t *testing.T) {
			t.Parallel()

			got := failsInBubble(t, still, func(c *prop.Case) {
				w, _, _ := cells(c)
				w.RestoreLink("a", "b")
			})

			assert.Equal(t, got.Contract, `sim: the link between "a" and "b" is not slow`, "the failure names the link")
		})

		t.Run("ends the slowness of the link on the network", func(t *testing.T) {
			t.Parallel()

			got := slowThenSend(t, func(w *sim.World[sim.None, sim.None]) { w.RestoreLink("a", "b") })

			assert.Equal(t, got, []string{"1"}, "the datagram arrives at the latency of the link before its slowness")
		})
	})
}

// slowThenSend returns the datagrams that b receives in a run of the cells
// whose one step slows the link between a and b, ends the slowness through
// end, and sends a datagram from a to b.
func slowThenSend(t *testing.T, end func(w *sim.World[sim.None, sim.None])) []string {
	t.Helper()

	var cs cellSet
	s := cs.simulation()
	s.Blocking = true
	s.Actions = []sim.Action[sim.None, sim.None]{{
		Name: "slow and send",
		Run: func(w *sim.World[sim.None, sim.None], _ int, _ any) {
			w.SlowLink("a", "b", time.Second)
			end(w)
			c := cs.a.Value()
			c.env.Go(func() { _, _ = c.packets.WriteTo([]byte("1"), hostAddr("b:9001")) })
		},
	}}
	cs.runCase(t, s, scriptOf(opening(true), stepOf("slow and send")))
	return cs.received[0]
}

// serve listens on the port of env and accepts each stream on a goroutine of
// its incarnation until the listener closes. accepted is closed once the
// first stream is accepted.
func serve(c *prop.Case, env *host.Env, port int) (accepted chan struct{}) {
	l, err := env.Listen(port)
	assert.NoError(c, err, "the component listens")
	accepted = make(chan struct{})
	env.Go(func() {
		for first := true; ; first = false {
			if _, err := l.Accept(); err != nil {
				return
			}
			if first {
				close(accepted)
			}
		}
	})
	return accepted
}

// dial dials addr from env on a goroutine of its incarnation, and returns the
// error of the dial.
func dial(c *prop.Case, env *host.Env, addr string) error {
	var err error
	done := make(chan struct{})
	env.Go(func() {
		defer close(done)
		var conn net.Conn
		if conn, err = env.Dial(c.Context(), addr); err == nil {
			_ = conn.Close()
		}
	})
	<-done
	return err
}
