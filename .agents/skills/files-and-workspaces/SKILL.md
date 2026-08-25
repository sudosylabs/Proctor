---
name: files-and-workspaces
description: Change managed file metadata or bytes, renditions, upload leases, profile pictures, Exam Resources, Starter Workspaces, Attempt Workspaces, content search, retention, legal holds, or physical purge.
---

# Change files and workspaces

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) first. Completion: File Entry,
   Revision, Rendition, Workspace Entry, Path, and Content Version keep their
   distinct identity and lifecycle meanings.
2. Read the relevant section of the
   [file and workspace reference](references/files.md). Completion: PostgreSQL
   metadata authority, VFS byte ownership, application purpose, visibility,
   upload state, and retention rules are explicit.
3. Keep semantic metadata, authorization, retention, and immutable selection in
   the server; use `packages/vfs` only for backend-neutral byte operations.
   Completion: object keys and mutable paths never become authorization or
   identity.
4. Stage fallible byte work before the named atomic metadata transition and
   reclaim losing objects only after unknown outcomes and durable references
   resolve. Completion: retries cannot expose partial or wrong-purpose content.
5. Update Store/VFS conformance, content processing, authorization, lease,
   retention, and race tests, then run `make -C server architecture`.
   Completion: metadata and physical content converge under success, replay,
   failure, and purge.
