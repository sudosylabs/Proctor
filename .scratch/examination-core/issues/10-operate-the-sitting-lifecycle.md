# 10 — Operate the Sitting lifecycle

**What to build:** Make scheduled Sittings open and reach their effective
deadline durably while authorized managers can pause, resume, extend, or close
them under explicit lifecycle rules.

**Blocked by:** 09 — Schedule and cancel Exam Sittings.

**Status:** completed

- [x] The implemented state machine is Scheduled to Open, Open to/from Paused,
      Open/Paused to Closing, Closing to Closed, and Scheduled to Canceled; no
      undocumented transition is accepted.
- [x] Atomically queued delayed work, backed by permanently deduplicated daily
      recovery, opens due Sittings using PostgreSQL time, opens late before the
      end without moving the deadline, and cancels an entirely elapsed schedule
      as `schedule_elapsed`.
- [x] Pause immediately blocks new Attempts, workspace mutation, execution, and
      submission while retaining protected read-only presentation and integrity
      monitoring.
- [x] Resume restores only the capabilities allowed by current Attempt and
      Participation state; the frozen version-1 policy has no pause-extension
      rule, so paused duration never moves `ScheduledEndAt`.
- [x] After opening, managers may only extend—not shorten—the effective end and
      every operator transition requires expected Sitting revision and a bounded
      reason where required.
- [x] Effective deadline or authorized early close enters Closing and denies new
      participation/mutation immediately; a zero-Attempt Sitting can complete
      Closed in this slice.
- [x] Jobs and manager commands call application use cases with explicit system
      or user invocation, named atomic Store operations, audit, idempotency, and
      commit-before-effect ordering.
- [x] Restart and multi-node races converge on one transition and one published
      outcome without leader election or reliance on cluster delivery.
- [x] Safe candidate and manager events contain only Sitting identity, state,
      revision, reason code, effective deadline, and time.
- [x] Clock-boundary, pause accounting, recovery, multi-node, authorization,
      HTTP/OpenAPI, Job, race, integration, and architecture tests pass.
