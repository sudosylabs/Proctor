# 09 — Schedule and cancel Exam Sittings

**What to build:** Allow Exam Managers to schedule a published Revision for one
exact Class, inspect and list those Sittings, adjust a not-yet-open schedule, and
cancel a Scheduled Sitting.

**Blocked by:** 04 — Browse and archive authorized Exams; 08 — Publish immutable Exam Revisions.

**Status:** completed

- [x] An Exam Sitting has stable identity, selected immutable Revision, exact
      Class, half-open schedule, lifecycle state, timestamps, and optimistic
      revision; schedule and Class never live on Exam Revision.
- [x] The selected Class's Programme lineage must belong to the Exam's exact
      Academic Unit and the complete Sitting interval must fit its Academic
      Period; authorization cannot override those structural rules.
- [x] Only a published Revision of the same active Exam can be scheduled and a
      Draft or foreign Revision is rejected deterministically.
- [x] Scheduled Sittings may change Class, Revision, start, or end through an
      expected-revision command; Open or later Sittings reject those mutations
      except the separately defined end extension.
- [x] Cancel is permitted only from Scheduled, requires a bounded manager reason,
      and retains the Sitting for audit rather than deleting it.
- [x] Get and bounded keyset list projections support Exam, Class, lifecycle, and
      time filters without in-memory authorization filtering or N+1 queries.
- [x] Every mutation requires current Exam Manager relationship plus role
      permission, command idempotency, atomic audit, and post-commit safe effects.
- [x] Candidate eligibility is not snapshotted at scheduling and no Attempt or
      Submission is fabricated for a roster member who never connects.
- [x] PostgreSQL constraints and named Store operations enforce lineage,
      interval, lifecycle, and concurrency invariants under races.
- [x] Domain transition, schedule boundary, authorization, pagination,
      HTTP/OpenAPI, Job registration readiness, integration, race, and
      architecture tests pass.
