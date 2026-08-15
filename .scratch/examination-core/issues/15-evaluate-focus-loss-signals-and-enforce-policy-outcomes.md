# 15 — Evaluate Focus Loss signals and enforce policy outcomes

**What to build:** Accept bounded trusted-client Focus Loss observations, apply
the frozen Exam policy using server receipt time, retain useful evidence without
database flooding, and carry out the configured flag, warning, or suspension.

**Blocked by:** 03 — Configure the Draft Focus Loss policy; 12 — Admit a candidate and establish the first Participation; 13 — Enforce Participation renewal, Connection Loss, and re-allow.

**Status:** completed

- [x] When Focus Loss is disabled, the Sitting projection tells the client not
      to collect or transmit it; an unexpected signal records only a bounded
      diagnostic and cannot invent a flag.
- [x] One authenticated claim contains current Participation generation,
      monotonic sequence, duration in integer milliseconds, and optional bounded
      source classification—never client-selected outcome, severity, or guilt.
- [x] Server receipt time drives the rolling window; duration equal to the
      configured minimum qualifies, while client clocks remain evidence only.
- [x] Duplicate sequence returns its prior accepted result, stale or fenced
      sequence is rejected, and a forward gap records uncertainty without
      inventing missing incidents.
- [x] The threshold fires immediately when the configured incident count falls
      inside the rolling window, consumes that evaluation bucket, and starts a
      fresh count rather than waiting for the window to expire.
- [x] At most one open flag exists per Attempt, policy kind, and Participation
      generation; later crossings append evidence rather than flooding flags.
- [x] Each kind/generation stores at most 100 qualifying episodes, then retains
      bounded overflow count, first/last receipt times, and maximum duration.
- [x] Flag creates evidence and manager notification; flag-and-warn additionally
      sends one stable safe candidate warning per generation without requiring
      acknowledgement; flag-and-suspend atomically opens a policy Suspension.
- [x] Re-allow resets only the causal evaluation window and does not erase flags
      or evidence; a later generation can enforce again independently.
- [x] Named atomic operations, audit, idempotent signal outcomes, commit-before-
      effect ordering, policy-boundary cases, gaps, concurrency, HTTP/WebSocket,
      manager events, race, and integration tests pass without retaining source
      code, clipboard, screenshots, terminal output, or arbitrary payloads.
