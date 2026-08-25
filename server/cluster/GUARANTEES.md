# Cluster delivery guarantees

This document records what Proctor’s cluster transports promise and what they
deliberately do not. It is normative for tests under `cluster/` and for
application security recovery expectations. The
[`runtime-operations` reference](../../.agents/skills/runtime-operations/references/runtime.md#cluster-transport)
owns the cross-component rationale; this file is the transport-facing contract
used by recovery tests.

## Guarantees

1. **Best-effort only.** Messages may be delayed, reordered, duplicated, or
   dropped. Transports never claim durable or at-least-once application
   processing.
2. **Peer-only broadcast.** `Broadcast` must not invoke the sender’s local
   handlers, preventing rebroadcast loops.
3. **Self-targeted delivery.** `SendToNode` to the current node may invoke local
   handlers synchronously so single-node installations exercise the same path.
4. **Idempotent handlers.** Application handlers for session revocation,
   authorization invalidation, and realtime fan-out must tolerate duplicates,
   finish bounded local work, and avoid durable or network work on the
   Memberlist receive path.
5. **Authoritative state is PostgreSQL.** Session validity, account enablement,
   role bindings, and permissions are decided from durable stores (and
   reconstructible caches with bounded TTLs), not from whether a cluster
   message arrived.
6. **Discovery is not a message bus.** PostgreSQL discovery rows advertise join
   addresses and protocol ranges only; they never carry application event
   payloads.
7. **Discovery is lifecycle-owned.** Memberlist advertises before seed
   selection and join, combines configured seeds first with compatible live
   leases, and starts periodic maintenance only after startup checks succeed.
   The transport owns cancellation and waits for maintenance before its
   persistence dependency can close. A failure after advertisement succeeds
   withdraws the advertisement best-effort and shuts down the incomplete
   Memberlist instance before returning the primary failure.
8. **Discovery convergence is continuous and best-effort.** Each heartbeat
   renews the local lease, attempts idempotent expired-row cleanup, re-lists
   compatible live peers, and attempts a rotating batch of at most three
   seeds from a configured-first candidate set bounded at 64. Each
   operation may fail independently without changing readiness; failures are
   diagnosed and later ticks continue.
9. **Protocol capability is compiled.** The current wire codec supports and
   advertises version 1 only. Deployment configuration cannot widen that range;
   the range expands only when another codec is implemented and tested.
10. **Protocol admission fails closed continuously.** A node does not become
   ready when a joined peer has malformed metadata, an identity different from
   its Memberlist name, a blank server version, a duplicate remote identity in
   a merge, or an invalid or non-overlapping protocol range. Alive and merge
   callbacks apply the same rejection after startup. Memberlist incarnation
   handling distinguishes an old address during stable-ID restart from a live
   name conflict; conflicts are refused and diagnosed. A dead name becomes
   reclaimable at a new address after one discovery TTL, and periodic joins
   retry during that safety window. An initial network join failure itself
   remains nonfatal because periodic discovery retries it; admission rejection
   is fatal during startup.
11. **Key rotation preserves overlap.** One primary key encrypts new traffic;
   at most eight distinct fallback keys decrypt traffic during staged rolling
   rotation. All key material is copied at construction and never logged.
12. **Shutdown preserves ownership.** Stop first makes operations observe
    terminal state, cancels and waits for maintenance, leaves and synchronously
    shuts down Memberlist, then withdraws its lease while the stop context is
    usable. The context bounds graceful leave and withdrawal; exhaustion is
    observable after owned work is safe. Withdrawal failure is diagnostic-only.

## Authentication cache expiry

Access-credential authentication may cache a resolved principal snapshot under
the `authentication/access/` key namespace. The entry TTL is the remaining time
until the earliest of:

- the access credential’s `expires_at`;
- the session’s idle expiry (`idle_expires_at`);
- the session’s absolute expiry (`expires_at`).

A successful cache hit still re-checks `Session.IsExpiredAt` (including
`revoked_at`) and user activity before accepting the principal. Cluster
invalidation deletes those keys best-effort; when a delete is lost, correctness
returns on the next cache miss, process restart, or when the encoded session
state is already expired.

Authorization decisions are never cached: each `Can` / `Authorize` call
resolves active role bindings from PostgreSQL.

## Non-guarantees

1. Every peer receives every security invalidation promptly.
2. A node with a still-valid cache entry becomes unauthorized the instant a
   peer commits a revocation if the invalidation message is lost. Recovery
   occurs when the cache entry expires, is deleted, or is reconstructed from
   PostgreSQL after a miss.
3. WebSocket subscribers always observe every state-change event. Clients must
   tolerate loss and resynchronize authoritative state over HTTP when needed.
4. Node churn, partition, or restart preserves in-flight best-effort messages.

## Recovery model

| Condition | Recovery |
| --- | --- |
| Missed session revocation message | Access credential resolution falls back to store after cache miss/TTL; revoked credentials are absent or sessions report revoked/expired. |
| Missed authorization invalidation | Authorization is not session-cached; each decision resolves current roles from PostgreSQL. |
| Missed realtime event | Clients fetch current state; local replay/resync covers connection-local loss only. |
| Node stop and later start | Graceful stop withdraws the lease best-effort; a newly constructed transport advertises and joins current seeds; periodic rediscovery repairs startup isolation and later churn. |
| Duplicate invalidation | Cache deletes and connection closes are idempotent. |

## Tests

- `server/cluster/memberlist` recovery tests: rejoin after churn, duplicate
  self-targeted delivery, lost-broadcast then later delivery.
- `server/app` cluster recovery tests: missed session invalidation + cache miss,
  duplicate session revocation, current-state authorization after binding end
  without cluster fan-out, expired cached sessions, duplicate peer realtime
  publication without rebroadcast.
