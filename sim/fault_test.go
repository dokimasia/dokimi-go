// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim_test

import (
	"context"
	"syscall"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

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

		t.Run("fails the case for a name that no process has", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { pair(c).Partition([]string{"a"}, []string{"missing"}) })
			expect.Equal(t, got.Contract, `sim: no process of the world is named "missing"`,
				"the failure names the process")
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

		t.Run("moves the clock of the process forwards and backwards", func(t *testing.T) {
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
	})

	t.Run("Tear", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the first bytes of the next write of each medium of the process at a crash", func(t *testing.T) {
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

		t.Run("fails the case for a process that does not run", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore, mediumWith(""))
				w.Crash("store")
				w.Tear("store", 1)
			})
			expect.Equal(t, got.Contract, `sim: the process "store" does not run`, "the failure names the process")
		})

		t.Run("fails the case for a process without a medium", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).Tear("store", 1) })
			expect.Equal(t, got.Contract, `sim: the process "store" has no medium`, "the failure names the process")
		})
	})

	t.Run("Corrupt", func(t *testing.T) {
		t.Parallel()

		t.Run("flips the lowest bit of the durable byte of the file of each medium of the process", func(t *testing.T) {
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
				want: `sim: no medium of the process "store" has the byte 4 of the file "f"`,
			},
			{
				name: "fails the case for a negative offset", giveFile: "f", giveOffset: -1,
				want: `sim: no medium of the process "store" has the byte -1 of the file "f"`,
			},
			{
				name: "fails the case for a file that the media lack", giveFile: "g", giveOffset: 0,
				want: `sim: no medium of the process "store" has the byte 0 of the file "g"`,
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
				expect.Equal(t, got.Contract, tt.want, "the failure names the process, the byte and the file")
			})
		}
	})

	t.Run("Fill", func(t *testing.T) {
		t.Parallel()

		t.Run("refuses each write of the media of the process after the bytes that they take", func(t *testing.T) {
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

		t.Run("fails the case for a process without a medium", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).Fill("store", 1) })
			expect.Equal(t, got.Contract, `sim: the process "store" has no medium`, "the failure names the process")
		})

		t.Run("fails the case for a process whose media are full", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) {
				w := sim.New(c, anatomy)
				w.Spawn("store", newStore, mediumWith(""))
				w.Fill("store", 1)
				w.Fill("store", 1)
			})
			expect.Equal(t, got.Contract, `sim: the media of the process "store" are full`,
				"the failure names the process")
		})
	})

	t.Run("RestoreDisk", func(t *testing.T) {
		t.Parallel()

		t.Run("lifts the fill of the media of the process", func(t *testing.T) {
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

		t.Run("fails the case for a process whose media are not full", func(t *testing.T) {
			t.Parallel()

			got := failure(t, func(c *prop.Case) { single(c).RestoreDisk("store") })
			expect.Equal(t, got.Contract, `sim: the media of the process "store" are not full`,
				"the failure names the process")
		})
	})

	t.Run("Recover", func(t *testing.T) {
		t.Parallel()

		t.Run("lifts the fill of the media of each process", func(t *testing.T) {
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

		t.Run("ends each fault in force and restarts each crashed process", func(t *testing.T) {
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
	})
}
