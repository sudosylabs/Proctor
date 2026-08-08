# 44 — Extract the cluster contract and local adapter

**Status:** complete  
**Work classification:** Required  
**Blocked by:** #43 Make WebSocket lifecycle explicit and prove conformance

**What to build:** Create the dedicated cluster sibling package with the target best-effort contract and loop-safe single-node local implementation.

## Purpose

Create the dedicated cluster sibling package with the target best-effort contract and loop-safe single-node local implementation.

## Background

AP-07 places cluster contracts in model/platform and includes a reliable Redis delivery class rejected by ADR-0026. Local mode is the degenerate clustered architecture.

## Scope

- Define cluster messages/handlers and lifecycle in server/cluster.
- Move the local transport behind that contract.
- Temporarily adapt existing Redis transport without carrying reliable semantics into the new public contract.

## Files or modules expected to change

server/cluster and local adapter, former model/platform cluster contracts, root composition, application event adapter.

## Architectural rules and ADRs

Architecture Clustering and Model Boundaries; ADR-0005, ADR-0018, ADR-0026, ADR-0027.

## Acceptance criteria

- [x] Single-node mode uses the same cluster contract and requires no Redis.
- [x] Cluster API promises best-effort transient delivery only.
- [x] Application and model do not own transport wire details.

## Validation steps

- `cd server && go test ./cluster/...`
- `cd server && go test -race ./cluster/...`
- `cd server && go test ./app/... -run 'Realtime'`
- `cd server && go test ./...`

## Risks

Adapting old reliable messages can imply false guarantees. Classify correctness-critical invalidation as recoverable/current-state behavior.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
