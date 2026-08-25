---
name: http-api
description: Add or change Proctor HTTP routes, handlers, DTOs, credential or assurance declarations, Problem Details, validation, WebSocket protocol behavior, replay, compatibility, uploads, downloads, or transport mappings.
---

# Change HTTP or realtime transport

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) when public domain language or
   shapes change. Completion: wire names preserve canonical meaning without
   leaking internal model or Store types.
2. Read the exact [`httpapi` contract](../../../server/httpapi/CONTRACT.md) and
   the relevant [transport reference](references/transport.md). For OpenAPI
   source changes, also invoke [`$openapi-authoring`](../openapi-authoring/SKILL.md).
   Completion: credential, assurance, CSRF, idempotency, validation, error,
   compatibility, and streaming requirements are explicit.
3. Keep handlers thin: decode and bound input, establish immutable invocation,
   call one application use case, and map only safe output. Completion:
   resource authorization, transactions, and infrastructure selection remain
   outside transport.
4. Declare every route's accepted credential and assurance requirement
   explicitly. Completion: cookie, bearer, desktop, WebSocket, and public
   routes cannot inherit accidental authentication behavior.
5. Update route, mapping, Problem Details, OpenAPI agreement, upload/download,
   WebSocket, and concealment tests, then run the focused HTTP/OpenAPI checks
   and `make -C server architecture`.

## Current open decision

Choose whether cross-node WebSocket reconnection transfers bounded replay
queues or always performs authoritative HTTP resynchronization before changing
the current replay contract.
