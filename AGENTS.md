# Proctor Agent Guide

This file is the living architectural and domain contract for the Proctor
repository. It applies to the entire repository unless a more specific
`AGENTS.md` exists in a subdirectory.

Read this file completely before analyzing, planning, or changing the project.
Do not treat it as historical documentation: it describes the current agreed
direction. When the codebase, domain model, build workflow, deployment model,
or architectural decisions evolve, update this file in the same change whenever
practical.

If the implementation and this document disagree:

1. do not silently choose one;
2. determine whether the implementation is an incomplete migration, a bug, or a
   deliberate newer decision;
3. preserve user work;
4. explain the discrepancy;
5. update the implementation, this document, or both as authorized by the task.

## Project Mission

Proctor is an open-source, self-hosted examination and proctoring platform for
universities and schools. A university should be able to operate one logical
installation for all of its academic components while scaling the application
across multiple server nodes when required.

Proctor is a new product, but it is not a clean-room reimplementation.
Mattermost is an important upstream implementation reference for proven
behavior, especially around models, application errors, server lifecycle, API
authentication, sessions, scoped authorization, WebSockets, cache invalidation,
and high-availability clustering.

Directly copy or substantially adapt eligible Mattermost code when its behavior
and architecture fit Proctor better than a new implementation would. Preserve
the upstream license and notices, record exact provenance immediately, and make
the adaptation explicit. Do not rewrite sound upstream code merely to make it
look different. Equally, do not copy folders or subsystems wholesale when their
product assumptions, coupling, or complexity do not fit Proctor.

## Current Repository State

The repository currently tracks four independent Go modules:

- `github.com/sudosylabs/proctor/packages/vfs`
- `github.com/sudosylabs/proctor/packages/cache`
- `github.com/sudosylabs/proctor/packages/mail`
- `github.com/sudosylabs/proctor/server`

The root `go.work` is the local development workspace for these modules.

The old server snapshot derived substantially from Mattermost is preserved
locally at `/legacy/server` and ignored by Git. It is a requirements and
behavior reference, not part of the new implementation. The local `/tools`,
old root frontend/build files, and old GitHub workflows are ignored for the
same reason.

The tracked `/server` directory is the new server implementation. Its first
walking skeleton is operational and includes:

- the `proctor serve`, `proctor config validate`, `proctor version`, and
  `proctor help` commands;
- a typed, schema-versioned configuration store with memory and atomic-file
  backings, strict field checking, cloning, redaction, defaults, aggregate
  validation, `PROCTOR_` environment overrides, reload/set listeners, diffs,
  and separation of persisted from effective configuration;
- the Proctor-owned `mlog` subsystem with multiple independently filtered text
  or JSON targets, dynamic reconfiguration, bounded field sizes, contextual
  fields, standard-library log bridging, configuration locking, flushing,
  shutdown, and thread-safe test capture;
- one cohesive construction graph rooted at `app.NewServer`, containing one
  `platform.Service`, one `app.App`, one configuration store, and one logger;
- a shared `testlib` that constructs the same application graph with memory
  configuration and captured logs;
- bounded HTTP server timeouts, request body/header limits, request IDs,
  security headers, access logging, panic recovery, and graceful shutdown;
- liveness, readiness, and version endpoints;
- a Mattermost-adapted `model.AppError` flow with stable translation IDs,
  translation hooks, wrapping, protected internal details, explicitly safe
  public fields, and RFC 9457 HTTP Problem Details mapping;
- a cohesive `model` package with Mattermost-inspired IDs, millisecond
  timestamps, `PreSave`, `PreUpdate`, `IsValid`, and safe `Auditable`
  representations;
- the confirmed structural academic models: institution, hierarchical academic
  unit, programme, programme level, academic period, and class;
- identity and authorization model foundations: user, external identity, local
  password credential, affiliation, academic-unit member, class member, role,
  role binding, session, hashed session credential, and personal access token;
- explicit authentication classification metadata for every registered route;
- platform-owned cache, mail, and VFS adapters selected from typed deployment
  configuration, with memory/Redis cache, disabled/SMTP mail, local/S3 VFS,
  dependency checks, deterministic cleanup, and memory test implementations;
- a Mattermost-shaped cluster message contract and server-owned cluster port
  with typed handlers, explicit best-effort/reliable delivery classes, stable
  node identity, bounded messages, startup/readiness/shutdown ownership, and a
  loop-safe single-node local transport;
- the first complete identity slice: transactional local-user/password
  persistence, bounded Argon2id password hashing, generic login failures,
  server-side sessions, hashed opaque access and refresh credentials, refresh
  rotation and replay revocation, debounced activity, concurrent-session
  limits, cache-backed authentication resolution and login throttling;
- public login, refresh-credential, session-required logout, and current-user
  HTTP endpoints, plus self-service active-session listing, individual session
  revocation, and account-wide session revocation, all using Authorization
  bearer credentials and the immutable request principal;
- serialized per-user session lifecycle transactions across login, refresh,
  individual revocation, and revoke-all, preventing refresh rotation or a
  concurrent login from escaping an account-wide security reset;
- a closed action registry, resource contracts, and current-state scoped
  authorization evaluator with additive roles, deny-by-default behavior,
  institution and ancestor academic-unit inheritance, exact class scope,
  deleted-role exclusion, and no permission snapshots in sessions;
- complete role, role-binding, and durable audit SQL stores with reusable
  conformance suites, role batch resolution, serialized time-range overlap
  protection, polymorphic scope-reference validation, hierarchy resolution,
  keyset audit pagination, and attempt-only terminal audit transitions;
- fail-closed durable auditing of authorization decisions with actor, session,
  request, node, direct peer, client, authentication, resource, and scope
  context, plus application primitives for audited critical mutations;
- an explicitly privileged `GET /api/v1/audits` vertical slice that enforces
  `audit.view` in the application layer and returns cursor-paginated events.

The server now includes PostgreSQL connection management, embedded versioned
migrations, a separate migration command, platform-owned schema validation, a
Mattermost-shaped root store with per-model contracts, and all structural
academic SQL stores: institution, academic unit, programme, programme level,
academic period, and class. It also includes user, password-credential,
session, session-credential, role, role-binding, and audit SQL stores with
reusable conformance tests. External identity login, password
reset/verification, MFA, personal access token services, role-administration
APIs, academic membership services, exam-domain, WebSocket, and a concrete
multi-node cluster transport remain unimplemented.

