# 16 — Migrate the Programme Level capability

**Status:** completed
**Work classification:** Required
**Blocked by:** #15 Migrate the Programme capability

**What to build:** Move Programme Level operations into their own cohesive application capability and public DTO contract.

## Purpose

Move Programme Level operations into their own cohesive application capability and public DTO contract.

## Background

AP-08 identifies oversized academic administration modules that combine glossary concepts.

## Scope

- Migrate Programme Level reads and mutations.
- Preserve Programme ownership and lifecycle rules.
- Add focused tests and OpenAPI agreement.

## Files or modules expected to change

Programme Level application capability, API DTOs/handlers, persistence seam, OpenAPI/tests.

## Architectural rules and ADRs

Architecture Naming, Files, Application, HTTP; ADR-0002, ADR-0006, ADR-0007, ADR-0013 through ADR-0015, ADR-0024, ADR-0027.

## Acceptance criteria

- [x] Programme Level has a focused consumer-owned service.
- [x] No Programme Level policy remains in HTTP.
- [x] Contract tests preserve external behavior.

## Validation steps

- `cd server && go test ./... -run 'ProgrammeLevel'`
- `Run OpenAPI agreement target`
- `cd server && go test ./...`

## Risks

ID or naming ambiguity may cause cross-entity mistakes; keep typed command fields and explicit names.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `programmeLevelService` exposes typed queries and commands over narrow Programme Level, Programme-owner, academic-unit authorization, mutation-audit, clock, and ID seams.
- all five HTTP operations use transport-owned DTOs, immutable `Invocation`, and an explicitly injected `ProgrammeLevelApplication`; handler permission preflights and domain-shaped request decoding were removed.
- creation derives ownership only from the Programme route, while update commands cannot move a Programme Level between Programmes.
- named store operations atomically commit create, update, and archive with their success audits; reusable conformance proves success and rollback behavior.
- Programme Level archival remains blocked by active Classes and shares a lifecycle lock with Class creation/update; concurrent PostgreSQL coverage proves exactly one safe winner.
- checked OpenAPI and runtime agreement covers routes, authentication/CSRF alternatives, DTOs, stable errors, success responses, Problem Details, and PATCH null/empty compatibility.
