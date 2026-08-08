---
status: accepted
---

# Allow constrained store layers

The root composition package may wrap a concrete store with Mattermost-shaped
`timerlayer`, `retrylayer`, and `localcachelayer` decorators that implement the
same store contracts. Timing is observability-only; retry applies only to an
explicit allowlist of safe idempotent operations; local caching applies only to
documented disposable reads with tested keys, TTLs, invalidation, and
security-staleness rules. Workflow-specific caching remains an explicit
application port. These constraints preserve visible consistency semantics
without giving up reusable operational store decoration.

The standard chain is
`localcachelayer(timerlayer(retrylayer(sqlstore)))`. Cache hits use their own
metrics and bypass database timing; timing captures complete cache-miss latency
including safe retries; retry stays nearest SQL so it can classify transient
database failures accurately.

Mechanical forwarding for complete store contracts is produced by a small
deterministic `go generate` tool. Generated files are clearly marked, checked
in, and verified clean in CI; caching, retry, timing, and exceptional behavior
remain handwritten so generation never hides policy.

Each decorator implements the root `store.Store`, exposes wrapped per-model or
per-aggregate stores, and overrides only relevant methods. Composition produces
one final layered store; no reflection proxy or per-model application wiring is
used.

The initial local-cache allowlist excludes authorization decisions, active
role bindings, account enabled state, sessions and credentials, MFA assurance,
and token revocation. Security-sensitive caching requires a separately
reviewed bounded-staleness and reliable cross-node invalidation contract;
low-risk reference data is added only after measurement.