Do not:

- modify or stage the ignored legacy snapshot unless the user explicitly asks;
- treat its package names or directory layout as the target architecture;
- remove ignored legacy files merely to make the repository look clean;
- introduce imports from `github.com/prctr/prctr/server`;
- assume ignored files will exist on another developer's machine or in CI.

A full local Mattermost checkout may be available at:

`/Users/hammed/Projects/mattermost`

Use it read-only as an implementation reference when present. Other machines
may not have this path, so repository documentation and tests must never depend
on it.

## Non-Negotiable Architectural Principles

1. Business logic must not depend on HTTP, WebSocket, SQL, Redis, SMTP, or VFS
   implementation details.
2. Reusable packages must not import the Proctor server.
3. The server may use reusable packages through narrow server-owned ports and
   adapters.
4. Only the composition root may select concrete infrastructure
   implementations.
5. PostgreSQL is authoritative for durable application state.
6. Cache entries are disposable and reconstructible.
7. WebSocket events are delivered through the application/cluster layer, not
   through the database.
8. Authentication and authorization are separate concerns.
9. Authorization is enforced in application use cases, not only in HTTP
   handlers.
10. Every API route must explicitly declare its authentication requirement.
11. One logical installation represents one educational institution.
12. Multiple server processes connected to the same state form nodes of one
    logical installation, not separate tenants.
13. No enterprise license gate may be required for horizontal scaling or high
    availability.
14. Avoid speculative abstraction. Extract a reusable package only when its
    contract is understood independently of Proctor.
15. `server/model` is the deliberate cohesive package for durable domain
    contracts, following Mattermost's readable one-package model organization.
    Do not turn it into a general utility dumping ground, and avoid catch-all
    packages named `utils`, `common`, or `shared`.
16. Avoid global mutable application state and global service locators.
17. Use Proctor's cohesive `app.Server`, `app.App`, and `platform.Service`
    construction flow. Persistence deliberately has one bounded root
    `store.Store` with per-model store contracts, following Mattermost; do not
    turn the application or platform objects into cyclic, unlimited general
    service locators.
18. Security-sensitive state changes and authorization decisions must be
    auditable.
19. Student data, credentials, tokens, exam answers, and secrets must never be
    written to ordinary logs.
20. A single-node installation must be a valid degenerate form of the clustered
    architecture, not a separate product.

## Monorepo and Module Policy

The repository is intentionally a monorepo containing independently versioned
modules.

Target top-level layout:

```text
/
├── AGENTS.md
├── go.work
├── packages/
│   ├── cache/
│   ├── mail/
│   └── vfs/
├── server/
├── tools/
└── docs/
```

Rules:

- Each reusable package has its own `go.mod`, license, README, tests, and public
  compatibility policy.
- The server has its own `go.mod`.
- Tools that require independent dependency graphs may have their own modules.
- `go.work` is for repository development; published modules must build and
  test independently of it.
- A package's public API must not expose server-specific types.
- Cross-module imports must use `github.com/sudosylabs/proctor/...`, never local
  relative replacements in committed module files.
- Do not move code into `packages/` merely because two server directories call
  it. It must have a coherent independent contract and plausible external use.
- Before v1, public package APIs may evolve, but changes must update tests,
  documentation, and compatibility notes.

## Licensing and Mattermost Provenance

The intended licensing split is:

- reusable packages under `packages/`: Apache License 2.0;
- the Proctor server: GNU AGPL v3;
- repository-level documentation must clearly explain per-directory licenses.

The server license is compatible with adapting eligible Mattermost AGPL source,
subject to the exact upstream license, notices, and attribution requirements.
Open source is not blanket permission to copy code under a different license.

For Mattermost-derived work:

1. identify the exact upstream repository, revision, path, and governing
   license;
2. preserve required copyright and attribution notices;
3. mark substantially modified files where the governing license requires it;
4. maintain a server NOTICE or provenance document;
5. do not copy Mattermost Source Available or commercial/enterprise code
   without explicit permission;
6. do not move AGPL-derived implementation into Apache-licensed reusable
   packages;
7. concepts, interfaces, and behavior may be reimplemented cleanly when direct
   source reuse is undesirable.

Mattermost's public repository exposes several cluster interfaces and
application integration points while the concrete high-availability cluster
implementation is enterprise code. Proctor must implement its own cluster
transport.

Before committing the first new server code, add and verify the server license,
root licensing explanation, and required NOTICE/provenance files.

## Institution and Academic Domain

### Installation boundary

One Proctor installation represents one institution, for example Northbridge
University.

Another university should normally run another logical Proctor installation
with a separate database or schema and configuration. Multiple logical
universities are not tenants inside one Proctor installation.

Multiple Proctor application nodes sharing one database and resources are nodes
of the same installation.

### Academic hierarchy

Do not collapse all academic structures into `Department` or `Group`.

The confirmed conceptual hierarchy is:

```text
Institution
└── AcademicUnit (hierarchical)
    └── Programme
        └── ProgrammeLevel
            └── Class ── AcademicPeriod
```

`AcademicUnit` is a generic hierarchical node. The core does not classify units
with a fixed kind: organizational terminology differs between institutions, and
authorization depends on hierarchy and scoped roles rather than labels. If
classification is needed later for presentation, imports, or reporting, it must
be optional and institution-defined.

Example:

```text
Northbridge University                         Institution
├── College of Engineering                     AcademicUnit
│   └── School of Computing                    AcademicUnit (child)
│       └── Bachelor of Computer Science       Programme
│           ├── Year 1                         ProgrammeLevel
│           │   ├── Year 1 - Class A           Class, 2026-2027 period
│           │   └── Year 1 - Class B           Class, 2026-2027 period
│           ├── Year 2                         ProgrammeLevel
│           └── Year 3                         ProgrammeLevel
└── College of Health Sciences                 AcademicUnit
    └── School of Nursing                      AcademicUnit (child)
        └── Bachelor of Nursing                Programme
            └── Year 1                         ProgrammeLevel
                └── Nursing Year 1 - Class A   Class, 2026-2027 period
```

Domain terms:

- `Institution`: the university or school represented by the installation.
- `AcademicUnit`: a hierarchical organizational component.
- `Programme`: a course of study such as Bachelor of Computer Science.
- `ProgrammeLevel`: a reusable curriculum stage such as Foundation, Year 1, or
  Year 2.
