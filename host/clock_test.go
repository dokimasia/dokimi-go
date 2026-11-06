// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/prop"
	"go.dokimi.dev/assert/stateful"

	"go.dokimi.dev/host"
)

// twice are the draws of a host whose monotonic clock runs twice as fast as
// the clock of the run.
const twice = `[{"label": "drift.node-1", "value": {"type": "int", "value": 1000}}]`

// drifted are the draws of a host whose monotonic clock runs 1.333 times as
// fast as the clock of the run, so its arithmetic has remainders.
const drifted = `[{"label": "drift.node-1", "value": {"type": "int", "value": 333}}]`

// midnight is the instant at which the clock of a bubble and the clock of a
// scheduler start.
var midnight = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

// wallWait reports whether a wait of an incarnation for the condition cond of
// its wall clock ends at the fault of the world. Each step of before runs
// before the wait.
func wallWait(
	t *testing.T,
	fault func(h *host.Host),
	cond func(wall time.Time) bool,
	before ...func(h *host.Host),
) bool {
	t.Helper()

	var woke bool
	bubbled(t, targets, func(c *prop.Case, _ bool) {
		h, i, env := started(c)
		for _, step := range before {
			step(h)
		}
		done := make(chan struct{})
		i.Go(func() {
			env.Wait(func() bool { return cond(env.Clock().Wall()) })
			close(done)
		})
		synctest.Wait()
		fault(h)
		<-done
		woke = true
		i.Crash(host.ProcessCrash)
	})
	return woke
}

