# go.dokimi.dev/stub

## 0.3.1

### Patch Changes

- a43530a: Require `go.dokimi.dev/assert` v0.17.0.
- Updated dependencies [a43530a]
  - go.dokimi.dev/fault@0.2.2

## 0.3.0

### Minor Changes

- c06eebc: **Breaking:** Check the arguments, the hook `OnRecord` and the fixed results of a method in `Behaviour` through the new fields `Arguments`, `Reset` and `Returns` of `Subject`.
- c06eebc: **Breaking:** Check the reset of a double through the subject of each method in `ResetBehaviour`, in place of the field `Reset` of `Instance`.
- c06eebc: **Breaking:** Pass the call of its arguments to the function that `Subject.Override` sets.

### Patch Changes

- go.dokimi.dev/fault@0.2.1

## 0.2.1

### Patch Changes

- e708642: Require `go.dokimi.dev/assert` v0.12.0.
- Updated dependencies [e708642]
  - go.dokimi.dev/fault@0.2.1

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