- `Class`: the concrete student roster for a programme level and academic
  period, such as Year 1 - Class A.
- `AcademicPeriod`: an academic year or another institution-defined enrollment
  period.
- `ClassMember`: the durable, time-bounded enrollment of a student in a class
  for an academic period.
- `Affiliation`: a user's relationship to the institution, such as student,
  teacher, staff, or external collaborator.
- `RoleBinding`: a role assigned to a user at a particular authorization scope.

Use `Class`, not `Group`, for the student teaching unit. Reserve `Group` for a
future genuinely ad-hoc grouping feature, if one is ever required.

### Academic invariants

- An academic unit may have a parent academic unit.
- Cycles in the academic-unit hierarchy are forbidden.
- A programme belongs to one academic unit.
- A programme level belongs to one programme.
- A class belongs to one programme level and one academic period.
- A student's broader programme and academic-unit placement is derived from
  the active class membership.
- A student has at most one active `ClassMember` in an academic period.
- Historical class memberships are retained when a student progresses or
  transfers.
- Changing a student's class closes/replaces the previous active membership
  transactionally.
- A teacher may have simultaneous role bindings in several academic units.
- The same teacher may hold different roles in different academic units.
- A user may have more than one affiliation; do not force all people into a
  permanently exclusive user-type enum.
- Affiliation describes who a user is; roles and permissions determine what the
  user may do.
- Membership alone grants no implicit unrestricted access.

The exact exam-to-programme/class relationships remain a business-model
decision. Do not invent them without user confirmation.

## Identity, Authentication, and Accounts

Identity is a server domain, not a reusable top-level package at this stage.

The identity area owns:

- accounts and affiliations;
- local credentials when enabled;
- external identity links;
- login policy;
- sessions;
- refresh credentials;
- personal access tokens;
- password reset and verification tokens;
- MFA state and recovery;
- security-related audit events.

External identity providers must be adapters. The core must not be coupled to
OIDC, CAS, SAML, LDAP, or a particular university directory. The initial
provider support order is still to be confirmed.

Identity model rules:

- `User` contains profile/account state, not passwords, external-provider
  subjects, academic roles, or permissions;
- `ExternalIdentity` supports several provider links for one user and preserves
  the provider subject as an opaque value;
- `PasswordCredential` stores only an established password hasher's encoded
  output and is absent for external-only accounts;
- `Affiliation` is non-exclusive and time-bounded, so one person may
  simultaneously be a student, teacher, or staff member;
- `AcademicUnitMember` records organizational membership but grants no
  permission by itself;
- `ClassMember` is the student enrollment record; staff access uses scoped
  roles instead;
- `RoleBinding` assigns a role at institution, academic-unit, or class scope;
- `Session` stores authentication context but no bearer credential or role
  snapshot;
- `SessionCredential` stores hashed access/refresh credentials and refresh
  rotation lineage;
- `PersonalAccessToken` is separate from sessions, hashed, expiring, scoped,
  and optionally constrained to an academic unit;
- `UserToken` is a hashed, expiring, single-use password-reset or
  email-verification credential; invitations require their own future model.

### API authentication boundary

Preserve the strong behavior behind Mattermost-style `APISessionRequired`
wrappers without copying the large custom handler object.

The HTTP pipeline should be conceptually:

```text
panic recovery
→ request ID and request metadata
→ body/URL limits and security headers
→ credential extraction
→ session authentication
→ principal attached to context
→ route authentication-strength requirement
→ handler/application use case
→ resource authorization
→ response/error mapping
→ audit and operational logging
```

Every route must explicitly be one of:

- public;
- authenticated session required;
- MFA/strong authentication required;
- recent reauthentication required;
- personal/service credential required;
- privileged administrative route.

Route authentication requirements must be composable and testable. Add a route
matrix test that fails when a route lacks an explicit classification.

Authentication proves identity. It does not grant permission to a resource.

### Request principal

An authenticated request carries a small immutable principal containing only
security-relevant identity context, such as:

- user ID;
- session ID;
- credential type;
- authentication provider/method;
- authentication strength;
- client type;
- relevant authentication timestamps.

Do not embed a permanent snapshot of every role, permission, academic-unit
membership, or class membership in the session. Those values become stale in a
cluster and make revocation difficult.

### Sessions

Use revocable server-side sessions backed by opaque, cryptographically random
credentials.

Requirements:

- store token hashes, not recoverable raw tokens;
- show generated tokens only when required and only once;
- PostgreSQL is authoritative;
- Redis may cache resolved session state;
- cache TTL must not outlive credential expiry;
- revocation must invalidate every application node;
- activity writes must be debounced;
- idle and absolute expiration are separate;
- users can list and revoke individual sessions;
- account disablement and security-sensitive credential changes can revoke all
  sessions;
- record safe device/client metadata for session management;
- never log session or refresh credentials;
- configure a defensible maximum number of concurrent sessions.

Session authorization must resolve current role bindings rather than trust
roles copied at login time.

### Desktop credentials

The desktop application is a primary client.

Preferred interactive model:

- short-lived opaque access credential;
- rotating refresh credential;
- refresh credential stored in the operating-system credential store;
- access credential normally held only in memory;
- bearer authentication over HTTPS;
- automatic refresh with replay/reuse detection;
- server-side revocation.

For external university login, prefer system-browser authorization with
Authorization Code and PKCE. The desktop application should not collect
university passwords when a proper external flow is available.

### CLI credentials

For interactive human login, prefer:

- device authorization when the identity provider supports it; or
- browser authorization with a local callback.

For scripts and automation, use personal access tokens with:

- token value displayed only once;
- stored hash;
- mandatory expiry;
- explicit scopes;
- optional academic-unit constraints;
- active/disabled/revoked state;
- last-used metadata;
- audit records.

Do not model personal access tokens as effectively permanent user sessions.
Separate service accounts from human personal access tokens when service
identity is needed.

### Purpose-specific tokens

Password-reset, email-verification, invitation, MFA-recovery, and similar
tokens must be:

- purpose-specific;
- random;
- stored hashed;
- short-lived;
- single-use where applicable;
- consumed transactionally;
- redacted from logs and API responses after creation.

