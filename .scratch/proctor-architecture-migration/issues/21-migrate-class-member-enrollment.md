# 21 — Migrate Class Member enrollment

**Status:** completed
**Work classification:** Required
**Blocked by:** #20 Migrate Academic Unit Member administration

**What to build:** Move enrollment, transfer, and progression into a focused transactional use case.

## Purpose

Move enrollment, transfer, and progression into a focused transactional use case.

## Background

ClassMember carries the one-active-enrollment-per-period invariant and historical progression; this requires explicit atomic operations rather than handler orchestration.

## Scope

- Migrate listing, enrollment, transfer, and progression paths.
- Preserve serialized one-active-enrollment-per-student-and-period behavior.
- Retain history, audit, and post-commit effects.

## Files or modules expected to change

Class Member application capability, API DTOs/handlers, atomic store operations, OpenAPI/tests.

## Architectural rules and ADRs

Architecture Academic Invariants and Persistence; ADR-0006, ADR-0007, ADR-0013 through ADR-0015, ADR-0018, ADR-0027.

## Acceptance criteria

- [x] Concurrent enrollment cannot create overlapping active memberships.
- [x] Transfer/progression closes and replaces membership transactionally while retaining history.
- [x] Authorization and audit remain application-owned.

## Validation steps

- `cd server && go test ./... -run 'ClassMember|Enrollment|Transfer|Progression'`
- `Run SQL conformance tests for ClassMember`
- `cd server && go test -race ./...`

## Risks

Concurrency regressions can corrupt academic history. Include competing transaction tests.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `classMemberService` owns typed list, enrollment/transfer, and end use cases over narrow Class, persistence, authorization, audit, clock, and ID seams.
- application authorization is authoritative for exact Class scope; HTTP preflights and the broad membership administration interface were removed.
- enrollment derives the Academic Period from the destination Class, and the SQL transaction serializes by User and period, validates student Affiliation, closes the prior membership, inserts the replacement, and completes the success audit atomically.
- Class Member revisions protect end operations from stale commands, while historical rows and transfer boundaries remain queryable.
- DTO and OpenAPI agreement coverage preserve route-owned Class identity, ignored compatibility fields, bare-array lists, history filtering, enrollment result shape, stable errors, and explicit principal authentication.
- SQL conformance covers audit rollback, transfer history, stale ends, and competing enrollments.
