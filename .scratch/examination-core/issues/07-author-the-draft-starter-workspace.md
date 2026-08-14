# 07 — Author the Draft Starter Workspace

**What to build:** Allow Exam Managers to construct the optional logical code
and directory hierarchy that publication freezes and first candidate admission
copies into a private Attempt Workspace.

**Blocked by:** 01 — Create and retrieve an Exam Draft with safe shipped defaults.

**Status:** completed

- [x] Starter Workspace is a distinct examination-owned hierarchy, never an
      Exam Resource, shared candidate workspace, or generic File Revision chain.
- [x] Stable entry identity is separate from a normalized, bounded,
      case-sensitive POSIX-relative path; directories are PostgreSQL metadata
      and do not imply VFS/S3 directory objects.
- [x] Traversal, absolute paths, empty segments, reserved `.proctor` paths,
      symlinks, devices, sockets, path collisions, and invalid parent moves fail
      before publication.
- [x] Managers can list, create, rename, move, replace, and remove starter files
      and directories through purpose-specific operations with expected Draft
      revision and command idempotency.
- [x] Code-file content uses one current opaque object and content version rather
      than creating a retained File Revision for every edit; replacement and
      cleanup are safe across unknown commits.
- [x] The implementing slice selects and documents explicit bounded entry,
      depth, path, per-file, and total-workspace limits; zero or unlimited
      defaults are prohibited.
- [x] PostgreSQL owns hierarchy, identity, current object selection, checksums,
      media/size metadata, and mutation outcomes while VFS owns opaque bytes.
- [x] Archived Exams, stale Draft revisions, missing Manager relationship, or
      lost role permission deny mutation without leaking or partially publishing
      content.
- [x] Audit and realtime projections contain safe IDs, revisions, operation kind,
      and time but never logical paths, source code, objects, or checksums.
- [x] Hierarchy invariants, conditional replacement, VFS failure recovery,
      Store conformance, HTTP/OpenAPI, race, architecture, and backend tests pass.
