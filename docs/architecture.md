# Proctor Server Architecture

This is the canonical developer guide to Proctor Server's target architecture. It defines ownership, dependency direction, naming, error flow, testing, and structural conventions. The current implementation predates some decisions; see [Migration gaps](#migration-gaps).

Domain language is defined in [`CONTEXT.md`](../CONTEXT.md). Durable trade-offs are recorded in [`docs/adr`](./adr/). `AGENTS.md` summarizes current state, security rules, workflow, and implementation status.

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

Reusable modules never import Proctor Server. Identity, authorization, academics, examinations, WebSocket, and clustering remain in `server`. Extract another module only when it has a Proctor-independent contract, plausible external consumers, its own compatibility policy, and no server imports.

The server is not promised as a reusable Go library, but direct readable package paths are retained. A broad `internal/` tree is not used as a substitute for deliberate exports and import tests.

## Target structure

~~~text
server/
├── server.go                 # package server: runtime and composition
├── infrastructure.go        # cohesive root construction helpers
├── cmd/proctor/              # thin CLI boundary
├── model/                    # cohesive domain types and invariants
├── app/                      # commands, queries, policy, orchestration
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
├── mlog/
├── migrations/
└── testlib/
~~~

This is a destination, not permission for a bulk move. Create a package only when working code has a stable responsibility to inhabit it.

## Dependency direction

~~~text
model
  ↑
store
  ↑
app
  ↑          ↑
app/api   websocket
   ↖        ↗
      server
        ↑
   cmd/proctor
~~~

Infrastructure adapters sit to the side and point inward at their contracts. The root `server` package imports the components needed to assemble the graph.

| Package | Allowed production dependencies | Forbidden examples |
| --- | --- | --- |
| `model` | Standard library and narrowly justified domain libraries | `app`, HTTP, SQL, cluster, WebSocket |
| `store` | `model` | `sqlstore`, HTTP, application services |
| `app` | `model`, `store`, consumer-owned ports | `platform`, `app/api`, `sqlstore` |
| `app/api` | `app`, `model`, HTTP libraries | `store`, `sqlstore`, `platform` |
| `websocket` | `app`, `model`, WebSocket libraries | SQL and platform service location |
| concrete adapters | Their inward contracts and implementation libraries | Application policy |
| `server` | Construction dependencies | Business rules |
| `cmd/proctor` | Module-root `server` | Independent infrastructure construction |

Tests and `testlib` may cross production boundaries for verification. An architecture test enforces the production allowlist.

## Composition and lifecycle

`server.New` is the sole composition root. It loads configuration, constructs concrete adapters and store layers, gives infrastructure to `platform.Service` for lifecycle ownership, translates configuration into narrow application policies, constructs `app.App`, constructs HTTP and WebSocket transports, wires post-commit effects, and starts components in dependency order.

`platform.Service` owns shared infrastructure health, reconfiguration, startup, and shutdown. It is retained by `server.Server`, never passed into `app`, and never used as a service locator.

Constructors are inert. They validate required dependencies but do not normally start listeners or goroutines. Explicit `Start` methods begin work; `Close` or `Shutdown` is idempotent and bounded. Partial failure unwinds resources already acquired.

Dependency injection is manual. Reflection containers, generated DI containers, global registries, and global mutable state are prohibited.

Application services do not receive `config.Config` or `config.Store`. Composition supplies small immutable policies, such as `SessionPolicy`, or narrow providers for explicitly dynamic behavior.

## Domain

`model` is one cohesive, flat, domain-focused package. It owns entities, value objects, entity-specific IDs, principals, authorization actions/resources, local invariants, and domain transition failures.

It does not own HTTP status, DTOs, SQL rows, request metadata, WebSocket frames, cluster messages, Redis structures, SMTP objects, or filesystem contracts.

Models use explicit constructors, named domain transitions, and `Validate() error` for complete rehydrated state. They do not use `PreSave`, `PreUpdate`, `IsValid`, global clocks, global ID generators, or persistence lifecycle terminology.

Time is UTC `time.Time`. IDs are distinct string-backed types such as `UserID` and `ClassID`, using the shared opaque 26-character random z-base-32 representation. The zero ID is invalid. Mutable, conflict-prone aggregates use revisions; timestamps are not concurrency tokens.

## Application

Use cases are direct methods with typed commands or queries and typed results:

