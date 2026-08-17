# Durable jobs

## Scope and ownership

A Job is a finite, retryable unit of background work whose execution has
operational or product value to trace. Default-picture generation, content
extraction, mail delivery, exports, and bounded purge batches qualify.
Continuous runtime loops such as cluster heartbeats, WebSocket pings, and cache
reapers do not become Jobs merely because they run in the background.
When a bounded periodic maintenance pass shares the Job engine's lifecycle, it
is an immutable runtime task rather than a descriptor or recurrence: it runs
without overlap, stops with the engine, reports transient failures, and creates
no Job, Job Attempt, occurrence, or permanent deduplication-ledger row. The
named domain Store operation remains responsible for cross-node convergence.

`model.Job` owns stable intent and current lifecycle state. The append-only
`model.JobAttempt` records every execution claim and outcome so retries do not
erase evidence. `store.JobStore` owns atomic enqueue, claim, fencing,
checkpoint, terminal, and retention contracts. Application use cases own job
creation, actor-sensitive cancellation authorization, progress meaning, and
type-specific handlers. The application constructs the `app/job` engine from
those domain handler adapters and immutable recurrence definitions. The
module-root server then owns the engine's start and close order as part of the
runtime lifecycle. Root ownership therefore means lifecycle ownership, not
that the root defines Job types or application policy. Handlers call
application use cases rather than manipulating unrelated stores.

## Delivery and claiming

Job execution is at-least-once. Handlers are idempotent and external effects
use the durability mechanism appropriate to their contract; a claim is never
treated as exactly-once execution.

All nodes may propose and execute work. The primary PostgreSQL database
atomically claims a bounded selection ordered by availability and creation
time, using row locking such as `FOR UPDATE SKIP LOCKED`. A claim creates a Job
Attempt with worker node ID, an unguessable claim token, heartbeat time, and
lease expiry. Heartbeat, checkpoint, and terminal updates require the current
token. An expired lease is reclaimable, and a former worker cannot commit job
state after losing its fence.

A handler may relinquish an immutable command when the claiming node lacks a
required deployment capability but another node can execute it. Relinquishment
records an `Incompatible` Attempt, requeues with the descriptor's bounded
backoff, restores the consumed failure-attempt budget, and excludes that stable
node identity from claiming the same Job again. This prevents a mixed-version
or mixed-key node from hot-looping, starving a capable peer, or terminating
shared work. If no capable node exists, the Job remains durably queued and the
incompatible Attempt is operator-visible; it does not silently succeed or
exhaust its failure policy.

Recurring and delayed work uses `AvailableAt`. A stable deduplication key and
database unique constraint make one logical occurrence win even when every
node proposes it; scheduling does not require cluster leadership. Priority is
omitted until real workloads require explicit preemption.

Deduplication policy is persisted with each Job rather than inferred from its
type. Per-resource repair work uses `active` deduplication, so a new Job may be
created after the prior one reaches a terminal state. Date-keyed maintenance
uses `permanent` deduplication, so a terminal outcome cannot cause another node
or process restart to repeat that day's occurrence. This distinction preserves
repairability without weakening the scheduler's one-occurrence invariant. A
small occurrence ledger retains permanent keys after expired execution history
is removed; it contains no command, checkpoint, result, or error payload.

## State and retry

Jobs use `Queued`, `Running`, `CancelRequested`, `Succeeded`, `Failed`, and
`Canceled`. A retryable failure returns the Job to `Queued` with a future
availability time and increments its attempt count. Exhausting the type's
retry policy produces `Failed`. There is no ambiguous warning terminal state
or sentinel progress value.

Each Job Type declares timeout, concurrency, retry/backoff, cancelability,
visibility, and retention. Cancellation is cooperative: queued work may cancel
immediately; running work records the request and cancels its context after the
worker observes it. A non-cancelable atomic commit section completes, and the
fenced terminal operation resolves races without undoing committed domain
state.

Progress is optional `Current`, `Total`, and a closed safe Stage code. A
percentage is derived only when the total is known. Bounded jobs checkpoint a
cursor and counts so a retry can resume safely.

Job Attempts use `Running`, `Succeeded`, `Failed`, `Canceled`, `LeaseExpired`,
and `Incompatible`. Lease expiry records lost ownership rather than inventing
a handler error, while Incompatible records a safe capability mismatch that
does not consume the failure-attempt budget. A handler returns one of
`Succeeded`, `RetryableFailure`, `Relinquished`, `PermanentFailure`, or
`Canceled`. Transient dependency failures retry;
invalid payload versions, rejected content, invariant failures, and unsupported
operations fail permanently. Panics are contained and retry with a safe code
until the attempt limit is exhausted.