Accept API credentials from the `Authorization` header. Do not accept access
tokens in URL query parameters. URLs are frequently recorded in logs, history,
and monitoring systems.

If a browser client is introduced later, cookie authentication must use Secure,
HttpOnly, and appropriate SameSite settings plus CSRF protection. Desktop and
CLI clients should use bearer credentials.

### MFA and authentication strength

Do not implement MFA as an unused handler flag.

The principal/session must record authentication strength and relevant
completion time. Sensitive operations can require stronger or recent
authentication, including:

- creating or rotating access tokens;
- changing passwords or MFA;
- changing roles;
- exporting student data;
- viewing especially sensitive submissions;
- starting, forcibly ending, or administratively altering an exam.

External SSO assertions may satisfy MFA only when the provider assertion is
trusted and carries sufficient authentication-method information.

## Authorization

Authorization is resource- and action-based.

Conceptually:

```text
Can(principal, action, resource)
```

Scopes include:

- institution/system;
- academic unit;
- programme or programme level when required;
- class;
- exam.

Example actions may include:

- `academic_unit.manage`;
- `class.view`;
- `class.members.view`;
- `class.members.manage`;
- `exam.create`;
- `exam.view`;
- `exam.manage`;
- `exam.start`;
- `exam.end`;
- `exam.join`;
- `exam.submit`;
- `exam.submissions.view`;
- `exam.violations.review`.

Action names are stable domain contracts. Do not name permissions after HTTP
methods or routes.

### Role inheritance

A teacher may receive a role binding on an academic unit. A permission granted
by that role applies to resources below that unit when the action's inheritance
rules allow it.

Example:

- a department teacher role may grant `class.view` for descendant classes;
- a department external-contributor role may omit `class.members.view`;
- a class-specific proctor binding may grant access only to one class or exam;
- a system administrator role may operate across the institution.

Roles are additive and access is denied by default. Do not introduce explicit
deny rules without a documented need and precedence model.

### Enforcement rules

- HTTP middleware verifies that a valid credential/assurance level exists.
- Application use cases enforce permission to the actual resource.
- Repository list/search methods must constrain results by authorized scope;
  do not fetch all records and filter them in memory.
- WebSocket commands and subscriptions must use the same authorization service.
- Background tasks act under an explicit system/service principal where an
  actor is required.
- Permission and role changes must invalidate authorization/session-related
  caches across cluster nodes.
- Centralize policy evaluation, but keep action checks explicit at use-case
  boundaries.
- Add table-driven policy tests covering roles, scopes, inheritance, and
  cross-department isolation.

The current evaluator deliberately performs no authorization-result caching:
each decision resolves active bindings and non-deleted roles from PostgreSQL.
This makes revocation immediately visible to every node sharing the database.
If authorization caching is introduced, role and binding mutations must
invalidate it through the shared cache/cluster layer before the mutation API is
considered complete.

## Target Server Architecture

The server should use explicit composition and business-oriented vertical
slices.

Current foundation and growth direction:

```text
server/
├── go.mod
├── LICENSE
├── NOTICE
├── cmd/
│   └── proctor/
├── config/
│   └── configtest/
├── mlog/
├── platform/
├── app/
│   └── api/
├── store/
│   └── sqlstore/
├── testlib/
├── migrations/
└── config.example.json
```

Only create `store`, `migrations`, or further directories when they receive
working code. Prefer several cohesive files in an existing package over a new
one-file package.

Dependency direction:

```text
cmd/proctor → app.NewServer
app.Server → config + platform + app/api + app.App
platform.Service → config + mlog + concrete shared infrastructure
app/api → narrow application-facing contracts
app.App → platform-owned capabilities and product services
testlib → the same app.NewServer construction path with test overrides
```

`app.NewServer` is the composition root. The command must not independently
construct another logger, cache, store, mailer, VFS, or application service.

The server module is deliberately cohesive and may have internal coupling.
Coupling must follow ownership and construction flow rather than form import
cycles. API handlers receive the application operations they need; product
logic must not create infrastructure independently.

## Configuration

Separate deployment configuration from application settings.

Deployment configuration includes:

- HTTP listener and public URL;
- PostgreSQL connectivity and pool settings;
- cache backend;
- cluster transport;
- VFS backend and credentials;
- SMTP transport;
- logging;
- security and process-level limits.

Application settings include:

- institution display information;
- branding;
- academic policies;
- exam defaults;
- invitation rules;
- administrator-editable behavior.

Deployment configuration is operator-owned, loaded from a typed configuration
file and environment variables, and normally immutable for the process
lifetime. Application settings are durable application data and may be changed
through authorized application APIs.

Configuration rules:

- precedence is defaults, configuration file, then environment variables;
- command flags primarily select command and configuration location;
- use the `PROCTOR_` environment prefix;
- reject unknown fields;
- report validation problems together when possible;
- parse URLs and durations once at the boundary;
- redact secrets explicitly;
- never log or return complete unredacted configuration;
- distinguish structural validation from external connectivity checks;
- carry a configuration schema version;
- avoid pointer fields unless a real three-state value is required;
- configuration consumers read cloned effective snapshots from one
  concurrency-safe `config.Store`;
- the store keeps persisted configuration separate from environment-overridden
  effective configuration and must never persist environment values;
- backing stores implement a common contract; memory and atomic file backings
  are present, while a database backing may be added when clustered
  configuration requirements are finalized;
- listeners receive cloned old/current values after successful changes;
- runtime reconfiguration is capability-specific: logging currently
  reconfigures dynamically, while listener addresses, HTTP limits, cluster
  backend, and cluster node identity require a process restart;
- configuration backing conformance must be reusable when another backing is
  introduced.

Planned administrative commands:

- `proctor serve`;
- `proctor migrate`;
- `proctor config validate`;
- `proctor doctor`;
- `proctor version`.

## Models and Error Flow

`server/model` is an intentional shared domain-contract package. Keep model
files flat and cohesive, normally one file per substantive model, rather than
creating a directory tree for every entity.

Durable models use the Mattermost-inspired conventions:

- 26-character z-base-32 random IDs generated by `model.NewId`;
- `CreateAt`, `UpdateAt`, and `DeleteAt` Unix-millisecond fields;
- `PreSave` to assign ID/timestamps and sanitize stored text;
- `PreUpdate` to update timestamps and sanitize stored text;
- `IsValid() *model.AppError` for shape-level invariants;
- `Auditable() map[string]any` for a deliberately safe, bounded audit
  representation.

