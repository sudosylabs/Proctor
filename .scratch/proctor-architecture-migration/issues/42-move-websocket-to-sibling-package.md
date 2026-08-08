# 42 — Move WebSocket transport to a sibling package

**Status:** complete  
**Work classification:** Required  
**Blocked by:** #41 Remove the platform locator from application code

**What to build:** Relocate WebSocket protocol, hub, connection ownership, replay, and subscription handling from HTTP/model into the dedicated sibling transport package.

## Purpose

Relocate WebSocket protocol, hub, connection ownership, replay, and subscription handling from HTTP/model into the dedicated sibling transport package.

## Background

AP-06 violates ADR-0025 because WebSocket lives under HTTP and wire contracts live in model.

## Scope

- Move WebSocket-owned wire types and implementation to server/websocket.
- Expose narrow dependencies on application services and transient-event inputs.
- Keep HTTP upgrade integration as a thin boundary and preserve wire behavior.

## Files or modules expected to change

server/websocket, server/app/api WebSocket upgrade route, former model WebSocket contracts, root composition, tests.

## Architectural rules and ADRs

Architecture WebSocket and Model Boundaries; ADR-0005, ADR-0009, ADR-0025, ADR-0027.

## Acceptance criteria

- [x] WebSocket is a sibling of HTTP transport and model has no WebSocket wire contracts.
- [x] Origin, authentication, subscriptions, sequences, replay, liveness, and backpressure behavior remain stable.
- [x] Dependency gate accepts the new direction.

## Validation steps

- `cd server && go test ./websocket/...`
- `cd server && go test ./app/api/... -run 'WebSocket'`
- `cd server && go test ./...`

## Risks

Package movement can subtly alter concurrency ownership. Keep the move behavior-preserving and retain race tests.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
