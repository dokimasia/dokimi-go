<!--
  ~ Copyright Dokimasia B.V. 2026
  ~ SPDX-License-Identifier: MIT
-->

# dokimi-go

The runtime of the code that dokimi generates for Go. Each module contains one concern of the
runtime, and the code that dokimi generates imports it:

| Module | Contents |
|---|---|
| [`go.dokimi.dev/conform`](conform) | The runtime of the conformance suites: the checks, their handle, the applications and the falsification |
| [`go.dokimi.dev/stub`](stub) | The runtime of the doubles: the record of each call, the order of results, strict mode and the expected counts of calls |
| [`go.dokimi.dev/fault`](fault) | The fault strategies of the doubles |

## Development

`make check` runs the gate of the repository: the formatters, the linters and the tests of every
language. [CONTRIBUTING.md](CONTRIBUTING.md) describes how to propose a change.

## License

dokimi-go is licensed under MIT. See [LICENSE](LICENSE).
