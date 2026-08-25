---
name: exam-lifecycle
description: Change Exam ownership, authoring, publication, Resources, Starter or Attempt Workspaces, Sittings, Attempts, Participation, Submission, integrity evidence, review, or result release.
---

# Change examination behavior

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) first. Completion: Exam, Draft,
   Revision, Sitting, Attempt, Participation, Workspace, Submission, and Review
   meanings are not collapsed into transport or storage terms.
2. Read the relevant sections of the
   [examination lifecycle reference](references/examinations.md): authoring and
   publication; resources and starter material; Sitting and Participation;
   Workspace and Submission; or integrity and review. Completion: every
   affected state, actor, fence, deadline, and immutable snapshot is accounted
   for.
3. Keep lifecycle policy in `app/exam` and its justified children; cross-model
   atomicity belongs in named Store operations. Completion: transport, Jobs,
   VFS, execution hosts, and realtime adapters do not become lifecycle owners.
4. Order durable commits and audit before cache, cluster, realtime, execution,
   mail, or other effects. Completion: replay and effect loss cannot repeat or
   erase a lifecycle transition.
5. Update model, Store conformance, use-case, HTTP/OpenAPI, integration, and
   race tests affected by the transition. Completion: stale authority, expired
   leases, duplicate commands, and unauthorized actors fail closed.
6. Run the focused examination phase target plus `make -C server architecture`.
   Completion: the vertical slice agrees from domain through public contract.

Do not infer deferred behavior from nearby products or stale summaries. A new
lifecycle transition requires an explicit owner, authorization resource,
audit intent, idempotency policy, concurrency rule, and terminal/replay result.

## Current open decisions

Define any dedicated proctor-assignment role beyond Exam Managers, candidate
accommodations, review appeals, exact retention periods, and future
manager-controlled export or deletion policy before implementing them.