~~~go
CreateAcademicUnit(ctx, invocation, command) (*model.AcademicUnit, error)
GetAcademicUnit(ctx, invocation, query) (*model.AcademicUnit, error)
~~~

`app.Invocation` is immutable and explicitly contains the principal and safe call metadata needed for authorization and audit. `context.Context` remains for cancellation, deadlines, and propagation, not hidden security dependencies.

Each actor-sensitive use case authorizes immediately before expensive work or mutation. HTTP and WebSocket establish credential and assurance requirements but do not preflight resource permissions or issue decision receipts.

Domain models own local invariants and transitions. Application services own use-case policy, authorization, multi-aggregate coordination, transaction intent, audit, and external effects. Focused service implementations are normally unexported behind `app.App` and consumer-owned interfaces.

Background jobs call application use cases with a system/service invocation. They do not manipulate stores directly.

## Interfaces and public surface

Interfaces normally live beside their consumer and expose only what it needs. Broad aggregates may exist at composition but are not daily dependencies. Prefer concrete types during construction and narrow interfaces at consumption.

Persistence is the exception: the bounded root `store.Store` and complete per-model or aggregate contracts live together in `store` for shared conformance testing.

Do not prefix interfaces with `I`. Add compile-time assertions where an adapter intentionally implements an important cross-package contract. Every substantive package has a package comment explaining what it owns, excludes, and may depend on.

## HTTP

`app/api` owns routing, request/response DTOs, strict decoding, authentication/assurance wrappers, Problem Details, and OpenAPI agreement.

`api.New` calls unexported registration functions such as `registerAcademicUnitRoutes`. Each area owns its routes, DTOs, handlers, and mappings and registers through one central registrar. Every route has an explicit authentication classification.

Ordinary handlers return a typed status/body result and `error`. Central code writes JSON, headers, and Problem Details. Streaming, downloads, and upgrades are explicit exceptions.

Mutable domain entities never double as wire DTOs. Command decoding:

- applies body limits first;
- accepts exactly one JSON value;
- rejects unknown fields and trailing data;
- uses `Optional[T]` for omitted, zero, and explicit-null PATCH states;
- permits unknown keys only inside a named bounded extension object.

Use plural kebab-case paths and `snake_case` JSON, query, and path-variable names. Collections return an object with non-null `items` and optional `next_cursor`. Cursors are opaque, versioned, untrusted keysets; growing offsets are not used for unbounded collections.

A reviewed, checked-in OpenAPI document is the public contract. CI compares it with routes, authentication classifications, DTOs, and error mappings. Generate clients and documentation from OpenAPI, never handlers or domain models.

Stable API versions evolve additively. Existing routes, fields, meanings, and error codes are not removed or repurposed. New required inputs or changed semantics require a new version.

## WebSocket

The sibling `websocket` package owns the hub, connection state, upgrade handler, wire DTOs, versioned errors, sequencing, replay, liveness, and backpressure. HTTP mounts its handler but does not own its protocol.

WebSocket reuses stable application error codes and safe fields inside its own versioned envelope, not HTTP Problem Details. It maps transport-neutral application events into versioned wire events.

## Errors and validation

Application methods return standard `error`. Expected public failures use `*app.Error` with a stable dotted domain code, explicitly safe fields, and an optional wrapped cause. They contain no protocol status, localization, request ID, SQL detail, or stack trace.

~~~text
driver failure
    ↓
typed store/port failure
    ↓
domain/application interpretation
    ↓
app.Error for an expected public failure
    ↓
HTTP Problem Details or WebSocket protocol error
~~~

HTTP and WebSocket each maintain exhaustive mappings. Unknown failures become generic correlated internal errors. Unexpected failures preserve their cause and are logged once at the outer operational boundary.

Validation ownership is divided:

- transport: encoding, shape, required wire fields, and size;
- application: use-case prerequisites and authorization;
- domain: local invariants and transitions;
- atomic store/database: cross-row and concurrency invariants.

`panic` is reserved for impossible initialization invariants and test-only `Must*` helpers. Long-running boundaries recover unexpected panics without exposing diagnostics.

## Persistence

`Store` is the canonical persistence term. Do not use `Repository`, `DAO`, `Manager`, or `Gateway` as synonyms.

Contracts return domain types or explicit store projection/aggregate results. SQL rows, nullable driver types, builders, handles, and column names stay private to `sqlstore`. Invalid rehydrated state is an internal integrity failure.

