# go.dokimi.dev/conform

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
