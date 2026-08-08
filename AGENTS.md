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

Core architecture documents:

- [`CONTEXT.md`](CONTEXT.md) defines the implementation-independent domain
  glossary;
- [`docs/architecture.md`](docs/architecture.md) is the canonical developer
  guide to boundaries, dependencies, structure, naming, errors, and testing;
- [`docs/adr/`](docs/adr/) records the rationale for durable decisions.

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

Mattermost is never a directory template. Prohibit wholesale, cosmetic, or
unreviewed file copying. Narrowly targeted source adaptation is allowed only
when the behavior genuinely fits Proctor, the exact upstream revision and path
are recorded, required notices are preserved, the adaptation is explained, and
Proctor-specific architecture and security review passes.

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

The tracked `/server` directory is the new server implementation. The
required architecture migration roadmap is complete; see
`docs/architecture-migration-acceptance-20260808.md`. Its first
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
- one cohesive construction graph currently rooted at `app.NewServer`,
  containing one `platform.Service`, one `app.App`, one configuration store,
  and one logger; moving this graph to the module-root `server.New` composition
  package is a documented architecture migration;
- a shared `testlib` that constructs the same application graph with memory
  configuration, captured logs, and a lifecycle-only persistence stub for
  unit tests that do not exercise durable stores;
- bounded HTTP server timeouts, request body/header limits, request IDs,
  security headers, access logging, panic recovery, and graceful shutdown;
- liveness, readiness, and version endpoints;
- a Mattermost-adapted `model.AppError` flow with stable translation IDs,
  translation hooks, wrapping, protected internal details, explicitly safe
  public fields, and RFC 9457 HTTP Problem Details mapping;
- a cohesive `model` package whose durable aggregates use entity-specific IDs
  (`UserID`, `ClassID`, …), UTC `time.Time`/`OptionalTime`, explicit
  constructors and transitions, `Validate`, and safe `Auditable`
  representations; SQL rows use native PostgreSQL temporal values, while
  transport-owned compatibility mappings preserve the existing public v1 wire
  contract where required;
- the confirmed structural academic models: institution, hierarchical academic
  unit, programme, programme level, academic period, and class;
- identity and authorization model foundations: user, external identity, local
  password credential, affiliation, academic-unit member, class member, role,
  role binding, session, hashed session credential, personal access token,
  purpose-specific user token, encrypted MFA credential, and hashed MFA
  recovery code;
- typed Mattermost-style `APIHandler`, `APISessionRequired`,
  `APIStrongSessionRequired`, `APIRecentSessionRequired`, composed
  strong/recent, and `APIRefreshCredentialRequired` wrappers that make the
  authentication contract explicit for every registered route;
- Mattermost-style per-domain API registration, currently using exported
  `Init*` methods, with one central `BaseRoutes` tree and registrar enforcing
  explicit authentication policy, duplicate detection, regex-constrained path
  variables, centralized typed request parameters, and route-matrix tests;
  replacing exported initializers with unexported `register<Area>Routes`
  functions is a documented convention migration;
- a checked-in OpenAPI 3.1 contract covering every registered HTTP operation,
  including its authentication alternatives, transport-owned request and
  response DTOs, and stable errors, with schema and bidirectional runtime
  agreement checks enforced by the ordinary server validation gate;
- platform-owned cache, mail, and VFS ports with root-selected concrete
  adapters from typed deployment configuration (memory/Redis cache,
  disabled/SMTP mail, local/S3 VFS), dependency checks, deterministic cleanup,
  and memory test implementations; the architecture dependency debt list is
  empty and forbidden production imports fail immediately;
- a Mattermost-shaped cluster message contract and server-owned cluster port
  with typed handlers, best-effort-only delivery, stable node identity, bounded
  messages, startup/readiness/shutdown ownership, a loop-safe single-node
  `local` transport, and a built-in multi-node Memberlist transport with
  PostgreSQL discovery leases and encrypted gossip; clustering requires no
  Redis service and configuration accepts only `local` or `memberlist`;
- a Mattermost-shaped WebSocket hub with authenticated session upgrades,
  CPU-sharded connection ownership, cookie-origin enforcement,
  resource/action-authorized subscriptions,
  bounded outbound and replay queues, monotonically increasing per-connection
  sequences, ping/pong liveness, backpressure disconnects, local reconnection
  replay, and explicit client resynchronization when replay is unavailable;
- application-owned local-first event publication with loop-free best-effort
  cluster fan-out, plus cross-node session revocation, authentication-cache
  invalidation, and role/permission-change connection invalidation;
- the first complete identity slice: transactional local-user/password
  persistence, bounded Argon2id password hashing, generic login failures,
  server-side sessions, hashed opaque access and refresh credentials, refresh
  rotation and replay revocation, debounced activity, concurrent-session
  limits, cache-backed authentication resolution and login throttling;
- public login, refresh-credential, session-required logout, and current-user
  HTTP endpoints, plus self-service active-session listing, individual session
  revocation, and account-wide session revocation;
- dual session transport: Electron/web sessions use host-only HttpOnly
  access/refresh cookies plus a rotating signed double-submit CSRF contract,
  while CLI sessions use Authorization bearer credentials; ambiguous mixed
  credential sources and duplicate credential cookies are rejected;
- serialized per-user session lifecycle transactions across login, refresh,
  individual revocation, and revoke-all, preventing refresh rotation or a
  concurrent login from escaping an account-wide security reset;
- a closed action registry, resource contracts, and current-state scoped
  authorization evaluator with additive roles, deny-by-default behavior,
  institution and ancestor academic-unit inheritance, exact class scope,
  deleted-role exclusion, and no permission snapshots in sessions;
- reusable principal permission predicates, typed institution/academic-unit/
  class/user helpers, contextual cross-user visibility through institution
  `user.view` or inherited `class.members.view`, and transport-safe user reads
  that retain default denial outside those relationships;
- complete role, role-binding, and durable audit SQL stores with reusable
  conformance suites, role batch resolution, serialized time-range overlap
  protection, polymorphic scope-reference validation, hierarchy resolution,
  keyset audit pagination, and attempt-only terminal audit transitions;
- fail-closed durable auditing of authorization decisions with actor, session,
  request, node, direct peer, client, authentication, resource, and scope
  context, plus application primitives for audited critical mutations;
- an explicitly privileged `GET /api/v1/audits` vertical slice that enforces
  `audit.view` in the application layer and returns cursor-paginated events.
- an explicit public installation-status/bootstrap boundary backed by an
  atomic PostgreSQL aggregate that creates the singleton institution, first
  local administrator, protected built-in system-administrator role,
  institution-scoped binding, installation marker, and success audit event;
- audited role and role-binding administration APIs, recognized-permission
  validation, protected built-in roles, institution-only system-administrator
  bindings, immediate current-state authorization changes, and transactional
  protection against ending the final active system-administrator binding;
- visible API-handler permission preflights for the current role,
  role-binding, and audit administration routes, with a private, sealed,
  request-bound, one-use decision receipt that prevents duplicate
  authorization queries/audits while preserving authoritative application
  authorization for direct and non-HTTP callers; retiring these preflights and
  receipts in favor of application-only resource authorization is a documented
  architecture migration;
- complete audited structural-academic administration through application and
  HTTP layers for the singleton institution, academic-unit hierarchy,
  programmes, programme levels, academic periods, and classes;
- effective-dated affiliation and academic-unit membership administration,
  plus transactionally serialized class enrollment, transfer, progression
  history, and enforcement of one non-overlapping class enrollment per student
  and academic period;
- administrative user search/profile updates, enable/disable, immediate
  account-session revocation, affiliations, organizational membership, and
  enrollment operations;
- teacher-to-student visibility derived from current class enrollment and
  inherited `class.members.view`, while unrelated and cross-scope users remain
  hidden by default;
- explicit strong, recent, and combined strong/recent session route
  requirements backed by the immutable request principal;
- a transactional user-token SQL store with target-bound, hashed, expiring,
  single-use email-verification and password-reset credentials;
- email-verification and password-reset application/API slices with
  shared-cache throttling, generic public reset responses, fragment-delivered
  mail links, durable success audits, and atomic account-wide session
  revocation after a password reset;
- a complete personal-access-token vertical slice with bounded finite
  lifetimes, one-time raw credential delivery, hashed PostgreSQL persistence,
  explicit known-action scopes, optional academic-unit subtree constraints,
  debounced last-used metadata, self-service listing/disable/enable/revocation,
  recent-session creation and re-enablement, durable audits, and bearer
  authentication on ordinary principal routes;
- separate `APIPrincipalRequired` and `APISessionRequired` route contracts:
  PATs may call ordinary application resources subject to both current roles
  and token ceilings, but can never satisfy interactive session, recent
  authentication, refresh, or token-management requirements.
- TOTP MFA with AES-256-GCM encrypted secrets, expiring pending setup,
  transactionally replay-protected activation and challenges, hashed
  single-use recovery codes, one-time recovery-code display and regeneration,
  local-password login-time second-factor enforcement, current-session
  assurance upgrades, and account-wide assurance downgrade on disable;
- dedicated `session.view` and `session.manage` actions with visible API
  preflights, authorized active/history listing, audited individual and
  account-wide administrative revocation, and immediate access-cache
  invalidation;
