# Dependencies

## Dependency direction

~~~text
model ← store ← app/job ← app
model ← app/realtime ← {app, websocket}
{model, store} ← app/exam ← app
app/exam/safemarkdown ← {app/exam/attempt, app/exam/review}
secretseal ← app
i18n ← templates ← cmd/mailpreview
app ← app/api
model ← filecontent
packages/vfs ← filecontent
app ← filecontent
{app, app/api, app/realtime, websocket, filecontent} ← server ← cmd/proctor
~~~

Infrastructure adapters sit to the side and point inward at their contracts. The root `server` package imports the components needed to assemble the graph.

| Package | Allowed production dependencies | Forbidden examples |
| --- | --- | --- |
| `model` | Standard library and narrowly justified domain libraries | `app`, HTTP, SQL, cluster, WebSocket |
| `store` | `model` | `sqlstore`, HTTP, application services |
| `app/job` | `model`, `store.JobStore`, standard library | parent `app`, transports, concrete adapters |
| `app/realtime` | `model`, standard library, consumer-owned ports | parent `app`, HTTP, WebSocket libraries, cluster adapters |
| `app/exam` | `model`, bounded `store` contracts, standard library, consumer-owned ports, and explicitly shared leaf packages such as `app/exam/safemarkdown` | parent `app`, transports, platform, concrete adapters |
| `app/exam/safemarkdown` | Standard library | model, store, parent `app`, transports, concrete adapters |
| `secretseal` | Standard library cryptography and encoding | model, persistence, configuration, transports, concrete adapters |
| `i18n` | Standard library and embedded server-owned locale catalogs | application, domain, persistence, transports, concrete adapters |
| `templates` | `i18n`, standard-library HTML and text templating | application, persistence, transport, mail adapters |
| `app` | `model`, `store`, `app/job`, `app/realtime`, `app/exam`, consumer-owned ports | `platform`, `app/api`, `sqlstore` |
| `filecontent` | `model`, consumer-owned `app` content contracts, `packages/vfs`, narrowly allowlisted content codecs | persistence, transports, platform service location, Jobs, configuration, concrete VFS backends |
| `app/api` | `app`, `model`, HTTP libraries | `store`, `sqlstore`, `platform` |
| `websocket` | `app`, `app/realtime`, `model`, WebSocket libraries | SQL and platform service location |
| concrete adapters | Their inward contracts and implementation libraries | Application policy |
| `server` | Construction dependencies | Business rules |
| `cmd/proctor` | Module-root `server` | Independent infrastructure construction |
| `cmd/mailpreview` | `templates`, `i18n`, standard library | application, persistence, infrastructure adapters, mail delivery |

Tests and `testlib` may cross production boundaries for verification. An architecture test enforces the production allowlist.

## Reusable capability boundaries

The reusable modules own portable infrastructure behavior; the server owns
product meaning and policy:

- `packages/vfs` owns backend-neutral file operations. The server owns semantic
  metadata, authorization, retention, and content policy. The server-owned
  `filecontent` module concentrates private semantic keys, bounded content
  processing, immutable rendition mechanics, exact reads, and physical purge
  over that reusable contract. Local VFS is for development or genuinely
  shared single-node storage; clustered production uses shared storage.
- `packages/cache` owns memory/Redis semantics, codecs, TTLs, conditional
  writes, and counters. The server owns namespaces, cache eligibility,
  invalidation, and security staleness. Cache is never durable state, an
  implicit message bus, or an unconstrained lock.
- `packages/mail` owns transport-neutral messages, MIME composition,
  validation, SMTP, and sender conformance. The server owns templates,
  localization, recipients, rate limits, retries, and durable delivery policy.
  Mail is not sent synchronously inside a durable business transaction.
- `secretseal` owns versioned AES-256-GCM envelopes, bounded key rings,
  authenticated purpose/owner binding, and safe cryptographic failures for
  recoverable server application secrets. It has no persistence or
  configuration dependency and is used directly as an in-process module, not
  hidden behind a replaceable cryptography port.
- `packages/guest` owns the exam-blind execution-host contract: readiness,
  ensure and revoke, tree projection, one PTY, freeze, capacity, and typed
  errors. The server owns the Execution Profile, placement, workspace
  acknowledgement, the Attempt Terminal bridge, and when a grant may exist.
  The Execution Host binary lives outside this repository and implements the
  server side of that contract. Isolation machinery stays there.

Identity, authorization, examinations, WebSockets, clustering, and MFA
remain server concerns until they have coherent Proctor-independent
contracts, plausible external consumers, and their own compatibility
policies.

## Rationale

- Conceptual transport/application/domain/persistence boundaries preserve
  cohesive packages without imposing a mechanical layer tree.
- Consumer-owned interfaces make each dependency narrow; the bounded
  `store.Store` family is grouped deliberately for shared conformance testing.
- Import tests make inward dependency direction enforceable instead of relying
  on prose or review memory.
