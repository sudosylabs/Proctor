---
status: accepted
---

# Use additive API versioning

After an API version is declared stable, its routes, field meanings, and error
codes evolve additively and are never removed or repurposed in place. Clients
must tolerate unknown response fields, while new required inputs or changed
semantics require a new API version and documented deprecation path.
Pre-stable contracts may change only when releases state clearly that
compatibility is not yet promised.
