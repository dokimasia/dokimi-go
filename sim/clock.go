// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package sim

import "time"

// Start is the time at which every clock of a world starts.
var Start = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

// The bounds of the offset of the drift of a clock from the run, per mille: a
// clock runs between half and twice as fast as the run.
const (
	minDrift = -500
	maxDrift = 1000
)

// perMille is the rate of a clock without drift.
const perMille = 1000

// Clock is the virtual clock of one host. It advances when the run advances
// time, at the rate of its drift. The faults of a clock move the time that
// Now returns by a jump or by the offset of a strobe. They do not move the
// timers of the processes of the host, which wait for the clock to advance,
// as the timers on the monotonic clock of an operating system do.
type Clock struct {
	elapsed time.Duration
	rate    int64
	jumps   time.Duration
	strobe  time.Duration
}

// newClock returns a clock at Start whose drift is offset per mille.
func newClock(offset int) *Clock {
	return &Clock{rate: perMille + int64(offset)}
}

// Now returns the time of the clock: Start, the time of the run that the
// clock has advanced through times its drift, its jumps and the offset of
// its strobe.
func (c *Clock) Now() time.Time { return Start.Add(c.advanced() + c.jumps + c.strobe) }

// advance advances the clock through the time d of the run.
func (c *Clock) advance(d time.Duration) { c.elapsed += d }

// advanced returns the time by which the clock has advanced: the time of the
// run that it has advanced through times its drift, without its jumps and
// the offset of its strobe.
func (c *Clock) advanced() time.Duration { return c.scaled(c.elapsed) }

// scaled returns the time of the clock after the time d of the run.
func (c *Clock) scaled(d time.Duration) time.Duration {
	n := int64(d)
	return time.Duration(n/perMille*c.rate + n%perMille*c.rate/perMille)
}

// runTime returns the shortest time of the run after which the clock has
// advanced by at least d from its time now, and 0 for a clock that is there.
func (c *Clock) runTime(d time.Duration) time.Duration {
	target := int64(c.advanced() + d)
	run := time.Duration(target/c.rate*perMille + (target%c.rate*perMille+c.rate-1)/c.rate)
	return max(run-c.elapsed, 0)
}
