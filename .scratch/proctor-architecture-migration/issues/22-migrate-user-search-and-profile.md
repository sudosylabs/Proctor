# 22 — Migrate user search and profile administration

**Status:** completed
**Work classification:** Required
**Blocked by:** #21 Migrate Class Member enrollment

**What to build:** Move user lookup, visibility, and profile mutations behind a focused application service and stable public DTOs.

## Purpose

Move user lookup, visibility, and profile mutations behind a focused application service and stable public DTOs.

## Background

User handlers currently leak domain entities and broad application dependencies. Visibility depends on current contextual authorization.

## Scope

- Migrate search, read, and administrative profile update paths.
- Preserve teacher-to-student and cross-user visibility rules.
- Map safe transport DTOs explicitly.

## Files or modules expected to change

User application capability, API DTOs/handlers, user search store seam, OpenAPI/tests.

## Architectural rules and ADRs

Architecture Interfaces, HTTP, Authorization; ADR-0002, ADR-0011, ADR-0013 through ADR-0015, ADR-0024, ADR-0027.

## Acceptance criteria

- [x] Unrelated and cross-scope users remain hidden by default.
- [x] Authorized contextual visibility is unchanged.
- [x] Sensitive user fields never enter public DTOs.

## Validation steps

- `cd server && go test ./... -run 'User.*Search|User.*Profile|Visibility'`
- `Run OpenAPI agreement target`
- `cd server && go test ./...`

## Risks

DTO changes can expose private account data. Use allowlisted response fields and negative disclosure tests.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `userProfileService` owns typed search, contextual read, and profile-update use cases over narrow User persistence, visibility authorization, audit, and clock seams.
- contextual reads preserve self access, institution `user.view`, active-Class `class.members.view`, final default denial, and exactly one authoritative audited authorization decision.
- User profile updates use a revision-protected named SQL transaction that commits the mutation and success audit atomically; stale updates and missing-audit rollback are covered by conformance tests.
- explicit transport DTOs preserve the existing v1 field set while excluding persistence revisions, credentials, provider subjects, MFA material, and tokens.
- OpenAPI/runtime agreement covers search pagination and filtering, current-user and contextual reads, profile patching, explicit principal authentication, stable errors, and response schemas.
