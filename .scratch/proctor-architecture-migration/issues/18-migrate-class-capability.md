# 18 — Migrate the Class capability

**Status:** completed
**Work classification:** Required
**Blocked by:** #17 Migrate the Academic Period capability

**What to build:** Move Class administration through a focused service while preserving Programme Level and Academic Period relationships.

## Purpose

Move Class administration through a focused service while preserving Programme Level and Academic Period relationships.

## Background

Class is the concrete teaching roster and must not be conflated with Group or enrollment membership.

## Scope

- Migrate Class reads and mutations.
- Preserve exact class scope authorization and parent relationships.
- Add DTO/OpenAPI agreement and regression tests.

## Files or modules expected to change

Class application capability, API DTOs/handlers, Class store seam, OpenAPI/tests.

## Architectural rules and ADRs

Architecture Glossary and Academic Invariants; ADR-0002, ADR-0006, ADR-0007, ADR-0013 through ADR-0015, ADR-0024, ADR-0027.

## Acceptance criteria

- [x] Class operations use the focused seam and explicit invocation.
- [x] Exact class scope and parent references remain correct.
- [x] No Group terminology is introduced.

## Validation steps

- `cd server && go test ./... -run 'Class'`
- `Run OpenAPI agreement target`
- `cd server && go test ./...`

## Risks

Broad test filters may include ClassMember; keep assertions separated by responsibility.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `classService` exposes typed reads and mutations over narrow Class, Programme Level, Programme, authorization, audit, clock, and ID seams.
- exact Class reads authorize the Class resource before persistence access; collection and mutation operations resolve and authorize the owning Academic Unit, including both owners when moving a Class between Programme Levels.
- transport DTOs preserve both parent references, PATCH field-presence semantics, bare-array collection responses, and compatibility behavior that accepts but ignores server-owned create fields.
- named store operations atomically commit Class creation, update, and archive with their success audits; dependent enrollments and Class-scoped role bindings block archival.
- Class update and archive carry the authorized Academic Unit and expected revision into the transaction; stale owner or revision snapshots fail with a conflict before mutation, preventing concurrent moves from bypassing current-state authorization.
- Class archival and dependent creation share one PostgreSQL lifecycle lock. Integration races prove that archive versus enrollment and archive versus Class-scoped role binding each yield exactly one safe winner.
- OpenAPI/runtime agreement covers all six routes, explicit principal authentication, DTO schemas, stable errors, Problem Details, and success responses.
