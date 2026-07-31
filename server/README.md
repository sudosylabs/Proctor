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
  codes;
- provider-neutral external identities and durable, hashed, browser-bound,
  one-use external login transactions.

The server also includes:

- PostgreSQL connection and schema management with explicit migrations;
- Mattermost-shaped per-model stores for the complete structural academic
  hierarchy and the first identity/session slice;
- platform-owned memory/Redis cache, disabled/SMTP mail, and local/S3 VFS
  adapters with startup dependency checks and deterministic shutdown;
- a typed, bounded cluster message contract and server-owned transport port,
  with a loop-safe `local` backend and a Redis multi-node backend using
  lease-backed discovery, Pub/Sub best effort, and acknowledged per-node
  Streams for reliable delivery;
- authenticated WebSocket sessions with exact-origin cookie upgrades,
  CPU-sharded connection ownership, resource/action-authorized subscriptions,
  bounded send and replay queues, per-connection sequences, ping/pong
  liveness, backpressure handling, and local reconnection replay;
- local-first application event publication with loop-free cluster fan-out,
  reliable session revocation, cache invalidation, and permission-change
  propagation across nodes;
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
  local-password login-time enforcement, assurance upgrades, and account-wide
  downgrade on disable;
- dedicated `session.view` and `session.manage` authorization for
  administrator listing, individual revocation, and revoke-all, with visible
  handler preflights, durable mutation audits, and immediate cache
  invalidation;
- an instance-scoped external-provider registry with independent direct CAS 3
  and generic OIDC adapters, strict CAS back-channel XML validation, OIDC
  discovery and Authorization Code with S256 PKCE, exact callback binding,
  institution/home-organization allowlists, explicit MFA-assurance mapping,
  collision-safe auto-provisioning, ordinary Proctor session creation, and
  durable provisioning/login audits.

Service accounts, account-linking administration, exams, cross-node WebSocket
replay handoff, and SAML remain future vertical slices.

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
- `GET /api/v1/auth/providers` (safe provider discovery)
- `GET /api/v1/auth/providers/{provider_id}/login` (provider browser
  initiation)
- `GET /api/v1/auth/providers/{provider_id}/callback` (browser-bound provider
  callback)
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

External login uses the same browser session transport. The initiation endpoint
stores only hashes of a one-use state and browser-binding credential in
PostgreSQL, places the raw binding in a host-only HttpOnly SameSite=Lax cookie,
and delegates protocol construction to the configured provider adapter. The
callback consumes the state exactly once, asks that adapter to validate the
provider response, resolves `(provider ID, opaque subject)`, and creates an
ordinary Proctor session before redirecting to the validated local `return_to`
path. CAS tickets, OIDC codes/tokens, subjects, released claims, and binding
credentials are never logged or audited.

The provider registry is instance-scoped and atomically replaced on relevant
configuration reloads. The application flow contains no CAS/OIDC switch:
adapters own challenge construction, callback-state extraction, and assertion
validation, then return the same normalized assertion contract. Adding a
protocol requires a factory and strict configuration block at the composition
boundary, not another branch in the application login service.

Auto-provisioning never merges accounts by email. A username or email collision
requires an explicit future account-linking operation. Released affiliation
attributes do not create role bindings, class enrollments, or permissions.
Provider MFA is trusted only when an operator-configured released attribute
matches an explicit accepted value. Without that evidence the resulting session
is single-factor and may use Proctor's MFA challenge endpoint for step-up
authentication.

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
lifetime, recovery-code count, a rotatable AES-256 encryption-key ring, and
operator-owned external-provider definitions.
When MFA is enabled, `authentication.mfa.encryption_key` must be a standard
base64-encoded 32-byte key. Previous keys may remain in `decryption_keys`
during rotation.

The first external provider types are `cas` and `oidc`. Every enabled provider
has a stable lowercase ID, display name, one matching protocol block, explicit
claim mappings, optional home-organization allowlisting, and optional trusted
MFA values. The checked-in example leaves the provider list empty so local
development does not depend on an identity service.

For CAS, `subject: "user"` selects `<cas:user>`; another released attribute may
be selected explicitly. Proctor never assumes that `<cas:user>` is an ePPN and
never uses email as the external identity key. CAS email is considered verified
only when `trust_email` is explicitly enabled or a mapped boolean released
attribute says so.

Example provider entry:

