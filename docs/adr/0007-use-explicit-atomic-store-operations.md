---
status: accepted
---

# Use explicit atomic store operations

Cross-model transactions are exposed as named, aggregate-oriented persistence
operations whose contracts state the complete atomic guarantee. Application
services authorize and choose policy, while adapters own locking, concurrency
checks, constraints, and commit or rollback. Proctor does not expose raw
database transactions or a generic unit-of-work callback to application code,
because named operations make transaction boundaries, race guarantees, and
conformance tests discoverable.
