# Proctor server

This directory contains the new Proctor application server. It is an
independent Go module and is licensed under AGPL-3.0-only.

The server exposes a narrow module-root construction and lifecycle facade:

```go
node, err := server.New(ctx, server.WithConfigPath(path))
if err != nil {
	return err
}
defer node.Close()
return node.Start(ctx)
```

The module-root facade selects concrete infrastructure and owns platform
startup, HTTP serving, readiness, bounded HTTP drain, transport cleanup, and
platform shutdown:

```text
server.New → acquire concrete adapters → platform.Accept (ownership)
                                         ↓ borrowed construction projection
                              File Content → app.App → HTTP/WebSocket/Jobs
                                         ↓ projection discarded
                                      inert Server
```

`platform.Accept` takes ownership of already-constructed store, cache, mail,
VFS, cluster, logging, configuration, and external-authentication capabilities
at call entry, including failure outcomes. A short-lived lifecycle-free
projection lets the root wire consumers without turning Platform into a
locator. The projection is discarded before construction returns.

The facade intentionally exposes construction, start, close, readiness, and
the narrow operator-command capabilities used by the CLI. The CLI remains a
thin caller of this API. Its Cobra tree is assembled explicitly under
`cmd/proctor/commands`, creates fresh command state for each execution, and
keeps concrete infrastructure construction in this module-root facade.
`testlib` uses the same private composition recipe
through concrete typed overrides and receives only Server, the application
facade and an HTTP handler; it retains its supplied adapters for assertions.

Durable architecture and capability inventories are maintained outside this
module README:

- [`docs/architecture/`](../docs/architecture/) defines boundaries and their
  rationale;
- [`docs/project/status.md`](../docs/project/status.md) records implemented
  capability areas and unresolved decisions;
- [`httpapi/CONTRACT.md`](httpapi/CONTRACT.md) defines the HTTP contract; and
- [`cluster/GUARANTEES.md`](cluster/GUARANTEES.md) defines cluster delivery and
  recovery behavior.

This README focuses on running, configuring, and verifying the server so those
authorities do not drift through duplicated implementation inventories.

## Run locally

From the repository root:

```sh
go run ./server/cmd/proctor serve --config ./server/config.example.json
```

The server requires a migrated PostgreSQL database before startup. Cache, mail,
and VFS backends are selected from `config.example.json`; the development
defaults use memory cache, disabled mail, and local VFS.

### Pre-release database reset

The schema is currently a single pre-release baseline at version 1. Earlier
development migrations used incompatible millisecond timestamp and lifecycle
representations. There is intentionally no upgrade path from those development
schemas: existing development databases must be discarded and recreated. Back
up any data you need before resetting a database.

The checked-in Docker PostgreSQL service stores its database on a temporary
filesystem. Recreate it, apply the baseline, and run the PostgreSQL integration
suite with:

```sh
make -C server postgres-down
make -C server postgres-up
PROCTOR_DATABASE_DATA_SOURCE='postgres://proctor:proctor@127.0.0.1:15432/proctor?sslmode=disable' \
  go run ./server/cmd/proctor migrate up
make -C server integration-postgres
```

For another development PostgreSQL instance, drop and recreate the dedicated
Proctor database (or its isolated schema) with that instance's administration
tools, then run `proctor migrate up` against the empty database. Do not point a
new server build at a database created from an earlier development migration
set; those schemas predate the current squashed baseline and are unsupported.

The default listener is `127.0.0.1:8065`. Available endpoints are:

- `GET /health/live`
- `GET /health/ready`
- `GET /api/v1/system/version`
- `GET /api/v1/bootstrap` (public boolean installation status)
- `POST /api/v1/bootstrap` (public only until the atomic bootstrap succeeds)
- `GET /api/v1/discovery` (public safe access and desktop compatibility)
- `GET /api/v1/access-policy` (authorized policy and bounded history)
- `POST /api/v1/access-policy/preflight` and `PUT /api/v1/access-policy`
  (strong recent Session; replacement also requires `Idempotency-Key`)
