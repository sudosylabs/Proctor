---
name: user-settings
description: Change the portable User Settings Document, format negotiation, validation, revisions, persistence, idempotency, concurrency, self-service HTTP behavior, privacy, or derived client presentation settings.
---

# Change user settings

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) first. Completion: the User
   Settings Document remains user-owned presentation source, not deployment
   configuration, Exam workspace content, or permission state.
2. Read the relevant section of the
   [user-settings reference](references/user-settings.md). Completion: exact
   bytes, supported format/version, validation, revision, concurrency,
   idempotency, derived behavior, and opaque unsupported-format handling are
   explicit.
3. Preserve the exact accepted source while deriving only bounded typed
   behavior. Completion: unknown content is not silently rewritten, executed,
   logged, or treated as authorization.
4. Use named Store operations for replace/replay and publish only a private
   refetch hint after commit. Completion: two nodes converge without exposing
   document content through realtime or audit.
5. Update parser, Store conformance, HTTP/OpenAPI, rollback, multi-node,
   idempotency, privacy, and compatibility tests, then run the focused server
   and architecture checks.
