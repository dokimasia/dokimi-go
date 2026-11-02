// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/sim"
)

func TestMessage(t *testing.T) {
	t.Parallel()

	t.Run("Step", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the reply of the receiver of a message to its sender", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				expect.Equal(c, w.Pending(), []string{"b"}, "the receiver takes the call of the message")
				drain(w)
				expect.Equal(c, events(w, "b")[1], event(put{key: "k", value: "v", reply: 3}),
					"the call of the message has the next reply token")
				expect.Equal(c, events(w, "a")[2], event(called{value: "stored", token: 7}),
					"the sender takes the result of the reply")
				expect.Empty(c, w.History().Events(), "the history does not record a message")
			})
		})

		t.Run("fails a message to a crashed process with ErrCrashed", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Crash("b")
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				drain(w)
				expect.Equal(c, events(w, "a")[2:], []event{called{err: sim.ErrCrashed, token: 7}},
					"the sender takes the crash of the receiver")
			})
		})

		t.Run("fails a message to a stopped process with ErrRunEnded", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("b", put{key: "k", reply: 9})
				w.Step("b")
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				drain(w)
				expect.Equal(c, events(w, "a")[2:], []event{called{err: sim.ErrRunEnded, token: 7}},
					"the sender takes the stop of the receiver")
			})
		})
	})

	t.Run("Advance", func(t *testing.T) {
		t.Parallel()

		t.Run("fails a message at its timeout and cancels its call at the receiver", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Pause("b")
				w.Deliver("a", forward{to: "b", key: "k", token: 7, timeout: time.Second})
				w.Step("a")
				w.Advance(time.Hour)
				w.Resume("b")
				drain(w)
				expect.Equal(c, events(w, "a")[2:], []event{called{err: context.DeadlineExceeded, token: 7}},
					"the sender takes the timeout of the message alone")
				expect.Equal(c, events(w, "b")[2], event(cancelled{reply: 3}),
					"the receiver takes the cancel of the call after the call")
			})
		})

		t.Run("cancels the message that expires alone", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Pause("b")
				w.Deliver("a", forward{to: "b", key: "k", token: 7, timeout: time.Second})
				w.Deliver("a", forward{to: "b", key: "j", token: 8})
				w.Step("a")
				w.Step("a")
				w.Advance(time.Hour)
				w.Resume("b")
				drain(w)
				expect.Equal(c, events(w, "a")[3:], []event{
					called{err: context.DeadlineExceeded, token: 7}, called{value: "stored", token: 8},
				}, "the message without a timeout takes its reply")
				expect.NotContains(c, events(w, "b"), event(cancelled{reply: 4}),
					"the receiver takes no cancel of the message without a timeout")
			})
		})

		t.Run("leaves a message that has its reply alone at its timeout", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "k", token: 7, timeout: time.Second})
				drain(w)
				w.Advance(time.Hour)
				drain(w)
				expect.Equal(c, events(w, "a")[2:], []event{called{value: "stored", token: 7}},
					"the sender takes the reply alone")
				expect.Length(c, events(w, "b"), 3, "the receiver takes no cancel")
			})
		})
	})

	t.Run("Crash", func(t *testing.T) {
		t.Parallel()

		t.Run("fails each message to the crashed process at its sender", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				w.Crash("b")
				drain(w)
				expect.Equal(c, events(w, "a")[2:], []event{called{err: sim.ErrCrashed, token: 7}},
					"the sender takes the crash of the receiver")
			})
		})

		t.Run("cancels each message of the crashed process at its receiver", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Step("a")
				w.Step("b")
				w.Crash("a")
				drain(w)
				expect.Equal(c, events(w, "b")[2:], []event{committed{token: 1}, cancelled{reply: 3}},
					"the receiver takes the cancel of the call after the result of its commit")
				expect.Length(c, events(w, "a"), 2, "the crashed sender takes no result")
			})
		})

		t.Run("leaves the messages of the other senders alone", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Spawn("c", newStore)
				w.Deliver("a", forward{to: "b", key: "k", token: 7})
				w.Deliver("c", forward{to: "b", key: "j", token: 8})
				w.Step("a")
				w.Step("c")
				w.Step("c")
				w.Crash("a")
				drain(w)
				expect.Equal(c, events(w, "c")[2:], []event{called{value: "stored", token: 8}},
					"the other sender takes its reply")
			})
		})

		t.Run("ends the timeout of each message of the crashed process", func(t *testing.T) {
			t.Parallel()

			inCase(t, func(c *prop.Case) {
				w := pair(c)
				w.Pause("b")
				w.Deliver("a", forward{to: "b", key: "k", token: 7, timeout: time.Second})
				w.Step("a")
				w.Crash("a")
				w.Restart("a")
				w.Advance(time.Hour)
				drain(w)
				w.Step("a")
				expect.False(c, slices.ContainsFunc(w.Log(), func(e sim.Entry[event, effect]) bool {
					return e.Dropped
				}), "the timeout queues no result for the crashed sender")
			})
		})
	})
}
