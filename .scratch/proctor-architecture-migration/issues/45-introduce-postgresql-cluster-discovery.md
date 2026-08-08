# 45 — Introduce PostgreSQL cluster discovery

**Status:** complete  
**Work classification:** Required  
**Blocked by:** #44 Extract the cluster contract and local adapter

**What to build:** Use short-lived PostgreSQL records only for node discovery and membership bootstrap, not as an event bus.

## Purpose

Use short-lived PostgreSQL records only for node discovery and membership bootstrap, not as an event bus.

## Background

ADR-0026 chooses built-in Memberlist clustering with PostgreSQL discovery so no Redis dependency is required.

## Scope

- Add node registration, heartbeat/expiry, and discovery persistence suitable for multi-node bootstrap.
- Make discovery records disposable and self-healing.
- Keep event delivery out of PostgreSQL.

## Files or modules expected to change

server/cluster discovery port, PostgreSQL adapter/store operation, root configuration, conformance tests.

## Architectural rules and ADRs

Architecture Clustering and Persistence; ADR-0007, ADR-0026.

## Acceptance criteria

- [x] Nodes can discover live peers through bounded-lifetime PostgreSQL records.
- [x] Stale nodes expire without manual cleanup.
- [x] No application event payload is persisted as a cluster queue.

## Validation steps

- `cd server && go test ./cluster/... -run 'Discovery'`
- `cd server && go test ./store/... -run 'Cluster|Node'`
- `Run PostgreSQL integration/conformance suite`
- `cd server && go test ./...`

## Risks

Clock skew and stale discovery can hinder joining. Use explicit TTL semantics and bounded retry.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
