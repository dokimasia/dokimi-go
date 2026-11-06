# go.dokimi.dev/conform

## 0.14.0

### Minor Changes

- 984587a: Add `conform.Bubble` to run each case of a world check in a `testing/synctest` bubble whose watchdog ends the test binary at a stall.
- 984587a: Add `conform.Handle.FreshHost` to open the subject of an application over the environment of an incarnation on a planted defect.
- 984587a: Add the option `conform.Hosted`, the door `Doors.Host`, `Handle.Host` and `Handle.Hostable` to open a subject over the environment of an incarnation of a host.
- 984587a: Move `Injector`, `Dependency`, `DependencyCall`, `ErrInjected` and `FaultOf` to `go.dokimi.dev/host`, and keep each in `conform` under its name.
- 2831344: Add `probe.Pool`, a pool of the keys that the operations of a world pick.
- 984587a: Add `conform.RecordStep` to count the steps of a world for the watchdog of `conform.Bubble`.
- 984587a: Add `Handle.RecordWorld` to add the world of each case to the field `world` of the record of its check.
- 984587a: Add the options `conform.Tasks` and `conform.Blocking`, `Handle.Mode` and `Handle.Blocking` for the execution mode of the world of a subject.
- 2831344: Add the probes `probe.FaultyWorldHistory` and `probe.WorldRecovery` of the world of a subject, with `probe.World`, `probe.Operation`, `probe.WorldOptions` and `probe.Amnesiac`.

### Patch Changes

- 1cbb662: Require `go.dokimi.dev/assert` v0.18.0.
- Updated dependencies [e7c7771, 531155e, 1cbb662, e49374f]
  - go.dokimi.dev/medium@0.2.0
  - go.dokimi.dev/host@0.1.0
  - go.dokimi.dev/fault@0.2.3
  - go.dokimi.dev/spec@0.5.0

## 0.13.1

### Patch Changes

- a43530a: Require `go.dokimi.dev/assert` v0.17.0.
- Updated dependencies [a43530a]
  - go.dokimi.dev/fault@0.2.2
  - go.dokimi.dev/medium@0.1.1
  - go.dokimi.dev/spec@0.4.1

## 0.13.0

### Minor Changes

- 80acf19: Add `ExcuseSuite` to excuse every check of a suite with one option.
- 80acf19: Add `ID.Generated` to return the identity of the generated binding of a check.
- 80acf19: Take the probe, the reason code and the law of a derived check from its claim where the check does not state them.

## 0.12.1

### Patch Changes

- Updated dependencies [45feafe]
  - go.dokimi.dev/spec@0.4.0

## 0.12.0

### Minor Changes

- 267fa2c: Add the probe `FieldBesideError` of the claim `returns-on-error`.
- 267fa2c: Add the probes `SerializableHistory` and `SnapshotHistory` of the claims `serializable` and `snapshot-isolation`.
- 267fa2c: Leave a check whose inputs a builder of the subject returns out of the isolation of the planted defect of another callable.

### Patch Changes

- Updated dependencies [b9681ea]
  - go.dokimi.dev/spec@0.3.0

## 0.11.0

### Minor Changes

- 0261c82: Fail a check that panics on a subject or on its own planted defect, in place of ending the test binary.

## 0.10.0

### Minor Changes

- a8b6b1c: Add `conform.Durable` with `Handle.Open` and `Handle.FreshOpen`, which open each subject of an application over a new `medium.Memory`.
- 53fe828: Add the probe `probe.ConcurrentReadsAgree`, which checks each read of concurrent clients against the read alone.
- 53fe828: Add the probe `probe.ExclusiveUnderConcurrency`, which checks that concurrent calls with one key take the lease once.
- 53fe828: Add the probe `probe.LinearizableHistory`, which checks a history of concurrent clients against a reference.
- 90b15e0: Add the probe `probe.PinnedUnderConcurrentWrites`, which checks that reads with the same arguments return one value beside concurrent writes.
- 53fe828: Add the probe `probe.SharesOneComputation`, which checks that concurrent calls with one key record one computation in the dependencies.
- 90b15e0: Add the probe `probe.VersionsFollowTheCausalOrder`, which checks that the versions of reads and writes keep the causal order of their calls.
- 53fe828: Add `probe.Pull` and the seven probes of deliveries, iterations, totals and failed calls, such as `probe.AppendsArrive` and `probe.MovesKeepTheTotal`.
- a8b6b1c: Add `probe.Durable` with the four probes of a durable subject and the faulty media `probe.Unsynced` and `probe.Unfilled`.
- a8b6b1c: Add `Handle.AddPoints` and the field `points` of a check record, the points of a medium that the check enumerated.
- b7847bc: Add `Handle.Schedule` and `Handle.Yield`, which run the clients of a planted defect as tasks of a scheduler with the strategy Uniform.

