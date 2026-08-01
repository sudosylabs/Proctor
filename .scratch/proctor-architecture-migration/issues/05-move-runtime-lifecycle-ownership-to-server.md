# 05 — Move runtime lifecycle ownership to package server

**Status:** completed
**Work classification:** Required
**Blocked by:** #04 Introduce the module-root server facade

**What to build:** Make the module-root server package own startup, readiness, shutdown ordering, and cleanup across the assembled runtime.

## Purpose

Make the module-root server package own startup, readiness, shutdown ordering, and cleanup across the assembled runtime.

## Background

The current app-owned lifecycle crosses application and infrastructure responsibilities. AP-01 and ADR-0008 place runtime ownership at the composition root.

## Scope

- Move lifecycle orchestration behind the root facade.
- Preserve bounded HTTP shutdown and deterministic cleanup order.
- Keep application use cases free of process lifecycle concerns.

## Files or modules expected to change

server root, app server lifecycle, platform service lifecycle, lifecycle tests.

## Architectural rules and ADRs

Architecture Composition and Lifecycle; ADR-0008, ADR-0009, ADR-0010.

## Acceptance criteria

- [x] Root server owns all runtime start/stop sequencing.
- [x] Partial startup failure cleans up already-created resources.
- [x] Readiness and graceful shutdown behavior match characterization tests.

## Validation steps

- `cd server && go test ./...`
- `cd server && go test -race ./...`
- `Run lifecycle failure-path tests`

## Risks

Ordering changes may leak goroutines or close dependencies too early. Cover partial construction and repeated shutdown.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `server.Server` now owns lifecycle state, platform startup, listener and HTTP
  startup, readiness transitions, bounded graceful HTTP shutdown, transport
  cleanup, platform cleanup, and idempotent repeated close.
- Component construction temporarily delegates to `app.NewServer`, but the
  root runtime consumes its platform, HTTP transport, and readiness components
  directly and never calls the legacy app-owned `Start` or `Close` methods.
- Focused root lifecycle tests prove cleanup after platform-start and
  post-platform listener failures, readiness transitions, bounded draining
  after cancellation or unexpected HTTP serving failure, shutdown ordering,
  waiting for the serving loop, and repeated-close behavior.
