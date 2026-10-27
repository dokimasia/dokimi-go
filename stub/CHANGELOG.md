# go.dokimi.dev/stub

## 0.2.0

### Minor Changes

- b481d12: **Breaking:** Pass the call to `stub.Answer` by value and return the stamped call from `Arms.Stamp`, so a call of a double in bench mode does not allocate its record.

## 0.1.1

### Patch Changes

- Updated dependencies [a5fec39]
  - go.dokimi.dev/fault@0.2.0

## 0.1.0

### Minor Changes

- 2de5bfb: Add the package `stub` with the runtime of the doubles that dokimi generates.
- 861503e: Add `Arms.Default` and `Subject.Default` for the default results of an unconfigured call of a double.

### Patch Changes

- Updated dependencies [26a7a38, 550534b]
  - go.dokimi.dev/fault@0.1.0