`IsValid` must validate a fully prepared persistent model. It must not perform
database or network I/O. Cross-row invariants such as uniqueness, academic-unit
cycle detection, parent/child institution consistency, and the student's
single-active-class rule belong in application/store transactions and database
constraints.

`Auditable` is not a replacement for an audit event. It controls which model
fields may appear in the prior/result state of a future audit record. Actor,
session, request, action, outcome, and cluster metadata belong to the audit
service. Never include secrets, credentials, tokens, exam answers, or unbounded
user-controlled content in an `Auditable` map.

Models and application services use the Mattermost-adapted `model.AppError`.
Its `Id` is both the stable machine-readable error code and the translation ID.
It may carry:

- translation parameters that remain private unless explicitly exposed;
- translated public message;
- HTTP status for the HTTP-oriented server contract;
- request ID assigned at a transport boundary;
- explicitly safe public fields;
- internal detail and a wrapped cause compatible with `errors.Is/As`.

`DetailedError` and wrapped causes are operator-only and must never be
serialized to an untrusted client. HTTP maps `AppError` to RFC 9457 Problem
Details and adds request correlation. A WebSocket boundary will map the same
error to its versioned protocol shape. Translation can occur at construction
through the one-time default translator and again at the boundary for the
request/user locale.

Unexpected errors are logged once at a boundary. Do not log and wrap the same
error at every layer. Expected client errors normally do not require error-level
logs.

Persistence adapters translate driver errors into domain/application errors.
Never expose SQL, Redis, SMTP, filesystem, stack, or credential details to
clients.

## Logging, Metrics, and Audit

Use the Proctor-owned `server/mlog` package for operational logging. It uses
Go's structured logging primitives internally but owns Proctor's target,
reconfiguration, lifecycle, and testing behavior.

Rules:

- the platform service owns the active logger and configures it from the shared
  configuration store;
- support multiple independently leveled text or JSON console/file targets;
- a failed reconfiguration must leave the previous working targets active;
- logger configuration can be locked by tests so application startup cannot
  replace captured targets;
- use contextual loggers rather than a global application logger;
- flush and shut down logging through the platform lifecycle;
- bound serialized field sizes;
- prefer console output under a process supervisor; file targets remain
  supported for self-hosted deployments that require them;
- attach component, request ID, node ID, and safe entity IDs;
- never attach complete user, session, configuration, exam-answer, or token
  objects;
- keep metrics/tracing hooks optional and boundary-oriented.

Operational logs and audit records are separate systems.

Audit events must capture, as applicable:

- actor and effective principal;
- action;
- resource type and ID;
- academic scope;
- result;
- request ID;
- node ID;
- safe prior/result metadata;
- occurrence time.

Audit storage, retention, and access are explicit. Audit failures for critical
security actions must have a documented policy.

Current audit policy:

- PostgreSQL `audit_events` records are authoritative; operational logs are not
  a substitute;
- authorization decisions are fail-closed: an otherwise allowed action is not
  allowed when its durable decision record cannot be written;
- a critical mutation must write an `attempt` before changing state;
- the attempt may transition exactly once to `success` or `fail`;
- if terminal completion fails after a mutation committed, return an internal
  failure and leave the durable attempt for operator reconciliation;
- audit JSON parameters and prior/result states are individually bounded to
  16 KiB and must be deliberately safe projections;
- direct peer addresses are recorded; forwarded client addresses are not
  trusted until trusted-proxy configuration exists;
- audit retention is currently indefinite. Automated deletion must not be
  introduced until an administrator-visible retention and legal-preservation
  policy is confirmed.

## High Availability and Cluster Model

The correct term for multiple Proctor application processes serving one shared
installation is a high-availability cluster or horizontally scaled deployment.

The application tier is active-active:

```text
Desktop / CLI
       ↓
Load balancer
  ↙           ↘
Proctor A   Proctor B
  ↘           ↙
shared PostgreSQL
shared Redis/coordination
shared VFS
shared SMTP/provider
```

Full high availability also requires redundant database, cache/coordination,
storage, and load-balancer infrastructure. Multiple application nodes alone do
not remove those external single points of failure.

Cluster requirements:

- no node-local durable business state;
- all nodes run compatible server versions and configuration;
- each node has a stable runtime node ID;
- readiness is false until mandatory shared dependencies are usable;
- shutdown drains HTTP and WebSocket work with deadlines;
- migrations run once as an explicit deployment step;
- database pool sizing accounts for the number of nodes;
- rolling upgrades use backward-compatible expand/migrate/contract schema
  changes;
- local VFS is rejected in explicitly clustered production mode;
- session, permission, and cache invalidation reaches every node;
- singleton work uses safe coordination rather than every node running it.

Prefer durable database-backed work claiming for jobs over broad leader
election. Use PostgreSQL advisory locks for the small number of truly singleton
maintenance operations when appropriate.

## Application-Side Cluster Messaging

WebSocket events and cache invalidations are application cluster messages.
PostgreSQL is not their transport.

Define a narrow cluster transport owned by the server. It should eventually
support:

- starting/stopping inter-node communication;
- node discovery and health;
- registering typed message handlers;
- broadcast to peers;
- targeted node messages;
- best-effort messages;
- reliable messages where required;
- cache and session invalidation;
- WebSocket event fan-out;
- cross-node WebSocket reconnection support.

The initial concrete transport may use Redis, NATS, or another suitable
technology, but business code must depend on the port, not that technology.
Do not turn the cache package into a message-bus package.

Do not extract the cluster transport into `packages/` until its API is stable
and independently useful.

The current implementation establishes the transport-independent contract:

- `model.ClusterMessage` carries a typed event, explicit best-effort or reliable
  send class, bounded opaque data, and bounded string properties;
- the transport owns source/target identity, wire versioning, message IDs,
  serialization, acknowledgements, and retry mechanics;
- one handler owns each event on a node, matching the Mattermost application
  dispatch shape and making duplicate ownership an explicit error;
- `Broadcast` means peers only and must never call the sending node's handler;
- `SendToNode` is targeted delivery and may address the current node;
- handlers receive cloned messages, so neither callers nor other handlers can
  mutate shared message state;
- handler panics are contained at the transport boundary and message data is
  never included in ordinary logs;