func TestClock(t *testing.T) {
	t.Parallel()

	t.Run("Wall", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			giveF func(h *host.Host)
			want  time.Time
		}{
			{
				name:  "returns the start of the host plus its monotonic time",
				giveF: func(*host.Host) {},
				want:  midnight.Add(5 * time.Second),
			},
			{
				name:  "adds the bumps of the host",
				giveF: func(h *host.Host) { h.Bump(time.Hour); h.Bump(-time.Minute) },
				want:  midnight.Add(5*time.Second + time.Hour - time.Minute),
			},
			{
				name:  "adds the delta of a strobe in its first period",
				giveF: func(h *host.Host) { h.Strobe(time.Minute, time.Second) },
				want:  midnight.Add(5*time.Second + time.Minute),
			},
			{
				name:  "ends each bump and each strobe at ResetClock",
				giveF: func(h *host.Host) { h.Bump(time.Hour); h.Strobe(time.Minute, time.Second); h.ResetClock() },
				want:  midnight.Add(5 * time.Second),
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var got time.Time
				bubbled(t, targets, func(c *prop.Case, first bool) {
					h, _, env := started(c)
					time.Sleep(5 * time.Second)
					tt.giveF(h)
					if wall := env.Clock().Wall(); first {
						got = wall
					}
				})

				assert.True(t, got.Equal(tt.want), "the wall time is "+tt.want.String()+", not "+got.String())
			})
		}

		t.Run("drops the delta of a strobe in its second period", func(t *testing.T) {
			t.Parallel()
			var got time.Time
			bubbled(t, targets, func(c *prop.Case, first bool) {
				h, _, env := started(c)
				h.Strobe(time.Minute, time.Second)
				time.Sleep(time.Second)
				if wall := env.Clock().Wall(); first {
					got = wall
				}
			})

			assert.True(t, got.Equal(midnight.Add(time.Second)), "the wall time without the delta, not "+got.String())
		})

		t.Run("adds the delta of a strobe again in its third period", func(t *testing.T) {
			t.Parallel()
			var got time.Time
			bubbled(t, targets, func(c *prop.Case, first bool) {
				h, _, env := started(c)
				h.Strobe(time.Minute, time.Second)
				time.Sleep(2 * time.Second)
				if wall := env.Clock().Wall(); first {
					got = wall
				}
			})

			assert.True(
				t,
				got.Equal(midnight.Add(2*time.Second+time.Minute)),
				"the wall time with the delta, not "+got.String(),
			)
		})

		t.Run("counts the periods of a strobe from its start", func(t *testing.T) {
			t.Parallel()
			var got time.Time
			bubbled(t, targets, func(c *prop.Case, first bool) {
				h, _, env := started(c)
				time.Sleep(500 * time.Millisecond)
				h.Strobe(time.Minute, time.Second)
				if wall := env.Clock().Wall(); first {
					got = wall
				}
			})

			assert.True(t, got.Equal(midnight.Add(500*time.Millisecond+time.Minute)),
				"the first period starts at the strobe, not "+got.String())
		})

		t.Run("returns a time in UTC without a monotonic reading", func(t *testing.T) {
			t.Parallel()
			var got time.Time
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				got = env.Clock().Wall()
			})

			assert.False(t, strings.Contains(got.String(), "m="), "the time has no monotonic reading")
			assert.Equal(t, got.Location(), time.UTC, "the time is in UTC")
		})
	})

	t.Run("Monotonic", func(t *testing.T) {
		t.Parallel()

		t.Run("advances at the rate of the host", func(t *testing.T) {
			t.Parallel()
			var got time.Duration
			bubbled(t, twice, func(c *prop.Case, first bool) {
				_, _, env := started(c)
				time.Sleep(time.Second)
				if mono := env.Clock().Monotonic(); first {
					got = mono
				}
			})

			assert.Equal(t, got, 2*time.Second, "one second of the run is two seconds of the host")
		})

		t.Run("rounds a time of the run at the rate of the host down to a nanosecond", func(t *testing.T) {
			t.Parallel()
			var got time.Duration
			bubbled(t, drifted, func(c *prop.Case, first bool) {
				_, _, env := started(c)
				time.Sleep(1234567 * time.Nanosecond)
				if mono := env.Clock().Monotonic(); first {
					got = mono
				}
			})

			assert.Equal(t, got, 1645677*time.Nanosecond, "1,234,567 ns of the run times 1.333")
		})
	})

	t.Run("Bump", func(t *testing.T) {
		t.Parallel()

		t.Run("wakes a wait for the wall clock", func(t *testing.T) {
			t.Parallel()
			woke := wallWait(t, func(h *host.Host) { h.Bump(2 * time.Hour) }, func(wall time.Time) bool {
				return wall.After(midnight.Add(time.Hour))
			})

			assert.True(t, woke, "the bump wakes the wait")
		})
	})

	t.Run("ResetClock", func(t *testing.T) {
		t.Parallel()

		t.Run("wakes a wait for the wall clock", func(t *testing.T) {
			t.Parallel()
			woke := wallWait(t, func(h *host.Host) { h.ResetClock() }, func(wall time.Time) bool {
				return wall.Before(midnight.Add(time.Hour))
			}, func(h *host.Host) { h.Bump(2 * time.Hour) })

			assert.True(t, woke, "the reset wakes the wait")
		})
	})

	t.Run("Sleep", func(t *testing.T) {
		t.Parallel()

		t.Run("returns once the monotonic clock has advanced by d", func(t *testing.T) {
			t.Parallel()
			var slept time.Duration
			bubbled(t, twice, func(c *prop.Case, first bool) {
				_, _, env := started(c)
				start := time.Now()
				env.Clock().Sleep(2 * time.Second)
				if elapsed := time.Since(start); first {
					slept = elapsed
				}
			})

			assert.Equal(t, slept, time.Second, "two seconds of the host are one second of the run")
		})

		t.Run("returns after the shortest time of the run in which the host clock advances by d", func(t *testing.T) {
			t.Parallel()
			var slept time.Duration
			bubbled(t, drifted, func(c *prop.Case, first bool) {
				_, _, env := started(c)
				time.Sleep(time.Second)
				start := time.Now()
				env.Clock().Sleep(1000001 * time.Nanosecond)
				if elapsed := time.Since(start); first {
					slept = elapsed
				}
			})

			assert.Equal(t, slept, 750189*time.Nanosecond, "750,189 ns of the run are 1,000,001 ns of the host")
		})

		t.Run(
			"returns at once under the scheduler for a duration of zero, without a turn of another task",
			func(t *testing.T) {
				t.Parallel()
				var order []string
				scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, _ bool) {
					h := host.New(c, "node-1", nil, s)
					_, env := h.Start()
					var log []string
					env.Go(func() {
						env.Clock().Sleep(0)
						log = append(log, "a")
					})
					env.Go(func() { log = append(log, "b") })
					s.Run()
					order = log
				})

				assert.Equal(t, order, []string{"a", "b"}, "a runs through its sleep of no time")
			},
		)

		t.Run("returns at once for a duration of zero or less", func(t *testing.T) {
			t.Parallel()
			var slept time.Duration
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, _, env := started(c)
				start := time.Now()
				env.Clock().Sleep(-time.Second)
				slept = time.Since(start)
			})

			assert.Equal(t, slept, time.Duration(0), "no time passes")
		})

		t.Run("returns early at the crash of the incarnation", func(t *testing.T) {
			t.Parallel()
			var slept time.Duration
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				start := time.Now()
				done := make(chan time.Duration)
				i.Go(func() {
					env.Clock().Sleep(time.Hour)
					done <- time.Since(start)
				})
				synctest.Wait()
				i.Crash(host.ProcessCrash)
				slept = <-done
			})

			assert.Equal(t, slept, time.Duration(0), "the sleep ends at the crash")
		})

		t.Run("returns from a sleep that ends during a pause after Resume", func(t *testing.T) {
			t.Parallel()
			var early int
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				done := make(chan struct{}, 1)
				i.Go(func() {
					env.Clock().Sleep(time.Second)
					done <- struct{}{}
				})
				synctest.Wait()
				i.Pause()
				time.Sleep(2 * time.Second)
				early = len(done)
				i.Resume()
				<-done
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, early, 0, "the sleep returns after the pause")
		})
	})

	t.Run("AfterFunc", func(t *testing.T) {
		t.Parallel()

		t.Run("calls f once the monotonic clock has advanced by d", func(t *testing.T) {
			t.Parallel()
			var at time.Duration
			bubbled(t, twice, func(c *prop.Case, first bool) {
				_, i, env := started(c)
				start := time.Now()
				done := make(chan time.Duration)
				env.Clock().AfterFunc(2*time.Second, func() { done <- time.Since(start) })
				if elapsed := <-done; first {
					at = elapsed
				}
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, at, time.Second, "the call comes after one second of the run")
		})

		t.Run("cancels the call at a stop before f starts", func(t *testing.T) {
			t.Parallel()
			var stopped bool
			var calls atomic.Int32
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				stop := env.Clock().AfterFunc(time.Second, func() { calls.Add(1) })
				stopped = stop()
				time.Sleep(2 * time.Second)
				i.Crash(host.ProcessCrash)
			})

			assert.True(t, stopped, "the stop cancels the call")
			assert.Equal(t, calls.Load(), int32(0), "f is not called")
		})

		t.Run("ends the goroutine of the call at a stop", func(t *testing.T) {
			t.Parallel()
			var leaked []string
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				stop := env.Clock().AfterFunc(time.Hour, func() {})
				stop()
				synctest.Wait()
				leaked = i.Leaked()
				i.Crash(host.ProcessCrash)
			})

			assert.Empty(t, leaked, "no goroutine of the incarnation waits for the hour")
		})

		t.Run("calls f at once for a duration of zero", func(t *testing.T) {
			t.Parallel()
			var at time.Duration
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				start := time.Now()
				done := make(chan time.Duration)
				env.Clock().AfterFunc(0, func() { done <- time.Since(start) })
				at = <-done
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, at, time.Duration(0), "the call comes without a wait")
		})

		t.Run("reports false at a stop once f has started", func(t *testing.T) {
			t.Parallel()
			var stopped bool
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				done := make(chan struct{})
				stop := env.Clock().AfterFunc(time.Second, func() { close(done) })
				<-done
				stopped = stop()
				i.Crash(host.ProcessCrash)
			})

			assert.False(t, stopped, "the stop comes after the call")
		})

		t.Run("cancels the call at the crash of the incarnation", func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				env.Clock().AfterFunc(time.Second, func() { calls.Add(1) })
				i.Crash(host.ProcessCrash)
				time.Sleep(2 * time.Second)
			})

			assert.Equal(t, calls.Load(), int32(0), "f is not called after the crash")
		})

		t.Run("defers the call during a pause until Resume", func(t *testing.T) {
			t.Parallel()
			var during int32
			bubbled(t, targets, func(c *prop.Case, _ bool) {
				_, i, env := started(c)
				var calls atomic.Int32
				done := make(chan struct{})
				env.Clock().AfterFunc(time.Second, func() {
					calls.Add(1)
					close(done)
				})
				i.Pause()
				time.Sleep(2 * time.Second)
				during = calls.Load()
				i.Resume()
				<-done
				i.Crash(host.ProcessCrash)
			})

			assert.Equal(t, during, int32(0), "the call waits for the end of the pause")
		})

		t.Run("calls f as a task once an advance passes d", func(t *testing.T) {
			t.Parallel()
			var during, after bool
			scheduled(t, targets, func(c *prop.Case, s *stateful.Scheduler, first bool) {
				h := host.New(c, "node-1", nil, s)
				_, env := h.Start()
				called := false
				env.Clock().AfterFunc(time.Second, func() { called = true })
				s.Advance(999 * time.Millisecond)
				before := called
				s.Advance(time.Millisecond)
				if first {
					during, after = before, called
				}
			})

			assert.Equal(t, []bool{during, after}, []bool{false, true}, "the call comes at one second")
		})
	})

	t.Run("Strobe", func(t *testing.T) {
		t.Parallel()

		t.Run("wakes a wait for the wall clock", func(t *testing.T) {
			t.Parallel()
			woke := wallWait(t, func(h *host.Host) { h.Strobe(2*time.Hour, time.Hour) }, func(wall time.Time) bool {
				return wall.After(midnight.Add(time.Hour))
			})

			assert.True(t, woke, "the strobe wakes the wait")
		})

		t.Run("fails the case for a period of zero or less", func(t *testing.T) {
			t.Parallel()
			got := failure(t, func(c *prop.Case) {
				h, _, _ := started(c)
				h.Strobe(time.Minute, 0)
			})

			assert.Equal(
				t,
				got.Contract,
				"host: Strobe(1m0s, 0s) of node-1 states no period",
				"the failure names the call",
			)
		})
	})

	t.Run("WallRead", func(t *testing.T) {
		t.Parallel()

		t.Run("reports whether an incarnation has read the wall clock", func(t *testing.T) {
			t.Parallel()
			var before, after bool
			inCase(t, targets, func(c *prop.Case, _ bool) {
				h, i, env := started(c)
				before = h.WallRead()
				env.Clock().Wall()
				i.Crash(host.ProcessCrash)
				h.ResetClock()
				after = h.WallRead()
			})

			assert.Equal(t, []bool{before, after}, []bool{false, true}, "the read lasts through a crash and a reset")
		})
	})
}
