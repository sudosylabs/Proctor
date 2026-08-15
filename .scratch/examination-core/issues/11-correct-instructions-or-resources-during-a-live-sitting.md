# 11 — Correct instructions or resources during a live Sitting

**What to build:** Allow an Exam Manager to correct instructions or supporting
resources for one Open or Paused Sitting without mutating a published Revision,
changing security policy, or forcing candidates to rejoin.

**Blocked by:** 06 — Manage protected Draft Exam Resources; 08 — Publish immutable Exam Revisions; 10 — Operate the Sitting lifecycle.

**Status:** in-progress

- [x] A purpose-specific command starts from the Sitting's current Revision and
      accepts only bounded instructions and resource corrections plus a required
      manager reason and expected Sitting revision.
- [x] The operation creates a new immutable Revision rather than changing the
      current one and records publication kind as live correction with old/new
      Revision, actor, reason, and effective time.
- [x] New and current policy digests and Starter Workspace digests must match;
      schedule, eligibility, starter files, and security behavior cannot change
      through live correction.
- [x] One named Store operation atomically writes the correction Revision,
      retargets only the affected Open or Paused Sitting, completes audit, and
      records the idempotent outcome.
- [x] Other existing Sittings remain pinned to their selected Revision and
      future-default selection is a separate explicit choice rather than an
      accidental side effect.
- [ ] Attempts retain their admission Revision for provenance while current
      instructions/resources resolve through the Sitting and become visible
      without a new Attempt or Participation generation.
- [x] Resource replacement preserves older referenced File Revisions and failed
      uploads/commits cannot leave a Sitting pointing at unavailable bytes.
- [ ] Candidate refresh and manager events publish only safe Revision/Sitting
      identifiers after commit; missed events recover through authoritative Get.
- [x] Scheduled, Closing, Closed, Canceled, stale, no-op, unauthorized, policy or
      starter-changing corrections fail with stable bounded errors.
- [x] Atomic-retarget, history, VFS retention, concurrent correction,
      authorization, HTTP/OpenAPI, realtime, multi-node, race, and integration
      tests pass.
