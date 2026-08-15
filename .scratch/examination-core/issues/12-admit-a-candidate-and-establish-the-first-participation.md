# 12 — Admit a candidate and establish the first Participation

**What to build:** Admit an eligible student to an Open Sitting, lazily create
their unique Exam Attempt and protected Workspace, and establish the first
fenced Participation generation and Connection.

**Blocked by:** 07 — Author the Draft Starter Workspace; 10 — Operate the Sitting lifecycle.

**Status:** completed

- [x] Every connection resolves current membership in the exact Sitting Class;
      membership is never snapshotted as permanent eligibility or selected by
      the student.
- [x] Missing current membership denies that connection without creating or
      deleting an Attempt; restored membership permits later admission only if
      Sitting and Attempt state otherwise allow it.
- [x] First eligible admission atomically creates exactly one Attempt under a
      Sitting/student uniqueness constraint, one private Workspace copied from
      the frozen Starter Workspace, generation one, and its first Connection.
- [x] Attempt remains the stable work identity and records its admission Revision
      for provenance; it is not a connection, session, or Participation row.
- [x] The Participation receives a random bounded continuity credential over TLS,
      stores only its hash, and records generation, start, initial 20-second
      PostgreSQL-time lease, renewal sequence, and active state.
- [x] One current candidate Connection is a durable child of the generation and
      its committed open/close produces a bounded manager event after commit.
- [x] Candidate admission is a current relationship-and-state decision, not a
      reusable role grant; Paused, Closing, Closed, Canceled, Suspended, or
      Submitted state denies admission with a safe reason.
- [x] Concurrent first connections converge on one Attempt, Workspace,
      Participation, and idempotent response without duplicating starter objects.
- [x] Candidate projections expose protected instructions/resources and
      Workspace bootstrap only through the exam application, never raw object
      keys, downloadable URLs, policy JSON, or credential hashes.
- [x] Store/VFS atomicity, membership transfer, concurrent admission,
      authorization, credential privacy, HTTP/WebSocket contract, manager
      realtime, multi-node, race, integration, and architecture tests pass.