Lookup semantics:

- `Get`: missing data is `store.ErrNotFound`;
- `Find`: absence is expected and returned as `(value, found, error)`;
- `List`: no rows is an empty collection;
- `(nil, nil)` never means absence.

Cross-model transactions are named aggregate operations, such as bootstrap, enrollment transfer, or password-reset consumption. The application decides policy; the adapter owns locking, constraints, concurrency checks, and commit/rollback. Raw SQL transactions and generic `WithTransaction(func(Store))` callbacks do not cross into `app`.

### Store layers

~~~text
application
    ↓
localcachelayer
    ↓
timerlayer
    ↓
retrylayer
    ↓
sqlstore
~~~

- `timerlayer` changes no semantics and measures cache-miss latency including retry.
- `retrylayer` retries only a handwritten allowlist of safe idempotent operations.
- `localcachelayer` caches only an allowlist of disposable reads with documented keys, TTLs, invalidation, and recovery.
- Authorization, roles, account enablement, sessions, credentials, MFA, and token revocation are initially excluded from caching.

Each layer implements the root store and wraps its sub-stores. Deterministic generated code forwards mechanical methods and is checked into Git; behavioral overrides remain handwritten. Reflection proxies are prohibited.

### Schema and migrations

SQL uses plural `snake_case` tables, `id` primary keys, `<entity>_id` foreign keys, meaning-specific `_at` columns, and deterministic constraint/index names. Vocabulary follows `CONTEXT.md`.

Go uses UTC `time.Time`, PostgreSQL uses `timestamptz`, and HTTP uses RFC 3339. Optional lifecycle events are nullable fields such as `archived_at`, not integer zero sentinels.

Normal serving validates schema compatibility and never migrates. Deployments run `proctor migrate` under a lock. Before the first supported release, migrations may be rewritten or squashed and development databases recreated. That release freezes the baseline; later changes are append-only expand/backfill/contract migrations.

`Archive` is reversible removal from active use, `Disable` is reversible prevention from operating, and `Purge` is explicit irreversible removal. A soft archive is not named `Delete`.

## Events, idempotency, and effects

Commit PostgreSQL before transient publication. Events are past-tense facts such as `ClassCreated` or `SessionRevoked` and carry minimal IDs, event time, safe revision, and approved metadata. They never carry mutable entities, credentials, exam answers, arbitrary maps, or transport JSON.

Cache invalidation, WebSocket publication, and transient events use narrow ports after commit. Use an atomic outbox only for confirmed durable delivery such as queued mail or external integrations.

Retryable client commands atomically persist principal, operation, request fingerprint, outcome, and expiry. Replaying identical input returns the recorded outcome; different input with the same key is a conflict.

## Clustering

`cluster/local` is the in-process single-node adapter. `cluster/memberlist` is the peer-to-peer multi-node adapter. Redis is optional cache infrastructure and is not required for clustering.

Memberlist mode:

- is explicitly selected, never auto-detected;
- bootstraps from short-lived PostgreSQL discovery/heartbeat records, with optional static seeds;
- requires authenticated gossip encryption and a cluster key;
- rejects public-interface binding by default;
- advertises server/protocol compatibility and rejects incompatible peers before readiness;
- provides best-effort, non-durable delivery.

Cluster delivery is not a correctness authority. PostgreSQL reads, bounded TTLs, periodic revalidation, and client resynchronization recover missed messages. Durable work uses a database-backed job or outbox.

## Naming and files

- Packages are short, lowercase, singular responsibilities.
- Avoid `util`, `common`, `shared`, `base`, `core`, `services`, and `repositories`.
- Protocol names such as `oidc`, `cas`, and `memberlist` are valid adapter packages.
- Files follow a responsibility or vertical slice, not arbitrary size.
- Avoid catch-all `helpers.go`, `utils.go`, `common.go`, and `types.go`.
- Preserve `ID`, `URL`, `HTTP`, `API`, `SQL`, `MFA`, `VFS`, `OIDC`, and `CAS`.
- Use `New` for a package's primary exported construction and unexported `new<Type>` for internal services.
- Use `With<Option>` only when absence has defined optional semantics.
- Prefer domain verbs over vague `Process`, `Handle`, `Execute`, and `Manage`.

