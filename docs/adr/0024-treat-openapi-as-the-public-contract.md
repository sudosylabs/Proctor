---
status: accepted
---

# Treat OpenAPI as the public contract

A reviewed OpenAPI document checked into the repository is the source of truth
for Proctor's public HTTP contract. CI compares it with registered routes,
authentication classifications, transport DTO schemas, and stable errors.
Documentation and clients may be generated from the specification, but
handlers and domain models remain handwritten so the wire contract cannot
dictate application or domain structure.
