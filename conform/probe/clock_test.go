// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package probe_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/conform/probe"
	"go.dokimi.dev/spec"
)

// errExpired is the error of a read of an entry whose lifetime has passed.
var errExpired = errors.New("probe_test: the entry has expired")

// The durations of the specs of the clock. doubled is the time that the
// doubling steps of MovesWithTheClock add up to.
const (
	budget   = time.Second
	window   = time.Minute
	delay    = 10 * time.Second
	lifetime = time.Minute
	doubled  = 1<<53 - 1
)

// timer is the subject of the specs of the clock. It reads the time from the
// clock of its handle. broken names the one call that a planted defect
// breaks, and is empty for a correct timer. calls counts the calls of hour
// and runs. mu guards the fields that the goroutines of after write.
type timer struct {
	clock   assert.Clock
	broken  string
	written time.Time
	events  []time.Time
	calls   int

	mu      sync.Mutex
	ran     int
	expired bool
	stamp   time.Time
}

// timers returns a factory of timers on the clock of the handle, whose call
// broken breaks.
func timers(broken string) func(*conform.Handle[*timer]) *timer {
	return func(h *conform.Handle[*timer]) *timer { return &timer{clock: h.Clock(), broken: broken} }
}

// settle yields the processor many times. A goroutine that settles is
// runnable, so synctest.Wait waits for it, and a probe that does not wait
// finds the goroutine before its next step.
func settle() {
	for range 10_000 {
		runtime.Gosched()
	}
}

// after runs fn on a goroutine once the clock has passed d. The goroutine
// settles before it sleeps on the clock and again before it runs fn.
func (t *timer) after(d time.Duration, fn func()) {
	go func() {
		settle()
		t.clock.Sleep(d)
		settle()
		t.mu.Lock()
		defer t.mu.Unlock()
		fn()
	}()
}

// wait settles, sleeps the budget on the clock and settles again. A broken
// slow sleeps twice the budget.
func (t *timer) wait(context.Context) error {
	d := budget
	if t.broken == "slow" {
		d = 2 * budget
	}
	settle()
	t.clock.Sleep(d)
	settle()
	return nil
}

// record records an event at the current instant.
func (t *timer) record(context.Context) error {
	t.events = append(t.events, t.clock.Now())
	return nil
}

// count returns the number of events inside the window that ends now. A
// broken longer counts a window one nanosecond longer, and a broken
// uncounted fails.
func (t *timer) count(context.Context) (int, error) {
	if t.broken == "uncounted" {
		return 0, errOther
	}
	limit := window
	if t.broken == "longer" {
		limit += time.Nanosecond
	}
	n := 0
	for _, at := range t.events {
		if t.clock.Now().Sub(at) <= limit {
			n++
		}
	}
	return n, nil
}

// schedule registers a task that runs once delay has passed, and returns
// delay. A broken eager also runs the task at once, and a broken late runs it
// two nanoseconds after its instant.
func (t *timer) schedule(context.Context) (time.Duration, error) {
	at := delay
	switch t.broken {
	case "eager":
		t.mu.Lock()
		t.ran++
		t.mu.Unlock()
	case "late":
		at += 2 * time.Nanosecond
	}
	t.after(at, func() { t.ran++ })
	return delay, nil
}

// runs returns the number of runs of the tasks. A broken unready fails its
// first call.
func (t *timer) runs(context.Context) (int, error) {
	t.calls++
	if t.broken == "unready" && t.calls == 1 {
		return 0, errOther
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ran, nil
}

// arm sets the stamp to the current instant, and sets it again once doubled
// has passed. A broken frozen never sets it again.
func (t *timer) arm(context.Context) error {
	t.mu.Lock()
	t.stamp = t.clock.Now()
	t.mu.Unlock()
	if t.broken != "frozen" {
		t.after(doubled, func() { t.stamp = t.clock.Now() })
	}
	return nil
}

// stamped returns the stamp.
func (t *timer) stamped(context.Context) (time.Time, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stamp, nil
}

// phase returns the time since the write modulo doubled. It changes at the
// first step of MovesWithTheClock and returns to its first value at the last.
func (t *timer) phase(context.Context) (time.Duration, error) {
	return t.clock.Now().Sub(t.written) % doubled, nil
}

// hour returns the current instant truncated to the hour. A broken drift
// returns another instant on each call.
func (t *timer) hour(context.Context) (time.Time, error) {
	t.calls++
	if t.broken == "drift" {
		return t.clock.Now().Add(time.Duration(t.calls)), nil
	}
	return t.clock.Now().Truncate(time.Hour), nil
}

// write writes the entry at the current instant.
func (t *timer) write(context.Context) error {
	t.written = t.clock.Now()
	return nil
}

// store writes the entry and expires it once lifetime has passed. A broken
// eternal never expires it.
func (t *timer) store(context.Context) error {
	if t.broken != "eternal" {
		t.after(lifetime, func() { t.expired = true })
	}
	return nil
}

// read reads the entry, and returns errExpired once it has expired.
func (t *timer) read(context.Context) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.expired {
		return false, errExpired
	}
	return true, nil
}

