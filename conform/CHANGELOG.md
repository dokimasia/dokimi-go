# go.dokimi.dev/conform

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
