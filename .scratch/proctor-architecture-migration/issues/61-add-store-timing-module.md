# 61 — Add the store timing module

**Status:** completed
**Work classification:** Required
**Blocked by:** #60 Standardize SQL initialism naming

**What to build:** Introduce a transparent timer decorator that records bounded operation metrics without changing store semantics or exposing implementation details upward.

## Purpose

Introduce a transparent timer decorator that records bounded operation metrics without changing store semantics or exposing implementation details upward.

## Background

AP-10 notes the agreed Mattermost-inspired constrained store layers are absent. ADR-0019 permits timer(retry(sql)) with explicit ownership.

## Scope

- Implement timer wrappers for the bounded root/per-model store contracts.
- Measure operation name, duration, and outcome without logging sensitive arguments.
- Wire the layer at root composition and add conformance tests.

## Files or modules expected to change

server/store timer layer, root composition/metrics port, store conformance tests.

## Architectural rules and ADRs

Architecture Persistence and Observability; ADR-0010, ADR-0019.

## Acceptance criteria

- [x] The timer layer is behaviorally transparent and preserves errors/results.
- [x] Metrics contain no student data, credentials, tokens, answers, or secrets.
- [x] Layer construction is explicit at the root.

## Completion evidence

- Added a complete `store.Store` timer decorator, including every root and
  per-model contract, with stable accessor identity and exact forwarding of
  results, errors, lifecycle calls, and missing per-model stores.
- Restricted observations to a closed, argument-free operation vocabulary,
  `success`/`error` outcomes, and duration. The recorder cannot receive store
  arguments, results, context values, or error details, and recorder panics
  cannot alter persistence behavior.
- Added deterministic checked-in forwarding generation plus a currency test;
  behavioral timing remains handwritten and uses no reflection.
- Wired one timed persistence root in module-root composition for both SQL and
  test overrides, with the same decorated store shared by platform and
  application construction.
- Ran every reusable PostgreSQL store conformance suite through the timer
  decorator with `TestTimerLayerConformance`.
- Passed the focused timer/conformance command, the PostgreSQL-backed timer
  conformance suite, `go test -race ./store/...`, `go vet ./...`, and the full
  `go test ./...` server suite.

## Validation steps

- `cd server && go test ./store/... -run 'Timer|Conformance'`
- `cd server && go test -race ./store/...`
- `cd server && go test ./...`

## Risks

High-cardinality labels or sensitive values can leak. Use a closed operation vocabulary and outcome categories only.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
