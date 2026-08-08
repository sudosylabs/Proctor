---
status: accepted
---

# Persist idempotent command outcomes

For commands vulnerable to client retry, transport extracts a bounded
idempotency key but the application command owns its semantics. An atomic store
operation records the key with principal, operation, request fingerprint,
outcome, and expiry so behavior is consistent across nodes and restarts.
Reusing a key with different input is a conflict; replaying identical input
returns the recorded outcome.
