# 14 — Operate the acknowledged Attempt Workspace

**What to build:** Allow an actively participating candidate to work with a
private IDE-style logical hierarchy whose acknowledged current contents survive
reconnects, node loss, and S3's object-only storage model.

**Blocked by:** 12 — Admit a candidate and establish the first Participation; 13 — Enforce Participation renewal, Connection Loss, and re-allow.

**Status:** completed

- [x] List/read/create/replace/rename/move/delete operations use stable Workspace
      Entry identity and mutable normalized POSIX-relative paths; empty
      directories exist as PostgreSQL metadata only.
- [x] A code file has one current opaque Workspace Content Version rather than a
      retained general File Revision for every save; deletion removes it from
      the eventual Submission manifest while retaining required journals.
- [x] Every write validates the active Attempt, Open Sitting, current unfenced
      Participation generation and credential, and current Class membership;
      disconnected, Paused, Suspended, Closing, or Submitted state denies it.
- [x] There is no offline-authorized mutation path: the client protects local
      outcome-unknown work but only server-acknowledged state becomes authority.
- [x] A write stages a private opaque VFS object, conditionally swaps the
      PostgreSQL current pointer/version, then acknowledges; losing or unknown
      objects are reclaimed only after durable references and retry outcomes are
      resolved.
- [x] Attempt-scoped idempotent mutation keys and an ordered journal record safe
      entry identities, operation kind, resulting version, and Workspace Cursor
      without retaining every prior body.
- [x] The implementing slice selects and documents bounded entry, depth, path,
      per-file, total-workspace, request, and journal limits; traversal, links,
      devices, sockets, path collisions, and `.proctor` writes fail closed.
- [x] Object keys and backend revisions are path-independent and absent from
      models, authorization, DTOs, audit, logs, and realtime events.
- [x] Authorized managers cannot inspect live Workspace content; candidate
      access remains purpose-specific and offers no download/export/local-folder
      projection beyond the protected IDE experience.
- [x] VFS local/S3 conformance, rename without object move, concurrent writes,
      unknown commit replay, suspension fencing, cursor recovery, HTTP/realtime,
      race, multi-node, and integration tests pass.
