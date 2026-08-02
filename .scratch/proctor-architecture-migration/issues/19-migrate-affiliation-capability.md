# 19 — Migrate the Affiliation capability

**Status:** completed
**Work classification:** Required
**Blocked by:** #18 Migrate the Class capability

**What to build:** Move effective-dated Affiliation administration through a focused application service.

## Purpose

Move effective-dated Affiliation administration through a focused application service.

## Background

Affiliation describes who a user is and must remain separate from roles, permissions, and organizational membership.

## Scope

- Migrate Affiliation reads and mutations.
- Preserve non-exclusive, time-bounded semantics.
- Keep authorization distinct from affiliation data.

## Files or modules expected to change

Affiliation application capability, API DTOs/handlers, store seam, OpenAPI/tests.

## Architectural rules and ADRs

Architecture Identity Domain and Application; ADR-0002, ADR-0006, ADR-0007, ADR-0013 through ADR-0015, ADR-0027.

## Acceptance criteria

- [x] A user may retain multiple concurrent affiliations.
- [x] Affiliation changes grant no implicit permissions.
- [x] HTTP contains no affiliation business rules.

## Validation steps

- `cd server && go test ./... -run 'Affiliation'`
- `Run OpenAPI agreement target`
- `cd server && go test ./...`

## Risks

Tests may accidentally encode affiliation as an exclusive user type. Add multi-affiliation coverage.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `affiliationService` exposes typed list, create, and end operations over narrow persistence, enrollment-read, authorization, audit, clock, and ID seams.
- every operation authorizes `user.manage` against the target User in the application layer; Affiliation kinds remain descriptive data and never participate in permission evaluation.
- transport DTOs keep route-owned User identity and server-owned persistence fields out of application input while preserving the existing bare-array list and ended-Affiliation response shapes.
- PostgreSQL permits simultaneous non-overlapping ranges for different Affiliation kinds while continuing to reject overlapping ranges of the same kind.
- named create and end operations atomically commit their successful audit transitions, compare and increment an Affiliation revision, and roll back when audit completion fails.
- Affiliation ending and Class enrollment share a lifecycle lock and transactionally revalidate the student relationship, so a student cannot acquire an active enrollment concurrently with ending the required Affiliation.
- OpenAPI/runtime agreement covers all three routes, explicit principal authentication, DTO schemas, stable errors, Problem Details, and success responses.
