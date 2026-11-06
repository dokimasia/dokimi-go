# go.dokimi.dev/medium

## 0.2.0

### Minor Changes

- e7c7771: Add `FailBefore`, `FailAfter`, `FailPart`, `Hold`, `Held` and `Release` to `medium.Memory`.
- e7c7771: Add `Kill` and `CrashKeeping` to `medium.Memory` to kill the process of a medium and to keep a part of its pending writes at a crash.

### Patch Changes

- 1cbb662: Require `go.dokimi.dev/assert` v0.18.0.

## 0.1.1

### Patch Changes

- a43530a: Require `go.dokimi.dev/assert` v0.17.0.

## 0.1.0

### Minor Changes

- 1bc3e13: Add the module `go.dokimi.dev/medium`, the storage of a durable subject over the operating system and in memory, with crashes, torn writes, flipped bits and a full disk.
