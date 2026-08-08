# 43 — Make WebSocket lifecycle explicit and prove conformance

**Status:** complete  
**Work classification:** Required  
**Blocked by:** #42 Move WebSocket transport to a sibling package

**What to build:** Ensure constructing the WebSocket transport is inert and root lifecycle methods explicitly start and stop all background work.

## Purpose

Ensure constructing the WebSocket transport is inert and root lifecycle methods explicitly start and stop all background work.

## Background

AP-06 notes construction starts background work. Explicit lifecycle is required for deterministic ownership, testing, and cleanup.

## Scope

- Separate construction from goroutine startup.
- Make start/stop idempotence and shutdown ordering explicit.
- Complete WebSocket conformance coverage, including resynchronization when replay is unavailable.

## Files or modules expected to change

server/websocket lifecycle, root server wiring, conformance/race tests.

## Architectural rules and ADRs

Architecture Lifecycle and WebSocket; ADR-0008, ADR-0025.

## Acceptance criteria

- [x] Construction starts no goroutines or listeners.
- [x] Root startup/shutdown owns all WebSocket background work without leaks.
- [x] Conformance covers origin, upgrade, auth, subscription authorization, replay, sequence, ping/pong, backpressure, and resync.

## Validation steps

- `cd server && go test ./websocket/...`
- `cd server && go test -race ./websocket/...`
- `cd server && go test ./...`

## Risks

Shutdown races may only appear under load. Use deterministic cancellation and race coverage.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