- `POST /api/v1/auth/register` (policy-fenced public local registration; API
  only, with the hosted `/register` page still deferred)
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
- `GET /api/v1/users/me/settings` and `PUT /api/v1/users/me/settings`
  (interactive Session only; exact JSONC source with conditional revision and
  required `Idempotency-Key` on replacement)
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
- `GET|POST /api/v1/academic-periods` and resource `GET|PATCH|DELETE`;
  creation names an immutable Institution or Academic Unit owner and list
  visibility is constrained by authorized owner scopes
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
academic unit. Academic periods authorize as first-class resources through
their immutable Institution or Academic Unit owner; unit ownership applies to
that unit's subtree. Membership lists
default to records active now and accept `active_at`; `history=true` returns
the retained effective-dated history.

Bootstrap is explicit: normal account creation never promotes a “first user.”
`POST /api/v1/bootstrap` requires the deployment-owned `bootstrap_secret`.
The successful transaction creates the institution, unverified administrator
and password credential, initial User Settings and profile-picture Job,
protected system-administrator role and institution binding, conservative
Access Policy revision 1, installation marker, and audit event together. A
PostgreSQL advisory lock plus secret digest and command fingerprint make exact
replay safe and conflicting concurrent attempts fail without partial state.
The status response exposes only `initialized`, and bootstrap creates no
Session.

When every Proctor node is stopped, a host operator can restore one existing
active system administrator's local authentication path without a network
backdoor:

```console
proctor administrator recover --config /etc/proctor.json \
  --institution-id <institution-id> --user-id <user-id> \
  --enable-local-login --rotate-password
```

`--rotate-password` reads the password from redirected, non-terminal private
standard input and never accepts it as an argument. Capture it without echo
using the host shell's private-input facility (for example, `read -rs` followed
by a stdin redirection); never put the value in the command itself. At least one of
`--enable-local-login` or `--rotate-password` is required. The operation mints
no Session, preserves MFA and existing Sessions, and commits a secret-free
pending security record atomically with the repair. The next normal startup
must reconcile that record into audit before the node serves traffic.
The command also rejects the repair while any PostgreSQL-clocked serving-node
lease is unexpired. Graceful shutdown withdraws its lease; after a crashed node,
wait for the bounded lease to expire before retrying. Merely constructing this
offline command never creates a serving lease.

Production sets `authentication.bootstrap.secret` (or
`PROCTOR_AUTHENTICATION_BOOTSTRAP_SECRET`) to an operator-generated value of at
least 32 bytes. Only loopback listener plus loopback public-origin development
may set `authentication.bootstrap.development_mode` without a secret; the
server then prints a temporary value once to the controlling terminal while
the installation is pristine. The secret is redacted from configuration
display and structured logs.

Web login (`client_type` equal to `web`) returns the user and session but omits
raw credentials from JSON. It sets host-only
`PROCTOR_ACCESS`, `PROCTOR_REFRESH`, `PROCTOR_CSRF_BINDING`, and
`PROCTOR_CSRF` cookies. The first three are HttpOnly except for
`PROCTOR_CSRF`, which the client copies into `X-Proctor-CSRF-Token` on unsafe
requests. The refresh cookie is restricted to `/api/v1/auth/refresh`; refresh
rotates the full cookie set. Production cookie security follows the configured
HTTPS public URL.

Desktop authentication does not use the JSON login operation or those browser
cookies. The native client starts the system-browser authorization protocol at
`POST /api/v1/auth/desktop/authorizations` and exchanges its short-lived,
one-use code at `POST /api/v1/auth/desktop/token` for an ordinary rotating
Desktop Session. The hosted `/authorize/desktop` page and Desktop UI remain
separate implementation work.

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

`--config` is a persistent operator flag and may also precede the command.
Run `proctor --help` for the command tree or `proctor completion --help` for
shell-completion generation.

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

Durable recoverable mail payloads use the independent
`mail.secret_sealing.encryption_key`, canonical standard base64 for 32 bytes.
Up to eight previous keys may remain in
`mail.secret_sealing.decryption_keys` while durable payloads are re-encrypted.
The ring may be absent only while mail is disabled. Enabling mail requires a
primary key; a partially or unsafely configured ring is rejected during
startup. MFA, Memberlist, and mail encryption keys are intentionally not
interchangeable.

Mail-key rotation is staged: first deploy the new key as a readable fallback
on every node; then restart every node with that key promoted to
`encryption_key` and the old primary retained in `decryption_keys`; finally, a
strong recently authenticated operator starts `POST /api/v1/mail/rekey` with
the old key ID. The returned durable Job must succeed with zero retiring and
non-primary references before the old key is removed from configuration and
nodes are restarted again. Starting a node with the wrong promoted primary or
without any still-referenced key fails closed. The operation and diagnostics
contain key IDs and counts only, never key material.

