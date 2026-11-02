---
"go.dokimi.dev/conform": patch
---

Fix `conform.Isolate` to leave out the check of linearizability for every planted defect, as it leaves out the check of agreement with a reference.