- an instance-scoped external-identity provider registry with protocol-neutral
  application contracts, independently configured direct CAS 3 and generic
  OIDC adapters, durable hashed/browser-bound one-use login state, exact
  callback binding, OIDC discovery and Authorization Code with PKCE, strict
  assertion validation, collision-safe user provisioning, ordinary Proctor
  sessions, explicit provider MFA mapping, safe public provider discovery, and
  durable provisioning/login audits.

The server now includes PostgreSQL connection management, one pre-release
version-1 schema baseline with native `timestamptz` and nullable lifecycle
columns, a separate migration command, platform-owned schema validation, a
Mattermost-shaped root store with per-model contracts, and all structural
academic SQL stores: institution, academic unit, programme, programme level,
academic period, and class. It also includes user, password-credential,
external-identity, external-login-state, session, session-credential,
user-token, personal-access-token, MFA credential, MFA recovery-code,
affiliation, academic-unit-member, class-member, role, role-binding, and audit
SQL stores with reusable conformance tests, plus the atomic
installation-bootstrap store. Root composition wraps that complete store in a
bounded local-cache layer around a semantics-preserving timer layer and a
safe-by-default retry layer. The initial cache allowlist contains only
`AcademicPeriodStore.Get`: values use defensive serialization copies, a
30-second TTL, bounded process-local capacity, successful-mutation invalidation,
generation-guarded concurrent fills, best-effort peer invalidation, and
authoritative fallback after misses, cache failures, corrupt entries, lost
invalidation messages, or expiry. Authorization, role, account, session,
credential, MFA, and token reads bypass this cache. Retry is
handwritten and opt-in for idempotent reads, uses bounded cancellation-aware
backoff, and recognizes only adapter-classified PostgreSQL serialization and
deadlock failures; mutations, domain errors, and other failures remain
single-attempt. The timer's closed operation and outcome vocabulary cannot
receive persistence arguments, results, context values, or error details;
store-layer SQL conformance tests cover every bounded contract.
External account-linking administration, service accounts, exam-domain, and
SAML/LDAP providers remain unimplemented. Cross-node WebSocket replay handoff
is also not implemented: reconnecting on a node without the prior bounded
queue receives an explicit resynchronization instruction.

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
    contracts, following the readable aspect of Mattermost's one-package model
    organization without adopting its broader shared-contract role. Do not put
    HTTP, WebSocket wire, cluster transport, SQL, Redis, SMTP, or filesystem
    contracts there. Do not turn it into a general utility dumping ground, and
    avoid catch-all packages named `utils`, `common`, or `shared`.
16. Avoid global mutable application state and global service locators.
17. Use Proctor's module-root `server.Server`, `app.App`, and bounded
    `platform.Service` construction flow. Persistence deliberately has one bounded root
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
- Identity, authorization, academics, exams, application services, transports,
  and persistence remain within the cohesive server module while they are
  Proctor-specific. Internal package size or reuse by multiple server packages
  is not sufficient reason to create another module.
- Extract a server capability into an independently versioned module only when
  it has a coherent Proctor-independent contract, plausible external
  consumers, its own compatibility policy, and no imports from the server.
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
OIDC, CAS, SAML, LDAP, or a particular university directory. Providers are
constructed through an instance-scoped registry owned by the platform
composition root; do not introduce global mutable provider registration or
protocol switches in the application login orchestration. Direct institutional
CAS and generic OIDC are the first adapters. SAML/RENATER may be added behind
the same boundary; LDAP and service-account priority remain undecided.

Provider boundary rules:

- each protocol lives in its own adapter package beneath
  `server/platform/externalauth`;
- factories are explicitly assembled once at the platform composition root and
  registered on an instance registry, following the useful separation in
  Mattermost's OAuth-provider design without Mattermost's process-global
  mutable registry;
- the application sees only provider descriptors, a generic begin challenge,
  protocol-owned callback-state extraction, and a normalized trusted
  assertion;
- provider callbacks are bounded opaque fields at the HTTP boundary. The API
  and application must not learn the meaning of `ticket`, `code`,
  `SAMLResponse`, or future protocol-specific values;
- configuration is a strict discriminated union: exactly one protocol block
  must correspond to the selected provider type;
- configuration reload builds a complete replacement provider set before
  atomically swapping it into the registry. In-flight requests retain their
  already-resolved provider instance;
- provider network clients use bounded responses, timeouts, and redirect
  rejection. Errors exposed to logs or `AppError` must redact provider
  credentials, codes, tickets, tokens, response bodies, and claims.

Do not register a generic SAML HTTP-POST callback prematurely. The current
browser proof cookie is SameSite=Lax and is intentionally not sent on a
cross-site IdP POST. A future SAML adapter needs a reviewed two-stage design:
validate and retain the bounded signed response as an intermediate one-use
transaction, redirect to a same-origin GET where the browser proof is present,
then finalize the Proctor session. Do not weaken the cookie globally to
SameSite=None merely to make SAML POST appear to work.

CAS identity rules:

- a configured provider has a stable lowercase ID independent of its protocol
  endpoint, and the durable identity key is `(provider ID, opaque subject)`;
- the authoritative subject is explicitly mapped to `<cas:user>` or a released
  attribute; never assume `<cas:user>` is an ePPN and never use email as the
  identity key;
- external login initiation creates a PostgreSQL-backed, hashed, expiring,
  one-use state bound to a separate host-only HttpOnly SameSite=Lax cookie so
  any application node can receive the callback without enabling login CSRF;
- the callback consumes state once, validates the ticket through the CAS back
  channel against the exact service URL, and then creates an ordinary Proctor
  session through the shared session service;
- CAS ticket, raw state/binding credentials, subjects, and released attributes
  must never appear in operational logs or audit data;
- auto-provisioning may create a new user and `ExternalIdentity` atomically but
  must never merge with an existing user merely because username or email
  matches; collisions require explicit future account linking;
- released affiliation values do not create role bindings, permissions, class
  memberships, or academic-unit memberships. Affiliation reconciliation is
  deferred until provider ownership and deprovisioning semantics are modeled;
- provider MFA satisfies `AuthenticationMultiFactor` only when an explicitly
  configured trusted assertion value is present. CAS success or `renew=true`
  alone does not prove MFA; otherwise the session starts single-factor and may
  use Proctor's MFA challenge for step-up authentication;
- proxy-granting tickets, proxy tickets, gateway login, and CAS single logout
  are outside the first slice. Local Proctor logout always remains available;
- direct CAS is for the institution owning the installation. A future
  multi-institution federation use case belongs in SAML/RENATER or OIDC, not
  implicit tenant routing inside the CAS adapter.

OIDC identity rules:

- use issuer discovery and verify the discovered issuer exactly; never
  configure independent authorization, token, user-info, and JWKS endpoints
  that can silently drift apart;
- use Authorization Code flow with S256 PKCE even for the confidential Proctor
  server client;
- bind state to the durable one-use transaction and HttpOnly browser proof. The
  same high-entropy proof is the PKCE verifier, and a domain-separated digest
  of it is the OIDC nonce;
- require and cryptographically verify the ID token signature, issuer,
  audience, expiry, and nonce. Verify `at_hash` whenever the ID token includes
  it;
- if user-info retrieval is enabled, require its `sub` to exactly equal the ID
  token subject. User-info data may enrich profile claims but may never
  override ID-token authentication time or MFA claims;
- `sub` is always the OIDC external-identity subject. Do not substitute email,
  username, ePPN, or another profile claim;
- provider access tokens, refresh tokens, authorization codes, ID tokens, and
  raw claims are ephemeral login material. Do not persist, return, log, or
  audit them;
- OIDC MFA is recognized only from an explicitly configured trusted ID-token
  claim/value. An arbitrary successful OIDC login is not automatically MFA;
- Apereo CAS can act as an OIDC provider only when that support is enabled and
  Proctor is registered as an OIDC relying party. A CAS deployment does not
  imply that its OIDC or SAML endpoints are available.

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

### Initial installation bootstrap

Bootstrap is an explicit installation operation, not a side effect of ordinary
user registration. Never grant administrator access merely because a user
happens to be the first account observed by one application process.

The public bootstrap status exposes only whether the logical installation has
been initialized. It must not expose the administrator ID, institution ID,
account existence, or other durable identifiers.

The one successful bootstrap transaction creates:

- the singleton institution;
- the first local administrator account and encoded password credential;
- the protected built-in `system_admin` role containing every action currently
  recognized by the closed action registry;
- an institution-scoped role binding for that administrator;
- the singleton durable installation marker;
- a terminal successful `installation.bootstrap` audit event.

PostgreSQL serializes bootstrap attempts with a transaction-scoped advisory
lock. The transaction requires a pristine database: an absent marker is not
enough if institution, user, role, or role-binding records already exist.
Concurrent nodes may attempt bootstrap, but exactly one transaction may
succeed. Failed or losing attempts create no partial account, institution,
credential, role, binding, marker, or success audit event.

Bootstrap requests are source-rate-limited before password hashing. Plaintext
passwords and encoded password hashes are never returned, logged, or audited.
The successful administrator signs in through the ordinary authentication
flow after bootstrap; bootstrap does not mint a special session.

