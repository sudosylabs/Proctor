# Proctor server

This directory contains the new Proctor application server. It is an
independent Go module and is licensed under AGPL-3.0-only.

The server currently establishes one cohesive construction flow:

```text
config.Store → platform.Service → app.Server/app.App → app/api
```

`app.NewServer` is the sole composition root. The shared `testlib` constructs
that same graph with an in-memory config store and captured logs.

The flat `model` package now establishes the durable model contract:

- Mattermost-inspired 26-character IDs and millisecond timestamps;
- `PreSave`, `PreUpdate`, and `IsValid` lifecycle methods;
- safe `Auditable` representations;
- translation-ID-based `AppError` values mapped to HTTP Problem Details;
- institution, hierarchical academic unit, programme, programme level,
  academic period, and class models;
- user profiles separated from external identities and local password
  credentials;
- time-bounded affiliations, academic-unit memberships, and class memberships;
- roles and scoped role bindings;
- sessions separated from hashed access/refresh credentials, plus scoped
  expiring personal access tokens;
- hashed, expiring, single-use password-reset and email-verification tokens;
- encrypted TOTP MFA credentials and independently hashed, single-use recovery
  codes.

The server also includes:

- PostgreSQL connection and schema management with explicit migrations;
- Mattermost-shaped per-model stores for the complete structural academic
  hierarchy and the first identity/session slice;
- platform-owned memory/Redis cache, disabled/SMTP mail, and local/S3 VFS
  adapters with startup dependency checks and deterministic shutdown;
- a typed, bounded cluster message contract and server-owned transport port,
  with a loop-safe `local` backend as the single-node form of the architecture;
- bounded Argon2id local-password authentication;
- revocable server-side sessions with separately hashed opaque access and
  rotating refresh credentials, replay detection, activity debouncing, and
  concurrent-session limits;
- Electron/web cookie delivery using host-only HttpOnly access/refresh
  cookies, rotating CSRF cookies and header verification, while retaining
  bearer credentials for the CLI and rejecting mixed credential sources;
- login throttling through the configured shared cache;
- an immutable request principal and typed Mattermost-style authentication
  wrapper on every route, including composable strong and recent
  authentication requirements;
- active-session listing and self-service individual or account-wide
  revocation, with serialized refresh/login races and complete access-cache
  invalidation;
- current-state scoped authorization with institution and ancestor
  academic-unit inheritance, exact class scope, additive roles, and default
  denial;
- reusable principal/resource permission helpers and contextual user
  visibility: institution-wide `user.view` or inherited
  `class.members.view` over a student's current enrollment;
- dedicated role and role-binding stores with overlap-safe effective periods;
- durable PostgreSQL security audits with fail-closed decision recording,
  bounded prior/result data, request/node correlation, and keyset pagination.
- Mattermost-style per-domain `Init*` API registration using typed
  `APIHandler`/`APISessionRequired` wrappers and a route-matrix test, with a
  single versioned `BaseRoutes.APIRoot`, regex-constrained resource IDs, and
  centrally populated typed request parameters;
- atomic one-time installation bootstrap with a protected built-in
  system-administrator role and durable success audit;
- audited custom-role and scoped role-binding administration, including
  immediate permission changes and last-administrator protection;
- fail-fast permission checks visible in privileged API handlers, connected to
  authoritative application authorization by a sealed, request-bound,
  one-use receipt so the same decision is not queried or audited twice. Each
  privileged handler calls its scoped `PrincipalHasPermissionTo*` method
  directly rather than hiding the check behind a generic preflight helper;
- audited structural administration for the institution, academic-unit tree,
  programmes, levels, periods, and classes;
- effective-dated affiliations and academic-unit membership, plus serialized
  student enrollment and transfer with retained history;
- user search/profile administration, enable/disable, affiliation and
  membership management, and administrative session revocation;
- target-bound, hashed, expiring, single-use email-verification and
  password-reset credentials with shared-cache throttling, generic public
  reset responses, SMTP delivery, transactional audits, and account-wide
  session revocation after password reset;
