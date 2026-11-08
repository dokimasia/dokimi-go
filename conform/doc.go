// Copyright Dokimasia B.V. 2026
// SPDX-License-Identifier: MIT

// Package conform is the runtime of the conformance suites that dokimi
// generates.
//
// A [Suite] contains the checks of one declaration. A [Check] has an
// identity, a statement, a function that returns its planted defect, and a
// function that applies the check through its [Handle]. A check with Apply
// runs once. A check with Property runs under the property engine of the
// assert library, with a handle and a fresh subject for each case. [Run]
// applies every check of a suite to a subject of the consumer. [Falsify]
// applies every check to its planted defect, and every other check of the
// suite to the same defect. Each check runs as a test of its own, named by
// its identity. The field Around of a [Check] runs each run of the check
// inside an environment, such as a testing/synctest bubble.
//
// A generated suite declares the application of its declaration, and a
// consumer adds checks of its own through [With]:
//
//	func TestInMemory(t *testing.T) {
//		t.Parallel()
//
//		storetest.RunStoreSuite(t, storetest.NewInMemory, conform.With(keepsTheValue()))
//	}
//
// The options of an application also excuse a check through [Excuse], excuse
// every check of the suite through [ExcuseSuite], set the number of cases of
// each property run through [Cases], state the generator of a type through
// [Using], and supply the machinery that only the consumer can build through
// [Reference], [Second], [Restartable], [Crashable], [Durable],
// [Faultable] and [Supplies]. A subject of [Faultable] asks an [Injector] at
// each call of a dependency, through [FaultOf] for a double, so a probe can
// fail the calls of the dependencies and read the calls that the subject
// made. A subject of [Durable] writes through a medium.FS, and
// [Handle.Open] opens it over a medium.Memory that a probe crashes, tears,
// corrupts and fills. [Open] returns the value of a door of [Supplies], such
// as the client of the backend of a store, for a subject. An
// application falsifies each check that [With] adds before it applies the
// check to the subject. The check must fail on its planted defect at the
// assertion whose message is its statement. The field Doors of a [Check]
// supplies the same machinery to its planted defect, and its field Options
// states the options of its property run in an application.
//
// A world check runs its subject on a [host.Host]. The option
// [Hosted] states the door that opens a subject over the environment of an
// incarnation of the host, and [Handle.Host] opens it. The option [Tasks]
// states that a subject blocks only through its host, so its world runs under
// the task scheduler of the case. The option [Blocking] states that no
// goroutine of a subject waits durably while it has locked a mutex that
// another goroutine waits for. Its world then strikes the blocking faults in a
// testing/synctest bubble too. [Bubble] runs each case of a world check in
// such a bubble, and its watchdog ends the test binary at a stall. A world
// counts its steps for the watchdog through [RecordStep].
// [Handle.RecordWorld] adds the record of the world of a case to the record of
// its check.
//
// The calls of a suite contain the call generator and the budget of each
// callable. [RunBudgets] applies the allocation ceilings of the budgets to a
// subject, and [FalsifyAllocs] applies each of them to its planted defect.
// Both also run each check that counts allocations, which [Check.Counted]
// marks, in a test that does not run in parallel. [Run], [Falsify] and [Fuzz]
// leave such a check out.
// [Bench] measures a callable of a subject under the ceilings of its budget.
// [BenchParallel] measures a callable that admits concurrent calls on
// GOMAXPROCS goroutines at once. [Falsify] applies each ceiling of bytes and
// latency to its planted defect. The option [Ceilings] states a budget for
// one application. A call that waits for another call, which the field Waits
// of a [Call] marks, has no benchmark and no budget, because both repeat its
// calls on one subject. A call that runs until its context ends, which the
// field RunsUntilCancelled marks, has neither, because both call it with a
// context that does not end. [Fuzz] registers the fuzz target of the property
// checks of one callable.
//
// Each application writes its suite records into the output of its test,
// which go test -json reports as attribute events under the key
// dokimi.suite.<n>. [Run] writes the record of the falsification and the
// record of the subject, [RunBudgets] the record of the subject, and
// [Falsify] and [FalsifyAllocs] the record of the falsification. A record
// contains the suite, the seed of the application, the result of each check,
// and the refusals and the unclaimed classifications of the suite. The record
// of a property check also contains the number of its valid cases, and the
// counterexample and the failure of its smallest failing case. The record of a
// check that enumerates the points of a medium, such as its crash points,
// contains their number. The records of
// an application that problems stop have no check, and their field ended
// lists the problems.
//
// A generated suite states the values of the channels and of the interfaces
// of package io in its inputs. [Stream] generates a channel that a subject
// receives from, and [Closed] is the end value of such a channel. [SinkOf]
// returns the sink of a send channel type, the one channel of the type that
// takes every send while the test of an entry point runs. The field Sinks of
// a [Suite] lists them. [NewBytes] returns a [Bytes], a value of the
// interfaces of io over bytes.
//
// An action of a generated sequence records each call in the history of its
// case through [Complete]. A call that returned an error has an [Output]
// without values. A partner or a reference that a probe applies returns the
// Output that [OutputOf] builds.
//
// # Dependency position
//
// The package imports go.dokimi.dev/assert, go.dokimi.dev/assert/bench,
// go.dokimi.dev/assert/history, go.dokimi.dev/assert/prop,
// go.dokimi.dev/assert/stateful, go.dokimi.dev/conform/internal/dump,
// go.dokimi.dev/fault, go.dokimi.dev/host, go.dokimi.dev/medium,
// go.dokimi.dev/spec and the Go stdlib, testing among it.
package conform
