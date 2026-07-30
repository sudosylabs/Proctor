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
- hashed, expiring, single-use password-reset and email-verification tokens.

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
- login throttling through the configured shared cache;
- an immutable request principal and explicit authentication policy on every
  route;
- active-session listing and self-service individual or account-wide
  revocation, with serialized refresh/login races and complete access-cache
  invalidation;
- current-state scoped authorization with institution and ancestor
  academic-unit inheritance, exact class scope, additive roles, and default
  denial;
- dedicated role and role-binding stores with overlap-safe effective periods;
- durable PostgreSQL security audits with fail-closed decision recording,
  bounded prior/result data, request/node correlation, and keyset pagination.
- Mattermost-style per-domain `Init*` API registration through one
  policy-aware registrar and route-matrix test, with a single versioned
  `BaseRoutes.APIRoot`, regex-constrained resource IDs, and centrally populated
  typed request parameters;
- atomic one-time installation bootstrap with a protected built-in
  system-administrator role and durable success audit;
- audited custom-role and scoped role-binding administration, including
  immediate permission changes and last-administrator protection.

External identity login, password recovery, MFA, personal access-token
services, academic enrollment services, exams, a concrete multi-node cluster
backend, and WebSockets remain intentionally unimplemented until their next
vertical slices.

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
- `GET /api/v1/users/me`
- `GET /api/v1/users/me/sessions`
- `POST /api/v1/users/me/sessions/revoke`
- `POST /api/v1/users/me/sessions/revoke-all`
- `GET /api/v1/audits` (requires an institution-scoped role granting
  `audit.view`; accepts `limit`, opaque `cursor`, `actor_id`, `action`,
  `resource_type`, and `resource_id` filters)
- `GET|POST /api/v1/roles` and
  `GET|PATCH|DELETE /api/v1/roles/{role_id}` (requires `role.manage`)
- `GET|POST /api/v1/role-bindings` and
  `DELETE /api/v1/role-bindings/{role_binding_id}` (requires `role.manage`;
  list by `user_id` or by `scope_type` plus `scope_id`)

Bootstrap is explicit: normal account creation never promotes a “first user.”
The successful transaction creates the institution, administrator and password
credential, protected system-administrator role, institution binding,
installation marker, and audit event together. A PostgreSQL advisory lock and
pristine-state check make this safe when several application nodes start at
once. The status response exposes only `initialized`.

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
redacted. Environment-overridden values are effective only for the running
process and are never persisted back into the configuration file.

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