- finite personal access tokens with explicit known-action scopes, optional
  academic-unit subtree constraints, hashed storage, one-time credential
  display, recent-session creation, durable audits, debounced last-used
  metadata, reversible disable/enable, and immediate self-service revocation.
- TOTP MFA with AES-256-GCM encrypted secrets, expiring pending setup,
  transactionally replay-protected codes, hashed one-time recovery codes,
  login-time enforcement, assurance upgrades, and account-wide downgrade on
  disable;
- dedicated `session.view` and `session.manage` authorization for
  administrator listing, individual revocation, and revoke-all, with visible
  handler preflights, durable mutation audits, and immediate cache
  invalidation.

External identity login, service accounts, exams, a concrete multi-node
cluster backend, and WebSockets remain intentionally unimplemented until their
next vertical slices.

## Run locally

From the repository root:

```sh
go run ./server/cmd/proctor serve --config ./server/config.example.json
```

The server requires a migrated PostgreSQL database before startup. Cache, mail,
and VFS backends are selected from `config.example.json`; the development
defaults use memory cache, disabled mail, and local VFS.

The default listener is `127.0.0.1:8065`. Available endpoints are:

- `GET /health/live`
- `GET /health/ready`
- `GET /api/v1/system/version`
- `GET /api/v1/bootstrap` (public boolean installation status)
- `POST /api/v1/bootstrap` (public only until the atomic bootstrap succeeds)
- `POST /api/v1/auth/login`
- `POST /api/v1/auth/refresh`
- `POST /api/v1/auth/logout`
- `POST /api/v1/auth/email-verification/request` (session required)
- `POST /api/v1/auth/email-verification/complete` (public token consumption)
- `POST /api/v1/auth/password-reset/request` (generic public acceptance)
- `POST /api/v1/auth/password-reset/complete` (public token consumption)
- `GET /api/v1/users/me`
- `GET /api/v1/users/me/sessions`
- `POST /api/v1/users/me/sessions/revoke`
- `POST /api/v1/users/me/sessions/revoke-all`
- `GET /api/v1/users/me/mfa`
- `POST /api/v1/users/me/mfa/setup` (recent session; returns the TOTP secret
  once)
- `POST /api/v1/users/me/mfa/activate` and `/challenge`
- `POST /api/v1/users/me/mfa/recovery-codes/regenerate` (strong and recent;
  returns replacement codes once)
- `POST /api/v1/users/me/mfa/disable` (strong and recent)
- `POST /api/v1/users/me/tokens` (recent interactive session required; returns
  the raw credential once)
- `GET /api/v1/users/me/tokens` and
  `DELETE /api/v1/users/me/tokens/{personal_access_token_id}` (interactive
  session required)
- `POST /api/v1/users/me/tokens/{personal_access_token_id}/disable` (interactive
  session required) and `/enable` (recent interactive session required)
- `GET /api/v1/audits` (requires an institution-scoped role granting
  `audit.view`; accepts `limit`, opaque `cursor`, `actor_id`, `action`,
  `resource_type`, and `resource_id` filters)
- `GET|POST /api/v1/roles` and
  `GET|PATCH|DELETE /api/v1/roles/{role_id}` (requires `role.manage`)
- `GET|POST /api/v1/role-bindings` and
  `DELETE /api/v1/role-bindings/{role_binding_id}` (requires `role.manage`;
  list by `user_id` or by `scope_type` plus `scope_id`)
- `GET|PATCH /api/v1/institution`
- `GET|POST /api/v1/academic-units`, resource
  `GET|PATCH|DELETE`, and nested `/children`, `/programmes`, and `/members`
- programme resource `GET|PATCH|DELETE` and nested `/levels`
- programme-level resource `GET|PATCH|DELETE` and nested `/classes`
- `GET|POST /api/v1/academic-periods` and resource `GET|PATCH|DELETE`
- class resource `GET|PATCH|DELETE` and nested `/members`
- `GET /api/v1/users`, user resource `GET|PATCH`, `/enable`, `/disable`,
  nested `/affiliations`, `GET /sessions`, `DELETE /sessions/{session_id}`,
  and `POST /sessions/revoke-all`
