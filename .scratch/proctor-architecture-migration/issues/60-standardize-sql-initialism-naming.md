# 60 — Standardize SQL initialism naming

**Status:** completed
**Work classification:** Required
**Blocked by:** #59 Rename soft-delete contracts to Archive

**What to build:** Apply consistent Go initialism naming to SQL store types and constructors without changing package behavior.

## Purpose

Apply consistent Go initialism naming to SQL store types and constructors without changing package behavior.

## Background

AP-10 calls out Sql* naming that conflicts with the project convention. This is a wide mechanical refactor best performed after domain/store call sites stabilize.

## Scope

- Expand or rename Sql* identifiers to SQL* in dependency-safe batches.
- Update constructors, tests, documentation, and internal references.
- Remove temporary aliases after all callers migrate.

## Files or modules expected to change

server/store/sqlstore and callers, tests/documentation.

## Architectural rules and ADRs

Architecture Naming and Files; ADR-0027 and documented Go naming conventions.

## Acceptance criteria

- [x] Exported and internal Go identifiers use SQL initialism consistently.
- [x] Package names and external behavior do not change.
- [x] No deprecated Sql* alias remains.

## Completion evidence

- Renamed the root `SqlStore`/`SqlStoreStores` types to
  `SQLStore`/`SQLStoreStores` and every per-model `Sql<Model>Store` adapter to
  `SQL<Model>Store`.
- Renamed all per-model constructors from `newSql<Model>Store` to
  `newSQL<Model>Store`, including root composition, compile-time assertions,
  tests, and callers.
- Updated the living architecture contract to use `SQL<Model>Store` and
  `SQLStore`; retained `newSqlxDBWrapper` because it names the distinct `sqlx`
  library wrapper rather than a mis-cased SQL store identifier.
- Verified `rg -n '\bSql[A-Z]' server` returns no matches and introduced no
  compatibility aliases.
- Passed focused store and architecture tests, `go vet ./...`,
  `go test -race ./...`, and the full server test suite.

## Validation steps

- `rg -n '\bSql[A-Z]' server`
- `cd server && go test ./store/...`
- `cd server && go test ./...`

## Risks

Large rename blast radius can create merge conflicts. Keep it mechanical and avoid unrelated store refactors.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
