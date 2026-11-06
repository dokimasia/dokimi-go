// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform_test

import (
	"os"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"go.dokimi.dev/assert"
	"go.dokimi.dev/assert/expect"
	"go.dokimi.dev/assert/prop"

	"go.dokimi.dev/conform"
	"go.dokimi.dev/host"
)

func TestBubble(t *testing.T) {
	t.Parallel()

	t.Run("Bubble", func(t *testing.T) {
		t.Parallel()

		t.Run("runs the function on the clock of a bubble", func(t *testing.T) {
			t.Parallel()

			var slept time.Duration
			conform.Bubble(t, func(*testing.T) {
				start := time.Now()
				time.Sleep(time.Hour)
				slept = time.Since(start)
			})

			assert.Equal(t, slept, time.Hour, "the clock of the bubble advances by the hour of the sleep at once")
		})

		t.Run("ends the test binary at a stall while another bubble runs", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				t.Run("stalls", func(t *testing.T) {
					t.Parallel()
					var mu sync.Mutex
					mu.Lock()
					conform.Bubble(t, func(*testing.T) {
						durably := make(chan struct{})
						go func() { <-durably }()
						mu.Lock()
					})
				})
				t.Run("runs", func(t *testing.T) {
					t.Parallel()
					conform.Bubble(t, func(*testing.T) {
						for {
							runtime.Gosched()
						}
					})
				})
				return
			}

			assert.Contains(t, childOutput(t),
				"conform: the bubble stalled. A goroutine that waits for a mutex or a system call",
				"the watchdog reports the stall before it ends the test binary")
		})

		t.Run("lists the goroutines that do not wait durably first", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				var mu sync.Mutex
				mu.Lock()
				conform.Bubble(t, func(*testing.T) { mu.Lock() })
				return
			}

			expect.ContainsInOrder(t, childOutput(t), []string{
				"The goroutines that do not wait durably:",
				"[sync.Mutex.Lock, synctest bubble ",
				"The goroutines that wait durably:",
				"[synctest.Run (durable), synctest bubble ",
			}, "the panic lists the goroutine that waits for the mutex in the first list")
		})

		t.Run("lists the labels of an incarnation in the header of each of its goroutines", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				var mu sync.Mutex
				mu.Lock()
				prop.ForAll(t, "the goroutine of an incarnation waits for a mutex", func(c *prop.Case) {
					i, _ := host.New(c, "node-1", nil, nil).Start()
					waits := make(chan struct{})
					i.Go(func() {
						mu.Lock()
						close(waits)
					})
					<-waits
				}, prop.Around(conform.Bubble), prop.Cases(1))
				return
			}

			assert.Contains(t, childOutput(t),
				`] {dokimi.host: "node-1", dokimi.incarnation: 1, dokimi.run: `,
				"the header of the goroutine of the incarnation has its labels")
		})

		t.Run("does not end the test binary while a goroutine of the bubble can run", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				samples := make(chan struct{}, 3)
				go eachSample(3, func() { samples <- struct{}{} })
				var mu sync.Mutex
				conform.Bubble(t, func(*testing.T) {
					mu.Lock()
					waits := make(chan struct{})
					go func() {
						mu.Lock()
						close(waits)
					}()
					for len(samples) < 3 {
						runtime.Gosched()
					}
					mu.Unlock()
					<-waits
				})
				return
			}

			assert.Contains(t, childOutputWith(t, "GOMAXPROCS=1"), "--- PASS: "+t.Name(),
				"the bubble ends after three samples of a goroutine that yields")
		})

		t.Run("does not end the test binary for a wait whose stack changes between two samples", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				outside := make(chan struct{})
				go eachSample(3, func() { outside <- struct{}{} })
				conform.Bubble(t, func(*testing.T) {
					<-outside
					<-outside
					<-outside
				})
				return
			}

			assert.Contains(t, childOutput(t), "--- PASS: "+t.Name(),
				"the bubble ends after three samples of a wait on a channel outside the bubble")
		})

		t.Run("ends its watchdog once the function returns", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				conform.Bubble(t, func(*testing.T) {})
				for {
					var dump strings.Builder
					assert.NoError(t, pprof.Lookup("goroutine").WriteTo(&dump, 2),
						"the profile writes the stacks of every goroutine")
					if !strings.Contains(dump.String(), "created by go.dokimi.dev/conform.Bubble in goroutine ") {
						return
					}
					runtime.Gosched()
				}
			}

			assert.Contains(t, childOutput(t), "--- PASS: "+t.Name(), "the goroutine of the watchdog ends")
		})
	})

	t.Run("RecordStep", func(t *testing.T) {
		t.Parallel()

		t.Run("keeps the watchdog from ending the test binary while a world records its steps", func(t *testing.T) {
			t.Parallel()

			if os.Getenv(childEnv) == t.Name() {
				outside := make(chan struct{})
				go eachSample(3, func() { outside <- struct{}{} })
				conform.Bubble(t, func(*testing.T) {
					for range 3 {
						<-outside
						conform.RecordStep()
					}
				})
				return
			}

			assert.Contains(t, childOutput(t), "--- PASS: "+t.Name(),
				"the bubble ends after three samples of the same stacks with a step between each two")
		})

		t.Run("does nothing outside a bubble of Bubble", func(t *testing.T) {
			t.Parallel()

			assert.NotPanics(t, conform.RecordStep, "a step outside a bubble of Bubble has no world")
		})
	})
}

// eachSample calls f after each of the next n stops of the world of the
// process. In a child process of these specs, the samples of a watchdog stop
// the world and nothing else does.
func eachSample(n int, f func()) {
	seen := stops()
	for range n {
		for stops() == seen {
			runtime.Gosched()
		}
		seen = stops()
		f()
	}
}

// stops returns the number of the stops of the world of the process that no
// garbage collection made.
func stops() uint64 {
	sample := []metrics.Sample{{Name: "/sched/pauses/total/other:seconds"}}
	metrics.Read(sample)
	var n uint64
	for _, count := range sample[0].Value.Float64Histogram().Counts {
		n += count
	}
	return n
}
