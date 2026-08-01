# 14 — Migrate the Institution capability

**Status:** completed
**Work classification:** Required
**Blocked by:** #13 Lock the Academic Unit HTTP contract

**What to build:** Move Institution reads and administration through a focused application seam using the proven reference-slice conventions.

## Purpose

Move Institution reads and administration through a focused application seam using the proven reference-slice conventions.

## Background

Institution currently participates in the broad aggregate and transport coupling identified by AP-02, AP-03, AP-05, and AP-08.

## Scope

- Migrate Institution queries and mutations end to end.
- Keep the one-installation/one-institution invariant authoritative.
- Add DTO, error, authorization, audit, and OpenAPI agreement coverage.

## Files or modules expected to change

Institution application capability, HTTP handlers/DTOs, store seam, OpenAPI/tests.

## Architectural rules and ADRs

Architecture Institution Boundary, Application, HTTP; ADR-0002, ADR-0006, ADR-0007, ADR-0011, ADR-0013 through ADR-0015, ADR-0024, ADR-0027.

## Acceptance criteria

- [x] Institution operations use a narrow service and explicit invocation.
- [x] Singleton and authorization invariants remain intact.
- [x] Runtime and OpenAPI behavior agree.

## Validation steps

- `cd server && go test ./... -run 'Institution'`
- `Run OpenAPI agreement target`
- `cd server && go test ./...`

## Risks

Singleton behavior and bootstrap ownership overlap. Do not fold bootstrap into ordinary Institution administration.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `institutionService` depends only on the singleton store seam, current-state authorizer, shared mutation auditor, and clock; facade calls use immutable `Invocation` plus typed query/command values.
- GET and PATCH handlers map HTTP-owned DTOs, call the narrow Institution application field, and contain no permission preflight or domain mutation logic.
- The singleton Institution is resolved once, authorized as `institution.manage`, and cannot be selected or changed by a caller-supplied identifier.
- `InstitutionStore.UpdateWithAudit` restricts the update to the active singleton row and commits the mutation plus success audit in one PostgreSQL transaction; conformance proves audit failure rolls the mutation back.
- `Optional[T]` preserves omitted, explicit-null, and present-zero PATCH wire states while the application command retains the characterized v1 null/no-op behavior.
- The legacy exported `InstitutionPatch`, direct domain serialization, and exported route initializer were removed without folding bootstrap into ordinary administration.
- OpenAPI and agreement coverage now includes Institution routes, authentication/CSRF alternatives, DTO schemas, exact stable errors, success responses, and Problem Details.
