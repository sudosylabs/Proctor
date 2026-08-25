---
name: runtime-operations
description: Change Proctor runtime composition, startup or shutdown, readiness, serving leases, clustering, peer delivery, configuration, TLS, observability, logging, metrics, deployment topology, or operational failure behavior.
---

# Change runtime and operations

## Workflow

1. Select the branch reference:
   - lifecycle, effects, HA, cluster transport, readiness, observability,
     testing, or operational boundaries: [runtime](references/runtime.md);
   - deployment-owned settings, secrets, reloadability, validation, or
     source precedence: [configuration](references/configuration.md).
   Completion: authoritative state, transient effects, lifecycle owner, and
   operator-owned inputs are explicit.
2. Keep `server.New` as the only concrete selection point and
   `platform.Service` as infrastructure lifecycle owner. Completion:
   application services receive immutable policy or narrow providers, never
   platform/config service location.
3. Commit durable state before cache, cluster, realtime, execution, mail, or
   other effects. Completion: single-node and multi-node behavior differ only
   by adapter, not product semantics.
4. Preserve distinct liveness, readiness, and authorized diagnostics; drain
   entered stages in bounded reverse ownership order. Completion: partial
   startup, lease failure, normal shutdown, and repeated close dispose every
   owner exactly once.
5. Update configuration, composition, cluster conformance, lifecycle,
   readiness, metrics, logging, race, and multi-node tests as relevant, then
   run `make -C server architecture` and the focused integration target.

The exact cluster delivery and recovery behavior remains in
[`server/cluster/GUARANTEES.md`](../../../server/cluster/GUARANTEES.md).
