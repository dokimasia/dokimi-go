// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package stub_test

import (
	"math/rand/v2"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.dokimi.dev/assert"

	"go.dokimi.dev/dokimi-go/stub"
)

// instance returns the instance of a fake that reports to tb, with each
// option applied to the stub of its method as a generated constructor applies
// it through the group of the double.
func instance(tb assert.TB, options ...func(stub.Configurable)) stub.Instance[call] {
	f := newFake(tb)
	for _, apply := range options {
		apply(f.on)
	}
	return stub.Instance[call]{Stub: f.on, Call: func() { _ = f.Get("k") }, Reset: f.on.Reset}
}

// doubleOf returns the constructors of a double whose options each reach the
// stub of its method. build builds each instance with its options.
func doubleOf(build func(tb assert.TB, options ...func(stub.Configurable)) stub.Instance[call]) stub.Double[call] {
	return stub.Double[call]{
		New: func(tb assert.TB) stub.Instance[call] { return build(tb) },
		WithClock: func(tb assert.TB, clk assert.Clock) stub.Instance[call] {
			return build(tb, func(c stub.Configurable) { c.SetClock(clk) })
		},
		WithLatency: func(tb assert.TB, d time.Duration, clk assert.Clock) stub.Instance[call] {
			return build(tb, func(c stub.Configurable) {
				c.SetClock(clk)
				c.SetLatency(d)
			})
		},
		WithRandSource: func(tb assert.TB, src rand.Source) stub.Instance[call] {
			return build(tb, func(c stub.Configurable) { c.SetRandSource(src) })
		},
		BenchMode: func(tb assert.TB) stub.Instance[call] {
			return build(tb, stub.Configurable.BenchMode)
		},
		Strict: func(tb assert.TB) stub.Instance[call] {
			return build(tb, stub.Configurable.Strict)
		},
	}
}

// DoubleBehaviour passes a double whose options each reach the stub of its
// method, and fails each check that a broken double breaks.
func TestDoubleBehaviour(t *testing.T) {
	t.Parallel()

	t.Run("DoubleBehaviour", func(t *testing.T) {
		t.Parallel()

		t.Run("passes a double whose options reach its methods", func(t *testing.T) {
			t.Parallel()

			stub.DoubleBehaviour(t, doubleOf(instance))
		})

		t.Run("runs each check as a parallel subtest", func(t *testing.T) {
			t.Parallel()

			var checks atomic.Int32
			counted := func(tb assert.TB, options ...func(stub.Configurable)) stub.Instance[call] {
				checks.Add(1)
				return instance(tb, options...)
			}
			stub.DoubleBehaviour(t, doubleOf(counted))
			assert.Equal(t, checks.Load(), int32(0), "no check starts before the test returns")
		})

		tests := []struct {
			name string
			give func(d *stub.Double[call])
			want []string
		}{
			{
				name: "fails the check of bench mode for a double that records in bench mode",
				give: func(d *stub.Double[call]) { d.BenchMode = d.New },
				want: []string{"stops_recording_in_bench_mode"},
			},
			{
				name: "fails the check of strict mode for a double that is lenient when built strict",
				give: func(d *stub.Double[call]) { d.Strict = d.New },
				want: []string{"refuses_an_unconfigured_call_when_built_strict"},
			},
			{
				name: "fails the check of the reset for a double whose reset keeps the log",
				give: func(d *stub.Double[call]) {
					d.New = func(tb assert.TB) stub.Instance[call] {
						i := instance(tb)
						i.Reset = func() {}
						return i
					}
				},
				want: []string{"clears_timestamps_on_reset"},
			},
			{
				name: "fails the checks of the log for a double that records no call",
				give: func(d *stub.Double[call]) {
					d.New = d.BenchMode
					d.WithClock = func(tb assert.TB, clk assert.Clock) stub.Instance[call] {
						return instance(tb, stub.Configurable.BenchMode, func(c stub.Configurable) { c.SetClock(clk) })
					}
				},
				want: []string{"clears_timestamps_on_reset", "records_a_call_only_once_its_latency_has_elapsed"},
			},
			{
				name: "fails the check of the source for a double that ignores the source",
				give: func(d *stub.Double[call]) {
					d.WithRandSource = func(tb assert.TB, _ rand.Source) stub.Instance[call] { return instance(tb) }
				},
				want: []string{"draws_probabilistic_faults_from_the_injected_source"},
			},
			{
				name: "fails the checks of the clock for a double that ignores the clock",
				give: func(d *stub.Double[call]) {
					d.WithClock = func(tb assert.TB, _ assert.Clock) stub.Instance[call] { return instance(tb) }
				},
				want: []string{
					"sleeps_latency_against_the_injected_clock",
					"stops_a_time-windowed_fault_once_its_window_closes",
				},
			},
			{
				name: "fails the check of the preset for a double whose preset ignores the clock",
				give: func(d *stub.Double[call]) {
					d.WithLatency = func(tb assert.TB, lat time.Duration, _ assert.Clock) stub.Instance[call] {
						return instance(tb, func(c stub.Configurable) { c.SetLatency(lat) })
					}
				},
				want: []string{"holds_the_WithLatency_preset_to_the_same_clock"},
			},
			{
				name: "fails the checks of the latency for a double that does not sleep",
				give: func(d *stub.Double[call]) {
					hasty := func(i stub.Instance[call]) stub.Instance[call] {
						i.Call = func() { i.Stub.Record(call{}) }
						return i
					}
					withClock, withLatency := d.WithClock, d.WithLatency
					d.WithClock = func(tb assert.TB, clk assert.Clock) stub.Instance[call] {
						return hasty(withClock(tb, clk))
					}
					d.WithLatency = func(tb assert.TB, lat time.Duration, clk assert.Clock) stub.Instance[call] {
						return hasty(withLatency(tb, lat, clk))
					}
				},
				want: []string{
					"sleeps_latency_against_the_injected_clock",
					"records_a_call_only_once_its_latency_has_elapsed",
					"holds_the_WithLatency_preset_to_the_same_clock",
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				if os.Getenv(suiteEnv) == t.Name() {
					d := doubleOf(instance)
					tt.give(&d)
					stub.DoubleBehaviour(t, d)
					return
				}
				assertFailedChecks(t, tt.want...)
			})
		}
	})
}
