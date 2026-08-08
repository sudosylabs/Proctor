---
status: accepted
---

# Use built-in Memberlist clustering

A top-level `cluster` package owns inter-node transport contracts, wire
messages, node discovery, and concrete transports. Single-node installations
and tests use an in-process `local` transport; multi-node installations use a
built-in HashiCorp Memberlist peer transport with gossip membership and direct
node messaging. Redis remains optional cache infrastructure and is not required
for clustering. The root composes the transport, `platform.Service` owns its
lifecycle, and application services see only narrow publication or invalidation
ports.

Memberlist messages are best-effort and non-durable; the transport does not
claim at-least-once processing. Handlers remain idempotent, while correctness
recovers through authoritative PostgreSQL reads, bounded cache TTLs, periodic
revalidation, and client resynchronization. Durable eventual work uses a
database-backed job or outbox rather than gossip.

Nodes bootstrap through short-lived discovery records and heartbeats in the
shared PostgreSQL database, then use Memberlist for live membership and
messages. Static seed addresses are an operator override, not the only
discovery mechanism; PostgreSQL never carries cluster messages.

Multi-node mode requires a shared cluster key and mandatory Memberlist traffic
encryption and authentication. Bind and advertised addresses are explicit,
public-interface binding is rejected by default, and key rotation uses the
Memberlist keyring.

Discovery advertises server and supported protocol versions, and every message
carries a protocol version. Incompatible peers are rejected before readiness;
adjacent compatible versions tolerate unknown message types and fields to
support rolling upgrades.

Configuration explicitly selects `local` or `memberlist`; there is no automatic
mode promotion. Memberlist prerequisites are validated before readiness.

Protocol-neutral contracts live in `cluster`, while concrete adapters live in
`cluster/local` and `cluster/memberlist` and are imported only by root
composition. Memberlist discovery persistence uses a narrow store contract, not
SQL adapter types.
