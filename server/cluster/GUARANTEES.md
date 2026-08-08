# Cluster delivery guarantees

This document records what Proctor’s cluster transports promise and what they
deliberately do not. It is normative for tests under `cluster/` and for
application security recovery expectations. ADR-0026 and `AGENTS.md` remain the
architecture authorities; this file is the transport-facing summary used by
recovery tests.

## Guarantees

1. **Best-effort only.** Messages may be delayed, reordered, duplicated, or
   dropped. Transports never claim durable or at-least-once application
   processing (ADR-0026).
2. **Peer-only broadcast.** `Broadcast` must not invoke the sender’s local
   handlers, preventing rebroadcast loops.
3. **Self-targeted delivery.** `SendToNode` to the current node may invoke local
   handlers synchronously so single-node installations exercise the same path.
4. **Idempotent handlers.** Application handlers for session revocation,
   authorization invalidation, and realtime fan-out must tolerate duplicates.
5. **Authoritative state is PostgreSQL.** Session validity, account enablement,
   role bindings, and permissions are decided from durable stores (and
   reconstructible caches with bounded TTLs), not from whether a cluster
   message arrived.
6. **Discovery is not a message bus.** PostgreSQL discovery rows advertise join
   addresses and protocol ranges only; they never carry application event
   payloads.

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
| Node stop/rejoin | Discovery heartbeats re-advertise seeds; Memberlist rejoins; subsequent best-effort messages resume. |
| Duplicate invalidation | Cache deletes and connection closes are idempotent. |

## Tests

- `server/cluster/memberlist` recovery tests: rejoin after churn, duplicate
  self-targeted delivery, lost-broadcast then later delivery.
- `server/app` cluster recovery tests: missed session invalidation + cache miss,
  duplicate session revocation, current-state authorization after binding end
  without cluster fan-out, expired cached sessions, duplicate peer realtime
  publication without rebroadcast.