### Patch Changes

- a8b6b1c: Run the concurrent sections of `probe.PinnedUnderConcurrentWrites` and `probe.VersionsFollowTheCausalOrder` with a mean of 16 steps.
- b7847bc: Fix `conform.Isolate` to leave out the check of linearizability for every planted defect, as it leaves out the check of agreement with a reference.
- Updated dependencies [1bc3e13, 317d5ff, 0a9d402, d2d7f23]
  - go.dokimi.dev/medium@0.1.0
  - go.dokimi.dev/spec@0.2.0
  - go.dokimi.dev/fault@0.2.1

## 0.9.0

### Minor Changes

- e708642: Add `Handle.Expect` and `Handle.Fresh`.
- e708642: Add `conform.Named`.
- 62fce73: Add the field `RunsUntilCancelled` of `conform.Call` for a call that runs until its context ends.
- 62fce73: Add the door `Faults` with the option `conform.Faultable`, and build each subject of a check of the machinery `call-that-fails-on-demand` through it.
- 62fce73: Add `conform.Supply`, `conform.Supplies`, `conform.Open` and the field `Values` of `conform.Doors` for the doors of a value.
- e708642: Add the fields `Claim`, `Probe`, `Reason`, `Law`, `Machineries`, `Shallow` and `Entails` of `conform.Check`.
- 62fce73: Add `Handle.Injector`, which returns the injector of the dependencies of the subjects of a handle.
- 62fce73: Add `conform.Injector`, `conform.Dependency`, `conform.FaultOf` and `conform.ErrInjected` to fail the calls of the dependencies of a subject on demand.
- e708642: Add the option `conform.Isolate` to isolate the planted defect of each derived check and to check the edges of its claim.
- 39612ce: Add `conform.Output`, `conform.OutputOf` and `conform.Complete` to record the calls of generated sequences in the history of a case.
- e708642: Add the package `conform/probe` with a Go function for each probe of the conformance spec.
- 19d25da: Add `Suite.Package`, `Suite.Generator`, `Suite.Refusals` and `Suite.Unclaimed`.
- 1e87d26: Add `probe.EitherOrderAgrees` and `probe.EitherGroupingAgrees` for the claims `commutative` and `associative`.
- 1e87d26: Add the seven probes of the machinery `call-that-fails-on-demand`, such as `probe.ReportsAFailedDependency` and `probe.ReleasesWhatItAcquires`.
- f0b64bd: Add the 18 probes of generated sequences, such as `probe.ResultsNeverFall` and `probe.SequencesAgreeWithTheReference`.
- 68d618c: Add the seven probes of the classifications without a claim, such as `probe.ReadsWhatWasStored` and `probe.BoundedByTheSize`.
- 19d25da: Call `Handle.Engage` in each probe of `conform/probe` where its vacuous condition stops being true.
- 62fce73: Excuse the generated check of each check that `conform.Excuse` excuses, such as `Store.Get/keeps-the-value#generated` for `Store.Get/keeps-the-value`.
- 19d25da: Add `Handle.Engage` and `Handle.ObserveCall`, and fail each check that passes without engaging its claim.
- e708642: Falsify each derived check in `conform.Run` on its planted defect by the reason of its claim.
- 39612ce: Leave a sequence check of another callable and the check of agreement with a reference out of the isolation of a planted defect.
- 19d25da: Log the census of each application of `conform.Run` with the counts of each result.
- 19d25da: Add the number of cases and the counterexample of each property check to the suite records of `conform`.
- 19d25da: Write the suite records of an application that problems stop, with the problems in the field `ended`.
- 62fce73: **Breaking:** Record the type of the value that the factory builds as the subject of a suite record, in place of the name of the factory.
- e708642: **Breaking:** Register each cleanup of `Handle.Cleanup` with the seat of the check through `assert.Cleanup`.
- 19d25da: Report a malformed `DOKIMI_ASSERT_PROP_SEED` as a problem of the application of a suite.
- 19d25da: Pass one seed of each application to every property run of `conform.Run` and `conform.RunBudgets`.
- 19d25da: Take a hexadecimal `DOKIMI_SEED` after the prefix `0x`.
- 1e87d26: Write before the first read in `probe.PinnedAcrossWrite` and `probe.ReadsNeverRegress`.
- 62fce73: Add `conform.Refusal.Gate`, and write the gate of each refusal into the suite record.
- 19d25da: Write the suite records of `conform.Run`, `conform.Falsify`, `conform.FalsifyAllocs` and `conform.RunBudgets` into the output of `go test`.

