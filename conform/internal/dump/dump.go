// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package dump

import "strings"

// bubbleMark precedes the identity of the bubble of a goroutine in the header
// of its stack, as in [chan receive (durable), synctest bubble 3]. The runtime
// writes the bubble as the last part of the header.
const bubbleMark = ", synctest bubble "

// Stacks returns the stacks of the goroutines of the bubble id in the dump, in
// the order of the dump.
func Stacks(dump, id string) []string {
	var stacks []string
	for stack := range strings.SplitSeq(strings.TrimSuffix(dump, "\n"), "\n\n") {
		if strings.HasSuffix(bracketOf(stack), bubbleMark+id) {
			stacks = append(stacks, stack)
		}
	}
	return stacks
}

// Bubble returns the identity of the bubble of the goroutine of stack, from
// the header of the stack, and an empty identity outside a bubble.
func Bubble(stack string) string {
	_, id, _ := strings.Cut(bracketOf(stack), bubbleMark)
	return id
}

// Stalled reports whether no goroutine of stacks can run and one or more of
// them do not wait durably. A goroutine that waits for a semaphore of the
// runtime can run once the runtime releases the semaphore, so it counts as a
// goroutine that can run.
func Stalled(stacks []string) bool {
	outside := false
	for _, stack := range stacks {
		if statusOf(stack) == "runnable" || waitsForRuntime(stack) {
			return false
		}
		outside = outside || !Durable(stack)
	}
	return outside
}

// Durable reports whether the goroutine of stack waits durably.
func Durable(stack string) bool {
	return strings.HasSuffix(statusOf(stack), "(durable)")
}

// waitsForRuntime reports whether the goroutine of stack waits for a
// semaphore of the runtime in a function of the runtime or of a package under
// it, such as the goroutine profile of runtime/pprof. The runtime releases
// such a semaphore without the clock of the bubble.
func waitsForRuntime(stack string) bool {
	_, frames, _ := strings.Cut(stack, "\n")
	return statusOf(stack) == "semacquire" &&
		(strings.HasPrefix(frames, "runtime.") || strings.HasPrefix(frames, "runtime/"))
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
