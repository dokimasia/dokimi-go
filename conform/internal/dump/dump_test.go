// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package dump_test

import (
	"strings"
	"testing"

	"go.dokimi.dev/assert/expect"

	"go.dokimi.dev/conform/internal/dump"
)

// The stacks of the goroutines of the specs, in the form of the goroutine
// profile at debug level 2.
const (
	mutexWait = "goroutine 7 [sync.Mutex.Lock, synctest bubble 3]:\n" +
		"sync.(*Mutex).Lock(...)\n\t/go/src/sync/mutex.go:46"
	durableWait = "goroutine 8 [chan receive (durable), synctest bubble 3]:\n" +
		"example.com/shop.wait(0xc000010000)\n\t/shop/wait.go:12 +0x25"
	runnable = "goroutine 9 [runnable, synctest bubble 3]:\n" +
		"example.com/shop.spin()\n\t/shop/spin.go:8 +0x11"
	profileWait = "goroutine 10 [semacquire, synctest bubble 3]:\n" +
		"runtime/pprof.writeRuntimeProfile({0x1, 0x2}, 0x1, {0x3, 0x9}, 0x4)\n" +
		"\t/go/src/runtime/pprof/pprof.go:848 +0xb1"
	collectionWait = "goroutine 11 [semacquire, 2 minutes, synctest bubble 3]:\n" +
		"runtime.GC()\n\t/go/src/runtime/mgc.go:501 +0x45"
	fileWait = "goroutine 12 [semacquire, synctest bubble 3]:\n" +
		"internal/poll.runtime_Semacquire(0xc000020000)\n\t/go/src/runtime/sema.go:76 +0x25"
	profilerChannel = "goroutine 13 [chan receive, synctest bubble 3]:\n" +
		"runtime/pprof.StopCPUProfile()\n\t/go/src/runtime/pprof/pprof.go:912 +0x8a"
	otherBubble = "goroutine 14 [chan receive (durable), synctest bubble 4]:\n" +
		"example.com/shop.wait(0xc000030000)\n\t/shop/wait.go:12 +0x25"
	unbubbled = "goroutine 1 [chan receive]:\nmain.main()\n\t/shop/main.go:5 +0x1d"
)

func TestDump(t *testing.T) {
	t.Parallel()

	t.Run("Stacks", func(t *testing.T) {
		t.Parallel()

		t.Run("returns the stacks of the goroutines of one bubble in the order of the dump", func(t *testing.T) {
			t.Parallel()

			text := strings.Join([]string{unbubbled, durableWait, otherBubble, runnable}, "\n\n") + "\n"
			expect.Equal(t, dump.Stacks(text, "3"), []string{durableWait, runnable},
				"the stacks of bubble 3 are the wait and the goroutine that can run")
		})
	})

	t.Run("Bubble", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			giveStack string
			want      string
		}{
			{name: "returns the bubble of a goroutine", giveStack: durableWait, want: "3"},
			{
				name: "returns the bubble of a goroutine after the minutes of its wait", giveStack: collectionWait,
				want: "3",
			},
			{name: "returns no bubble for a goroutine outside a bubble", giveStack: unbubbled, want: ""},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				expect.Equal(t, dump.Bubble(tt.giveStack), tt.want, "the bubble is the last part of the header")
			})
		}
	})

	t.Run("Stalled", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			giveStacks []string
			want       bool
		}{
			{
				name:       "reports a bubble whose goroutine waits for a mutex",
				giveStacks: []string{mutexWait, durableWait}, want: true,
			},
			{
				name:       "reports a bubble whose goroutine waits for a semaphore outside the runtime",
				giveStacks: []string{fileWait, durableWait}, want: true,
			},
			{
				name:       "reports a bubble whose goroutine waits in the profiler for a channel",
				giveStacks: []string{profilerChannel, durableWait}, want: true,
			},
			{name: "leaves a bubble whose goroutines wait durably", giveStacks: []string{durableWait}, want: false},
			{
				name: "leaves a bubble with a goroutine that can run", giveStacks: []string{mutexWait, runnable},
				want: false,
			},
			{
				name:       "leaves a bubble whose goroutine waits for a semaphore of the profiler",
				giveStacks: []string{profileWait, durableWait}, want: false,
			},
			{
				name:       "leaves a bubble whose goroutine waits for a semaphore of the runtime",
				giveStacks: []string{mutexWait, collectionWait}, want: false,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				expect.Equal(t, dump.Stalled(tt.giveStacks), tt.want,
					"a bubble stalls when no goroutine can run and a wait of the program is not durable")
			})
		}
	})

	t.Run("Durable", func(t *testing.T) {
		t.Parallel()

		expect.True(t, dump.Durable(durableWait), "a receive from a channel of the bubble waits durably")
		expect.False(t, dump.Durable(mutexWait), "a wait for a mutex does not wait durably")
	})
}
