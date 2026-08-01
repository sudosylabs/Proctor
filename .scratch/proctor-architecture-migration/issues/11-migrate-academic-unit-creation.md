# 11 — Migrate Academic Unit creation

**Status:** completed
**Work classification:** Required
**Blocked by:** #10 Migrate Academic Unit read operations

**What to build:** Move Academic Unit creation end to end onto the reference service pattern while preserving validation, hierarchy, audit, and post-commit effects.

## Purpose

Move Academic Unit creation end to end onto the reference service pattern while preserving validation, hierarchy, audit, and post-commit effects.

## Background

Creation is the first command slice and proves application-owned policy plus explicit transactional persistence.

## Scope

- Define focused create command/result contracts.
- Perform validation and authorization in the use case.
- Use an explicit atomic store operation, durable audit, and publish transient effects only after commit.

## Files or modules expected to change

server/app Academic Unit commands, HTTP DTOs/handler, Academic Unit store operations and tests.

## Architectural rules and ADRs

Architecture Application, Persistence, Audit, and Effects; ADR-0006, ADR-0007, ADR-0013, ADR-0014, ADR-0015, ADR-0018, ADR-0027.

## Acceptance criteria

- [x] Root and child creation preserve hierarchy rules and authorization.
- [x] Success audit commits atomically with durable change.
- [x] Realtime/cache effects occur only after commit and HTTP behavior is unchanged.

## Validation steps

- `cd server && go test ./app/... -run 'AcademicUnit.*Create'`
- `cd server && go test ./app/api/... -run 'AcademicUnit'`
- `cd server && go test ./store/... -run 'AcademicUnit'`
- `cd server && go test ./...`

## Risks

Publishing before commit or duplicating audits would violate correctness. Include rollback and post-commit failure tests.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `CreateAcademicUnitCommand` carries only caller-owned fields; the route parent determines hierarchy and the application supplies installation ownership, ID, and time.
- The use case authorizes root creation against the Institution and child creation against the parent Academic Unit before audit or mutation work.
- `AcademicUnitStore.Create` inserts the unit and completes its success audit in one PostgreSQL transaction; a conformance case proves audit failure rolls the unit back.
- Mutation failures retain the durable failed-attempt audit, while the `academic_unit_created` transient event is published only after commit and remains best effort.
- Root and child handlers decode an HTTP-owned request DTO, invoke the application without permission preflight, and return the characterized Academic Unit response DTO.
- The compatibility DTO continues accepting legacy server-owned creation fields but deliberately excludes them from the application command; route scope remains authoritative.
- Post-commit publication failure does not change the committed result and is reported once through a safe application effect-failure port.
