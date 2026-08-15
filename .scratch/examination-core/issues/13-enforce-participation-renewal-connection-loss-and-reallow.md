# 13 — Enforce Participation renewal, Connection Loss, and re-allow

**What to build:** Robustly detect confirmed connectivity loss through the
server-owned Participation lease, permanently fence the expired generation,
flag and suspend the Attempt, and allow an authorized manager to re-allow access
without erasing evidence.

**Blocked by:** 12 — Admit a candidate and establish the first Participation.

**Status:** completed

- [x] The privileged coordinator sends an authenticated application renewal
      approximately every 5 seconds; WebSocket ping and native-process health
      remain distinct and cannot renew the Participation lease.
- [x] Renewal carries generation and monotonic sequence and returns accepted
      sequence, authoritative database time, and 20-second expiry; duplicate
      sequences return the accepted outcome and stale sequences cannot extend it.
- [x] PostgreSQL time is authoritative and `expires_at <= database_now` is
      irreversibly expired; neither a late heartbeat nor another node can revive
      that generation.
- [x] A late renewal and a recurring 2-second expiry scan invoke the same named
      conditional Store operation so exactly one caller claims and completes the
      transition across application nodes.
- [x] Expiry atomically ends the Participation as `lease_expired`, closes its
      current Connection, creates neutral bounded Connection Loss evidence and
      one flag, opens a policy suspension, completes audit, and retains the
      idempotent outcome.
- [x] Failure to persist the complete end/evidence/flag/suspension result leaves
      the expired credential unusable, denies new admission, and retries without
      publishing a partial success or effects.
- [x] Connection Loss is always flag-and-suspend and cannot be disabled or
      weakened by an Exam Manager; one failed request or ping never triggers it.
- [x] The candidate receives a stable safe suspended/connection-loss reason, not
      internal lease/generation language or an accusation; managers receive
      safe post-commit flag, suspension, and Connection events.
- [x] Re-allow requires the exact active Suspension, expected Attempt revision,
      current management authority, and a private trimmed reason of 1–1,000
      characters; it closes only that episode and preserves all evidence.
- [x] Re-allow creates no credential or Participation; fresh authenticated
      admission creates the next generation, and another expiry produces a new
      generation-scoped flag/suspension episode.
- [x] Exact-boundary clocks, duplicate/stale renewal, multi-node claim, failed
      commit retry, installation-wide loss, re-allow races, security logging,
      HTTP/WebSocket, Job, race, and integration tests pass.