For a later rotation, the preceding successful zero-reference proof permits a
node to restart with the next primary while retaining the currently fenced key
as a fallback. Until the next rekey command advances the PostgreSQL fence, that
node can deliver existing mail but cannot persist new encrypted payloads;
nodes still using the fenced primary may continue to enqueue. Failed or
missing proofs do not permit this staged promotion.

Inspect safe progress and the final recorded retirement proof at
`GET /api/v1/mail/rekey/{job_id}`. During a rolling deployment, an old-primary
node relinquishes this Job with bounded backoff and cannot reclaim it under the
same node identity; its incompatible Attempt does not consume the Job's
failure budget, and a new-primary node completes the shared operation.

The first external provider types are `cas` and `oidc`. Every enabled provider
has a stable lowercase ID, display name, one matching protocol block, explicit
claim mappings, optional home-organization allowlisting, and optional trusted
MFA values. An installation may define at most 64 external providers, matching
the Access Policy and public provider-projection bounds. The checked-in example
leaves the provider list empty so local development does not depend on an
identity service.

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

Set `cluster.backend` to `memberlist` and give every process a unique stable
`cluster.node_id` for a multi-node installation. Memberlist uses encrypted
gossip membership, PostgreSQL discovery heartbeats for bootstrap seeds, and
best-effort direct peer messaging. There is no durable cluster delivery class:
session and authorization correctness recover from PostgreSQL and bounded
authentication-cache TTLs when messages are delayed or lost. Handlers must be
idempotent under duplicates. Discovery is continuous: an isolated node
periodically re-lists compatible leases and retries a bounded rotating seed
batch without adding another lifecycle goroutine. Peer metadata advertises the
wire protocol compiled into the binary, and alive/merge admission rejects
malformed metadata, duplicate remote merge identities, or incompatible peers
after startup as well as during initial join. The cache backend is selected
independently: each node may use
its own memory cache or optional Redis as a shared disposable cache. Clustering
does not require Redis. Multi-node configuration requires shared VFS rather
than node-local storage, plus a shared primary encryption key, explicit
bind/advertise addresses, and optional `decryption_keys` during staged rotation.
Add the new fallback to every node, promote it while retaining the old primary
on every node, then remove the old fallback only after convergence; each stage
requires restart.

### Transactional-mail templates

The server owns localized transactional-mail copy and one MJML, tracked HTML,
and plain-text source set per closed message key. Install the pinned build
toolchain, regenerate HTML, check freshness, and render a delivery-free preview
with:

```sh
make -C server mail-templates-install
make -C server mail-templates-generate
make -C server mail-templates-check
make -C server mail-templates-test
make -C server mail-preview OUTPUT=/tmp/proctor-mail-preview
```

The first four commands delegate to the maintainer interface in
`server/templates/Makefile`; they may also be run directly as
`make -C server/templates install|generate|check|test`.

The exact property and maintenance contract lives in
[`templates/README.md`](templates/README.md).
`i18n/` and `templates/` contain data and build tooling only. General
translation behavior lives in `localization/`; mail-specific rendering lives
beside composition in `app/mail/`.

Consumer packages explicitly register every message they own, including
dynamic families that cannot be found reliably by scanning source literals.
Validate exact English coverage and placeholder contracts with
`make -C server i18n-check`. Maintainers can also run the `list`, `missing`, or
`format` subcommands through `go run ./cmd/ptool i18n`; formatting is an
explicit write operation and the normal check never rewrites catalogs. The
operator CLI selects a locale
from `PROCTOR_LOCALE`, then the standard `LC_ALL`, `LC_MESSAGES`, and `LANG`
environment variables, while keeping command names, flags, and machine values
stable.

### Production SMTP and deliverability

The checked-in [`config.example.json`](config.example.json) documents every
mail setting. A production installation enables the `smtp` backend, uses
`starttls` or `tls` for the relay, configures authentication only over TLS,
sets a stable sender and Message-ID domain, and supplies an independent
standard-base64 32-byte `mail.secret_sealing.encryption_key`. The equivalent
deployment overrides use the `PROCTOR_MAIL_` environment prefix. Cleartext
SMTP without authentication is for a loopback capture server such as Mailpit,
not a production relay.

