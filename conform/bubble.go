// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package conform

import (
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// samplePeriod is the period of the samples of the watchdog of [Bubble]. It
// sets how soon a stall shows, and limits no run.
const samplePeriod = time.Second

// bubbleMark precedes the identity of the bubble of a goroutine in the header
// of its stack, as in [chan receive (durable), synctest bubble 3].
const bubbleMark = ", synctest bubble "

// stallLimit is the text of the report of a stall: the limit of
// testing/synctest, and the options of a subject that keep a world out of it.
const stallLimit = "A goroutine that waits for a mutex or a system call does not wait durably in a " +
	"testing/synctest bubble, so the bubble cannot advance its clock while the goroutine waits " +
	"(https://go.dev/issue/77687). Run the subject under the option conform.Tasks, or without the " +
	"option conform.Blocking so that its world strikes no blocking fault."

// bubbles contains the bubble of each run of Bubble that runs its function,
// by the identity of the bubble, for RecordStep. The identity of a bubble is
// unique in the process. The mutex guards the map.
var bubbles = struct {
	of map[string]*bubble

	sync.Mutex
}{of: map[string]*bubble{}}

// bubble is a bubble of Bubble: its identity and the count of the steps that
// its worlds recorded.
type bubble struct {
	id    string
	steps atomic.Uint64
}

// Bubble runs run in a testing/synctest bubble on t, as the field Around of a
// check of a world states it. A watchdog outside the bubble samples the
// goroutines of the bubble once a second, and a world counts its steps
// through [RecordStep]. A stall is two samples with the same stacks and the
// same count of steps in which no goroutine of the bubble can run and one or
// more goroutines wait for a mutex or a system call. The bubble cannot
// advance its clock past such a wait. The watchdog then writes a report of
// the stall to standard error and ends the test binary with the exit status
// 2. The report states the limit of testing/synctest, and lists the
// goroutines that do not wait durably and then the goroutines that wait
// durably. The header of each goroutine has the labels of its incarnation.
func Bubble(t *testing.T, run func(*testing.T)) {
	t.Helper()
	started := make(chan *bubble, 1)
	stop := make(chan struct{})
	go watch(started, stop)
	defer close(stop)
	synctest.Test(t, func(t *testing.T) {
		b := &bubble{id: bubbleID()}
		bubbles.Lock()
		bubbles.of[b.id] = b
		bubbles.Unlock()
		//dokimi:mutate-skip sbr-delete: an entry that remains costs memory alone, because no later bubble has its identity
		defer func() {
			bubbles.Lock()
			//dokimi:mutate-skip sbr-delete: an entry that remains costs memory alone, because no later bubble has its identity
			delete(bubbles.of, b.id)
			bubbles.Unlock()
		}()
		started <- b
		run(t)
	})
}

// RecordStep records a step of the world that runs in the bubble of the
// calling goroutine, for the watchdog of [Bubble]. A world calls it after each
// of its steps. A call outside a bubble of Bubble does nothing.
func RecordStep() {
	id := bubbleID()
	bubbles.Lock()
	b := bubbles.of[id]
	bubbles.Unlock()
	if b != nil {
		b.steps.Add(1)
	}
}

// bubbleID returns the identity of the bubble of the calling goroutine, from
// the header of its stack, and an empty identity outside a bubble.
func bubbleID() string {
	buf := make([]byte, 256)
	_, id, _ := strings.Cut(bracketOf(string(buf[:runtime.Stack(buf, false)])), bubbleMark)
	return id
}

// watch samples the goroutines of the bubble that started sends, once a period
// until stop is closed, and ends the test binary at a stall. A sample reads
// the count of the steps before the stacks, because the stacks stop the world.
// A step that the world takes after that stop then counts for the next
// sample.
func watch(started <-chan *bubble, stop <-chan struct{}) {
	b := <-started
	ticker := time.NewTicker(samplePeriod)
	var last string
	var steps uint64
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		now := b.steps.Load()
		stacks := goroutinesOf(b.id)
		sample := strings.Join(stacks, "\n\n")
		if stalled(stacks) && sample == last && now == steps {
			stall(stacks)
		}
		last, steps = sample, now
	}
}

// goroutinesOf returns the stacks of the goroutines of the bubble id, in the
// order of a stack dump of every goroutine.
func goroutinesOf(id string) []string {
	var dump strings.Builder
	// A write to a strings.Builder does not fail.
	_ = pprof.Lookup("goroutine").WriteTo(&dump, 2)
	var stacks []string
	for stack := range strings.SplitSeq(strings.TrimSuffix(dump.String(), "\n"), "\n\n") {
		if strings.HasSuffix(bracketOf(stack), bubbleMark+id) {
			stacks = append(stacks, stack)
		}
	}
	return stacks
}

// stalled reports whether no goroutine of stacks can run and one or more of
// them do not wait durably.
func stalled(stacks []string) bool {
	outside := false
	for _, stack := range stacks {
		if statusOf(stack) == "runnable" {
			return false
		}
		//dokimi:mutate-skip lcr-true: a sample without a runnable goroutine has a goroutine that does not wait durably, because the runtime readies the goroutine of synctest.Run once every other goroutine of the bubble waits durably
		outside = outside || !durable(stack)
	}
	return outside
}

// stall writes the report of the stall of the bubble whose goroutines have
// stacks to standard error, and ends the test binary with the exit status 2.
// The exit runs the exit hooks of the runtime, so the test binary writes its
// coverage.
func stall(stacks []string) {
	var outside, durably []string
	for _, stack := range stacks {
		if durable(stack) {
			durably = append(durably, stack)
		} else {
			outside = append(outside, stack)
		}
	}
	// A report that standard error does not take leaves the exit status.
	_, _ = os.Stderr.WriteString("conform: the bubble stalled. " + stallLimit +
		"\n\nThe goroutines that do not wait durably:\n\n" + strings.Join(outside, "\n\n") +
		"\n\nThe goroutines that wait durably:\n\n" + strings.Join(durably, "\n\n") + "\n")
	os.Exit(2)
}

// durable reports whether the goroutine of stack waits durably.
func durable(stack string) bool {
	return strings.HasSuffix(statusOf(stack), "(durable)")
}

// statusOf returns the status of the goroutine of stack, such as chan receive
// (durable).
func statusOf(stack string) string {
	status, _, _ := strings.Cut(bracketOf(stack), ",")
	return status
}

// bracketOf returns the text between the brackets of the header of stack, such
// as chan receive (durable), synctest bubble 3.
func bracketOf(stack string) string {
	_, rest, _ := strings.Cut(stack, "[")
	bracket, _, _ := strings.Cut(rest, "]")
	return bracket
}
