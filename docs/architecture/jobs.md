# Durable jobs

## Scope and ownership

A Job is a finite, retryable unit of background work whose execution has
operational or product value to trace. Default-picture generation, content
extraction, mail delivery, exports, and bounded purge batches qualify.
Continuous runtime loops such as cluster heartbeats, WebSocket pings, and cache
reapers do not become Jobs merely because they run in the background.

`model.Job` owns stable intent and current lifecycle state. The append-only
`model.JobAttempt` records every execution claim and outcome so retries do not
erase evidence. `store.JobStore` owns atomic enqueue, claim, fencing,
checkpoint, terminal, and retention contracts. Application use cases own job
creation, authorization, cancellation, progress, and handler orchestration. A
root-owned runner invokes those application capabilities and type-specific
handlers; handlers call application use cases rather than manipulating
unrelated stores.

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

Recurring and delayed work uses `AvailableAt`. A stable deduplication key and
database unique constraint make one logical occurrence win even when every
node proposes it; scheduling does not require cluster leadership. Priority is
omitted until real workloads require explicit preemption.

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

Job Attempts use `Running`, `Succeeded`, `Failed`, `Canceled`, and
`LeaseExpired`. Lease expiry records lost ownership rather than inventing a
handler error. A handler returns one of `Succeeded`, `RetryableFailure`,
`PermanentFailure`, or `Canceled`. Transient dependency failures retry;
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

The composition root constructs one immutable instance-scoped handler
registry. Every descriptor declares supported payload versions, timeout,
per-node concurrency, retry/backoff, cancelability, retention, and handler.
Startup rejects duplicate types and missing handlers; there is no process-global
mutable registration.

The institution-scoped `job.view` action protects safe list, get, and attempt
history. `job.manage` protects cancellation and explicitly supported retry.
Neither permits arbitrary Job creation. The built-in system-administrator role
receives both through the closed action registry.

Succeeded and canceled Jobs and Attempts are initially retained for 30 days;
failed ones for 90 days. Queued and running work is never removed by retention
cleanup. Per-type policy may override these values, while security and domain
audit retention remains independent.

## Initial consumers and lifecycle

The first registered types are:

- `profile_picture.generate_default`, deduplicated by user ID;
- `profile_picture.reconcile_defaults`, a bounded recovery/backfill batch;
- `file.purge_expired_content`, a bounded cleanup of expired Upload Leases,
  partial renditions, and retention-eligible archived content; and
- `job.cleanup`, a daily bounded retention pass that cannot delete queued,
  running, or its own active work.

User-creation transactions enqueue individual generation atomically;
reconciliation remains the safety net. The runner starts after its mandatory
platform dependencies and drains before the store and VFS close. Readiness
requires those dependencies and a functioning runner, not an empty queue.
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
