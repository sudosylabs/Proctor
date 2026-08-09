# Persistence

`Store` is the canonical persistence term. Do not use `Repository`, `DAO`, `Manager`, or `Gateway` as synonyms.

Contracts return domain types or explicit store projection/aggregate results. SQL rows, nullable driver types, builders, handles, and column names stay private to `sqlstore`. Invalid rehydrated state is an internal integrity failure.

Lookup semantics:

- `Get`: missing data is `store.ErrNotFound`;
- `Find`: absence is expected and returned as `(value, found, error)`;
- `List`: no rows is an empty collection;
- `(nil, nil)` never means absence.

Cross-model transactions are named aggregate operations, such as bootstrap, enrollment transfer, or password-reset consumption. The application decides policy; the adapter owns locking, constraints, concurrency checks, and commit/rollback. Raw SQL transactions and generic `WithTransaction(func(Store))` callbacks do not cross into `app`.

Each named contract states its complete atomic and race guarantees so callers
and reusable conformance suites can verify the same behavior across adapters.

## Store layers

~~~text
application
    ↓
localcachelayer
    ↓
timerlayer
    ↓
retrylayer
    ↓
sqlstore
~~~

- `timerlayer` changes no semantics and measures total cache-miss latency,
  including safe retry; cache hits emit cache hit/miss metrics and bypass
  database timing.
- `retrylayer` stays nearest SQL for accurate transient-failure classification
  and retries only a handwritten allowlist of safe idempotent operations.
- `localcachelayer` initially caches only `AcademicPeriodStore.Get`, keyed by academic-period ID, with a 30-second TTL, bounded process-local capacity, defensive serialization copies, generation-guarded fills, local plus best-effort peer invalidation after successful mutations, and authoritative fallback after misses, failures, corrupt entries, lost invalidations, or expiry.
- Authorization, roles, account enablement, sessions, credentials, MFA, and token revocation are initially excluded from caching.

Application- or workflow-specific caching remains an explicit application
port rather than being hidden in a store decorator. Add low-risk reference
data only after measurement. A security-sensitive read requires a separate
reviewed contract for bounded staleness, revalidation, reliable invalidation,
and recovery before it can enter the allowlist.

Each layer implements the root store and wraps its sub-stores. Deterministic generated code forwards mechanical methods and is checked into Git; behavioral overrides remain handwritten. Reflection proxies are prohibited.

Constrained store layers are allowed only as described above. New layers and
overridden methods must be explicit, handwritten policy; reflection-based
interception is prohibited.

## Schema and migrations

SQL uses plural `snake_case` tables, `id` primary keys, `<entity>_id` foreign keys, meaning-specific `_at` columns, and deterministic constraint/index names. Vocabulary follows `CONTEXT.md`.

Go uses UTC `time.Time`, PostgreSQL uses `timestamptz`, and HTTP uses RFC 3339. Optional lifecycle events are nullable fields such as `archived_at`, not integer zero sentinels.

Normal serving validates schema compatibility and never migrates. Deployments run `proctor migrate` under a lock. Before the first supported release, migrations may be rewritten or squashed and development databases recreated. That release freezes the baseline; later changes are append-only expand/backfill/contract migrations.

`Archive` is reversible removal from active use, `Disable` is reversible prevention from operating, and `Purge` is explicit irreversible removal. A soft archive is not named `Delete`.

Idempotent command outcomes are persisted atomically: retryable client commands store principal, operation, request fingerprint, outcome, and expiry. Replaying identical input returns the recorded outcome; different input with the same key is a conflict.
