// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub

import (
	"math/rand/v2"
	"time"

	"go.dokimi.dev/assert"
)

// Configurable is the part of the stub of a method that does not depend on
// the signature of the method. A [MethodStub] of any type of call satisfies
// it, so a [Group] applies a setting to every method of a double.
type Configurable interface {
	// Strict turns strict mode on.
	Strict()
	// Reset clears the recorded calls, the counters of the fault and the
	// expected counts, and keeps the configuration of the answers.
	Reset()
	// Verify checks the expected counts of calls.
	Verify()
	// BenchMode turns recording off.
	BenchMode()
	// SetClock sets the clock of the latency, of the windows of faults and of
	// the instants of the recorded calls.
	SetClock(clk assert.Clock)
	// SetLatency sets the latency of each call.
	SetLatency(d time.Duration)
	// SetRandSource sets the source of the random numbers of faults.
	SetRandSource(src rand.Source)
}

// SetClock sets the clock as [MethodStub.WithClock] does, without the return
// value.
func (s *MethodStub[C]) SetClock(clk assert.Clock) { s.WithClock(clk) }

// SetRandSource sets the source as [MethodStub.WithRandSource] does, without
// the return value.
func (s *MethodStub[C]) SetRandSource(src rand.Source) { s.WithRandSource(src) }
