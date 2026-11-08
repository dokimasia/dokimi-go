---
"go.dokimi.dev/sim": patch
---

Fix `sim.Run` to start a host that exits in the final phase again, at once and then after a delay that doubles from 10 s to 5 minutes.