- effective-dated membership endings at `/affiliations/{id}`,
  `/academic-unit-members/{id}`, and `/class-members/{id}`

All academic and user-administration endpoints require an authenticated
principal and perform their scoped `PrincipalHasPermissionTo*` check before
decoding mutation bodies. An interactive session or PAT may supply that
principal. PAT actions are intersected with current role permissions and, when
configured, restricted to one academic-unit subtree; tokens never grant an
action absent from current roles.
Programme and programme-level operations authorize against their owning
academic unit; academic periods are institution-managed. Membership lists
default to records active now and accept `active_at`; `history=true` returns
the retained effective-dated history.

Bootstrap is explicit: normal account creation never promotes a “first user.”
The successful transaction creates the institution, administrator and password
credential, protected system-administrator role, institution binding,
installation marker, and audit event together. A PostgreSQL advisory lock and
pristine-state check make this safe when several application nodes start at
once. The status response exposes only `initialized`.

Electron/web login (`client_type` equal to `desktop` or `web`) returns the user
and session but omits raw credentials from JSON. It sets host-only
`PROCTOR_ACCESS`, `PROCTOR_REFRESH`, `PROCTOR_CSRF_BINDING`, and
`PROCTOR_CSRF` cookies. The first three are HttpOnly except for
`PROCTOR_CSRF`, which the client copies into `X-Proctor-CSRF-Token` on unsafe
requests. The refresh cookie is restricted to `/api/v1/auth/refresh`; refresh
rotates the full cookie set. Production cookie security follows the configured
HTTPS public URL. Electron is expected to load the installation's server
origin so SameSite=Lax remains effective.

CLI login (`client_type: "cli"`) returns the one-time access and refresh
credentials in the response body. CLI requests send exactly one
`Authorization: Bearer <credential>` header. Credentials are never accepted in
URLs, and requests containing both a relevant cookie and bearer header are
rejected. PATs use the same header but are not sessions: they cannot refresh,
log out, manage sessions, create/revoke PATs, or satisfy strong/recent
authentication wrappers.

Validate a configuration without starting the server:

```sh
go run ./server/cmd/proctor config validate --config ./server/config.example.json
```

Configuration is loaded in this order: built-in defaults, an optional strict
JSON file, then `PROCTOR_` environment variables. Unknown JSON fields and
invalid values are rejected at startup.

The deployment schema covers HTTP, PostgreSQL, cache, cluster transport and
node identity, mail, VFS, logging, password hashing, session lifetimes,
concurrent-session limits, and login rate limits. Secret fields are explicitly
redacted. Authentication configuration also controls the recent-authentication
window, verification/reset lifetimes, recovery throttles, MFA issuer and setup
lifetime, recovery-code count, and a rotatable AES-256 encryption-key ring.
When MFA is enabled, `authentication.mfa.encryption_key` must be a standard
base64-encoded 32-byte key. Previous keys may remain in `decryption_keys`
during rotation.
Environment-overridden values are effective only for the running process and
are never persisted back into the configuration file.

The active configuration is owned by one concurrency-safe store. It separates
persisted values from environment overrides, returns cloned snapshots, supports
atomic file writes, reload/set listeners, and structured diffs. Logging is the
first dynamically reconfigurable consumer; HTTP listener and timeout changes
require a process restart.

Logging supports multiple independently filtered console or file targets,
text/JSON formatting, contextual fields, bounded field sizes, runtime
reconfiguration, flush/shutdown, and locked test capture.

The default `cluster.backend` is `local`, with `node_id` set to `local`. This
backend has no peers: broadcast is deliberately a no-op and never loops back
into local handlers. The transport still participates in dependency checks and
is started before readiness and stopped through the platform lifecycle. A
multi-node backend and its reliable-delivery semantics are not selected yet.

## Verify

```sh
make -C server check
```

Run the PostgreSQL-backed store, migration, and authentication tests with:

```sh
make -C server conformance-postgres
```

The individual `test`, `test-race`, `vet`, and `build` targets use the root
workspace during repository development. The server declares exact pseudo
versions of the reusable modules; those versions must be published before the
server module is distributed independently of this monorepo.
