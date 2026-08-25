---
name: persistence
description: Design or change Proctor Store contracts, named atomic operations, SQL schema, migrations, cache/retry/timing layers, idempotent outcomes, or PostgreSQL/VFS authority boundaries.
---

# Change persistence

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) for domain records or persisted
   domain terminology. Completion: Store and schema names preserve the domain
   distinctions.
2. Identify the owning bounded Store contract and its complete absence,
   atomicity, concurrency, and race guarantees. Completion: the application can
   express its intent without SQL or a generic transaction callback.
3. Implement the adapter and update its reusable conformance suite. Completion:
   every supported adapter proves the same observable contract.
4. Preserve PostgreSQL as durable authority and order cache invalidation,
   cluster fan-out, realtime publication, VFS cleanup, and other transient
   effects after commit. Completion: loss or replay of an effect cannot erase
   the durable outcome.
5. Run the narrow Store tests, race tests where concurrency changed, migration
   tests for schema changes, and `make -C server architecture`. Completion: the
   contract, adapter, migration, and dependency boundaries agree.

## Contract vocabulary

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

SQL uses plural `snake_case` tables, `id` primary keys, `<entity>_id` foreign keys, meaning-specific `_at` columns, and deterministic constraint/index names. Vocabulary follows the repository `glossary` skill.

Go uses UTC `time.Time`, PostgreSQL uses `timestamptz`, and HTTP uses RFC 3339. Optional lifecycle events are nullable fields such as `archived_at`, not integer zero sentinels.

Normal serving connects to PostgreSQL and applies every pending forward
migration before constructing application consumers. Morph holds the named
PostgreSQL migration mutex for inspection and application, so concurrent node
starts serialize schema convergence; startup then validates the resulting
schema and fails closed on any connection, migration, unlock, or compatibility
error. The configured migration timeout bounds lock acquisition and each
migration statement; it is not a single wall-clock budget for the complete
migration set.
`proctor migrate status` and `proctor migrate up` remain operator tools for
inspection and deliberate pre-start convergence; rollback is never an
automatic startup action. Before the first supported release, migrations may
be rewritten or squashed and development databases recreated. That release
freezes the baseline; later changes are append-only expand/backfill/contract
migrations compatible with rolling node startup.

The baseline installs PostgreSQL's `pg_trgm` extension. Bounded directory
searches use its GIN operator classes for literal case-insensitive substring
matching; authorization predicates remain part of the same SQL statement and
query text never enters ordinary telemetry.

`Archive` is reversible removal from active use, `Disable` is reversible prevention from operating, and `Purge` is explicit irreversible removal. A soft archive is not named `Delete`.

Idempotent command outcomes are persisted atomically: explicitly retryable
client commands store the authenticated User, stable versioned operation,
digests of the bounded key and semantic request fingerprint, a bounded
versioned application outcome, the original audit identity, and expiry.
PostgreSQL transaction-scoped coordination serializes the complete namespace;
the named aggregate mutation, successful audit completion, and outcome insert
commit together. Replaying identical input returns the recorded outcome after
fresh authorization and audit; different input with the same live key is a
conflict. Raw keys, commands, and HTTP envelopes are not persisted.

The initial guarantee is at least 24 hours. Expired rows remain replayable
until the daily permanently deduplicated cleanup Job removes bounded pages;
only physical removal permits key reuse. Outcome cleanup is safe under
multiple nodes and uses the PostgreSQL clock.

## Examination persistence

The examination boundary is divided into cohesive Store contracts for Exam
authoring/publication, Sittings, Attempts and Participation, Workspace and
Submission, and Integrity and Review. They expose named atomic operations for
publication, Sitting scheduling/rescheduling/cancellation, live correction,
first connection, participation fencing, workspace mutation, submission
sealing, Sitting closure, suspension/re-allow, and review finalization. The
application never coordinates these guarantees through a raw transaction
callback.

Live correction separates fallible VFS transfer from atomic visibility. A
purpose-bound PostgreSQL stage reserves opaque file identities and an upload
lease before VFS writes; only a successfully verified rendition becomes Ready.
One named Store operation then locks the Exam and Sitting, resolves exact
idempotent replay, rechecks Open/Paused state and the PostgreSQL deadline,
validates every referenced Ready stage against the Sitting and current
Revision, writes the immutable `live_correction` Revision, consumes its stages,
retargets only that Sitting, completes safe audit, and persists the bounded
outcome in one transaction. The policy and Starter Workspace snapshots are
copied exactly from the base Revision, while the Draft, future default, other
Sittings, and attempt admission provenance remain unchanged. Pending,
superseded, and expired stage objects are never authoritative and are reclaimed
only after unknown outcomes and durable references are resolved. Reserving or
replaying a stage establishes a bounded cleanup-protection interval; purge
requires both physical command-outcome cleanup and expiry of that interval, so
an in-flight replay cannot lose its stage between the database result and VFS
work.

Fresh Sitting schedule mutations lock and recheck the active Exam, current
Manager relationship unless an explicit override was authorized, same-Exam
sealed Revision, active Class lineage, and full Academic Period containment.
PostgreSQL `statement_timestamp()` decides whether a new or changed start is
strictly future. The Sitting transition, audit completion, and small
idempotent outcome commit atomically; an exact replay resolves before stale or
current-relationship guards. Private cancellation rationale is retained in a
dedicated private column and is excluded from ordinary audit, event, and public
Sitting projections.

PostgreSQL owns Exam and Sitting state, logical workspace hierarchy, current
workspace-content selection, cursors and mutation journals, leases, policy
documents, evidence metadata, reviews, idempotent outcomes, and retention
eligibility. VFS owns opaque bytes only. Object keys are path-independent. A
workspace write stages a new object before a conditional database pointer
swap; losing and superseded objects are reclaimed only after unknown outcomes
and durable references are resolved. A Submission pins the acknowledged
entry/path/content-version manifest without copying the bytes.

Exam Policy Sets are bounded, versioned, strictly decoded typed documents.
Unknown kinds, versions, fields, duplicates, invalid combinations, and
oversized documents fail closed rather than being preserved as arbitrary
authoritative JSON. Publication stores a canonical typed serialization and its
SHA-256 digest; JSONB field ordering is never used as the digest contract.

Attempt Participation generations are retained under a unique Attempt and
generation identity with a hashed continuity credential, authoritative lease,
renewal sequence, start/end times, and end reason. PostgreSQL time decides
renewal and expiry. One named conditional operation owns the race between a
late renewal and any expiry worker and atomically completes the end,
Connection Loss evidence and Flag, suspension, and audit result. An expired
generation is unusable even while a failed completion is waiting for retry.

Until the first supported release, examination tables and constraints extend
the single version-1 PostgreSQL baseline. Development databases are recreated;
the feature must not grow a chain of pre-release migrations. The general
post-release append-only rule above remains unchanged.