Application capabilities start as cohesive files in `app`. Extract `app/<domain>` only when a stable capability has substantial internal structure, several collaborators, and a narrow facade. The extracted package never imports its parent.

## Testing and CI

- Domain tests prove constructors, transitions, and invariants.
- Application tests use small handwritten fakes/spies for consumer-owned ports.
- Store and infrastructure adapters share conformance suites.
- HTTP tests prove DTO mapping, route authentication, and errors.
- WebSocket tests prove sequencing, replay, authorization, and backpressure.
- Integration tests use the real `server.New` graph through `testlib`.

Use external `package_test` tests by default. Same-package tests are for important unexported logic. Name tests after observable behavior, not mock interactions. Pure isolated tests use `t.Parallel()`.

Ordinary `go test ./...` is network-free. PostgreSQL, Redis, SMTP, S3, and Memberlist suites use the `integration` build tag and dedicated CI targets. Missing dependencies fail an invoked integration target rather than silently skipping.

CI checks formatting, generated-file cleanliness, tests, `go vet`, network-free race tests, import boundaries, OpenAPI/route/error agreement, tagged conformance suites, and documentation links.

## Correct and incorrect examples

These examples are illustrative.

### Correct: transport uses an application capability

~~~go
type academicUnits interface {
    CreateAcademicUnit(context.Context, app.Invocation, app.CreateAcademicUnitCommand) (*model.AcademicUnit, error)
}
~~~

The handler owns DTO mapping; the application owns authorization and policy.

### Incorrect: transport reaches persistence

~~~go
type API struct {
    store store.Store
}

func (a *API) createAcademicUnit(...) {
    a.store.AcademicUnit().Save(...)
}
~~~

This bypasses application policy, audit, and transaction intent.

### Correct: explicit dependencies

~~~go
type Dependencies struct {
    Users    store.UserStore
    Sessions store.SessionStore
    Clock    Clock
}
~~~

### Incorrect: service location

~~~go
type App struct {
    platform *platform.Service
}

func (a *App) Store() store.Store { return a.platform.Store() }
~~~

### Correct: named atomic operation

~~~go
result, err := enrollmentStore.Transfer(ctx, transfer)
~~~

### Incorrect: arbitrary transaction callback

~~~go
store.WithTransaction(ctx, func(tx store.Store) error {
    // The atomic contract is undiscoverable.
})
~~~

### Correct files

~~~text
app/
├── authentication.go
├── password_reset.go
└── session_management.go
~~~

### Incorrect files

~~~text
app/
├── services/
├── managers/
├── helpers.go
└── types.go
~~~

### Correct error flow

~~~text
driver conflict → store.ErrConflict
                → app.Error{Code: "class.enrollment_conflict"}
                → HTTP 409 Problem Details
~~~

### Incorrect error exposure

~~~go
http.Error(w, err.Error(), http.StatusInternalServerError)
~~~

## Migration gaps

The target intentionally differs from existing code:

1. runtime composition and `Server` still live in `app`;
2. `App` retains `*platform.Service` and infrastructure getters;
3. `platform.New` still selects concrete backends;
4. `app/api` has a broad application aggregate and exposes some store option types;
5. handlers still use permission preflights and decision receipts;
6. `model` still contains `AppError`, request/client metadata, cluster, and WebSocket contracts;
7. models still use `PreSave`, `PreUpdate`, `IsValid`, plain string IDs, and integer timestamps;
8. some handlers serialize models directly;
9. WebSocket transport still lives in `app/api`;
10. clustering uses local and Memberlist backends only (Redis cluster retired);
11. root-composed store timer and safe retry layers are present; the
    local-cache layer is not yet present;
12. external-service tests are not consistently tagged `integration`;
13. import-boundary and OpenAPI agreement tests are not yet present.

Migration remains vertical and buildable:

1. establish root composition and import tests;
2. migrate one application capability at a time away from service location;
3. migrate each capability's invocation, commands, errors, DTOs, lifecycle, and tests together;
4. add store layers and extract WebSocket/cluster transports behind stable contracts;
5. establish native temporal types and typed IDs in the pre-release schema baseline.

This document does not authorize an implementation rewrite; each migration is a separately scoped task.

## Decision records

ADRs remain accepted historical records. Reversing a decision adds a new ADR that explicitly supersedes the old one. The current set starts with [`0001-conceptual-architecture-boundaries.md`](./adr/0001-conceptual-architecture-boundaries.md).
