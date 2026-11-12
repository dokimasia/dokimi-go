---
"go.dokimi.dev/host": patch
---

Fix `host.Network` to lose a datagram that arrives during a partition or before a connection listens at its port.
