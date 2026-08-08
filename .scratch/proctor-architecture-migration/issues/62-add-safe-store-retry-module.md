# 62 — Add the safe store retry module

**Status:** completed
**Work classification:** Required
**Blocked by:** #61 Add the store timing module

**What to build:** Introduce constrained retries only for explicitly safe, transient persistence operations.

## Purpose

Introduce constrained retries only for explicitly safe, transient persistence operations.

## Background

ADR-0019 permits a retry layer but forbids blanket retry of non-idempotent work. ADR-0029 requires durable idempotent command outcomes where retry safety depends on them.

## Scope

- Classify store operations by retry safety.
- Implement bounded backoff/cancellation for approved transient failures only.
- Prove unsafe mutations are never automatically retried.

## Files or modules expected to change

server/store retry layer, transient error classification, root composition, conformance/tests.

## Architectural rules and ADRs

Architecture Persistence and Error Handling; ADR-0007, ADR-0019, ADR-0029.

## Acceptance criteria

- [x] Only allowlisted idempotent/read or outcome-protected operations retry.
- [x] Context cancellation and deadlines stop retries promptly.
- [x] Transaction and domain errors pass through unchanged.

## Completion evidence

- Added a safe-by-default `retrylayer` for the complete root and per-model
  `store.Store` surface. Handwritten overrides opt in only idempotent reads;
  generated embedded forwarding keeps every other operation single-attempt.
- Kept mutating operations out of the allowlist, including personal-access-
  token `Resolve`, which also performs a debounced last-used update despite its
  read-shaped name. No operation relies on an unimplemented idempotent-command
  outcome contract for retry safety.
- Added a bounded production policy of three attempts with exponential backoff
  capped at 100 milliseconds. Context cancellation and deadlines interrupt
  backoff without starting another attempt.
- Added PostgreSQL-owned transient classification for serialization failures
  (`40001`) and deadlocks (`40P01`) only. The classifier rejects wrapped store
  domain errors and the retry layer returns final errors and results unchanged.
- Composed `timerlayer(retrylayer(sqlstore))` once at the module root; a graph
  test proves one timer observation covers all underlying retry attempts.
- Generalized the deterministic store-layer generator for timer and retry
  topology, with checked-in forwarding, currency tests, compile assertions,
  and PostgreSQL conformance coverage across all reusable store suites.
- Passed the focused retry/conformance tests, the PostgreSQL-backed retry
  conformance suite, `go test -race ./store/...`, `go vet ./...`, and the full
  server test suite.

## Validation steps

- `cd server && go test ./store/... -run 'Retry|Conformance'`
- `cd server && go test -race ./store/...`
- `cd server && go test ./...`

## Risks

Retrying a mutation can duplicate durable effects. Make safety opt-in and test attempt counts for every class.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
