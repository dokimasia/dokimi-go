---
"go.dokimi.dev/stub": minor
---

**Breaking:** Pass the call to `stub.Answer` by value and return the stamped call from `Arms.Stamp`, so a call of a double in bench mode does not allocate its record.
