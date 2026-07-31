# 04 — Introduce the module-root server facade

**Status:** completed
**Work classification:** Required
**Blocked by:** #01 Freeze public HTTP and realtime behavior; #03 Enforce the dependency graph with a shrinking debt list

**What to build:** Provide the stable module-root construction and lifecycle API that callers will use while existing internals remain operational.

## Purpose

Provide the stable module-root construction and lifecycle API that callers will use while existing internals remain operational.

## Background

AP-01 violates the composition contract because runtime construction is rooted in app. Phase 1 starts with an additive facade so callers can migrate without a rewrite.

## Scope

- Add the module-root server package facade and narrow construction/lifecycle surface.
- Delegate to the existing graph initially.
- Do not relocate all dependencies or remove legacy constructors yet.

## Files or modules expected to change

server module root, existing app server construction, facade tests.

## Architectural rules and ADRs

Architecture Composition and Interfaces; ADR-0002, ADR-0008, ADR-0009.

## Acceptance criteria

- [x] A caller can construct, start, stop, and query readiness through the module-root facade.
- [x] The facade does not expose platform or transport implementation types.
- [x] Existing construction remains compatible during the transition.

## Validation steps

- `cd server && go test ./...`
- `cd server && go test -race ./...`
- `Run architecture dependency gate`

## Risks

A shallow facade can become permanent indirection. Keep its public contract narrow and document temporary delegation.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- Module-root `server.New` and `server.WithConfigPath` provide construction without exposing legacy option, platform, or transport types.
- `server.Server` exposes only `Start`, `Close`, and `Ready`; a public-surface test rejects platform or HTTP implementation types in exported method signatures.
- The facade delegates to the existing `app.NewServer` lifecycle through an unexported adapter, leaving all legacy constructors and callers compatible for tickets #05–#08.
- Focused facade tests cover successful construction through `server.New`,
  lifecycle error propagation, context delegation, readiness, close, and
  invalid construction input. An external-package contract test verifies the
  caller-visible signature and rejects legacy implementation types.
- Public documentation records ownership, dependency direction, failure
  semantics, and the legacy delegate's temporary construction-time WebSocket
  replay maintenance; every successfully constructed node must be closed.
