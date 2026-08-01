# 15 — Migrate the Programme capability

**Status:** completed
**Work classification:** Required
**Blocked by:** #14 Migrate the Institution capability

**What to build:** Move Programme reads and mutations through a focused, transport-neutral service.

## Purpose

Move Programme reads and mutations through a focused, transport-neutral service.

## Background

Programme belongs to one Academic Unit and must retain scoped authorization without handler policy.

## Scope

- Migrate Programme queries, create, update, and archive.
- Preserve Academic Unit ownership and scoped authorization.
- Add DTO/OpenAPI agreement and focused tests.

## Files or modules expected to change

Programme application capability, HTTP handlers/DTOs, Programme store seam, OpenAPI/tests.

## Architectural rules and ADRs

Architecture Academic Domain and Application; ADR-0002, ADR-0006, ADR-0007, ADR-0011, ADR-0013 through ADR-0015, ADR-0024, ADR-0027.

## Acceptance criteria

- [x] All Programme operations use the focused seam.
- [x] Ownership and archive invariants are application-enforced.
- [x] Public behavior remains stable and documented.

## Validation steps

- `cd server && go test ./... -run 'Programme'`
- `Run OpenAPI agreement target`
- `cd server && go test ./...`

## Risks

Programme and ProgrammeLevel vocabulary can blur. Keep contracts entity-specific.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `programmeService` exposes typed queries and commands over a narrow Programme store, academic-unit authorization, mutation-audit, clock, and ID seam.
- HTTP handlers use transport-owned request/response DTOs, immutable `Invocation`, an explicitly injected `ProgrammeApplication`, and no authorization preflight.
- Creation takes ownership only from the Academic Unit route; update commands cannot move a Programme to another owner.
- archive retains the active-Programme-Level conflict invariant, while create, update, and archive commit their success audit in the same PostgreSQL transaction.
- focused application and HTTP tests cover scoped authorization, server-owned fields, ownership preservation, atomic store inputs, and PATCH null/empty compatibility.
- reusable PostgreSQL conformance covers atomic audit success and rollback, and the checked OpenAPI contract agrees with all five runtime operations, DTOs, security alternatives, responses, and stable errors.
