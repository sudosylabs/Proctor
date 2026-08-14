# 08 — Publish immutable Exam Revisions

**What to build:** Allow an authorized Exam Manager to publish the complete
validated Draft as an immutable, numbered Exam Revision that future Sittings can
select without depending on later Draft edits.

**Blocked by:** 02 — Edit Exam Draft title and Markdown instructions; 03 — Configure the Draft Focus Loss policy; 06 — Manage protected Draft Exam Resources; 07 — Author the Draft Starter Workspace.

**Status:** completed

- [x] Publication validates title, instructions, complete typed policy,
      available resource revisions, Starter Workspace hierarchy/content, and
      every selected quota before any durable mutation.
- [x] The policy value is canonically serialized, hashed with SHA-256, and stored
      with its schema version and digest; JSONB field ordering is not the digest
      contract.
- [x] One immutable monotonically numbered Revision freezes exact title,
      instructions, policy bytes/digest, resource snapshots, Starter Workspace,
      publisher, publication time, base Revision, and publication kind.
- [x] One named atomic operation locks the Draft, rejects a stale Draft revision,
      writes the Revision, selects it as the future default, rebases the Draft,
      completes audit, and records the idempotent command outcome.
- [x] Publishing an unchanged Draft relative to its base returns a stable
      no-changes result and never manufactures redundant Revision history.
- [x] Published Revisions cannot be updated or deleted; later content changes
      produce another Revision and referenced VFS objects remain retained.
- [x] Current Manager plus role authority is rechecked immediately before
      publication; system override cannot bypass invalid content or archive state.
- [x] Safe effects contain Exam/Revision identity, number, policy digest,
      publication kind, and time only and occur strictly after commit.
- [x] Exact Get/list projections expose published metadata without leaking raw
      policy JSON, resource object keys, or starter source to unauthorized users.
- [x] Store concurrency, canonical-policy freshness, immutable-history,
      retention, audit/idempotency, HTTP/OpenAPI, race, architecture, and
      PostgreSQL/VFS integration tests pass.