// on returns the call fn of the timer of h.
func on[V any](h *conform.Handle[*timer], fn func(t *timer, ctx context.Context) (V, error)) probe.Call[V] {
	return func(ctx context.Context) (V, error) { return fn(h.Subject(), ctx) }
}

// doing returns the call fn of the timer of h as an Act.
func doing(h *conform.Handle[*timer], fn func(t *timer, ctx context.Context) error) probe.Act {
	return func(ctx context.Context) error { return fn(h.Subject(), ctx) }
}

func TestClock(t *testing.T) {
	t.Parallel()

	schedules := func(h *conform.Handle[*timer]) {
		probe.RunsOnSchedule(h, on(h, (*timer).schedule), on(h, (*timer).runs))
	}
	counts := func(h *conform.Handle[*timer]) {
		probe.CountsWithinWindow(h, doing(h, (*timer).record), on(h, (*timer).count), window)
	}
	tests := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*timer])
		giveBreak string
		want      spec.ReasonID
	}{
		{
			name: "ReturnsWithin", giveProbe: spec.ReturnsWithin, giveBreak: "slow", want: spec.OverranBudget,
			giveApply: func(h *conform.Handle[*timer]) { probe.ReturnsWithin(h, doing(h, (*timer).wait), budget) },
		},
		{
			name: "CountsWithinWindow", giveProbe: spec.CountsWithinWindow, giveBreak: "longer",
			want: spec.MisreadWindow, giveApply: counts,
		},
		{
			name: "RunsOnSchedule with an eager task", giveProbe: spec.RunsOnSchedule, giveBreak: "eager",
			want: spec.OffSchedule, giveApply: schedules,
		},
		{
			name: "RunsOnSchedule with a late task", giveProbe: spec.RunsOnSchedule, giveBreak: "late",
			want: spec.OffSchedule, giveApply: schedules,
		},
		{
			name: "MovesWithTheClock", giveProbe: spec.MovesWithTheClock, giveBreak: "frozen",
			want: spec.UnmovedByTheClock,
			giveApply: func(h *conform.Handle[*timer]) {
				probe.MovesWithTheClock(h, on(h, (*timer).stamped), doing(h, (*timer).arm))
			},
		},
		{
			name: "SteadyWhileTheClockStops", giveProbe: spec.SteadyWhileTheClockStops, giveBreak: "drift",
			want: spec.DriftedWithoutClock,
			giveApply: func(h *conform.Handle[*timer]) {
				probe.SteadyWhileTheClockStops(h, on(h, (*timer).hour), doing(h, (*timer).write))
			},
		},
		{
			name: "ExpiresAfter", giveProbe: spec.ExpiresAfter, giveBreak: "eternal", want: spec.NotExpired,
			giveApply: func(h *conform.Handle[*timer]) {
				probe.ExpiresAfter(h, on(h, (*timer).read), doing(h, (*timer).store), lifetime, errExpired)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			t.Run("passes a correct subject and fails a planted defect with its reason", func(t *testing.T) {
				t.Parallel()

				conform.Check[*timer]{
					ID: "Timer.Call/claim", Statement: "the probe holds", Claim: "claim",
					Probe: tt.giveProbe, Reason: tt.want, Apply: tt.giveApply, Defect: timers(tt.giveBreak),
					Around: synctest.Test,
				}.Run(t, timers(""))
			})
		})
	}

	failing := func(context.Context) error { return errOther }
	// drifting returns a call that returns another value at each call, and
	// fails its first fails calls.
	drifting := func(fails int) probe.Call[int] {
		n := 0
		return func(context.Context) (int, error) {
			n++
			if n <= fails {
				return n, errOther
			}
			return n, nil
		}
	}
	ends := []struct {
		name      string
		giveProbe spec.ProbeID
		giveApply func(h *conform.Handle[*timer])
	}{
		{
			name: "CountsWithinWindow passes a call that fails", giveProbe: spec.CountsWithinWindow,
			giveApply: func(h *conform.Handle[*timer]) {
				probe.CountsWithinWindow(h, failing, returning(1), window)
			},
		},
		{
			name: "RunsOnSchedule passes a registration that fails", giveProbe: spec.RunsOnSchedule,
			giveApply: func(h *conform.Handle[*timer]) {
				probe.RunsOnSchedule(h, refusing[time.Duration](), returning(1))
			},
		},
		{
			name: "MovesWithTheClock passes a put that fails", giveProbe: spec.MovesWithTheClock,
			giveApply: func(h *conform.Handle[*timer]) { probe.MovesWithTheClock(h, returning(1), failing) },
		},
		{
			name: "MovesWithTheClock passes a first call that fails", giveProbe: spec.MovesWithTheClock,
			giveApply: func(h *conform.Handle[*timer]) { probe.MovesWithTheClock(h, failsFirst(1), passes) },
		},
		{
			name: "MovesWithTheClock passes a value that returns to its first value", giveProbe: spec.MovesWithTheClock,
			giveApply: func(h *conform.Handle[*timer]) {
				probe.MovesWithTheClock(h, on(h, (*timer).phase), doing(h, (*timer).write))
			},
		},
		{
			name: "SteadyWhileTheClockStops passes a put that fails", giveProbe: spec.SteadyWhileTheClockStops,
			giveApply: func(h *conform.Handle[*timer]) { probe.SteadyWhileTheClockStops(h, drifting(0), failing) },
		},
		{
			name: "SteadyWhileTheClockStops passes a first call that fails", giveProbe: spec.SteadyWhileTheClockStops,
			giveApply: func(h *conform.Handle[*timer]) { probe.SteadyWhileTheClockStops(h, drifting(1), passes) },
		},
		{
			name: "ExpiresAfter passes a write that fails", giveProbe: spec.ExpiresAfter,
			giveApply: func(h *conform.Handle[*timer]) {
				probe.ExpiresAfter(h, returning(1), failing, lifetime, errExpired)
			},
		},
		{
			name: "ExpiresAfter passes a first read that fails", giveProbe: spec.ExpiresAfter,
			giveApply: func(h *conform.Handle[*timer]) {
				probe.ExpiresAfter(h, failsFirst(1), passes, lifetime, errExpired)
			},
		},
	}
	for _, tt := range ends {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conform.Check[*timer]{
				ID: "Timer.Call/claim", Statement: "the probe holds", Probe: tt.giveProbe, Apply: tt.giveApply,
				Around: synctest.Test,
			}.Run(t, timers(""))
		})
	}

	guards := []struct {
		name       string
		giveProbe  spec.ProbeID
		giveApply  func(h *conform.Handle[*timer])
		giveBreak  string
		giveReason spec.ReasonID
	}{
		{
			name:      "CountsWithinWindow fails the guard without a reason for a count that fails",
			giveProbe: spec.CountsWithinWindow, giveApply: counts, giveBreak: "uncounted",
			giveReason: spec.MisreadWindow,
		},
		{
			name:      "RunsOnSchedule fails the guard without a reason for a first count that fails",
			giveProbe: spec.RunsOnSchedule, giveApply: schedules, giveBreak: "unready",
			giveReason: spec.OffSchedule,
		},
	}
	for _, tt := range guards {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &onTest{Recorder: assert.NewRecorder(), t: t}
			conform.Check[*timer]{
				ID: "Timer.Call/claim", Statement: "the probe holds", Claim: "claim", Probe: tt.giveProbe,
				Reason: tt.giveReason, Apply: tt.giveApply, Defect: timers(tt.giveBreak), Around: synctest.Test,
			}.Run(r, timers(""))
			assert.Contains(t, r.Message(), "fails the check without a reason", "the guard sets no reason")
		})
	}

	t.Run("SteadyWhileTheClockStops fails the guard for a second call that fails", func(t *testing.T) {
		t.Parallel()

		r := assert.NewRecorder()
		conform.Check[*timer]{
			ID: "Timer.Call/claim", Statement: "the probe holds", Probe: spec.SteadyWhileTheClockStops,
			Apply: func(h *conform.Handle[*timer]) { probe.SteadyWhileTheClockStops(h, failsSecond(1), passes) },
		}.Run(r, timers(""))
		failures := r.Failures()
		assert.NotEmpty(t, failures, "the guard fails")
		assert.Equal(t, failures[0].Assertion, "err-absent", "the guard asserts the success of the call")
	})
}

// onTest is a recorder of the test t. Its method Unwrap returns t, so a check
// with Around runs inside the environment of t, and the recorder keeps what
// falsification reports on the seat.
type onTest struct {
	*assert.Recorder

	t *testing.T
}

// Unwrap returns the test of the recorder.
func (s *onTest) Unwrap() assert.TB { return s.t }