The operator of the sender domain must authorize the selected relay through
SPF, arrange DKIM signing at that relay, and publish an aligned DMARC policy.
Proctor composes MIME and submits it to SMTP; it is not a DKIM signer and does
not interpret SMTP acceptance as inbox delivery. After configuration, send the
controlled operator test message, confirm its stable Message-ID and `accepted`
state through the safe mail views, then verify receipt and authentication
results at an external mailbox. Monitor `GET /api/v1/mail/metrics` together
with the relay's delivery diagnostics; Proctor logs deliberately contain no
recipient, body, ciphertext, or SMTP dialogue.

During a transient relay outage, keep mail enabled so bounded retries and the
mail-health projection retain the normal recovery path. Setting `mail.enabled`
to false is an explicit terminal suppression operation: outstanding work
converges to suppressed and is not resurrected by later re-enablement. Use the
documented rekey sequence above for payload-key rotation; never remove a
fallback until the durable zero-reference proof succeeds.

The controlled operator tracer uses `POST /api/v1/mail/test` with no request
body. Operators with `mail.view` can inspect safe bounded delivery metadata at
`GET /api/v1/mail/deliveries` and
`GET /api/v1/mail/deliveries/{mail_delivery_id}`. The authorized
`GET /api/v1/mail/metrics` projection exposes only bounded template, state,
public-outcome, attempt, latency, queue, and mail-health metrics. A recent
interactive Session
with `mail.manage` may cancel queued delivery work or retry a failed, unexpired,
still-relevant delivery through the nested `/cancel` and `/retry` operations;
PATs cannot perform those mutations. The test enqueue additionally targets
only the operator's own verified address. SMTP delivery remains asynchronous
and `accepted` means only that the configured transport accepted the message
data.

## Verify

```sh
make -C server check
```

`check` includes the production import-boundary gate. Run that gate alone with:

```sh
make -C server architecture
```

The dependency-debt ledger at
`server/architecture/dependency_debt.txt` is currently empty and may never
grow. The gate rejects forbidden imports, stale entries, duplicates, and
unsorted debt; its immutable initial ceiling prevents a new violation from
being legalized by editing the ledger.

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

Run optional Redis **cache** adapter tests (clustering does not require Redis)
with:

```sh
make -C server integration-redis
```

Run shared-object VFS conformance and the server file-content integration
against an isolated S3-compatible MinIO service with:

```sh
make -C server integration-s3
```

Run the CAS and OIDC application integrations, using local provider test
servers and PostgreSQL, with:

```sh
make -C server integration-providers
```

Run the WebSocket and two-node Memberlist cluster suite (PostgreSQL; optional
Redis only when testing shared-cache mode) with:

```sh
make -C server integration-realtime
```

The established conformance aliases remain available:

```sh
make -C server conformance-postgres
make -C server conformance-realtime
```

The file-management and durable-Job phase acceptance gate combines the
hermetic server checks, PostgreSQL clustered-recovery tests, shared-object VFS
integration, and independent `GOWORK=off` builds/tests of all four modules:

```sh
make -C server phase-file-jobs
```

The access-and-onboarding server-phase gate combines the hermetic checks with
the real composition graph, PostgreSQL-backed JSON/CSV administration,
multi-node protocol and recovery scenarios, decorated Store conformance,
lock-order regressions, and migration round-trip coverage. It certifies the
server protocol and domain phase, not the deferred hosted pages or Desktop UI:

```sh
make -C server phase-access-onboarding
```

The transactional-mail phase gate combines the hermetic server gate,
PostgreSQL integration and Store conformance, independent module builds/tests,
template freshness/rendering, reusable SMTP sender conformance, and a real
application-through-Mailpit flow for representative credential, security, and
Sitting fan-out messages:

```sh
make -C server phase-transactional-mail
```

For local SMTP inspection only, the same pinned Mailpit service is available
at SMTP `127.0.0.1:11025` and HTTP `http://127.0.0.1:18025` through:

```sh
make -C server mailpit-up
make -C server mailpit-logs
make -C server mailpit-down
```

The individual `test`, `test-race`, `vet`, and `build` targets use the root
workspace during repository development. The server declares exact pseudo
versions of the reusable modules; those versions must be published before the
server module is distributed independently of this monorepo.
