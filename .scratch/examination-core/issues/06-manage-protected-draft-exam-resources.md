# 06 — Manage protected Draft Exam Resources

**What to build:** Allow Exam Managers to attach and maintain bounded supporting
files on an active Draft while candidates can use them only through the
protected application experience and published history remains exact.

**Blocked by:** 01 — Create and retrieve an Exam Draft with safe shipped defaults.

**Status:** completed

- [x] An Exam Resource has stable identity, required display name, optional
      bounded Markdown description, explicit order, and one selected available
      immutable File Revision.
- [x] A Draft contains at most ten active resources and each upload is at most
      10 MiB; PDF, PNG, JPEG, WebP, UTF-8 text, Markdown, CSV, and JSON are the
      initial verified allowlist.
- [x] Executables, archives, macro-bearing documents, disk images, malformed
      media, and unavailable/quarantined revisions cannot become Draft resources.
- [x] Upload, replace, metadata edit, reorder, protected view, and removal are
      purpose-specific Exam operations rather than a generic owner/file API.
- [x] Replacing bytes creates a new immutable File Revision and preserves every
      revision pinned by published Exam history; removal affects only the Draft.
- [x] Candidate-facing access has no download/export/print/public-URL/local-path
      contract, while authorized protected rendering streams bounded bytes with
      private caching and exact content identity.
- [x] Every mutation requires current Manager plus role authority, active Exam,
      expected Draft revision, audit, and command idempotency.
- [x] VFS objects remain opaque and path-independent; PostgreSQL owns resource
      identity, order, selected revision, availability, and retention references.
- [x] Commit precedes safe authoring events and orphan cleanup handles failed or
      unknown uploads without making partial content visible.
- [x] Domain, content validation, Store/VFS conformance, replacement retention,
      authorization, HTTP/OpenAPI, integration, race, and security tests pass.
