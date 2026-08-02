# 17 — Migrate the Academic Period capability

**Status:** completed
**Work classification:** Required
**Blocked by:** #16 Migrate the Programme Level capability

**What to build:** Move Academic Period operations through a focused seam with explicit temporal validation.

## Purpose

Move Academic Period operations through a focused seam with explicit temporal validation.

## Background

Academic periods are a distinct glossary concept and should not remain bundled with general academic administration.

## Scope

- Migrate Academic Period reads and mutations.
- Preserve institution-defined period semantics and overlap/validation behavior.
- Add DTO, error, authorization, audit, and agreement tests.

## Files or modules expected to change

Academic Period application capability, API DTOs/handlers, store seam, OpenAPI/tests.

## Architectural rules and ADRs

Architecture Domain, Validation, Application; ADR-0006, ADR-0007, ADR-0011, ADR-0013 through ADR-0015, ADR-0024, ADR-0027.

## Acceptance criteria

- [x] Academic Period operations have one focused public seam.
- [x] Temporal rules are application/domain-owned, not transport-owned.
- [x] Existing wire behavior remains stable.

## Validation steps

- `cd server && go test ./... -run 'AcademicPeriod'`
- `Run OpenAPI agreement target`
- `cd server && go test ./...`

## Risks

Later native-time migration must remain possible. Avoid introducing more millisecond or zero-sentinel coupling.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `academicPeriodService` exposes typed reads and mutations over narrow persistence, installation authorization, audit, clock, and ID seams.
- half-open interval validation remains domain-owned: `start_at` must be positive and `end_at` must be later, while overlapping and adjacent institution-defined periods remain valid.
- all five HTTP operations use transport DTOs, immutable `Invocation`, and an explicitly injected `AcademicPeriodApplication`; server-owned create fields remain accepted and ignored for v1 compatibility.
- named store operations atomically commit create, update, and archive with their success audits, preserve Institution ownership, and roll back mutations when audit completion fails.
- Academic Period archival remains blocked by active Classes or enrollments and shares a deterministic lifecycle-lock order with Class creation/update; concurrent PostgreSQL coverage proves exactly one safe winner.
- OpenAPI/runtime agreement covers route authentication, DTO schemas, integer-millisecond compatibility, bare-array lists, stable errors, Problem Details, and success responses.