- the platform constructs and health-checks the transport, the server starts
  it before becoming ready, and platform shutdown stops it before shared
  infrastructure is closed;
- `local` is the only concrete backend today. It is the valid single-node
  degenerate transport: peer broadcasts succeed without local delivery, while
  self-targeted messages exercise registered handlers synchronously.

The reliable send class is part of the application contract, but the local
backend has no remote delivery to acknowledge. No multi-node backend may claim
reliable delivery until retry, acknowledgement, ordering, duplicate handling,
backpressure, and node-failure behavior are explicitly defined and tested.

## WebSocket Architecture

Mattermost's application-side flow is the behavioral reference:

1. application code creates a WebSocket event;
2. the local node broadcasts it to matching local connections;
3. the node serializes it into a cluster publish message;
4. peers receive and decode it;
5. peers broadcast it locally without rebroadcasting to the cluster.

Proctor must prevent cluster rebroadcast loops.

Each WebSocket connection is owned by exactly one application node. The owning
node manages:

- authenticated principal/session;
- connection ID;
- monotonically increasing per-connection sequence;
- outbound queue;
- bounded recently-sent replay/dead queue;
- ping/pong and liveness;
- subscription and authorization state;
- backpressure and disconnect policy.

Reconnection requirements:

- the client sends its prior connection ID and last received sequence;
- the current node first attempts local recovery;
- in cluster mode it may request queue state from peer nodes;
- recoverable events are replayed in order;
- if the replay window is unavailable, create a fresh connection and tell the
  client to resynchronize authoritative state through HTTP;
- replay queues are bounded and not durable business storage.

Event delivery classes:

- best effort for presence, typing, transient diagnostics, and similar hints;
- reliable cluster send/replay for selected state-change notifications;
- durable business state is committed independently before publication.

Starting or updating an exam follows:

1. authorize the command;
2. commit authoritative state;
3. publish the application WebSocket/cluster event after successful commit;
4. let clients fetch current state if they miss the notification.

An outbox may be used for mail, durable jobs, integrations, or future
requirements that truly need durable event processing. Do not automatically
place the normal WebSocket fan-out path through a database outbox.

WebSocket subscriptions and commands must use the same principal and
authorization service as HTTP.

## Persistence

PostgreSQL is the initial supported relational database.

Rules:

- migrations are versioned and explicit;
- the server validates schema compatibility at startup;
- production deployment runs migrations separately from normal serving;
- `store.Store` is the root persistence contract and exposes model-specific
  stores such as `InstitutionStore` and `AcademicUnitStore`;
- each persisted model or cohesive aggregate receives its own store contract,
  `Sql<Model>Store` implementation, store file, adapter test, and reusable
  conformance suite when that persistence slice is implemented;
- the root store owns connection and schema lifecycle methods, while domain
  operations remain on their corresponding model stores;
- application and platform code consume `store.Store` or a model-store
  contract, never concrete SQL store types;
- do not expose raw database handles outside the adapter/composition boundary;
- database row structs, domain objects, and API DTOs are distinct where their
  responsibilities differ;
- cross-repository business transactions are explicit;
- uniqueness and foreign-key constraints enforce domain invariants where
  possible;
- use transactions for enrollment transfer, token consumption, state
  transitions, and related audit/outbox writes;
- translate driver-specific errors inside the PostgreSQL adapter;
- avoid generic retry layers; retry only known-safe idempotent work;
- pagination and authorization filters belong in queries, not post-processing;
- tests must prove cross-academic-unit isolation.

Do not add a `university_id` to every table for hypothetical multi-tenancy. One
installation is one institution. If the product later gains true multi-tenant
hosting, treat it as a major architecture decision and update this document.

## Reusable Package Boundaries

### VFS

Module: `github.com/sudosylabs/proctor/packages/vfs`

VFS owns portable file operations and backend semantics. It does not own:

- users or authorization;
- academic/exam metadata;
- retention policy;
- database records;
- application path conventions;
- antivirus/content policy.

The server owns semantic file paths, metadata, permissions, and lifecycle. Use
a narrow domain port where a use case does not need the complete VFS API.

Local VFS is for development/single-node use unless it points at genuinely
shared storage. S3-compatible storage is preferred for clustered deployments.

### Cache

Module: `github.com/sudosylabs/proctor/packages/cache`

Cache data must always be reconstructible. The package provides consistent
memory/Redis behavior, typed codecs, conditional writes, TTL semantics,
capability reporting, and optional counters.

The server owns:

- cache key namespaces;
- what is safe to cache;
- invalidation policy;
- authorization staleness limits;
- cluster invalidation messages.

Do not use cache as durable state, a distributed lock without an explicit
contract, or an implicit repository decorator. Prefer versioned namespaces for
broad invalidation.

### Mail

Module: `github.com/sudosylabs/proctor/packages/mail`

Mail owns transport-neutral messages, MIME composition, validation, SMTP, and
sender conformance.

The server owns:

- templates;
- localization;
- notification policy;
- recipient selection;
- rate limits;
- retries;
- durable delivery queue/outbox;
- dead-letter and administrative behavior.

Never send mail synchronously inside a transaction that changes authoritative
business state.

### Future packages

MFA, WebSockets, cluster coordination, identity, authorization, and coderunner
remain server/application concerns until their independent contracts are
understood. Do not extract them merely to imitate Mattermost's package layout.

Coderunner is security-sensitive and explicitly deferred until its threat
model, isolation boundary, resource limits, supported languages, artifact
model, and deployment topology are defined.

## Server Lifecycle

`app.NewServer` is the composition root and initializes in a deterministic
order:

1. parse command and locate configuration;
2. create the configuration backing and shared `config.Store`;
3. begin `platform.Service` construction and initialize operational logging;
4. within the platform, open PostgreSQL and validate schema compatibility;
5. within the platform, construct cache, VFS, and mail infrastructure, followed
   by cluster infrastructure when that transport exists;
6. construct the long-lived `app.App` and its product services;
7. finish cross-service wiring through the composition root;
8. construct HTTP API and WebSocket transports;
9. start background workers and inter-node communication;
10. start listeners;
11. mark readiness only after mandatory dependencies are ready.

Shutdown occurs in reverse dependency order with bounded deadlines.

Do not use scattered `defer` calls as the only lifecycle model for a complex
server. Track owned resources and make partial-startup failure cleanup explicit.

