---
name: durable-jobs
description: Add or change finite background Jobs, descriptors, payload versions, claiming, leases, checkpoints, retry, cancellation, progress, history, cleanup, recurring occurrence identity, or domain Job handlers.
---

# Change durable jobs

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) for domain Job payloads or
   outcomes. Completion: Job and Job Attempt remain distinct from Exam Attempt.
2. Read the relevant section of the
   [durable-job reference](references/jobs.md). Completion: natural identity,
   payload version, claim/lease fence, retry class, work budget, progress,
   cancellation, retention, and visibility are explicit.
3. Keep the generic engine in `app/job`; keep domain handlers beside their
   owning use cases and enter them through typed versioned documents.
   Completion: the engine imports neither parent application nor concrete
   infrastructure.
4. Commit domain state before completing the Job checkpoint/outcome according
   to the named contract. Completion: node loss, lease expiry, and retry cannot
   repeat a durable effect or lose resumable progress.
5. Add descriptor, Store conformance, multi-node claim, fencing, cancellation,
   cleanup, unknown-outcome, and handler tests, then run the relevant phase
   target and `make -C server architecture`.