## Payloads and visibility

Job Types form a closed registry. Each owns versioned, typed contracts for its
immutable command, mutable checkpoint, safe result, and public error code.
Persistence may use bounded JSONB, but arbitrary maps are not an application
contract. Payloads, checkpoints, raw errors, file content, credentials, and
student work are never copied wholesale into logs, audit records, realtime
events, or generic API responses.

System administrators receive allowlisted list, get, attempt-history, and
cancel operations. There is no generic API for creating arbitrary Job Types.
Domain-facing work may later expose status through its owning resource.
PostgreSQL is authoritative; general realtime Job events are not required for
the first slice.

Canonical safe Job and Job Attempt projections live with the `app/job` engine
and omit raw commands, checkpoints, result documents, deduplication keys,
claim ownership, tokens, and internal errors. Parent `app` names are stable
aliases for transport consumers. The engine owns descriptor visibility,
cursor validation, persistence reads, and descriptor-governed cancel/retry
transitions. Application use cases still authorize the Principal, resolve the
institution resource, create the fail-closed audit attempt, and translate
engine/store failures. Cancellation is therefore actor-sensitive in the
parent application even though descriptor checks and the cooperative state
transition belong to the engine. An opaque prepared control target carries the
engine's validated transition and revision across that audit boundary without
exposing persistence mechanics.

The composition root constructs one immutable instance-scoped handler
registry. Every descriptor declares supported payload versions, timeout,
per-node concurrency, retry/backoff, cancelability, retention, and handler.
Startup rejects duplicate types and missing handlers; there is no process-global
mutable registration.

Daily recurrence definitions are immutable constructor input to the same
engine. Application slices own each recurrence name, typed command, stable
date-keyed identity, deduplication policy, and bounded work definition. The
engine owns UTC timing, bounded retry of transient proposal failures, the
post-proposal local wake, and recurrence shutdown. Every node may propose; the
permanent PostgreSQL occurrence ledger, not process memory or leadership,
decides whether the logical occurrence is new.

The institution-scoped `job.view` action protects safe list, get, and attempt
history. `job.manage` protects cancellation and explicitly supported retry.
Neither permits arbitrary Job creation. The built-in system-administrator role
receives both through the closed action registry.

Succeeded and canceled Jobs and Attempts are initially retained for 30 days;
failed ones for 90 days. Queued and running work is never removed by retention
cleanup. Per-type policy may override these values, while security and domain
audit retention remains independent.

## Initial consumers and lifecycle

The initial registered work covers:

- `profile_picture.generate_default`, deduplicated by user ID;
- `profile_picture.reconcile_defaults`, a bounded recovery/backfill batch;
- `file.purge_expired_content`, a bounded cleanup of expired Upload Leases,
  partial renditions, and retention-eligible archived content;
- delayed and recovery-driven Exam Sitting lifecycle work, plus bounded
  non-cancelable sealing of Closing Sittings; and
- `job.cleanup`, a daily bounded retention pass that cannot delete queued,
  running, or its own active work.

Entering Closing atomically queues the sealing Job with the Sitting mutation.
Its command contains only the Sitting identity, its checkpoint contains a
stable Attempt cursor plus safe counts, and each claimed Attempt is sealed in a
separate idempotent aggregate transaction before checkpointing. Work
reservations are occurrence-wide and survive lease loss. A reservation ahead
of the checkpoint is therefore burned as uncertain work on retry; it is never
reused to exceed the cap. A permanently deduplicated successor continues after
1,000 units, while the ordinary daily lifecycle recovery occurrence recreates
missing or failed Closing work. Job history follows the same bounded retention
rules as the other lifecycle types.

User-creation transactions enqueue individual generation atomically;
reconciliation remains the safety net. The engine starts after its mandatory
platform dependencies and drains before the store and VFS close. Readiness
requires those dependencies and a functioning engine, not an empty queue.
Shutdown stops new claims, cancels or drains work under a deadline, and leaves
unfinished leases to expire for another node.

Workers discover work through bounded, jittered primary-database polling and
an in-process wake signal after local enqueue. Database polling remains the
cross-node authority; PostgreSQL notifications are added only after measured
need. Initial descriptors use a 60-second lease, 15-second heartbeat, and
bounded exponential backoff with jitter. Default generation allows eight
attempts; reconciliation and cleanup batches allow five. Operational overrides
remain per type rather than one unsafe global policy.

Installation bootstrap, local account creation, external-provider provisioning,
and future imports commit the User and deduplicated default-generation Job in
one named aggregate operation. A missing-default read renders the deterministic
fallback and enqueues work; GET never synchronously persists content.
