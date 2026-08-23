# Dependencies

## Dependency direction

~~~text
identityprovider ← {model, config}
model ← store ← app/job
{model, store, app/job, app/mail, app/exam} ← app/jobs ← app
model ← app/realtime ← {app, websocket}
{model, store} ← app/exam ← app
{model, store} ← app/execution ← app
{model, store, secretseal, app/exam, localization} ← app/mail ← app
localization ← {httpapi, websocket}
app/exam/safemarkdown ← {app/exam/attempt, app/exam/review}
secretseal ← app
{model, localization, app/mail} ← cmd/mailpreview
{localization, app/mail, cmd/proctor/commands, httpapi, websocket} ← cmd/ptool
logging ← platform
{config, store/timerlayer, store/localcachelayer} ← metrics ← server
app ← httpapi
model ← filecontent
packages/vfs ← filecontent
app ← filecontent
{app, httpapi, app/realtime, websocket, filecontent} ← server ← cmd/proctor/commands ← cmd/proctor
execenv ← executionhost ← server
internal/autocert ← server
~~~

Infrastructure adapters sit to the side and point inward at their contracts. The root `server` package imports the components needed to assemble the graph.

The physical layout reflects that graph: `httpapi` sits beside `app` because it
is an outer adapter depending inward on application capabilities. Packages
inside `app/` are application-owned modules, not transports.

| Package | Allowed production dependencies | Forbidden examples |
| --- | --- | --- |
| `identityprovider` | Standard library | Domain models, deployment configuration, application, persistence, transports |
| `model` | Standard library, `identityprovider`, and narrowly justified domain libraries | `app`, HTTP, SQL, cluster, WebSocket |
| `config` | Standard library, `identityprovider`, and narrowly scoped IDNA hostname validation | Domain models, application, persistence, transports |
| `store` | `model` | `sqlstore`, HTTP, application services |
| `app/job` | `model`, `store.JobStore`, standard library | parent `app`, concrete Jobs, transports, concrete adapters |
| `app/jobs` | `model`, bounded `store` contracts, `app/job`, leaf `app/mail` and `app/exam` capabilities, `secretseal`, standard library | parent `app`, `store.Catalog`, transports, platform, concrete adapters |
| `app/realtime` | `model`, standard library, consumer-owned ports | parent `app`, HTTP, WebSocket libraries, cluster adapters |
| `app/exam` | `model`, bounded `store` contracts, standard library, consumer-owned ports, and explicitly shared leaf packages such as `app/exam/safemarkdown` | parent `app`, transports, platform, concrete adapters |
| `app/execution` | `model`, the bounded Execution Grant store, standard library, and consumer-owned host/content ports | parent `app`, execenv, transports, platform, concrete adapters |
| `app/exam/safemarkdown` | Standard library | model, store, parent `app`, transports, concrete adapters |
| `app/mail` | `model`, bounded `store` mail records, `secretseal`, `localization`, the Exam Manager preparation contract, standard-library templating, and consumer-owned sending ports | parent `app`, transports, platform, SQL, configuration, concrete adapters |
| `secretseal` | Standard library cryptography and encoding | model, persistence, configuration, transports, concrete adapters |
| `localization` | Standard library, caller-supplied catalog filesystems, and the CLDR-aware localization engine | application, domain, persistence, transports, concrete adapters |
| `logging` | Standard library and the hidden logging engine/target implementation | application, domain, persistence, transports, global logger state |
| `metrics` | `config`, narrow store recorder contracts, standard library, and Prometheus client libraries | application policy, platform service location, concrete infrastructure adapters, global Prometheus registry |
| `app` | `model`, `store`, `app/job`, `app/jobs`, `app/realtime`, `app/exam`, `app/mail`, consumer-owned ports | `platform`, `httpapi`, `sqlstore` |
| `filecontent` | `model`, consumer-owned `app` content contracts, `packages/vfs`, narrowly allowlisted content codecs | persistence, transports, platform service location, Jobs, configuration, concrete VFS backends |
| `httpapi` | `app`, `model`, `localization`, HTTP libraries | `store`, `sqlstore`, `platform` |
| `websocket` | `app`, `app/realtime`, `model`, `localization`, WebSocket libraries | SQL and platform service location |
| concrete adapters | Their inward contracts and implementation libraries | Application policy |
| `executionhost` | `app/execution` ports, execenv, standard-library TLS and certificate loading | persistence, application policy, transports |
| `internal/autocert` | Standard library, `x/crypto/acme`, and `x/net/idna` | Product policy, application, persistence, transports, unrelated third-party libraries |
| `server` | Construction dependencies | Business rules |
| `cmd/proctor` | `cmd/proctor/commands` and standard-library process lifecycle | Server, application, persistence, and concrete infrastructure |
| `cmd/proctor/commands` | Module-root `server`, `localization`, Cobra, and standard-library presentation concerns | Application packages, persistence, platform, and independent infrastructure construction |
| `cmd/mailpreview` | `model`, `localization`, `app/mail`, standard library, and repository source assets | parent application, persistence, infrastructure adapters, mail delivery |
| `cmd/ptool` | `localization` plus the consumer-owned localization definition registries in `app/mail`, `cmd/proctor/commands`, `httpapi`, and `websocket` | runtime composition and concrete infrastructure construction |

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
  The `app/mail` child module concentrates server-owned composition,
  the complete definition registry, semantic preparation operations,
  suppression, shared payload freezing/encryption, stable routing metadata,
  closed typed rendering, and family-specific meaning. Parent use cases
  consume narrow preparation ports using the child package's contracts
  directly and persist the result through named aggregate transactions. Mail
  is not sent synchronously inside a durable business transaction.
- `secretseal` owns versioned AES-256-GCM envelopes, bounded key rings,
  authenticated purpose/owner binding, and safe cryptographic failures for
  recoverable server application secrets. It has no persistence or
  configuration dependency and is used directly as an in-process module, not
  hidden behind a replaceable cryptography port.
- [`github.com/sudosylabs/execenv`](https://github.com/sudosylabs/execenv)
  owns the exam-blind execution-host contract: readiness, ensure and revoke,
  tree projection, one PTY, freeze, capacity, and typed errors. The server
  owns the Execution Profile, placement, workspace acknowledgement, the
  Attempt Terminal bridge, and when a grant may exist. The Execution Host
  binary lives in the execenv repository and serves that contract.
  Isolation machinery stays there. This monorepo requires the module; it
  does not vendor Firecracker.

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
