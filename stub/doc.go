// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package stub is the runtime of the recording doubles that dokimi
// generates.
//
// A generated double of an interface has one stub for each method. The stub of
// a method embeds a [MethodStub], which records the calls of the method,
// injects faults, sleeps a latency, checks the expected count of calls and
// refuses an unconfigured call in strict mode. Each method of the double
// answers through [Answer]:
//
//	func (s *StoreStub) Get(ctx context.Context, id string) (Item, error) {
//		call := StoreGetCall{Ctx: ctx, ID: id}
//		r := stub.Answer(s.OnGet.MethodStub, &call, stub.Arms[StoreGetCall, StoreGetReturn]{
//			Invoke:   s.OnGet.invoke(ctx, id),
//			Fallback: s.OnGet.fallback,
//			Fault:    func(err error) StoreGetReturn { return StoreGetReturn{Err: err} },
//		})
//		return r.Item, r.Err
//	}
//
// A test configures each method through the double, and passes the double to
// the code under test:
//
//	store := NewStoreStub(t)
//	store.OnGet.Returns(item, nil)
//	store.OnClose.FaultsOnce(errBusy)
//
// A [Group] applies a setting of the whole double, such as a clock or strict
// mode, to the stub of each method. A [Recorder] keeps the log of calls with
// the instant of each, and an [OrderTracker] checks the order of the calls of
// two methods.
//
// [Behaviour] checks the contract of each method of a generated double, and
// [DoubleBehaviour] the contract of the double as a whole. A generated test
// runs both.
//
// # Dependency position
//
// The package imports go.dokimi.dev/assert, the package fault of this module
// and the Go stdlib, testing and testing/synctest among it.
package stub