The `system_admin` role is owned by server code. Administration APIs may not
patch or delete built-in roles, and system-administrator bindings may exist
only at institution scope. Ending administrator bindings is serialized and
must leave at least one other active system-administrator binding at the
effective end time. When the action registry grows, the same release must
include a migration or reconciliation step that adds the new actions to the
built-in role for initialized installations.

### API authentication boundary

Preserve the strong behavior behind Mattermost-style `APISessionRequired`
wrappers without copying the large custom handler object.

The HTTP pipeline should be conceptually:

```text
panic recovery
→ request ID and request metadata
→ body/URL limits and security headers
→ bearer/cookie credential extraction and ambiguity rejection
→ CSRF verification for unsafe cookie-authenticated requests
→ session authentication
→ principal attached to context
→ route authentication-strength requirement
→ request DTO decoding and application invocation construction
→ application use case with immediate authoritative resource authorization
→ response/error mapping
→ audit and operational logging
```

Every route must explicitly be one of:

- public;
- authenticated session required;
- MFA/strong authentication required;
- recent reauthentication required;
- personal/service credential required.

Administrative privilege is not a route authentication class. An
administrative route first declares the credential or assurance level it
requires, then its application use case checks a stable action against the
actual resource. A valid session alone never implies administrative access.

Route authentication requirements must be composable and testable. Add a route
matrix test that fails when a route lacks an explicit classification.

The API package follows Mattermost's readable per-domain route ownership
without exporting its initialization surface. `api.New` constructs shared
routing state and calls unexported functions such as
`registerSystemRoutes`, `registerAuthenticationRoutes`, and
`registerAcademicUnitRoutes`. Each transport-area file owns its routes, DTOs,
handlers, and mappings. Registration must pass through the central registrar,
use an explicit typed authentication/assurance wrapper, and never mutate router
internals directly. The registrar rejects nil or unclassified handlers and
duplicate route shapes. The root `api.go` must not become a flat inventory of
every endpoint. Existing exported `Init*` methods predate this convention and
are migration work.

Ordinary HTTP handlers return a typed status/body result and standard `error`
through one handler adapter. Central code writes JSON, common headers, and
Problem Details; a handler must not partially write a response while an
operation can still fail. Streaming, downloads, WebSocket upgrades, and similar
protocols are explicit dedicated-handler exceptions.

Command and mutation DTOs decode exactly one bounded JSON value, reject unknown
fields and trailing data, and return field-specific safe validation errors.
Unknown keys are allowed only inside an explicitly named, bounded extension
object whose contract is designed for them.

PATCH DTOs use a transport-owned `Optional[T]` representation that distinguishes
omitted, present zero, and explicit null where clearing is allowed. Handlers map
that state into typed application patch commands; plain pointers do not carry
ambiguous merge semantics into domain models.

The root router is a `gorilla/mux` route tree exposed through `API.BaseRoutes`.
All versioned resource routers descend from `BaseRoutes.APIRoot`, which is
constructed from the single client-facing `model.APIURLSuffix` constant. Domain
initializers register paths relative to their resource router; they must never
repeat `/api/v1` or another API generation in individual endpoint files.
Resource identifiers use named regex variables with the canonical Proctor
z-base-32 alphabet and length. Registration functions add paths relative to
their resource router and never repeat `/api/v1` or another API generation.
Add literal resource routes before permissive variable routes when their
patterns could overlap.

Route variables are read exactly once through `ParamsFromRequest`, normalized,
and attached to the request context before authentication and handlers run.
Handlers consume the typed `RequestParams` contract and its `Require*` methods;
they must not split `request.URL.Path`, call `request.PathValue`, or scatter
direct `mux.Vars` lookups across domain files. Extend `Params` only for
variables or common bounded query parameters actually used by registered
routes.

Authentication proves identity. It does not grant permission to a resource.
Transport middleware classifies credential and assurance requirements, then
constructs the explicit application invocation. Handlers do not preflight
resource permissions. The application use case is the single authoritative
resource-authorization boundary and performs its action/resource check
immediately, before avoidable expensive work or mutation. Request and body
limits protect the decoding boundary without duplicating policy in transport.

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

The primary desktop application is Electron. Electron/web interactive sessions
use the browser cookie transport:

- the opaque access and rotating refresh credentials are delivered only as
  host-only HttpOnly cookies and omitted from JSON;
- the access cookie applies to the server, while the refresh cookie is limited
  to the versioned refresh endpoint;
- production cookies are Secure and use SameSite=Lax;
- unsafe cookie-authenticated methods, including refresh and logout, require
  the `X-Proctor-CSRF-Token` value derived from the rotating CSRF cookie pair;
- successful refresh rotates both credentials and all CSRF cookies;
- logout, current-session revocation, revoke-all, and invalid cookie
  authentication clear the cookie set;
- bearer and cookie credentials in one request are rejected rather than
  resolved by precedence;
- automatic refresh with replay/reuse detection;
- server-side revocation.

This contract assumes the Electron renderer is loaded from the installation's
public server origin, as in Mattermost Desktop. If a future bundled renderer
uses a different origin, do not weaken SameSite or CORS casually: route
credential operations through a trusted Electron main-process boundary or
design and review an explicit cross-origin contract.

The current CAS and OIDC adapters use a server-origin browser login/callback
flow. CAS has no OAuth authorization code or PKCE; OIDC uses Authorization Code
with S256 PKCE and nonce validation. When Electron loads the installation
origin, either flow may run in its browser session and the resulting host-only
cookies remain in the same cookie jar. Do not claim that opening either flow in
the operating-system browser automatically authenticates Electron: that
requires a separately designed, one-time desktop handoff through a trusted
main-process boundary. The desktop application must not collect university
passwords when a proper external flow is available.

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

Implemented email-verification and password-reset tokens are additionally
bound to the normalized account email that existed when they were issued.
Changing the account email invalidates consumption rather than verifying or
resetting a different address. Issuing a new token transactionally invalidates
the prior active token of the same purpose. Browser links carry the raw
credential in the URL fragment so it is not sent in HTTP request targets; the
client must submit it in a bounded JSON body to the completion endpoint.

Public password-reset requests return the same accepted response for unknown,
disabled, external-only, and eligible local accounts. Known-account
persistence or delivery failures are logged without the email or credential
and do not change the public response. Password-reset completion atomically
updates the password credential, consumes the token, revokes all account
sessions, and inserts the terminal security audit.

CLI API credentials are accepted from the `Authorization` header. Electron/web
sessions use the cookie contract above. Never accept access or refresh tokens
in URL query parameters: URLs are frequently recorded in logs, history, and
monitoring systems.

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
- HTTP and WebSocket handlers do not evaluate resource permissions or issue
  authorization receipts.
- Every actor-sensitive application use case authorizes its stable action
  against the actual resource immediately and records the required durable
  authorization decision.
- Generic resource `PrincipalHasPermissionTo*` methods are non-auditing
  predicates for composing policy inside the application.
  `AuthorizePrincipalTo*` methods are the application security boundaries;
  they record allow/deny decisions durably and fail closed.
- Visibility helpers such as `UserCanSeeOtherUser` are contextual application
  policy, not aliases for session authentication.
- Store list/search methods must constrain results by authorized scope;
  do not fetch all records and filter them in memory.
- WebSocket commands and subscriptions must use the same authorization service.
- Background jobs invoke application use cases through an explicit
  system/service `Invocation`; they do not manipulate stores directly to
  bypass authorization, audit, invariants, or atomic operations. Batch work may
  use a dedicated application use case, which remains the policy boundary.
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

The current user-visibility policy grants self-view, but not implicit
self-management. Cross-user access is denied unless an institution-scoped role
explicitly grants `user.view` or `user.manage`. Academic-unit/class
teacher-to-student visibility must be added only after the membership stores
and its exact relationship rules are implemented; it must not be guessed from
profile fields. User authorization audit records keep the target user as the
resource and the institution as the separate academic authorization scope.

## Target Server Architecture

The server should use explicit composition and business-oriented vertical
slices.

Transport, application, domain, and persistence are conceptual responsibility
and dependency boundaries, not mandatory top-level directory names. Preserve
the cohesive Proctor package shape where responsibilities remain clear:
`app/api` owns HTTP transport, `app` owns application use cases, `model` owns domain
contracts, `store` owns persistence ports, `store/sqlstore` owns PostgreSQL
adapters, `websocket` owns the WebSocket transport, and `platform` owns shared
infrastructure lifecycle and external adapters. Introduce a new package only
when it creates a clearer ownership boundary; do not mirror every conceptual
layer with a directory mechanically.

Current foundation and growth direction:

