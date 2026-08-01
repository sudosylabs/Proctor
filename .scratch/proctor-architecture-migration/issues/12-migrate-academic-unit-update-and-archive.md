# 12 — Migrate Academic Unit update and archive

**Status:** completed
**Work classification:** Required
**Blocked by:** #11 Migrate Academic Unit creation

**What to build:** Complete the Academic Unit command slice, including hierarchy-safe updates and explicit archival semantics.

## Purpose

Complete the Academic Unit command slice, including hierarchy-safe updates and explicit archival semantics.

## Background

The reference slice is incomplete until every Academic Unit route follows the same seam and no handler preauthorization remains.

## Scope

- Migrate update, reparenting, and archive commands.
- Preserve cycle prevention, optimistic behavior where present, audit, and invalidation.
- Do not perform the global Delete-to-Archive contract rename yet.

## Files or modules expected to change

server/app Academic Unit commands, API handlers/DTOs, Academic Unit persistence operations.

## Architectural rules and ADRs

Architecture Domain, Application, Persistence, and HTTP; ADR-0006, ADR-0007, ADR-0012, ADR-0013, ADR-0014, ADR-0015, ADR-0018.

## Acceptance criteria

- [x] Update and archive routes contain no business policy or resource preflight.
- [x] Hierarchy cycles and invalid transitions fail with transport-neutral errors.
- [x] Durable and observable behavior matches the frozen contract.

## Validation steps

- `cd server && go test ./app/... -run 'AcademicUnit'`
- `cd server && go test ./store/... -run 'AcademicUnit'`
- `cd server && go test ./app/api/... -run 'AcademicUnit'`
- `cd server && go test ./...`

## Risks

Reparenting can alter inherited authorization. Test ancestor changes and concurrent updates explicitly.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `UpdateAcademicUnitCommand` and `ArchiveAcademicUnitCommand` carry only caller-owned mutation inputs through the typed application boundary.
- The update use case authorizes the current Academic Unit and, for non-root reparenting, the destination parent before audit or persistence work.
- Application-selected transition times and normalized mutable fields are validated before persistence; store conflicts and invalid references become transport-neutral application errors.
- `UpdateWithAudit` and `ArchiveWithAudit` complete the success audit in the same PostgreSQL transaction as the durable mutation, with conformance tests proving rollback when audit completion fails.
- A process-wide PostgreSQL advisory transaction lock serializes hierarchy mutations; a concurrent reciprocal-reparenting conformance case proves one update is rejected and no cycle is committed.
- PATCH and DELETE handlers now only authenticate, decode HTTP-owned inputs, invoke the application, and encode the characterized response; resource permission preflights were removed.
- `academic_unit_updated` and `academic_unit_archived` events publish only after commit and remain best effort, with operation-specific safe failure reporting.
- Full ordinary, race, vet, build, architecture, and isolated PostgreSQL Academic Unit conformance validation passed.