### Patch Changes

- 1e87d26: End `probe.RunsOnSchedule` without an engagement for a delay that is not positive.
- 19d25da: Fix `probe.NoElementTwice` and `probe.ReplaysInStampOrder` to end without an assertion after a write that fails.
- 62fce73: Exempt a check from the isolation of the generated binding of each check that it entails.
- 42d4231: Fix `probe.ReturnsWithin` to fail the check with the value of a panic of its call.
- 42d4231: Fix `conform.Isolate` to isolate a derived check against the other derived checks alone.
- 42d4231: Fix `conform.Isolate` to leave out the planted defects of the reasons `panicked` and `overran-budget`.
- 42d4231: Fix `probe.RefusedLeavesNothing` to pass a reader that fails, because it finds nothing.
- 19d25da: Fix the suite record of a property check to contain the failure of its smallest failing case.
- e708642: Require `go.dokimi.dev/assert` v0.12.0 and `go.dokimi.dev/spec`.
- 2a27078: Require `go.dokimi.dev/spec` at commit d177901 in place of the unpublished v0.1.0.
- 494250b: Fix the key of a check in the suite records to split each name into its words, as `spec.Key` does.
- 7c91c62: Fix `probe.RefusesWhatThePredicateRejects` to write a first value before it asks the predicate.
- Updated dependencies [f68d5b8, 4cf3c26, 9bfacac, e708642, 9421c56, 3da277c, 3e3aa34, 49e6f5f]
  - go.dokimi.dev/spec@0.1.0
  - go.dokimi.dev/fault@0.2.1

## 0.8.0

### Minor Changes

- 1e30595: Add the field `Check.Around` to run each run of a check inside an environment, such as a `testing/synctest` bubble.
- e7e97e0: Add the field `Call.Waits` and report a benchmark, a budget or a ceiling of such a call as a problem of the application.

### Patch Changes

- 2aad14c: Fix the verdict of the isolation to name the message alone for a check whose case fails outside an assertion.

## 0.7.0

### Minor Changes

- 3321c36: Add `conform.Crashable` and `Handle.Crash` to crash the medium of a subject during a call.
- 3321c36: Add `conform.Doors` and the field `Check.Doors` to supply the doors of a planted defect under falsification.
- 3321c36: Add the field `Check.Options` to state the options of the property run of a check in an application.

### Patch Changes

- 3321c36: Change the text of `conform.ErrOption` to "conform: the handle lacks the door".

## 0.6.0

### Minor Changes

- 77f316f: Add `conform.Bytes` and `conform.NewBytes`, a value of the interfaces of package io over bytes.
- 77f316f: Add `conform.SinkOf` and `Suite.Sinks` to give each send channel in the inputs of a suite a channel that takes every send while its test runs.
- 77f316f: Add `conform.Stream` and `conform.Closed` to generate the channels that a subject receives from.

## 0.5.0

### Minor Changes

- 77b7dce: Add `conform.Using` to state the generator of a type in the property runs, the fuzz targets and the benchmarks of an application.

