# 10 — Migrate Academic Unit read operations

**Status:** completed
**Work classification:** Required
**Blocked by:** #09 Introduce transport-neutral application errors

**What to build:** Deliver transport-neutral Academic Unit retrieval and listing through a narrow application service, explicit invocation, DTO mapping, and application-owned authorization.

## Purpose

Deliver transport-neutral Academic Unit retrieval and listing through a narrow application service, explicit invocation, DTO mapping, and application-owned authorization.

## Background

Academic Unit is the reference slice for AP-02 through AP-05 and AP-08. Read operations establish the pattern with lower mutation risk.

## Scope

- Introduce explicit immutable app.Invocation and focused Academic Unit query inputs/results.
- Move resource authorization into the application use case.
- Map domain results to HTTP DTOs and app errors.

## Files or modules expected to change

server/app Academic Unit service, server/app/api Academic Unit handlers/DTOs, store seam and tests.

## Architectural rules and ADRs

Architecture Application, Interfaces, HTTP, Errors; ADR-0002, ADR-0004, ADR-0006, ADR-0011, ADR-0013, ADR-0014, ADR-0015, ADR-0017, ADR-0027.

## Acceptance criteria

- [x] Get/list/search reads use a focused query service with only Academic Unit query persistence and authorization capabilities plus an explicit immutable invocation; installation resolution stays within the authorization adapter.
- [x] HTTP performs authentication, query parsing, application invocation, error translation, and DTO mapping but no resource authorization for migrated reads.
- [x] Existing array/object response shapes, stable error codes, visibility actions, root management scope, and child view scope remain stable.

## Validation steps

- `cd server && go test ./app/... -run 'AcademicUnit'`
- `cd server && go test ./app/api/... -run 'AcademicUnit|Routing|Authentication'`
- `cd server && go test ./...`

## Risks

Hierarchy visibility and not-found masking can drift. Preserve the existing permission matrix explicitly.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `app.Invocation` snapshots the principal and safe request metadata without hiding security state in `context.Context`.
- `academicUnitQueryService` depends on the Academic Unit query store and a consumer-owned authorization port that resolves the installation resource; denial prevents list/search query-store reads.
- Academic Unit GET, root list/search, and child list handlers call the application directly and map models into HTTP-owned response DTOs.
- The unused `app.NewServer` bridge was removed so `app/api` can follow the intended inward dependency on `app` without an import cycle; its CLI and testlib callers were already migrated by #07 and #08.
- Focused application and HTTP tests cover scoped authorization, denial behavior, input normalization, non-null empty collections, DTO compatibility, and absence of the legacy permission preflight.
- The migrated HTTP error registry covers every error reachable through Academic Unit reads, including fail-closed audit failures, while unmapped future codes still fail safe.
- The obsolete `platform.NewLegacy` concrete-selection path was removed; platform construction tests now supply explicit capabilities to `platform.New`.
- Orphaned platform-level cache, mail, VFS, and cluster backend selectors were removed; the module-root composition package is now the sole production selector.
