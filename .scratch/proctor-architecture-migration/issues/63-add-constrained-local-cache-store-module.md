# 63 — Add the constrained local-cache store module

**Status:** completed
**Work classification:** Required
**Blocked by:** #62 Add the safe store retry module

**What to build:** Introduce disposable in-process caching only for explicitly cacheable store reads, with safe invalidation and current-state fallbacks.

## Purpose

Introduce disposable in-process caching only for explicitly cacheable store reads, with safe invalidation and current-state fallbacks.

## Background

The agreed chain is localcache(timer(retry(sql)), but PostgreSQL remains authoritative and cache correctness cannot depend on cluster delivery.

## Scope

- Identify and document the small allowlist of reconstructible read operations.
- Implement bounded TTL/size, defensive copying, invalidation, and miss fallback.
- Wire the layer outermost at root composition and prove missed cluster invalidation recovers.

## Files or modules expected to change

server/store local-cache layer, root composition, cache invalidation adapter, conformance/tests.

## Architectural rules and ADRs

Architecture Persistence, Cache, Clustering; ADR-0019, ADR-0026.

## Acceptance criteria

- [x] Only allowlisted disposable reads are cached.
- [x] Cached values cannot be mutated by consumers and expire within a bounded interval.
- [x] Authoritative security decisions recover safely after lost invalidation.

## Completion evidence

- Added a safe-by-default `localcachelayer` across the complete root and
  per-model `store.Store` surface. The sole handwritten cache allowlist entry
  is `AcademicPeriodStore.Get`; generated wrappers leave all other reads and
  mutations as authoritative pass-throughs.
- Added a bounded in-process byte cache with defensive copies, a 1,024-entry
  production capacity, and a conservative 30-second production TTL capped at
  five minutes by construction.
- Cached academic periods use stable ID keys and validated JSON snapshots so
  callers cannot mutate shared state. Corrupt entries, cache failures, misses,
  and expiry all fall back to the authoritative store.
- Handwritten successful-mutation overrides invalidate academic-period ID
  entries locally and publish a narrow best-effort cluster message. Peer
  handlers validate and idempotently apply duplicate delivery without
  rebroadcasting. A two-node-style test proves delivered invalidation refreshes
  immediately, while a peer that misses delivery serves stale reference data
  only until TTL expiry and then recovers from the authoritative store.
- Guarded cache-fill generations prevent a concurrent miss from reinserting an
  old snapshot after a successful mutation invalidates the entry.
- Kept authorization, role bindings, account state, sessions, credentials,
  MFA, and tokens entirely outside the allowlist. Focused pass-through tests
  and the existing application recovery suites prove current security state is
  re-read after lost best-effort cluster invalidation.
- Composed `localcachelayer(timerlayer(retrylayer(sqlstore)))` once at the
  module root. A graph test proves cache hits bypass timing and persistence,
  while cache misses traverse the existing timer/retry chain.
- Extended deterministic forwarding generation and currency tests for the
  local-cache topology, plus PostgreSQL-backed conformance coverage across all
  reusable store suites.
- Passed the focused local-cache/conformance tests, PostgreSQL-backed
  conformance, `go test -race ./store/...`, the authorization/session recovery
  tests, `go vet ./...`, and the full server test suite.

## Validation steps

- `cd server && go test ./store/... -run 'LocalCache|Conformance'`
- `cd server && go test -race ./store/...`
- `cd server && go test ./app/... -run 'Authorization|Session'`
- `cd server && go test ./...`

## Risks

Caching authorization state can extend stale access. Use conservative TTLs and re-read PostgreSQL at critical seams.

## Completion criteria

All acceptance criteria pass, the dependency-policy debt does not grow, the repository compiles, focused and full tests are green, and no unrelated behavior or architecture work is included.
