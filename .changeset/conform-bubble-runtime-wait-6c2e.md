---
"go.dokimi.dev/conform": patch
---

Fix the watchdog of `conform.Bubble` to take a wait for a semaphore of the runtime, such as a goroutine profile, as no stall.