The process should distinguish:

- liveness: the process/event loop is functioning;
- readiness: the node can safely receive traffic;
- dependency diagnostics: detailed authorized/operator-only checks.

## API Contract

The server is API-first. Primary consumers are a desktop application and
probably a CLI.

Requirements:

- versioned HTTP API, initially `/api/v1`;
- consistent JSON and Problem Details error contracts;
- OpenAPI description maintained with the API;
- stable machine-readable error codes;
- cursor-based pagination where collections can grow;
- explicit idempotency support for commands vulnerable to client retry;
- request IDs returned to clients;
- capability/version endpoint;
- documented compatibility and deprecation policy;
- WebSocket protocol version and event schemas;
- reconnect/resynchronization behavior documented.

Avoid privileged behavior available only through an in-process API. The CLI
should normally use the same authenticated public/admin API as other clients.
Any local Unix-socket administrative mode requires an explicit threat model and
must not be an unauthenticated shortcut by default.

## Security and Privacy

Treat Proctor as a system containing sensitive educational data.

At minimum:

- TLS is required outside explicit local development;
- trust proxy headers only from configured proxies;
- set request body, header, URL, and upload limits;
- use constant-time credential comparisons where applicable;
- use established password hashing and cryptographic libraries;
- rate-limit login, token, invitation, and recovery endpoints;
- prevent account enumeration with safe public responses;
- store secrets and tokens hashed or encrypted according to their use;
- redact credentials from logs, traces, metrics, and errors;
- audit administrative and exam-sensitive actions;
- scope file access through authorization, never guessed paths alone;
- validate uploaded type/size and plan a malware/content scanning boundary;
- use least-privilege database and object-storage credentials;
- document retention and deletion semantics before implementing destructive
  student-data workflows;
- test horizontal privilege escalation and cross-department access.

Do not claim security merely because behavior was inspired by Mattermost.
Review and test every copied or adapted path.

## Testing Strategy

Every module must remain independently testable.

General Go checks:

```text
go test ./...
go test -race ./...
go vet ./...
```

Follow each module README and Makefile for its supported workflows.

Current reusable-package conformance:

- cache: memory tests plus Docker-backed Redis conformance;
- mail: memory tests plus Docker-backed Mailpit/SMTP conformance;
- VFS: shared backend conformance plus optional S3 integration tests.

Current server foundation tests include configuration, error contracts, route
authentication classification, HTTP middleware, health/readiness, CLI
behavior, graceful lifecycle shutdown, multi-target logger behavior,
configuration reloading/listeners, effective-versus-persisted environment
handling, platform logger reconfiguration, and shared application-graph
construction through `testlib`.

Future server tests should include:

- pure domain invariant tests;
- configuration default/validation/redaction tests;
- error-to-transport contract tests;
- route authentication classification tests;
- session creation, expiry, rotation, reuse, and revocation tests;
- authorization matrices across institution, academic unit, class, and exam;
- student single-active-class constraint and enrollment-history tests;
- PostgreSQL repository integration tests;
- two-node cluster tests sharing PostgreSQL, Redis/transport, and VFS;
- cross-node session and permission invalidation tests;
- cross-node WebSocket fan-out tests;
- WebSocket sequence, replay, loss, and resynchronization tests;
- graceful startup/shutdown and readiness tests;
- audit completeness and secret-redaction tests.

Docker-based conformance environments must:

- pin service image versions;
- bind test ports to loopback;
- use isolated names/networks;
- avoid persistence unless the test requires it;
- wait on health checks;
- clean up even when tests fail;
- allow ports/images to be overridden.

The server PostgreSQL conformance command is:

```text
cd server && make conformance-postgres
```

Do not make ordinary unit tests depend on network services or the local
Mattermost checkout.

## Go Engineering Rules

- Pass `context.Context` as the first parameter for I/O, waiting, and
  request-scoped operations.
- Do not store request contexts in long-lived structs.
- Wrap errors with useful operation context and preserve `errors.Is/As`.
- Avoid panic for expected runtime/configuration/input failures.
- Constructors validate dependencies and return errors.
- Prefer concrete types at construction and narrow interfaces at consumption.
- Define ordinary service interfaces near the consuming use case, not the
  adapter. Persistence contracts are the deliberate exception: root and
  per-model contracts live together in `server/store`, following Mattermost's
  cohesive store organization.
- Keep interfaces purposeful; a model store should contain the complete known
  persistence surface for that model rather than being fragmented into
  one-method interfaces. Do not add methods for hypothetical consumers.
- Make ownership of goroutines, channels, clients, and closers explicit.
- Every goroutine needs a shutdown path and an owner.
- Do not launch unbounded goroutines per request/event.
- Bound queues and define backpressure/drop/disconnect behavior.
- Avoid reflection-heavy configuration and framework magic.
- Use clocks and ID generators as injectable ports only where deterministic
  behavior or domain control requires it.
- Keep public APIs documented and stable in independently published modules.
- Format code with `gofmt`.

## Implementation Sequence

Unless the user reprioritizes, build the server as a walking skeleton:

1. licensing/provenance files and server module — complete;
2. cohesive `config → platform → app.Server → app.App → app/api` composition
   flow and shared `testlib` — complete;
3. configuration store, validation, backings, overrides, diffs, and listeners
   — complete for the current schema;
4. Proctor `mlog`, platform-owned configuration, and request correlation —
   complete for console/file targets;
5. lifecycle, liveness, readiness, and graceful shutdown — complete;
6. Mattermost-adapted application errors and HTTP Problem Details — complete;
7. model foundation, structural academic models, and identity/session model
   contracts — complete;
8. PostgreSQL connection management and migration command — complete for
   PostgreSQL 14+, including embedded up/down migrations, schema compatibility
   validation, Docker conformance, the Mattermost-shaped root/per-model store
   architecture, and the complete structural academic SQL store set;
9. cache, VFS, and mail adapters — complete for memory/Redis,
   disabled/SMTP, and local/S3 configuration, platform lifecycle, dependency
   checking, and memory test doubles;
10. cluster transport port and local implementation — complete for typed,
    bounded messages, one-handler-per-event dispatch, stable local node
    identity, peer-only broadcast semantics, self-targeted delivery, platform
    health/lifecycle ownership, and startup readiness gating;
