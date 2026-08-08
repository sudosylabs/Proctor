# 46 — Implement the Memberlist cluster adapter

**Status:** complete  
**Work classification:** Required  
**Blocked by:** #45 Introduce PostgreSQL cluster discovery

**What to build:** Provide the built-in multi-node best-effort cluster transport using Memberlist and PostgreSQL discovery.

## Purpose

Provide the built-in multi-node best-effort cluster transport using Memberlist and PostgreSQL discovery.

## Background

The target removes Redis as a clustering requirement while retaining one logical installation across nodes.

## Scope

- Implement join, membership, bounded message fan-out, handler dispatch, readiness, and shutdown.
- Use PostgreSQL discovery for seeds and Memberlist for transient communication.
- Preserve stable node identity and loop prevention.

## Files or modules expected to change

server/cluster/memberlist adapter, discovery integration, root configuration/composition, tests.

## Architectural rules and ADRs

Architecture Clustering; ADR-0018, ADR-0026.

## Acceptance criteria

- [x] Two or more nodes discover and exchange bounded best-effort messages without Redis.
- [x] Duplicate/self delivery and loops are prevented.
- [x] Lifecycle and readiness are root-controlled.

## Validation steps

- `cd server && go test ./cluster/...`
- `cd server && go test -race ./cluster/...`
- `Run multi-node integration test`
- `cd server && go test ./...`

## Risks

Distributed timing makes tests flaky. Use bounded eventual assertions and controllable discovery intervals.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
