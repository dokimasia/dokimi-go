---
"go.dokimi.dev/sim": patch
---

Fix `sim.Run` to judge the rules of a runtime over each entry of the log once, in O(n) time for a case of n steps.
