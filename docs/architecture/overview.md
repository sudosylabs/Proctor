# Overview

This is the entry point to the Proctor Server architecture. It defines why the boundaries exist and what the system is.

Domain language is defined in [`CONTEXT.md`](../../CONTEXT.md). Durable
trade-offs are folded into the topic files under this directory. Current state
and open choices live in [`docs/project/status.md`](../project/status.md), while
[`AGENTS.md`](../../AGENTS.md) contains the always-loaded repository guardrails.

## Principles

1. Transport, application, domain, and persistence are conceptual boundaries, not mandatory layer-named directory trees.
2. Dependencies point inward. Business policy does not depend on HTTP, WebSocket, PostgreSQL, Redis, SMTP, VFS, Memberlist, or concrete adapters.
3. The module-root `server` package is the sole composition root and the only place that selects infrastructure.
4. Interfaces are small and consumer-owned. Store contracts are the deliberate exception and live together in `store`.
5. `model` contains domain language, not wire, database, or infrastructure contracts.
6. PostgreSQL is authoritative. Caches and cluster messages are disposable accelerators.
7. Transport establishes an authenticated invocation; application use cases perform resource authorization.
8. Failures remain transport-neutral until the edge and expose only explicitly safe details.
9. Packages and abstractions require stable ownership, not merely repeated code or symmetry.
10. Tests enforce boundaries that prose describes.

## Module boundaries

| Module | Responsibility |
| --- | --- |
| `packages/cache` | Portable memory and Redis cache behavior |
| `packages/mail` | Transport-neutral mail and SMTP delivery |
| `packages/vfs` | Portable file operations and storage backends |
| `server` | Proctor-specific domain, application, transports, persistence, and runtime |

Reusable modules never import Proctor Server. Identity, authorization, academics, examinations, WebSocket, and clustering remain in `server`. Extract another module only when it has a Proctor-independent contract, plausible external consumers, its own compatibility policy, and no server imports. The accepted extraction is [`github.com/sudosylabs/execenv`](https://github.com/sudosylabs/execenv), which also owns the Execution Host daemon. This monorepo will require that module; it will not contain it. See [Execution environments](./execution.md).

The server is not promised as a reusable Go library, but direct readable package paths are retained. A broad `internal/` tree is not used as a substitute for deliberate exports and import tests.

## Target structure

~~~text
server/
├── server.go                 # package server: runtime and composition
├── infrastructure.go        # cohesive root construction helpers
├── cmd/proctor/              # thin CLI boundary
├── model/                    # cohesive domain types and invariants
├── app/                      # commands, queries, policy, orchestration
│   ├── mail/                 # transactional-mail meaning and rendering
│   └── api/                  # HTTP routes, DTOs, handlers, mappings
├── websocket/                # hub and versioned WebSocket protocol
├── cluster/
│   ├── local/                # single-node/test adapter
│   └── memberlist/           # peer-to-peer multi-node adapter
├── store/
│   ├── sqlstore/             # PostgreSQL adapter
│   ├── localcachelayer/      # constrained read cache
│   ├── timerlayer/           # store timing
│   ├── retrylayer/           # allowlisted safe retries
│   └── storetest/            # conformance suites
├── platform/                 # infrastructure lifecycle and health
├── config/
├── i18n/                     # flat locale-catalog data only
├── localization/             # general catalog validation and lookup
├── templates/                # mail presentation assets and build tooling only
├── logging/                  # bounded asynchronous operational logging
├── migrations/
└── testlib/
~~~

This is a destination, not permission for a bulk move. Create a package only when working code has a stable responsibility to inhabit it.

## Upstream reference and provenance

[Mattermost](https://github.com/mattermost/mattermost) is a behavioral and
eligible implementation reference, especially for lifecycle, errors,
authentication, sessions, authorization, WebSockets, cache invalidation, and
clustering. It is not a directory template.

Direct or substantial source adaptation is chosen only when the behavior and
architecture fit Proctor better than a fresh implementation. The exact
upstream revision, path, license, notices, and Proctor-specific modifications
are recorded in the applicable tracked notice at the time of adaptation.
Conceptual reference does not justify wholesale subsystem copying, and
commercial or Source Available code requires explicit permission.