11. identity/authentication services, credential rotation, and authentication
    middleware — complete for the first local-password, access/refresh session,
    login/refresh/logout/current-user vertical slice and self-service active
    session listing/individual/revoke-all management; external identity,
    password recovery, MFA, personal/service credentials, administrative
    session management, and durable security audit remain;
12. scoped authorization and audit — complete for action/resource contracts,
    current-state institution/academic-unit/class evaluation, role and binding
    stores, durable decision/critical-action auditing, and the privileged audit
    listing slice; role/binding administration APIs and bootstrap policy remain;
13. institution/academic hierarchy and enrollment vertical slice;
14. first two-node cluster tests;
15. WebSocket hub, cluster fan-out, and replay;
16. exam/proctoring vertical slices after their state models are confirmed.

Do not create every future domain entity or model-store interface up front.
Complete one vertical path through transport, use case, authorization,
persistence, and tests, then extend the root store with the next implemented
model store.

## Architecture and Documentation Evolution

This file must evolve with the project.

Update `AGENTS.md` when a change affects:

- domain terminology or invariants;
- module/package boundaries;
- dependency direction;
- licensing or source provenance;
- server directory structure;
- supported deployment topology;
- configuration sources or precedence;
- authentication/session/token behavior;
- authorization scopes or inheritance;
- cluster/WebSocket semantics;
- persistence or migration rules;
- required test/build commands;
- current implementation status;
- previously open decisions.

Do not update it for inconsequential implementation detail. Keep it normative,
accurate, and readable.

For substantial irreversible decisions, also add an Architecture Decision
Record under `docs/adr/` when that directory is introduced. An ADR explains why
a decision was made; this file states the resulting rule future agents must
follow.

When a task completes, check whether this document became stale. Prefer
updating it in the same commit as the architectural change.

## Agent Working Procedure

Before changing files:

1. read this file and any more specific nested `AGENTS.md`;
2. inspect `git status`;
3. identify ignored legacy versus tracked new implementation;
4. inspect relevant tests and module documentation;
5. state assumptions that could change architecture or domain behavior;
6. preserve unrelated user changes.

While working:

- make the smallest coherent change that advances the requested outcome;
- do not expand scope into unrequested migrations or rewrites;
- keep package dependency direction intact;
- add tests proportional to risk;
- use existing conformance suites where applicable;
- document copied/adapted Mattermost source immediately, not later;
- do not hide unresolved security or domain decisions behind generic
  abstractions.

Before handing off:

1. run relevant unit, race, vet, integration, and conformance checks;
2. review `git diff` for secrets, generated noise, and unrelated edits;
3. verify public docs and examples;
4. verify licensing/provenance;
5. update this file if the architecture or workflow evolved;
6. report what was verified and what remains unresolved.

## Confirmed Decisions

- The project is a new implementation in a large monorepo, not a clean-room
  reimplementation of every proven upstream component.
- Public module paths use `github.com/sudosylabs/proctor/...`.
- VFS, cache, and mail are reusable Apache-2.0 modules.
- The main server will be AGPLv3.
- Mattermost may be used as a behavior and eligible source reference with
  license compliance.
- Eligible Mattermost implementation should be copied or adapted when it fits
  Proctor better than a weaker reimplementation; exact provenance is mandatory.
- One logical installation represents one institution.
- Institution-defined organizational structures require a flexible,
  hierarchical academic-unit model.
- Programmes, programme levels, and classes are distinct concepts.
- `Class` replaces the old student-domain meaning of `Group`.
- A student has at most one active class per academic period and retains
  enrollment history.
- Teachers can hold different scoped roles in several academic units.
- Department/academic-unit roles inherit to descendant resources only for
  permissions granted by the role.
- Multiple active application nodes sharing state form an HA cluster.
- Clustered/horizontally scaled deployment will not be commercially gated.
- WebSocket fan-out is application-side and uses inter-node messages, not the
  database as an event transport.
- Authentication route wrappers, sessions, tokens, scoped permissions, and
  audit behavior should be inspired by Mattermost but redesigned around small
  explicit services.
- The server follows a cohesive Mattermost-inspired construction flow:
  `config.Store → platform.Service → app.Server/app.App → app/api`.
- `app.NewServer` is the sole composition root and `testlib` reuses that exact
  graph.
- Proctor owns its `mlog` API and implementation; the platform service owns its
  active configuration and lifecycle.
- `server/model` is the cohesive durable model package and uses
  Mattermost-inspired lifecycle, validation, audit, and `AppError` conventions.
- Academic identity is contextual: affiliations and memberships describe a
  user's relationships, while role bindings determine access.
- Sessions and API credentials persist hashes only and do not carry
  authorization role snapshots.
- PostgreSQL migrations use embedded Morph sources and an advisory migration
  lock; normal server startup validates schema compatibility but never applies
  migrations.
- Persistence follows Mattermost's store shape: one root `store.Store`,
  complete per-model store interfaces, one `Sql<Model>Store` per implemented
  model, and interface-returning accessors on `SqlStore`.
- SQL execution, PostgreSQL rebinding, timeouts, builders, and transactions are
  centralized in SQL wrappers. Per-model stores reuse select builders for reads
  and dynamic filters while retaining named SQL where clearer for writes.
- SQL adapters use explicit row representations and translate PostgreSQL
  constraint errors at the adapter boundary.
- Each model store has a reusable Mattermost-shaped conformance suite under
  `store/storetest` and a thin corresponding SQL adapter test.
- Desktop and CLI clients are primary API consumers.
- This `AGENTS.md` is a living document and must be maintained as the project
  evolves.

## Open Decisions

Keep this list current; remove items when decided and record the resulting rule
above.

- Exact initial identity-provider support order: OIDC, CAS, SAML, LDAP, and/or
  local accounts.
- Whether roles may bind directly to programme and programme-level scopes in
  addition to academic unit, class, and exam.
- Exam ownership and targeting: one class, several classes, programme level, or
  individually selected candidates.
- Exam lifecycle and concurrency/state-transition rules.
- Proctor assignment and violation review model.
- Concrete cluster transport: Redis, NATS, direct node protocol, or another
  implementation.
- Exact reliable cluster-delivery semantics and queue ownership across node
  failure.
- Whether a generated client SDK belongs in this monorepo and which languages
  are required by the desktop application.
- Coderunner threat model and service boundary.
