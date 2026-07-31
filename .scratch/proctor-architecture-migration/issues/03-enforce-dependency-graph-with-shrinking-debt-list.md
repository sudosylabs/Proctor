# 03 — Enforce the dependency graph with a shrinking debt list

**Status:** completed
**Work classification:** Required
**Blocked by:** None — can start immediately

**What to build:** Add an automated import-policy gate that prevents new architectural dependency violations while explicitly tracking existing migration debt.

## Purpose

Add an automated import-policy gate that prevents new architectural dependency violations while explicitly tracking existing migration debt.

## Background

AP-01, AP-02, and AP-11 show that the intended inward graph is not enforced. Phase 0 requires a ratchet, not an all-at-once cleanup.

## Scope

- Encode the required package dependency direction and forbidden imports.
- Baseline current violations in a reviewed debt list.
- Fail validation on new violations or growth in existing debt.

## Files or modules expected to change

server architecture tests or lint tooling, CI/Make validation configuration.

## Architectural rules and ADRs

Architecture Dependency Rules and Enforcement; ADR-0009, ADR-0027.

## Acceptance criteria

- [x] The gate detects representative forbidden imports.
- [x] Current known debt is explicit and can only shrink.
- [x] The gate runs deterministically in normal validation.

## Validation steps

- `Run the architecture dependency test/target`
- `cd server && go test ./...`
- `Introduce a temporary forbidden import locally and confirm the gate fails, then remove it`

## Risks

Over-broad rules can block valid sibling adapters. Encode documented exceptions narrowly and test them.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.

## Implementation evidence

- `server/architecture` parses production Go files across the server and all reusable modules, including files excluded by current build tags, and tests representative allowed and forbidden dependency edges.
- `dependency_debt.txt` records the reviewed exact, sorted file/import baseline; an immutable compiled ceiling rejects ledger growth even when a matching forbidden import is added, while stale entries, duplicates, and unsorted debt also fail.
- `make architecture` provides the focused gate and `make check` runs it during normal validation.
- A temporary application-to-`store/sqlstore` import was rejected both without and with a matching ledger entry, and both probes were removed before commit.
