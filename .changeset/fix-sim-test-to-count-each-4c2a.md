---
"go.dokimi.dev/sim": patch
---

Fix `sim.Test` to count each call of `World.Invoke` in a scenario of a world of components as a step, with two crash points and a check of the invariants.
