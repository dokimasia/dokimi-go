---
"go.dokimi.dev/sim": patch
---

Fix `sim.World` to fail an effect that a slow dependency keeps in flight with `context.Canceled` once an effect of its process ends its token.
