---
"go.dokimi.dev/conform": patch
---

Fix `conform.BenchParallel` to draw the calls of each goroutine on its own, so no two goroutines pass one input to the subject.
