# 20 — Migrate Academic Unit Member administration

**Status:** completed
**Work classification:** Required
**Blocked by:** #19 Migrate the Affiliation capability

**What to build:** Move organizational membership administration to a focused application seam without granting implicit authorization.

## Purpose

Move organizational membership administration to a focused application seam without granting implicit authorization.

## Background

AcademicUnitMember is distinct from Affiliation, ClassMember, and RoleBinding; broad modules currently obscure that distinction.

## Scope

- Migrate membership queries and effective-dated mutations.
- Preserve hierarchy references and no-implicit-permission rule.
- Add DTO and contract coverage.

## Files or modules expected to change

Academic Unit Member application capability, API handlers/DTOs, store seam, OpenAPI/tests.

## Architectural rules and ADRs

Architecture Glossary, Authorization, and Application; ADR-0002, ADR-0006, ADR-0007, ADR-0013 through ADR-0015, ADR-0027.

## Acceptance criteria

- [x] Organizational membership remains independent of permission grants.
- [x] Operations use explicit invocation and application authorization.
- [x] Public behavior is preserved.

## Validation steps

- `cd server && go test ./... -run 'AcademicUnitMember'`
- `Run OpenAPI agreement target`
- `cd server && go test ./...`

## Risks

Membership and scoped roles are easy to conflate. Tests must prove membership alone grants nothing.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `academicUnitMemberService` exposes typed list, create, and end operations over narrow persistence, authorization, audit, clock, and ID seams.
- every operation authorizes `academic_unit.manage` against the owning Academic Unit in the application layer; membership data is never consulted as a permission grant.
- transport DTOs preserve route-owned Academic Unit identity, ignored server-owned fields, bare-array lists, effective-date filtering, and ended-membership responses.
- named persistence operations atomically commit creation/end transitions with their audits, compare and increment revisions, retain history, and reject overlapping ranges for the same User and Academic Unit.
- OpenAPI/runtime agreement covers all three routes, explicit principal authentication, DTO schemas, stable errors, Problem Details, and success responses.