```json
{
  "id": "campus-cas",
  "type": "cas",
  "display_name": "Campus CAS",
  "enabled": true,
  "auto_provision": true,
  "cas": {
    "base_url": "https://cas.example.edu/cas",
    "validation_path": "/p3/serviceValidate",
    "timeout": "5s",
    "max_response_bytes": 65536
  },
  "claims": {
    "subject": "user",
    "username": "uid",
    "email": "mail",
    "first_name": "givenName",
    "last_name": "sn",
    "home_organization": "schacHomeOrganization",
    "affiliation": "eduPersonAffiliation",
    "allowed_home_organizations": ["example.edu"],
    "trust_email": true,
    "multi_factor_attribute": "authnContext",
    "multi_factor_values": ["mfa"]
  }
}
```

OIDC uses issuer discovery, Authorization Code flow, S256 PKCE, and a
transaction-bound nonce. Proctor verifies ID-token signature, issuer, audience,
expiry, nonce, and `at_hash` when present. If `use_userinfo` is enabled, its
`sub` must match the ID token and it cannot replace ID-token authentication-time
or MFA claims. The callback URL registered at the provider is:

`https://<proctor-origin>/api/v1/auth/providers/<provider-id>/callback`

An Apereo CAS installation must enable its OIDC provider support and register
this Proctor callback as an OIDC relying party; installing Apereo CAS alone
does not make OIDC endpoints available.

Example OIDC provider entry:

```json
{
  "id": "campus-oidc",
  "type": "oidc",
  "display_name": "Campus Login",
  "enabled": true,
  "auto_provision": true,
  "oidc": {
    "issuer": "https://cas.example.edu/cas/oidc",
    "client_id": "proctor",
    "client_secret": "replace-with-a-secret",
    "scopes": ["openid", "profile", "email"],
    "use_userinfo": false,
    "timeout": "5s",
    "max_response_bytes": 262144
  },
  "claims": {
    "subject": "sub",
    "username": "preferred_username",
    "email": "email",
    "email_verified_claim": "email_verified",
    "first_name": "given_name",
    "last_name": "family_name",
    "home_organization": "schacHomeOrganization",
    "affiliation": "eduPersonAffiliation",
    "allowed_home_organizations": ["example.edu"],
    "trust_email": false,
    "multi_factor_attribute": "amr",
    "multi_factor_values": ["mfa"]
  }
}
```

Environment-overridden values are effective only for the running process and
are never persisted back into the configuration file.

The active configuration is owned by one concurrency-safe store. It separates
persisted values from environment overrides, returns cloned snapshots, supports
atomic file writes, reload/set listeners, and structured diffs. Logging and the
external-provider registry are dynamically reconfigurable; HTTP listener and
timeout changes require a process restart.

Logging supports multiple independently filtered console or file targets,
text/JSON formatting, contextual fields, bounded field sizes, runtime
reconfiguration, flush/shutdown, and locked test capture.

The default `cluster.backend` is `local`, with `node_id` set to `local`. This
backend has no peers: broadcast is deliberately a no-op and never loops back
into local handlers. The transport still participates in dependency checks and
is started before readiness and stopped through the platform lifecycle.

Set `cluster.backend` to `redis` and give every process a unique stable
`cluster.node_id` for a multi-node installation. Redis Pub/Sub carries
best-effort messages with at-most-once semantics. Reliable messages use one
acknowledged Stream per live node, remain pending when a handler fails, and are
retried with at-least-once semantics; reliable handlers must therefore be
idempotent. `reliable_maximum` is a hard queue ceiling and messages are not
silently trimmed. The cache backend is selected independently: each node may
use its own memory cache, with reliable cluster invalidation, or use Redis as a
shared cache. Clustered configuration requires shared VFS rather than
node-local storage.

## Verify

```sh
make -C server check
```

The default `test`, `test-race`, and `check` targets are hermetic: they do not
require PostgreSQL, Redis, SMTP, S3, or another external service. Tests backed
only by local `httptest` servers remain in the default suite.

External-service tests use the `integration` build tag and are invoked through
targets that start and stop their required Docker services. A selected
integration suite fails when its configured dependency is unavailable rather
than silently skipping coverage.

Run every tagged integration test through the exhaustive CI entrypoint with:

```sh
make -C server integration-all
```

Run the PostgreSQL-backed store, migration, application, and authentication
suite with:

```sh
make -C server integration-postgres
```

Run only Redis cluster compatibility with:

```sh
make -C server integration-redis
```

Run the CAS and OIDC application integrations, using local provider test
servers and PostgreSQL, with:

```sh
make -C server integration-providers
```

Run the Redis and two-node WebSocket/cluster suite with:

```sh
make -C server integration-realtime
```

The established conformance aliases remain available:

```sh
make -C server conformance-postgres
make -C server conformance-realtime
```

The individual `test`, `test-race`, `vet`, and `build` targets use the root
workspace during repository development. The server declares exact pseudo
versions of the reusable modules; those versions must be published before the
server module is distributed independently of this monorepo.
