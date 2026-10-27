<!--
  ~ Copyright Dokimasia B.V. 2026
  ~ SPDX-License-Identifier: MIT
-->

# dokimi-go

[![CI](https://github.com/dokimasia/dokimi-go/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/dokimasia/dokimi-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/go.dokimi.dev/conform.svg)](https://pkg.go.dev/go.dokimi.dev/conform)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

The Go code that [dokimi](https://github.com/dokimasia/dokimi) generates calls the modules of
dokimi-go. A generated suite runs its checks through conform, and a generated double records its
calls through stub.

| Module | Contents |
|---|---|
| [`go.dokimi.dev/conform`](https://pkg.go.dev/go.dokimi.dev/conform) | The runtime of the conformance suites: the checks and their handle, the applications, the falsification of each check on its planted defect, the budgets, the fuzz targets and the benchmarks |
| [`go.dokimi.dev/stub`](https://pkg.go.dev/go.dokimi.dev/stub) | The runtime of the doubles: the record of each call, the order of results, faults, latency, strict mode and the expected counts of calls |
| [`go.dokimi.dev/fault`](https://pkg.go.dev/go.dokimi.dev/fault) | The strategies that select the calls of a double that fail |

The tags of the releases of each module start with the directory of the module, such as
`conform/v0.7.0`.

## Install

The modules need Go 1.27.2 or later. After `dokimi run`, run `go mod tidy`, which adds each module
that the generated code imports. To add a module yourself, run `go get`:

```sh
go get go.dokimi.dev/conform@latest
```

## Apply a suite

The function `Run<X>Suite` of a generated suite applies each check of the suite to a fresh subject
that your factory builds:

```go
func TestStore(t *testing.T) {
	t.Parallel()

	storetest.RunStoreSuite(t, memory.New)
}
```

The options of an application change what it runs:

| Option | Effect |
|---|---|
| `conform.With(checks...)` | Adds checks of your own. The application applies each of them to its planted defect before it applies the check to the subject |
| `conform.Excuse(id, reason)` | Excuses a check that the subject does not pass |
| `conform.Cases(n)` | Sets the number of cases of each property run |
| `conform.Using[S](g)` | Adds the generator of a type, such as a value that only a registry builds |
| `conform.Reference`, `conform.Second`, `conform.Restartable` and `conform.Crashable` | Supply the reference implementation, the second subject, the restart and the crash that a check needs and that only you can build |

A check fails the test of its suite when it passes on its planted defect. It has to fail there at
the assertion whose message is its statement.

## Configure a double

A generated double records each call of each method and returns the results that you configure:

```go
store := storetest.NewStoreStub(t)
store.OnGet.Returns("v", nil)
store.OnPut.FaultsOnce(errBusy)
```

The stub of a method also fails calls with `Faults`, `FaultsWhen`, `FaultsWithProbability`,
`FaultsUntil` and `FaultsFor`. Each of these methods arms a strategy of the package `fault`, such
as `fault.NewCountedFault` or `fault.NewProbabilityFault`, and `fault.And` and `fault.Or` combine
strategies. A probabilistic fault takes its random numbers from a source that the test passes, so
a seeded run repeats its faults.

## Compatibility

Each generated suite refers to the function `conform.CompatV1`. A major version of conform renames
that function. A suite that dokimi generated for another major version then fails to compile, and
the error contains the name of the function.

## Development

The repository uses [ergon](https://github.com/dokimasia/ergon) for its checks and its releases.
`go.work` lists the three modules. [CONTRIBUTING.md](CONTRIBUTING.md) describes how to propose a
change. The project follows the [Contributor Covenant 2.1](CODE_OF_CONDUCT.md).

```sh
make check   # the gate: lint, tests, race detector and vulnerability scan of every module
make test    # the tests of every module
make help    # every target
```

## Security

Report a vulnerability by email to security@dokimi.dev, or through a private security advisory of
the repository. Do not open a public issue. [SECURITY.md](SECURITY.md) describes what a report
contains.

## License

Dokimasia B.V. licenses dokimi-go under the MIT license. See [LICENSE](LICENSE).