```text
server/
├── go.mod
├── LICENSE
├── NOTICE
├── cluster/
│   ├── local/
│   └── memberlist/
├── cmd/
│   └── proctor/
├── config/
│   └── configtest/
├── mlog/
├── platform/
├── websocket/
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

Application capabilities begin as cohesive vertical-slice files in `app`.
Extract `app/<domain>` only when the capability has substantial internal
structure, a stable responsibility, several collaborating components, and a
narrow facade. An extracted domain package must not import its parent `app`
package. Do not create generic `services`, `usecases`, or `controllers`
directory trees, and do not create one package per entity for symmetry.

Within a package, organize files by responsibility or vertical slice, not by
arbitrary size or a mechanical type-per-file rule. Split a file when part of it
has a distinct reason to change. Keep tests beside the responsibility they
verify. Do not create catch-all `helpers.go`, `utils.go`, `common.go`, or
`types.go` files; name a shared file after the precise concept it owns.

Package names are short, lowercase, singular, and describe one responsibility.
Do not use underscores or vague names such as `util`, `common`, `shared`,
`base`, or `core`; do not create layer-only packages such as `services` or
`repositories`. Avoid package/type stutter. Protocol names such as `oidc` and
`cas` are appropriate for concrete adapters when the protocol is the package's
actual responsibility.

Keep readable direct server package paths rather than moving the implementation
under a broad `internal/` tree. The server module is not a promised reusable Go
library; independently reusable contracts belong in separate `packages/*`
modules. Control exported identifiers deliberately and enforce production
imports through architecture tests.

Application use cases are explicit methods with typed command or query inputs
and typed results, for example `CreateAcademicUnit(ctx,
CreateAcademicUnitCommand)` or `GetAcademicUnit(ctx, GetAcademicUnitQuery)`.
Use direct calls through small consumer-owned interfaces. Do not introduce a
generic command bus, query bus, reflection-based dispatcher, or a one-method
`Execute` interface for every use case. Command and query structs replace long
positional parameter lists and provide a visible home for use-case input.

Every actor-sensitive use case receives an explicit immutable `app.Invocation`
beside its command or query. The invocation contains the acting principal and
safe call metadata required for authorization and audit and is constructed by
HTTP, WebSocket, CLI, job, or test callers. Do not hide principals, permissions,
or audit metadata inside `context.Context`; reserve context for cancellation,
deadlines, and request-scoped propagation.

Focused service implementations inside package `app` are normally unexported,
for example `authenticationService`, `authorizationService`, and
`auditService`. The package exports only commands, queries, results,
`Invocation`, `Error`, and construction contracts genuinely needed across a
package boundary. If a capability is later extracted into `app/<domain>`, that
package may export one narrow facade and constructor.

Dependency direction:

```text
model
  ↑
store
  ↑
app
  ↑       ↑
app/api  websocket
   ↖     ↗
    server (composition root)
  ↑
cmd/proctor
```

The module-root `server.New` is the sole composition root. The command must not
independently construct another logger, cache, store, mailer, VFS, or
application service. Only this composition flow may select concrete
infrastructure implementations or depend on the broad `platform.Service`.
`server.New` translates platform-owned capabilities into an explicit
application dependency bundle; individual application services receive only
the narrow ports they consume.

Backend selection may be organized across cohesive files in the module-root
`server` package, but it remains part of the one composition boundary.
`platform.New` receives already-constructed persistence, cache, cluster, mail,
VFS, external-authentication, and similar capabilities and owns their shared
lifecycle; it must not switch on configuration to select their concrete
implementations.

`platform.Service` remains a bounded runtime owner for shared infrastructure
health, dynamic reconfiguration, startup, and orderly shutdown. It may be
retained by `server.Server`, but it is not passed into `app` and is not a
general capability locator. Composition passes each constructed capability
both to the platform lifecycle owner and, through a narrow port, to the actual
consumer that needs it.

Constructors build inert objects and do not normally start goroutines,
listeners, or background work. Explicit `Start` methods begin owned work;
`Close` or `Shutdown` is idempotent and bounded. The root starts components in
dependency order and unwinds every already-constructed or started resource if
later construction or startup fails.

The current runtime `Server` and `NewServer` still live in package `app`.
Because Go dependency boundaries apply to packages rather than files, this
forces the application package itself to import transport and platform
packages. Moving runtime composition to the module-root `server` package is a
documented architecture migration; after it, package `app` must not import
`platform`, `app/api`, or concrete adapters.

Production imports follow the inward dependency graph:

- `model` imports no other Proctor server package;
- `store` imports domain contracts from `model`;
- `app` imports `model`, persistence contracts from `store`, and
  consumer-owned capability ports, but no transport, platform, or concrete
  adapter;
- `app/api` is an outer transport boundary and may import `app` and `model`,
  but not persistence or concrete infrastructure;
- `websocket` is a sibling outer transport boundary with the same inward
  dependency rule; it owns the hub, connections, protocol DTOs, sequencing,
  replay, protocol errors, and upgrade handler;
- `platform` and concrete adapters sit on the infrastructure side, implement
  inward-facing ports, and never become application dependencies;
- the module-root `server` composition package may import all packages needed
  to select concrete backends and assemble the graph;
- `cmd/proctor` remains a thin invocation boundary for `server.New`.

Tests and `testlib` may cross production boundaries to assemble and inspect the
real graph. A small automated import-boundary test enforces the production
allowlist with an empty dependency debt ledger. The required architecture
migration is complete: module-root composition selects concrete infrastructure,
`platform.New` receives already-constructed capabilities, HTTP uses a
consumer-owned logger port, and WebSocket lives in the sibling `websocket`
package. See `docs/architecture-migration-acceptance-20260808.md`.

The server module is deliberately cohesive and may have internal coupling.
Coupling must follow ownership and construction flow rather than form import
cycles. API handlers receive the application operations they need; product
logic must not create infrastructure independently.

Interfaces are normally owned by the consuming package and expose only the
capability that consumer needs. Transport domains in `app/api` depend on
narrow application-operation interfaces, and application services define
narrow ports for infrastructure they consume. A broad aggregate interface may
exist at a composition boundary for wiring, but it must not become the
day-to-day dependency of unrelated handlers or services.

Persistence is the deliberate exception: the cohesive per-model store
contracts and bounded root `store.Store` live together in `server/store`.
Concrete adapters implement those contracts without redefining them, and
consumers may accept a narrower per-model store contract when they do not need
the root store.

`app.App` must not retain `*platform.Service` or expose general `Platform`,
`Store`, `Cache`, `Cluster`, `Mailer`, or `VFS` accessors. Such accessors hide
dependencies and turn the application facade into a service locator. The
current implementation predates this decision and still retains the platform
service; removing that dependency is a documented architecture migration, not
authorization to perform an unrelated rewrite.

Application services do not receive a logger by default. Request-driven use
cases return failures and let the outer operational boundary log unexpected
errors once. A long-lived worker or service may receive a small consumer-owned
logging port only for meaningful operational events no caller can observe.
Application code never reaches a global logger or depends directly on
`mlog.Logger`; the current `App.Log()` accessor is part of the platform-facade
migration.

## Configuration

Separate deployment configuration from application settings.

Deployment configuration includes:

- HTTP listener and public URL;
- PostgreSQL connectivity and pool settings;
- cache backend;
- cluster transport;
- VFS backend and credentials;
- SMTP transport;
- external identity-provider endpoints, claim mappings, provisioning policy,
  home-organization restrictions, and trusted assurance values;
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

Application services do not receive the full deployment `config.Config` or
`config.Store`. The composition root translates required values into small
immutable application policy structs or narrow provider functions, such as
`SessionPolicy` or `PasswordPolicy`. Runtime changes reach a consumer only
through an explicit capability-specific port; unrelated deployment settings
must not become visible to that service.

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
- runtime reconfiguration is capability-specific: logging and the
  external-provider registry reconfigure dynamically, while listener
  addresses, HTTP limits, cluster backend, and cluster node identity require a
  process restart;
- configuration backing conformance must be reusable when another backing is
  introduced.

Planned administrative commands:

- `proctor serve`;
- `proctor migrate`;
- `proctor config validate`;
- `proctor doctor`;
- `proctor version`.

## Models and Error Flow

`server/model` is the cohesive, domain-focused package. Keep domain model files
flat and cohesive, normally one file per substantive model, rather than
creating a directory tree for every entity or bounded context. It owns domain
entities, value objects, principals, actions and resources, local invariants,
and domain validation failures. Application error identity belongs to `app`,
not the domain package.

`server/model` is not a general server contract package. HTTP Problem Details,
WebSocket wire messages, cluster transport messages, persistence rows, and
adapter-specific configuration belong to their owning boundaries. Request and
client metadata that exist only to describe a transport invocation belong at
the application/transport boundary rather than in the domain package.

HTTP request and response DTOs belong to the corresponding transport area in
`app/api`. Handlers map request DTOs into application commands or queries and
map application results into stable response DTOs. Mutable domain entities do
not double as JSON wire formats. Domain identifiers and enums may be reused
only when they are deliberately part of the public contract.

Domain models own pure local invariants and state transitions involving one
entity or aggregate. Application services own use-case orchestration,
authorization, multi-aggregate coordination, transaction intent, auditing,
and external effects. Persistence adapters enforce storage constraints and
translate driver failures but must not decide business policy. Transports
validate and map wire contracts and establish authentication requirements but
must not implement business rules.

Validation follows the same ownership boundaries:

- transport validates wire shape, required encoded fields, and size limits;
- application commands validate use-case prerequisites and authorization;
- domain constructors and transitions enforce local business invariants;
- atomic store operations and database constraints enforce cross-row and
  concurrency invariants.

Do not duplicate the complete validation rule set across handlers, models, and
SQL. Each boundary translates only the validation failures it owns.

The current implementation predates this clarified boundary: cluster and
WebSocket contracts, request/client metadata, and `model.AppError` remain in
`server/model`; application methods still return the concrete
`*model.AppError`, which carries HTTP status and translation state. Their
relocation or redesign is a documented architecture migration to perform
through coherent vertical changes, not a reason for an unrelated bulk rewrite.
Several HTTP handlers also still decode or serialize model types directly;
introducing transport-owned DTOs is part of that vertical migration.

Domain models use explicit constructors and named domain transition methods
for normalization and local invariants. Use `Validate() error` when complete
rehydrated state must be checked. Do not use persistence-lifecycle methods such
as `PreSave`, `PreUpdate`, or `IsValid`: a domain model must not know whether it
is about to be inserted or updated in PostgreSQL.

The application supplies generated IDs and timestamps through narrow
dependencies where determinism matters. Domain operations may accept the
resulting ID or time explicitly, but must not reach for a global clock or
random generator. Domain and application time uses UTC `time.Time`; PostgreSQL
uses `timestamptz`; HTTP uses RFC 3339. Optional lifecycle times use nullable
values such as `archived_at`, never integer zero sentinels. The pre-release
schema baseline and durable aggregates follow this contract; explicit legacy
wire mappings do not restore millisecond persistence fields or zero sentinels.
Existing 26-character ID representation
remains the canonical identifier format: IDs are opaque, URL-safe, random
z-base-32 values generated without database coordination. Do not encode
ordering, institution, or domain meaning in an ID.

Each domain entity uses a distinct string-backed identifier type, such as
`UserID`, `ClassID`, or `AcademicUnitID`, with explicit parsing and validation;
the zero value is invalid. Transport DTOs and SQL rows convert at their
boundaries. Do not pass untyped strings through domain or application
contracts merely because the wire and database representations are textual.
Polymorphic authorization resources may retain a validated textual scope ID;
that is a deliberate sum-type boundary, not an entity field fallback.

Validation operates on complete domain state and returns a domain failure
through the standard `error` interface. It performs no database or network I/O.
Cross-row invariants such as uniqueness, academic-unit cycle detection,
parent/child institution consistency, and the student's single-active-class
rule belong in explicit application/store transactions and database
constraints.

Mutable, conflict-prone aggregates carry a revision. Update commands supply the
expected revision, and the atomic store operation compares and increments it;
a mismatch becomes a stable application conflict. Do not use timestamps as
concurrency tokens and do not add revisions mechanically to immutable or
append-only records.

Models expose `Auditable() map[string]any` only when a deliberately safe,
bounded audit projection is required.

`Auditable` is not a replacement for an audit event. It controls which model
fields may appear in the prior/result state of a future audit record. Actor,
session, request, action, outcome, and cluster metadata belong to the audit
service. Never include secrets, credentials, tokens, exam answers, or unbounded
user-controlled content in an `Auditable` map.

Application methods return Go's standard `error` interface. Expected
application failures use `*app.Error`, containing only:

- a stable machine-readable code;
- explicitly safe parameters or fields;
- an optional wrapped cause compatible with `errors.Is/As`.

`app.Error` is transport-neutral. It contains no HTTP status, Problem Details
fields, WebSocket code, localization state, translated message, or request ID.
Each transport maps recognized application codes to its protocol, adds request
correlation, and localizes public text when required. Unexpected failures use
ordinary wrapped errors and become a generic internal response at the
transport boundary. Wrapped causes and internal details are operator-only and
must never be serialized to an untrusted client.

HTTP owns one centralized, exhaustive mapping from stable application codes to
status, Problem Details type, and localization key. Tests fail when a public
application code lacks a mapping. Handlers never select statuses ad hoc.
Unknown or unexpected errors become a generic HTTP 500 response with request
correlation and no internal details.

WebSocket owns a separate versioned protocol error envelope. It carries the
same stable application code and explicitly safe fields, but defines its own
message type, correlation ID, and retry or resynchronization semantics through
another centralized, tested mapping. Do not serialize HTTP Problem Details
directly onto the WebSocket protocol.

Application error codes use lowercase dotted domain names that describe the
condition, such as `authentication.invalid_credentials`,
`academic_unit.not_found`, or `class.enrollment_conflict`. Do not encode
package names, function names, HTTP status names, or generic transport
conditions such as `bad_request`. Never repurpose a published code; safe
structured fields carry specific context, and localization keys may map from
codes without being identical to them.

Unexpected errors are logged once at a boundary. Do not log and wrap the same
error at every layer. Expected client errors normally do not require error-level
logs.

Each layer translates only failures owned by the layer immediately beneath it:

- SQL and other adapters translate driver failures into typed `store` or
  capability-port errors;
- domain models return typed validation or state-transition errors;
- public application use cases translate expected domain and port errors into
  stable `*app.Error` codes;
- transports inspect `app.Error` only and never branch on PostgreSQL, Redis,
  SMTP, filesystem, or `store` errors.

Unexpected failures preserve their wrapped cause until the outer operational
boundary logs them once and returns a generic protocol response. Never expose
SQL, Redis, SMTP, filesystem, stack, credential, or wrapped-cause details to
clients.

## Logging, Metrics, and Audit

Use the Proctor-owned `server/mlog` package for operational logging. It uses
Go's structured logging primitives internally but owns Proctor's target,
reconfiguration, lifecycle, and testing behavior.

Rules:

- the platform service owns the active logger and configures it from the shared
  configuration store;
- request-driven application use cases do not log returned failures; the outer
  operational boundary logs unexpected failures once;
- application workers receive a narrow logging port only when they own
  otherwise unobservable operational events;
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
- instrument HTTP, WebSocket, store, and outbound-adapter boundaries through
  explicit wrappers or decorators;
- application use cases may produce named domain outcomes or events but do not
  call Prometheus, OpenTelemetry, or a global metrics facade directly.

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
built-in Memberlist cluster transport
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
- cluster notifications accelerate session, permission, and cache convergence,
  but correctness recovers from PostgreSQL, bounded TTLs, periodic
  revalidation, and client resynchronization rather than assuming every peer
  receives every message;
- singleton work uses safe coordination rather than every node running it.

Prefer durable database-backed work claiming for jobs over broad leader
election. Use PostgreSQL advisory locks for the small number of truly singleton
maintenance operations when appropriate.

## Application-Side Cluster Messaging

WebSocket events and cache invalidations are application cluster messages.
PostgreSQL is not their transport.

The narrow server-owned cluster transport supports:

- starting/stopping inter-node communication;
- node discovery and health;
- registering typed message handlers;
- broadcast to peers;
- targeted node messages;
- best-effort messages;
- cache and session invalidation;
- WebSocket event fan-out;
- local WebSocket reconnection replay.

Business code depends on narrow application ports, not the concrete cluster
package. Cross-node WebSocket queue handoff may extend those ports later, but
ordinary event publication and invalidation must not depend on that future
feature. Do not turn the cache package into a message-bus package.

Do not extract the cluster transport into `packages/` until its API is stable
and independently useful.

The top-level `cluster` package owns inter-node transport contracts, wire
messages, node discovery, and concrete transports. It
provides:

- `cluster/local`, the in-process degenerate transport for a single node and
  tests;
- `cluster/memberlist`, the built-in peer-to-peer multi-node transport using gossip
  membership and direct node messaging without an external broker.

Both adapters implement `cluster.Transport`; only the module-root composition
package imports them. Memberlist discovery persistence uses a narrow `store`
contract and never imports SQL adapter types.

Multi-node bootstrap discovery uses short-lived node records and heartbeats in
the shared PostgreSQL database to find initial join addresses. Memberlist owns
live membership and messages after joining; PostgreSQL is never the cluster
message transport. Operators may configure static seed addresses as an
override, but static seeds are not the sole discovery mechanism.

Multi-node mode requires an explicit shared cluster key. Memberlist gossip
encryption and authentication are mandatory. Bind and advertised addresses are
explicit operator configuration, and binding the cluster transport to a public
interface is rejected by default. Key rotation uses Memberlist's keyring
capability without weakening traffic protection.

Discovery advertises the server version and supported cluster-protocol range;
every cluster message carries its protocol version. A node rejects
incompatible peers before becoming ready. Adjacent compatible versions ignore
unknown message types and fields where safe so rolling upgrades remain
possible.

Deployment configuration explicitly selects `local` or `memberlist`; Proctor
never auto-detects or silently promotes cluster mode. Memberlist mode validates
its key, bind and advertised addresses, shared VFS, discovery availability, and
other cluster prerequisites before the node becomes ready.

Redis is optional cache infrastructure and is not required for clustering.
The root composition package constructs the selected cluster transport;
`platform.Service` owns its health and lifecycle; application services receive
only narrow event-publication or invalidation ports. Cluster wire contracts do
not live in `model`.

The transport-independent contract retains these rules:

- `cluster.Message` carries a typed event, bounded opaque data, and bounded
  string properties;
- the transport owns source/target identity, wire versioning, message IDs,
  serialization, and bounded direct-send mechanics;
- one handler owns each event on a node, matching the Mattermost application
  dispatch shape and making duplicate ownership an explicit error;
- `Broadcast` means peers only and must never call the sending node's handler;
- `SendToNode` is targeted delivery and may address the current node;
- handlers receive cloned messages, so neither callers nor other handlers can
  mutate shared message state;
- handler panics are contained at the transport boundary and message data is
  never included in ordinary logs;
- the root composition package constructs the transport, the platform
  health-checks and owns it, the server starts it before becoming ready, and
  platform shutdown stops it before shared infrastructure is closed;
- `local` is the valid single-node degenerate transport: peer broadcasts
  succeed without local delivery, while self-targeted messages exercise
  registered handlers synchronously;
- cluster transport and cache selection are independent. A clustered node may
  use a local memory cache or a shared Redis cache; node-local cache entries
  remain disposable;
- two-node conformance verifies peer-only fan-out, best-effort delivery,
  duplicate-node rejection, application event publication, permission
  invalidation, and session revocation.

Memberlist delivery is best-effort and non-durable. Handlers are idempotent,
but the transport does not claim durable at-least-once processing. Security and
business correctness recover from authoritative PostgreSQL state, bounded
cache TTLs, periodic revalidation, and client resynchronization. Work requiring
durable eventual delivery uses a database-backed job or outbox. Normative
transport guarantees and non-guarantees live in `server/cluster/GUARANTEES.md`;
Memberlist rejoin/loss/duplicate recovery tests and application session,
authorization, and realtime recovery tests encode those expectations.

## WebSocket Architecture

Mattermost's application-side flow is the behavioral reference:

1. application code creates a WebSocket event;
2. the local node broadcasts it to matching local connections;
3. the node serializes it into a cluster publish message;
4. peers receive and decode it;
5. peers broadcast it locally without rebroadcasting to the cluster.

Proctor must prevent cluster rebroadcast loops.

The top-level `websocket` package is the concrete transport boundary. It owns
the hub, connections, wire DTOs, versioned errors, sequencing, replay, and
upgrade handler. `app/api` owns HTTP routing and mounts the supplied upgrade
handler without owning its protocol. `app` owns transport-neutral realtime use
cases and event intent. The module-root `server` package composes both
transports.

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
- the current implementation attempts recovery only on the node retaining the
  prior connection state;
- recoverable events are replayed in order;
- if the replay window is unavailable, create a fresh connection and tell the
  client to resynchronize authoritative state through HTTP;
- replay queues are bounded and not durable business storage.

The session-authenticated endpoint is `/api/v1/websocket`, derived from the
single versioned API root. Cookie-authenticated upgrades require an exact
configured public-origin match; bearer-authenticated native clients may omit
`Origin`. The connection keeps the immutable session principal and periodically
revalidates the authoritative session and account.

Clients subscribe with an explicit action/resource pair. The application uses
the ordinary durable authorization boundary before the hub stores the
subscription. Published resource events are delivered only to matching
authorized subscriptions. Direct user events are explicitly server-targeted.
Role, binding, MFA-assurance, account, and session security changes invalidate
affected connections rather than allowing a stale subscription to survive.

Event delivery classes:

- best effort for presence, typing, transient diagnostics, and similar hints;
- best-effort cluster notification plus bounded node-local replay for selected
  state changes;
- durable business state is committed independently before publication.

Starting or updating an exam follows:

1. authorize the command;
2. commit authoritative state;
3. publish the application WebSocket/cluster event after successful commit;
4. let clients fetch current state if they miss the notification.

An outbox may be used for mail, durable jobs, integrations, or future
requirements that truly need durable event processing. Do not automatically
place the normal WebSocket fan-out path through a database outbox.

More generally, an application use case commits authoritative state before it
invokes explicit narrow ports for transient events, cache invalidation, or
WebSocket publication. Never publish a state-change notification before its
commit. Use an atomic outbox only when a confirmed business requirement needs
durable eventual delivery, such as queued mail or an external integration.

Application events are past-tense domain facts such as `ClassCreated`,
`SessionRevoked`, or `RoleBindingEnded`, never imperative commands or
transport topic names. Transport packages map them into versioned wire event
names and payload DTOs.

Event payloads contain only the minimum typed facts consumers need, normally
entity-specific IDs, event time, a safe revision, and narrowly approved
metadata. Do not publish full mutable entities, credentials, exam answers,
arbitrary maps, or transport-ready JSON; consumers fetch authoritative state
when they need more.

WebSocket subscriptions and commands must use the same principal and
authorization service as HTTP.

## Persistence

PostgreSQL is the initial supported relational database.

Rules:

- migrations use plural `snake_case` table names, singular `id` primary keys,
  `<entity>_id` foreign keys, and meaning-specific `_at` temporal columns;
- constraints and indexes use deterministic
  `<table>_<columns>_{key|idx|fkey|check}` names;
- schema vocabulary matches the canonical terms in `CONTEXT.md`;
- migrations are versioned and explicit;
- before the first supported release, no installation depends on migration
  history: existing migrations may be rewritten or squashed and development
  databases may be recreated from scratch;
- the first supported release freezes its migration baseline. From that point,
  historical migrations are immutable and all changes are append-only
  expand/backfill/contract migrations;
- temporal columns use `timestamptz`; optional lifecycle events are nullable
  and named for their meaning, such as `archived_at`, rather than encoded with
  zero-value integer sentinels;
- normal server startup validates schema compatibility but never applies
  migrations;
- production deployment runs `proctor migrate` as a separate step under a
  database migration lock;
- rolling upgrades use expand/migrate/contract sequencing so compatible server
  versions can overlap safely;
- `store.Store` is the root persistence contract and exposes model-specific
  stores such as `InstitutionStore` and `AcademicUnitStore`;
- `Store` is the canonical durable-persistence term. Use `<Model>Store` or
  `<Aggregate>Store` for contracts, `SQL<Model>Store` for PostgreSQL
  implementations, and `storetest` for reusable conformance suites;
- do not use `Repository`, `DAO`, `Manager`, or `Gateway` as synonyms for a
  store. Reserve `Gateway` or `Client` for outbound remote-service adapters
  when that vocabulary accurately describes the boundary;
- each persisted model or cohesive aggregate receives its own store contract,
  `SQL<Model>Store` implementation, store file, adapter test, and reusable
  conformance suite when that persistence slice is implemented;
- `Get` requires the entity to exist and returns typed `store.ErrNotFound` on
  absence; `Find` reserves absence as an expected outcome and returns
  `(value, found, error)`; never encode absence as `(nil, nil)`;
- `List` returns an empty collection without error when no rows match;
- name aggregate mutations after the durable operation when generic `Save`,
  `Update`, or `Delete` would hide important behavior;
- `Archive` means reversible removal from ordinary active use; `Disable` means
  reversible prevention of an identity, credential, or capability from
  operating; `Delete` is used only when deletion is the actual non-recoverable
  domain operation; `Purge` means irreversible physical removal under an
  explicit retention or privacy workflow;
- do not call a soft archive `Delete` or expose generic hard-delete operations
  for sensitive educational records;
- the root store owns connection and schema lifecycle methods, while domain
  operations remain on their corresponding model stores;
- application and platform code consume `store.Store` or a model-store
  contract, never concrete SQL store types;
- do not expose raw database handles outside the adapter/composition boundary;
- store contracts accept and return domain types or explicit aggregate results
  defined in `store`;
- SQL row structs remain private to `store/sqlstore` and map explicitly to
  domain types. Application and transport code never receive nullable driver
  types, query builders, database column names, or row representations;
- simple reads return domain types. Join-heavy listings and reports use
  explicit projection types defined with their persistence query contract in
  `store`; application queries map them into application results when policy or
  presentation requires it;
- do not distort domain entities to match report rows or return anonymous maps
  or SQL-shaped structs through the application boundary;
- adapters validate rehydrated domain state. Invalid persisted state is an
  internal integrity failure, never a client validation error;
- cross-model business transactions are represented by explicit
  aggregate-oriented persistence operations whose contracts state their
  atomic guarantees, such as installation bootstrap, enrollment transfer, or
  password-reset consumption;
- the application authorizes and selects policy before invoking an atomic
  operation; the adapter owns transaction mechanics, locking, concurrency
  checks, constraint enforcement, and commit/rollback;
- do not expose raw database handles, SQL transaction objects, or a generic
  `WithTransaction(func(Store))` unit-of-work callback to application code;
- uniqueness and foreign-key constraints enforce domain invariants where
  possible;
- use transactions for enrollment transfer, token consumption, state
  transitions, and related audit/outbox writes;
- translate driver-specific errors inside the PostgreSQL adapter;
- the root composition package may wrap a concrete store with constrained
  `timerlayer`, `retrylayer`, and `localcachelayer` decorators before passing
  the resulting `store.Store` inward;
- the standard chain is
  `localcachelayer(timerlayer(retrylayer(sqlstore)))`: cache hits use cache
  hit/miss metrics and bypass database timing, timing measures the total
  cache-miss latency including safe retries, and retry remains nearest SQL for
  accurate transient-failure classification;
- `timerlayer` is observability-only and must not change store semantics;
- `retrylayer` retries only an explicit allowlist of known-safe idempotent
  operations and never retries arbitrary mutations;
- `localcachelayer` caches only an explicit allowlist of disposable read
  methods whose keys, TTLs, invalidation, and security-staleness rules are
  documented and tested;
- the initial cache allowlist excludes authorization decisions, active
  role-binding resolution, account enabled state, session or credential
  validity, MFA assurance, and token revocation. A security-sensitive read may
  be added only after a separately reviewed bounded-staleness, revalidation,
  and recovery contract is tested;
- begin with low-risk reference data and add caches only from measured need;
- store decorators implement the same contracts and remain invisible to
  application callers. Application- or workflow-specific caches still use
  explicit application cache ports;
- each decorator implements the complete root `store.Store`, returns wrapped
  per-model or per-aggregate stores from its accessors, overrides only methods
  with layer behavior, and forwards the rest mechanically. Do not use
  reflection-based proxies or runtime interception;
- the composition root produces one final layered `store.Store`; application
  wiring does not select layers per model;
- deterministic `go generate` tooling may generate mechanical pass-through
  wrappers for the complete store contracts. Generated files are checked in,
  clearly marked, never edited manually, and verified clean by CI;
- caching, retry, timing policy, and exceptional methods remain handwritten;
  generation must not encode or obscure behavioral policy;
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
contract, or an unconstrained store decorator. A constrained
`store/localcachelayer` may provide semantically transparent read caching only
for the documented allowlist in the persistence rules above. Prefer versioned
namespaces for broad invalidation.

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

The module-root `server.New` composition root initializes in a deterministic
order:

1. parse command and locate configuration;
2. create the configuration backing and shared `config.Store`;
3. in the root composition package, select and construct PostgreSQL, cache,
   VFS, mail, cluster, and external-authentication adapters from configuration;
4. validate persistence schema compatibility and mandatory dependency health;
5. construct `platform.Service` from those already-created capabilities so it
   can own their shared lifecycle;
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
- plural lowercase kebab-case resource paths;
- `snake_case` JSON fields, query parameters, and route-variable names;
- noun and subresource paths by default; use an explicit command endpoint only
  for a genuine domain action that does not fit ordinary resource mutation;
- transport-owned request and response DTOs, with explicit mapping to and from
  application/domain types;
- consistent JSON and Problem Details error contracts;
- a reviewed OpenAPI document checked into the repository as the public
  contract;
- stable machine-readable error codes;
- cursor-based pagination where collections can grow;
- collection responses use an object envelope with a non-null `items` array
  and optional opaque `next_cursor`; empty collections serialize as `[]`, not
  `null`, and public endpoints do not return bare arrays;
- cursors are opaque and versioned, encode only the stable keyset needed to
  continue a query, and are treated as untrusted input. Transport validates
  and maps them to typed store keysets; cursors never contain SQL fragments or
  become domain identifiers;
- do not use growing offset pagination for unbounded collections;
- explicit idempotency support for commands vulnerable to client retry:
  transport extracts and bounds the key, the application command defines its
  meaning, and an atomic store operation records the key with principal,
  operation, request fingerprint, outcome, and expiry;
- reusing an idempotency key with different input is a conflict; replaying the
  same input returns the recorded outcome;
- request IDs returned to clients;
- capability/version endpoint;
- documented compatibility and deprecation policy;
- WebSocket protocol version and event schemas;
- reconnect/resynchronization behavior documented.

Avoid privileged behavior available only through an in-process API. The CLI
should normally use the same authenticated public/admin API as other clients.
Any local Unix-socket administrative mode requires an explicit threat model and
must not be an unauthenticated shortcut by default.

Once an API version is declared stable, changes within it are additive:
existing fields, meanings, routes, and error codes are not removed or
repurposed; clients tolerate unknown response fields; new required request
fields or changed semantics require a new version. Deprecations are documented
before removal. Pre-stable behavior may change only when release notes state
that compatibility is not yet promised.

CI verifies that registered routes, authentication classifications, transport
DTO schemas, and stable error responses agree with OpenAPI. Documentation and
clients may be generated from the specification, but application handlers and
domain models are handwritten and are never generated from the wire contract.

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

Default test doubles are small hand-written fakes or spies for narrow
consumer-owned interfaces. Keep them beside the consuming package's tests
unless several packages genuinely share the same contract. Use reusable
conformance suites for store and infrastructure-port implementations. Avoid
mocking frameworks, generated mocks for every interface, and expectations tied
to incidental call order. Integration tests that need wiring confidence use
the real module-root `server.New` graph through `testlib`.

`testlib` may configure the real `server.New` graph with explicit test
overrides, manage integration resources, and expose safe test clients. It must
not maintain a second wiring path, bypass application use cases, or make
concrete SQL internals the normal test-setup API. Unit-test fakes remain local
to their consumers.

Use external `package_test` tests by default for exported contracts and
conformance behavior. Use same-package tests only when directly exercising
important unexported logic is clearer than forcing an export or testing it
indirectly.

Name tests and subtests after observable domain or contract behavior, so their
CI output explains the rule being exercised. Avoid generic names such as
`success`, `error case`, or numbered cases; table entries use short scenario
phrases.

Pure unit tests and isolated table subtests use `t.Parallel()`. Integration
tests run in parallel only when each test owns an isolated database or schema,
Redis namespace, storage prefix, ports, and cleanup. Tests that intentionally
share process-global configuration or infrastructure remain serial and state
the reason.

Ordinary `go test ./...` is network-free. Tests requiring PostgreSQL, Redis,
SMTP, S3, or multiple nodes use the `integration` build tag and dedicated Make
targets. CI compiles and runs every tagged suite; invoking an integration target
without its required service is a failure rather than a silent skip. Shared
conformance suites run against both in-memory implementations and tagged real
adapters. Existing external-service tests that are not consistently tagged
predate this convention and must migrate coherently with their Make and CI
targets.

General Go checks:

```text
go test ./...
go test -race ./...
go vet ./...
```

CI additionally requires:

- formatting and checked-in generated-file cleanliness;
- unit tests and `go vet` for every module;
- race tests for network-free suites;
- production import-boundary architecture tests;
- OpenAPI, route-matrix, authentication-classification, and public-error
  mapping consistency;
- tagged integration and conformance suites in dedicated jobs;
- glossary and ADR link validation.

Do not enable a broad opinionated linter bundle until each rule is reviewed and
accepted for this repository.

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

- an import-boundary architecture test for the production package allowlist;
- pure domain invariant tests;
- configuration default/validation/redaction tests;
- error-to-transport contract tests;
- route authentication classification tests;
- session creation, expiry, rotation, reuse, and revocation tests;
- authorization matrices across institution, academic unit, class, and exam;
- further enrollment concurrency and progression-policy tests;
- PostgreSQL repository integration tests;
- two-node Memberlist cluster tests sharing PostgreSQL and VFS, independently
  exercised with local-memory and optional Redis cache configurations;
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

- Preserve standard initialisms in identifiers: `ID`, `URL`, `HTTP`, `API`,
  `SQL`, `MFA`, `VFS`, `OIDC`, and `CAS`.
- Do not prefix interfaces with `I`; name them for the capability they provide.
- Use `New` for a package's primary exported construction and precise
  `New<Type>` constructors only when several exported constructions coexist.
  Use unexported `new<Type>` constructors for internal services.
- Use `With<Option>` only for genuinely optional construction behavior.
- Name methods with domain verbs. Avoid vague names such as `Process`, `Handle`,
  `Execute`, or `Manage` unless that word is the established domain term.
- Pass `context.Context` as the first parameter for I/O, waiting, and
  request-scoped operations.
- Pass security and audit call state explicitly as `app.Invocation`; do not use
  context values as an implicit dependency carrier.
- Do not store request contexts in long-lived structs.
- Wrap errors with useful operation context and preserve `errors.Is/As`.
- Avoid panic for expected runtime/configuration/input failures.
- Panic is reserved for impossible programmer invariants during initialization
  and test-only `Must*` helpers. Transport and worker boundaries recover
  unexpected panics, record safe diagnostics, and isolate or fail the affected
  operation without exposing stack details.
- Constructors validate dependencies and return errors.
- Dependency injection is explicit constructor wiring in the module-root
  `server` package. Do not use reflection-based DI frameworks, generated
  containers, service containers, or global registries.
- Use named options only when a dependency or behavior is genuinely optional
  and absence has defined semantics.
- Prefer concrete types at construction and narrow interfaces at consumption.
- Define ordinary service interfaces near the consuming use case, not the
  adapter. Persistence contracts are the deliberate exception: root and
  per-model contracts live together in `server/store`, following Mattermost's
  cohesive store organization.
- Add compile-time interface assertions where a concrete adapter or generated
  store layer intentionally implements an important cross-package contract.
  Do not add assertions for incidental interface satisfaction.
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
- Every substantive package has a concise package comment, normally in
  `doc.go`, stating what it owns, what it explicitly does not own, and its
  dependency direction. Exported contracts document behavioral guarantees and
  failure semantics rather than merely restating identifier names.
- Format code with `gofmt`.

## Implementation Sequence

Unless the user reprioritizes, build the server as a walking skeleton:

1. licensing/provenance files and server module — complete;
2. cohesive current `config → platform → app.Server → app.App → app/api`
   composition flow and shared `testlib` — complete; moving composition to the
   module-root `server` package is documented migration work;
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
10. cluster transport — complete for typed, bounded messages,
    one-handler-per-event dispatch, stable node identity, peer-only broadcast
    semantics, self-targeted local delivery, platform health/lifecycle
    ownership, startup readiness gating, local and Memberlist backends only,
    and retirement of the Redis cluster adapter and reliable delivery class;
    Redis remains optional solely as a disposable cache backend;
11. identity/authentication services, credential rotation, and authentication
    middleware — complete for the first local-password, access/refresh session,
    login/refresh/logout/current-user vertical slice and self-service active
    session listing/individual/revoke-all management, strong/recent route
    assurance contracts, target-bound email verification, password reset, and
    finite explicitly scoped personal access tokens, encrypted TOTP MFA,
    single-use recovery codes, administrative session management, and
    registry-backed direct CAS and generic OIDC external identity login;
    external account-linking administration, SAML, and service accounts remain;
12. scoped authorization and audit — complete for action/resource contracts,
    current-state institution/academic-unit/class evaluation, role and binding
    stores, durable decision/critical-action auditing, the privileged audit
    listing slice, atomic installation bootstrap, and audited role/binding
    administration with last-administrator protection, plus user resource
    actions, default-deny visibility helpers, and audited cross-user reads;
13. institution/academic hierarchy, membership/enrollment, and administrative
    user vertical slices — complete for application services, audited
    mutations, authorized scoped reads, PostgreSQL conformance, and end-to-end
    API integration; removing the existing handler permission preflights is a
    documented architecture migration;
14. remaining identity phase: personal access tokens, MFA/recovery codes, and
    administrative session management — complete; registry-backed direct CAS
    and generic OIDC external identity login are also complete, while
    account-linking administration, SAML, and service accounts remain;
15. first two-node cluster tests — complete for best-effort Memberlist
    transport, rejoin after churn, lost-message non-recovery with later
    delivery, duplicate-delivery tolerance, application event fan-out,
    and recovery proofs that session revocation and authorization correctness
    depend on PostgreSQL plus bounded auth-cache TTLs rather than cluster
    delivery;
16. WebSocket hub, cluster fan-out, and replay — complete for authenticated
    sockets, authorized subscriptions, bounded local replay, explicit
    resynchronization, and cross-node events; cross-node replay handoff remains
    open;
17. exam/proctoring vertical slices after their state models are confirmed.

Do not create every future domain entity or model-store interface up front.
Complete one vertical path through transport, use case, authorization,
persistence, and tests, then extend the root store with the next implemented
model store.

Architecture migrations are incremental and keep the repository buildable; do
not perform a big-bang package rewrite. Sequence the current migration as:

1. establish import-boundary tests and the module-root composition package;
2. remove platform/service-location dependencies one application capability
   at a time;
3. migrate each capability's invocation, commands/queries, errors, DTOs,
   domain lifecycle, and tests as one coherent vertical change;
4. introduce store layers and extract WebSocket and cluster transports behind
   stable contracts;
5. establish native temporal types and entity-specific IDs in the pre-release
   schema baseline, recreating development databases as needed.

## Architecture and Documentation Evolution

This file must evolve with the project.

Architecture documentation has distinct authorities:

- root `CONTEXT.md` is the implementation-free domain glossary;
- `docs/architecture.md` is the canonical developer-facing guide for
  boundaries, dependencies, layout, naming, errors, testing, and structural
  examples;
- `docs/adr/` records why durable decisions were made;
- `AGENTS.md` summarizes normative rules, current implementation state,
  workflow, and links to the canonical documents.

Do not require developers to reconstruct the current architecture from ADRs
alone, and avoid duplicating detailed narrative across these files.

`docs/architecture.md` includes concise paired correct/incorrect directory
trees and Go snippets, explains the boundary consequence of each, and links to
real repository examples when available. Do not create artificial production
packages solely as documentation. A snippet claiming executable behavior is
covered by a compiled Go example test; purely structural snippets are labeled
illustrative.

Accepted ADRs remain as historical records. Minor factual corrections are
allowed, but reversing a decision requires a new ADR that names and supersedes
the old one; both remain with explicit status metadata. ADRs created during
this architecture session are accepted unless later superseded.

The project-wide domain glossary lives in the root `CONTEXT.md`. Keep it free
of implementation details and group the shared language by domain area. Create
a root `CONTEXT-MAP.md` and split the glossary only when bounded contexts
develop genuinely distinct language, ownership, or meanings; do not infer code
package boundaries from glossary headings.

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
- The top-level `cluster` package provides an in-process `local` transport and
  a built-in Memberlist peer transport for multi-node deployments; clustering
  requires no Redis service.
- Cache and cluster transport are independent: clustered nodes may use
  per-node memory caches or optional shared Redis cache.
- Authentication route wrappers, sessions, tokens, scoped permissions, and
  audit behavior should be inspired by Mattermost while using Proctor's
  immutable principal and current durable role bindings.
- API route ownership uses unexported per-domain
  `register<Area>Routes` functions called by `api.New`; one typed wrapper
  classifies each handler before the central registrar detects duplicate route
  shapes and records the route matrix. All resource routers derive from the
  single versioned `BaseRoutes.APIRoot`, and typed request parameters are
  populated centrally from regex-constrained route variables.
- Route authentication and application authorization are deliberately
  separate: typed transport wrappers establish credential and assurance
  requirements, while each application use case immediately performs the sole
  authoritative action/resource check and durable decision audit. Handlers do
  not preflight resource permissions or issue decision receipts.
- Initial administrator creation is an explicit one-time, PostgreSQL-serialized
  bootstrap aggregate and never an implicit first-user side effect.
- Built-in roles are server-owned and immutable through administration APIs;
  the last active institution system-administrator binding cannot be ended.
- The server follows an explicit construction flow:
  `config.Store → concrete adapters → platform.Service → app.App + app/api`,
  all owned by the module-root `server` package.
- The module-root `server.New` is the sole composition root and `testlib`
  reuses that exact
  graph.
- Only the module-root composition package selects concrete infrastructure;
  `platform.New` receives constructed capabilities and owns their shared
  lifecycle.
- Production package imports follow the enforced inward graph
  `model ← store ← app ← {app/api, websocket} ← server ← cmd/proctor`, with
  platform and concrete adapters confined to the infrastructure side.
- Proctor owns its `mlog` API and implementation; the platform service owns its
  active configuration and lifecycle.
- `server/model` is the cohesive durable model package and uses
  explicit domain construction, named transitions, `Validate() error`, and
  safe audit projections while remaining domain-focused rather than becoming
  a general shared-contract package.
- Persistence-lifecycle methods such as `PreSave`, `PreUpdate`, and `IsValid`
  do not belong on domain models; the application supplies IDs and timestamps.
- Application methods return standard `error`; expected failures use a
  transport-neutral `*app.Error` with stable codes and explicitly safe fields,
  while transports own protocol status, localization, and request correlation.
- Domain models own local invariants and pure state transitions; application
  services own use-case policy, authorization, transactions, audit, and
  external-effect orchestration; adapters do not decide business policy.
- Academic identity is contextual: affiliations and memberships describe a
  user's relationships, while role bindings determine access.
- Sessions and API credentials persist hashes only and do not carry
  authorization role snapshots.
- PostgreSQL migrations use embedded Morph sources and an advisory migration
  lock; normal server startup validates schema compatibility but never applies
  migrations.
- Persistence follows Mattermost's store shape: one root `store.Store`,
  complete per-model store interfaces, one `SQL<Model>Store` per implemented
  model, and interface-returning accessors on `SQLStore`.
- Atomic changes spanning models use explicit aggregate-oriented store
  operations rather than exposing a generic transaction callback or database
  transaction to the application.
- SQL execution, PostgreSQL rebinding, timeouts, builders, and transactions are
  centralized in SQL wrappers. Per-model stores reuse select builders for reads
  and dynamic filters while retaining named SQL where clearer for writes.
- SQL adapters use explicit row representations and translate PostgreSQL
  constraint errors at the adapter boundary.
- Each model store has a reusable Mattermost-shaped conformance suite under
  `store/storetest` and a thin corresponding SQL adapter test.
- Electron desktop and CLI clients are primary API consumers. Electron/web
  sessions use cookie plus CSRF transport; CLI sessions use bearer transport.
- Direct institutional CAS and generic OIDC are the first external identity
  providers. Instance-registered adapters normalize assertions into the shared
  external-identity/session flow; provider ID plus opaque subject is
  authoritative, email collisions never auto-link accounts, and released
  affiliations never grant roles.
- This `AGENTS.md` is a living document and must be maintained as the project
  evolves.

## Open Decisions

Keep this list current; remove items when decided and record the resulting rule
above.

- Priority and exact requirements for the next external provider after CAS and
  OIDC: SAML/RENATER or LDAP.
- External account-linking, profile-ownership, affiliation reconciliation, and
  provider-driven deprovisioning policies.
- Whether roles may bind directly to programme and programme-level scopes in
  addition to academic unit, class, and exam.
- Exam ownership and targeting: one class, several classes, programme level, or
  individually selected candidates.
- Exam lifecycle and concurrency/state-transition rules.
- Proctor assignment and violation review model.
- Whether cross-node WebSocket reconnection should transfer bounded replay
  queues between nodes or always require authoritative HTTP resynchronization.
- Whether a generated client SDK belongs in this monorepo and which languages
  are required by the desktop application.
- Coderunner threat model and service boundary.