### Patch Changes

- 9932003: Require go.dokimi.dev/assert v0.8.0.
- 9932003: Fix `Handle.Skipf` to skip the falsification of a check.

## 0.4.0

### Minor Changes

- 57c0f4c: **Breaking:** Remove `conform.ErrCleanup`, because `Handle.Cleanup` registers a function on every seat.

### Patch Changes

- 57c0f4c: Fix `Handle.Cleanup` and `Handle.TempDir` on a seat without `Cleanup`, such as the seats of `Falsify` and of the isolation of a planted defect.

## 0.3.0

### Minor Changes

- a5ab931: Add `conform.Check.Counted` to run a check that counts allocations in `RunBudgets` and `FalsifyAllocs` instead of `Run`, `Falsify` and `Fuzz`.
- e01c5e3: Add `conform.Handle.Cleanup`, which registers a function on the test or the case of a check, and the fault `ErrCleanup`.
- 9420acf: Add `conform.Handle.Skipf`, which skips the test of a check or the property run of its case, and the fault `ErrSkip`.
- 5972ce2: Add `conform.Handle.TempDir`, which returns a new directory of a check or a case that the run removes.

## 0.2.1

### Patch Changes

- 8936ceb: Fix `conform.BenchParallel` to draw the calls of each goroutine on its own, so no two goroutines pass one input to the subject.

## 0.2.0

### Minor Changes

- ba3cddf: Add `conform.B`, the benchmark of an application, which `*testing.B` implements.
- ba3cddf: Add `conform.Bench`, which measures a callable of a suite under the ceilings of its budget.
- ba3cddf: Add `conform.BenchParallel`, which measures a callable that admits concurrent calls on GOMAXPROCS goroutines at once.
- 28447ae: Add `conform.Budget.Warmup` for the iterations that a benchmark of a callable runs before it measures.
- ba3cddf: Falsify the byte, p99 and mean ceilings of each budget in `conform.Falsify`.
- ba3cddf: Report a check that has the identity of a ceiling of a budget as a problem of the application.

### Patch Changes

- ba3cddf: Fix the property runs of `conform` to leave the slice `Suite.Using` unchanged.

## 0.1.0

### Minor Changes

- 7cb229f: Add `conform.Suite.Calls`, `conform.Call` and `conform.Budget` for the call generator and the budget of each callable of a suite.
- 7cb229f: Add the option `conform.Cases` for the number of cases of each property run of an application.
- 7cb229f: Add the option `conform.Ceilings` for the budget of a callable in one application.
- 7cb229f: Add `conform.Check.Property` for a check that runs once for each case of the property engine, on a handle of its own.
- 7cb229f: Add `conform.ErrConfig`, the fault of `conform.Check.Run` under an invalid setting such as a `DOKIMI_SEED` that is not a number.
- 7cb229f: Add `conform.ErrGenerated`, the error of a generated function or double whose declaration states no error.
- f8e092c: Add `conform.Falsify`, which applies each check of a suite to its planted defect.
- 7cb229f: Add `conform.FalsifyAllocs`, which applies each allocation ceiling of a suite to its planted defect.
- 7cb229f: Add `conform.Fuzz`, which registers the fuzz target of the property checks of a callable.
- 7cb229f: Add `conform.Handle.Rand`, which returns the random source of the case of a property check or of the seed of the run.
- a44b5cb: Add `conform.Handle.Unwrap`, which returns the seat of the check.
- 7cb229f: Add `conform.RunBudgets`, which applies the allocation ceilings of a suite to a fresh subject in each case.
- 7cb229f: Add `conform.Suite.Using` for the overrides of the generators that each property run of a suite takes.
- 81a0a90: Add the package `conform` with the runtime of the conformance suites that dokimi generates.
- 7cb229f: End a check that calls a door of `conform.Handle` without its option with a fault that wraps the new `conform.ErrOption`.
- 7cb229f: End a check that advances a clock that no subject read with a fault that wraps the new `conform.ErrClock`.

### Patch Changes

- 7cb229f: Fix `conform.Falsify` to fail a planted defect that fails another check of its suite.
