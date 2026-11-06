// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

package host

import "context"

// waiterKey is the key of the waiter of a host in the lifetime of each of its
// incarnations.
type waiterKey struct{}

// Park blocks until ctx ends, and returns the error of ctx. For a context that
// derives from the lifetime of an incarnation, under the scheduler of its host,
// it parks the running task through Scheduler.Wait, so the scheduler runs the
// other tasks. Call it from a task of that scheduler. Otherwise it waits for
// ctx.Done(), which is a durable wait in a testing/synctest bubble whose
// goroutine made ctx.
func Park(ctx context.Context) error {
	if w, found := ctx.Value(waiterKey{}).(*waiter); found && w.s != nil {
		w.s.Wait(func() bool { return ctx.Err() != nil })
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}
