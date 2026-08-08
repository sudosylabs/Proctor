# 59 — Rename soft-delete contracts to Archive

**Status:** completed
**Work classification:** Required
**Blocked by:** #58 Contract legacy lifecycle APIs and reset the schema baseline

**What to build:** Align persistence and application naming with domain-visible archival semantics across all soft-deleted aggregates.

## Purpose

Align persistence and application naming with domain-visible archival semantics across all soft-deleted aggregates.

## Background

AP-10 identifies store Delete methods that actually perform reversible/domain-visible soft deletion. ADR-0007 requires operations named for domain effects.

## Scope

- Expand Archive operations where needed, migrate call sites in compilable batches, then remove misleading soft Delete contracts.
- Do not rename genuine hard deletion or credential revocation operations.
- Preserve wire behavior and stored state semantics.

## Files or modules expected to change

store root/per-model contracts, SQL stores, application services, tests.

## Architectural rules and ADRs

Architecture Persistence and Naming; ADR-0007, ADR-0027.

## Acceptance criteria

- [x] Every soft-delete operation is named Archive end to end.
- [x] True deletion/revocation operations retain precise distinct names.
- [x] No compatibility alias remains after all callers migrate.

## Completion evidence

- Renamed reversible archival operations for Institution, Academic Unit,
  Programme, Programme Level, Academic Period, Class, and Role across store
  contracts, SQL adapters, application orchestration, audit metadata, and
  conformance tests.
- Retained `Delete` only for disposable cache eviction and irreversible removal
  of ephemeral cluster-discovery lease rows.
- Preserved the stable public v1 HTTP `DELETE`, `delete_at`, and OpenAPI
  compatibility contract while removing every application/store compatibility
  alias.
- Passed focused store and role tests, `go vet ./...`, `go test ./...`,
  `go test -race ./...`, and the reusable cache, mail, and VFS module suites.
- Completed two-axis standards/spec review with no remaining findings.

## Validation steps

- `rg -n '\bDelete\(' server/store server/app and classify remaining genuine deletions`
- `cd server && go test ./store/...`
- `cd server && go test ./...`

## Risks

Mechanical renaming can alter semantics or touch hard deletes. Inventory by behavior before changing names.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
