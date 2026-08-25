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
return node.Run(ctx)
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

The facade intentionally exposes construction, blocking run, close, readiness,
and the narrow operator-command capabilities used by the CLI. The CLI remains
a thin caller of this API. Its Cobra tree is assembled explicitly under
`cmd/proctor/commands`, creates fresh command state for each execution, and
keeps concrete infrastructure construction in this module-root facade.
`testlib` uses the same private composition recipe
through concrete typed overrides and receives only Server, the application
facade and an HTTP handler; it retains its supplied adapters for assertions.

Repository skills route durable architecture and workflow. Exact component
behavior remains beside the component:

- [`httpapi/CONTRACT.md`](httpapi/CONTRACT.md) defines the HTTP contract;
- [`openapi/README.md`](openapi/README.md) defines the human-first OpenAPI
  authoring and generation workflow; and
- [`cluster/GUARANTEES.md`](cluster/GUARANTEES.md) defines cluster delivery and
  recovery behavior.

This README focuses on running, configuring, and verifying the server without
duplicating implementation inventories.

## Run locally

From the repository root:

```sh
cp ./server/config/config.example.json ./server/config/config.json
npm --prefix ./webapp ci
npm --prefix ./webapp run build
go run ./server/cmd/proctor serve --config ./server/config/config.json
```

The active `config.json` is operator-owned and ignored by Git; Proctor never
creates it. The tracked example is the copy source, not an active fallback. It
renders every deployment field, including empty secret placeholders. Complete,
validated entries for the structured `Execution.Hosts` and
`Authentication.External.Providers` lists live under `config/examples/`; copy
the applicable object into the canonical file and replace its placeholder
values. Protect the active file because it contains real deployment secrets.
On startup Proctor connects to PostgreSQL, applies pending forward migrations
under a named database migration lock, validates the resulting schema, and
checks its configured cache, cluster, VFS, and execution-host dependencies.
Enabled SMTP is connection-tested and reported without making a temporary relay
outage fail general server readiness. The example uses memory cache, disabled
mail, and local VFS.

The root [product build](../build/README.md) owns complete development,
cluster, observability, packaging, and release lifecycles. Create a
release-style directory containing the executable, compiled hosted webapp,
copy-only configuration examples, legal notices, deployment support, and build
identity with:

```sh
make package
```

The default output is `dist/proctor/`, with the binary at its root, the Vite
distribution under `webapp/dist/`, the canonical example at
`config/config.example.json`, and structured entry examples under
`config/examples/`. Run from that directory after copying the canonical
example to `config/config.json`, or provide an explicit path. The package target
requires a nonexistent output path: it never reuses a directory that might
contain an operator's active configuration or secrets. Ordinary server builds
and `make -C server check` remain Node-free; the root product package is the
only build that combines Go and Vite.

`Server.WebappDirectory` selects the immutable Vite distribution and defaults
to `./webapp/dist` relative to the process working directory. Production
startup validates `webapp-build.json` against the Go binary version and commit
and fails before readiness when the artifacts do not belong to the same build.

### Run under systemd

The `serve` command supports systemd's notification protocol. When systemd
provides `NOTIFY_SOCKET`, Proctor attempts to send `READY=1` exactly once after
Platform, the serving-node lease, startup reconciliation, Jobs, WebSocket, and
HTTP serving have all reached readiness. A notification failure is logged but
does not stop an otherwise healthy server; systemd's start timeout remains the
external failure boundary.

The tracked [hardened service unit](../deploy/systemd/proctor.service) and
[deployment guide](../deploy/README.md) are copied into release archives.

Run the long-lived service as a dedicated non-root user. The CLI warns but
continues when its effective user is root, matching deployments where root is
temporarily intentional. To bind ports 80 and 443 without running the process
as root, grant only the operating-system bind capability to the executable or
have a load balancer bind the privileged public ports.

### Pre-release database reset

The schema is currently a single pre-release baseline at version 1. Earlier
development migrations used incompatible millisecond timestamp and lifecycle
representations. There is intentionally no upgrade path from those development
schemas: existing development databases must be discarded and recreated. Back
up any data you need before resetting a database.

The checked-in Docker PostgreSQL service stores its database on a temporary
filesystem. Recreate it and run the PostgreSQL integration suite with:

```sh
make -C server postgres-down
make -C server postgres-up
make -C server integration-postgres
```

For another development PostgreSQL instance, drop and recreate the dedicated
Proctor database (or its isolated schema) with that instance's administration
tools. Normal startup applies the empty database's baseline automatically.
`proctor migrate status` and `proctor migrate up` remain available for
deployment inspection and deliberate pre-start convergence. Do not point a new
server build at a database created from an earlier development migration set;
those schemas predate the current squashed baseline and are unsupported.

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
- `POST /api/v1/auth/browser/invitations` (exchange a fragment Invitation
  claim for a short-lived handle and HttpOnly browser proof)
- `POST /api/v1/auth/browser/invitations/accept` and `/accept-session`
  (purpose-separated local-account and existing-Session acceptance)
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

Production sets `Authentication.Bootstrap.Secret` (or
`PROCTOR_AUTHENTICATION_BOOTSTRAP_SECRET`) to an operator-generated value of at
least 32 bytes. Only loopback listener plus loopback public-origin development
may set `Authentication.Bootstrap.DevelopmentMode` without a secret; the
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
visual implementation work; the packaged runtime already owns the route.

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
go run ./server/cmd/proctor config validate --config ./server/config/config.json
```

`--config` is a persistent operator flag and may also precede the command.
Run `proctor --help` for the command tree or `proctor completion --help` for
shell-completion generation.

The configuration path is resolved in this order: an explicit `--config`,
`PROCTOR_CONFIG`, then `config/config.json` relative to the process working
directory. That file is required. Its schema uses PascalCase keys, is decoded
strictly over built-in field defaults, and is followed by `PROCTOR_`
environment overrides. Unknown JSON fields and invalid values are rejected at
startup; a missing file is reported with the example-copy instruction and is
never created automatically.

The deployment schema covers HTTP, PostgreSQL, cache, cluster transport and
node identity, mail, VFS, logging, password hashing, session lifetimes,
concurrent-session limits, and login rate limits. Secret fields are explicitly
redacted. Authentication configuration also controls the recent-authentication
window, verification/reset lifetimes, recovery throttles, MFA issuer and setup
lifetime, recovery-code count, an independent secret-sealing key ring, and
operator-owned external-provider definitions. When MFA is enabled,
`Authentication.MFA.EncryptionKey` must be the canonical standard-base64
encoding of exactly 32 bytes. Up to eight previous keys may remain in
`DecryptionKeys` so existing versioned envelopes remain readable during
rotation. MFA envelopes are authenticated to their owning User and the fixed
TOTP purpose; MFA, mail, Memberlist, and TLS keys are never interchangeable.

### Public TLS and HTTP forwarding

`Server.TLS.Mode` has three values:

- `disabled` serves cleartext HTTP. This is also the correct application-node
  setting when a production load balancer terminates public TLS; `PublicURL`
  remains the external HTTPS URL.
- `static` serves HTTPS with operator-provided `CertificateFile` and
  `PrivateKeyFile` paths.
- `lets_encrypt` retrieves and renews a certificate for the single exact DNS
  hostname in `PublicURL` and keeps its account and certificate material in the
  node-local `LetsEncrypt.CacheDirectory`.

Static TLS with port 80 forwarding can be configured as follows:

```json
{
  "ListenAddress": ":443",
  "PublicURL": "https://proctor.example.edu",
  "TLS": {
    "Mode": "static",
    "CertificateFile": "/etc/proctor/tls/certificate.pem",
    "PrivateKeyFile": "/etc/proctor/tls/private-key.pem",
    "LetsEncrypt": {
      "Email": "",
      "CacheDirectory": "/var/lib/proctor/acme"
    },
    "ForwardHTTPToHTTPS": true,
    "HTTPListenAddress": ":80"
  }
}
```

For automatic single-node certificates, change the TLS block to:

```json
{
  "Mode": "lets_encrypt",
  "CertificateFile": "",
  "PrivateKeyFile": "",
  "LetsEncrypt": {
    "Email": "operator@example.edu",
    "CacheDirectory": "/var/lib/proctor/acme"
  },
  "ForwardHTTPToHTTPS": true,
  "HTTPListenAddress": ":80"
}
```

The public DNS record must resolve to this server and ports 80 and 443 must be
reachable by the certificate authority. Binding those privileged ports may
require an operating-system capability or a supervisor that grants them. The
cache directory is created with mode `0700`; startup rejects an existing cache
directory that exposes the private material to group or other users or cannot
be written. Non-challenge HTTP requests receive `308 Permanent Redirect` to the
configured public authority with their path and query preserved.

Built-in Let's Encrypt is intentionally rejected with the Memberlist cluster
backend because its cache, issuance, and renewal are node-local. Active-active
deployments use redundant load balancers for the public certificate, ACME, and
HTTP forwarding. If the load balancer-to-node hop must also be encrypted,
configure static TLS on every application node and provision those certificates
through operator infrastructure. Public TLS certificates and Memberlist's
shared gossip-encryption key ring are separate and must never reuse key
material.

Durable recoverable mail payloads use the independent
`Mail.SecretSealing.EncryptionKey`, canonical standard base64 for 32 bytes.
Up to eight previous keys may remain in
`Mail.SecretSealing.DecryptionKeys` while durable payloads are re-encrypted.
The ring may be absent only while mail is disabled. Enabling mail requires a
primary key; a partially or unsafely configured ring is rejected during
startup. MFA, Memberlist, and mail encryption keys are intentionally not
interchangeable.

Mail-key rotation is staged: first deploy the new key as a readable fallback
on every node; then restart every node with that key promoted to
`EncryptionKey` and the old primary retained in `DecryptionKeys`; finally, a
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

For CAS, `Claims.Subject: "user"` selects `<cas:user>`; another released attribute may
be selected explicitly. Proctor never assumes that `<cas:user>` is an ePPN and
never uses email as the external identity key. CAS email is considered verified
only when `Claims.TrustEmail` is explicitly enabled or a mapped boolean released
attribute says so.

Example provider entry:

```json
{
  "ID": "campus-cas",
  "Type": "cas",
  "DisplayName": "Campus CAS",
  "Enabled": true,
  "AutoProvision": true,
  "CAS": {
    "BaseURL": "https://cas.example.edu/cas",
    "ValidationPath": "/p3/serviceValidate",
    "Timeout": "5s",
    "MaxResponseBytes": 65536
  },
  "Claims": {
    "Subject": "user",
    "Username": "uid",
    "Email": "mail",
    "FirstName": "givenName",
    "LastName": "sn",
    "HomeOrganization": "schacHomeOrganization",
    "Affiliation": "eduPersonAffiliation",
    "AllowedHomeOrganizations": ["example.edu"],
    "TrustEmail": true,
    "MultiFactorAttribute": "authnContext",
    "MultiFactorValues": ["mfa"]
  }
}
```

OIDC uses issuer discovery, Authorization Code flow, S256 PKCE, and a
transaction-bound nonce. Proctor verifies ID-token signature, issuer, audience,
expiry, nonce, and `at_hash` when present. If `OIDC.UseUserInfo` is enabled, its
`sub` must match the ID token and it cannot replace ID-token authentication-time
or MFA claims. The callback URL registered at the provider is:

`https://<proctor-origin>/api/v1/auth/providers/<provider-id>/callback`

An Apereo CAS installation must enable its OIDC provider support and register
this Proctor callback as an OIDC relying party; installing Apereo CAS alone
does not make OIDC endpoints available.

Example OIDC provider entry:

```json
{
  "ID": "campus-oidc",
  "Type": "oidc",
  "DisplayName": "Campus Login",
  "Enabled": true,
  "AutoProvision": true,
  "OIDC": {
    "Issuer": "https://cas.example.edu/cas/oidc",
    "ClientID": "proctor",
    "ClientSecret": "replace-with-a-secret",
    "Scopes": ["openid", "profile", "email"],
    "UseUserInfo": false,
    "Timeout": "5s",
    "MaxResponseBytes": 262144
  },
  "Claims": {
    "Subject": "sub",
    "Username": "preferred_username",
    "Email": "email",
    "EmailVerifiedClaim": "email_verified",
    "FirstName": "given_name",
    "LastName": "family_name",
    "HomeOrganization": "schacHomeOrganization",
    "Affiliation": "eduPersonAffiliation",
    "AllowedHomeOrganizations": ["example.edu"],
    "TrustEmail": false,
    "MultiFactorAttribute": "amr",
    "MultiFactorValues": ["mfa"]
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

### Prometheus metrics

`Metrics.Enabled` starts a dedicated node-local Prometheus listener. Its safe
default is `127.0.0.1:8067`; Prometheus must scrape every application node and
apply its own target labels because Proctor does not aggregate metrics through
Memberlist. Only `GET /metrics` exists on this listener—profiling endpoints are
not mounted. Listener settings are restart-required.

For a monitoring agent on the same host, keep the loopback address and scrape
`http://127.0.0.1:8067/metrics`. `Metrics.BearerToken` is optional on loopback.
For a remote monitoring network, choose a non-loopback `ListenAddress` and set
both `Metrics.TLS.CertificateFile` and `Metrics.TLS.PrivateKeyFile`, plus a
random bearer token of 32–512 bytes (prefer
`PROCTOR_METRICS_BEARER_TOKEN`). Configuration validation rejects an exposed
listener missing either protection. The token is sent as
`Authorization: Bearer <token>` and is redacted from configuration display.
The principal deployment overrides are `PROCTOR_METRICS_ENABLED`,
`PROCTOR_METRICS_LISTEN_ADDRESS`, `PROCTOR_METRICS_TLS_CERTIFICATE_FILE`, and
`PROCTOR_METRICS_TLS_PRIVATE_KEY_FILE`; timeout and scrape-concurrency fields
also have matching `PROCTOR_METRICS_` overrides.

Metric families use the `proctor_` prefix and cover build/process/readiness and
scrape health; sealed HTTP route templates, payload sizes, SQL pools, complete
store-call timing and retry exhaustion; logging drops and cache decisions;
WebSocket connections, messages, broadcasts, fan-out, replay, subscriptions,
and backpressure; cluster messages/bytes, fan-out, membership, discovery,
rejoin, and admission; durable Job claims, queue latency, lease heartbeats,
reservations, checkpoints, completion, recurrence, and periodic work;
execution-host state, capacity, calls, observations, files, and terminals; VFS
operation outcomes, object/page sizes, streams, and bytes; shared memory/Redis
cache hits, misses, conditional outcomes, latency, and bytes; SMTP stages,
portable delivery outcomes, recipients/bytes, and durable mail queue/health;
and selected authentication, authorization, realtime, and examination
outcomes. Labels never contain
raw request or VFS paths, cache keys, host/node/user/session/exam identifiers,
mail recipients, payloads, credentials, or error text.

The default `Cluster.Backend` is `local`, with `NodeID` set to `local`. This
backend has no peers: broadcast is deliberately a no-op and never loops back
into local handlers. The transport still participates in dependency checks and
is started before readiness and stopped through the platform lifecycle.

Set `Cluster.Backend` to `memberlist` and give every process a unique stable
`Cluster.NodeID` for a multi-node installation. Memberlist uses encrypted
gossip membership, PostgreSQL discovery heartbeats for bootstrap seeds, and
best-effort direct peer messaging. There is no durable cluster delivery class:
session and authorization correctness recover from PostgreSQL and bounded
authentication-cache TTLs when messages are delayed or lost. Handlers must be
idempotent under duplicates. Discovery is continuous: an isolated node
periodically re-lists compatible leases and retries a bounded rotating seed
batch without adding another lifecycle goroutine. Peer metadata advertises the
wire protocol compiled into the binary, and alive/merge admission rejects
malformed metadata, duplicate remote merge identities, or incompatible peers
after startup as well as during initial join. Single-node installations may use
the bounded in-process LRU application cache. Memberlist installations require
Redis so installation-wide disposable authentication counters have one shared
view; the transport itself remains peer-to-peer and does not route messages
through Redis. Multi-node configuration also requires shared VFS rather than
node-local storage, plus a shared primary encryption key, explicit
bind/advertise addresses, and optional `DecryptionKeys` during staged rotation.
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

### OpenAPI authoring

Edit routes, descriptions, examples, and schemas in the resource-oriented YAML
modules under [`openapi/`](openapi/), then regenerate the reviewed JSON
artifact. Never edit `openapi.json` directly.

```sh
make -C server openapi-build
make -C server openapi-check
make -C server openapi-agreement
```

The build and check commands use `ptool openapi`; the agreement target also
compares the compiled contract with registered HTTP routes, DTOs,
authentication requirements, and public errors.

### Production SMTP and deliverability

The checked-in [`config.example.json`](config/config.example.json) documents every
mail setting. A production installation enables the `smtp` backend, uses
`starttls` or `tls` for the relay, configures authentication only over TLS,
sets a stable sender and Message-ID domain, and supplies an independent
standard-base64 32-byte `Mail.SecretSealing.EncryptionKey`. The equivalent
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
mail-health projection retain the normal recovery path. Setting `Mail.Enabled`
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

The architecture gate evaluates production packages against ordered,
declarative import rules. Every package must have an owning rule and every
forbidden import fails immediately; there is no waiver path.

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

Run Redis cache adapter tests with:

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

Run the WebSocket and two-node Memberlist cluster suite with PostgreSQL and
Redis with:

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

The server module pins published revisions of the reusable modules so that the
isolated check cannot silently borrow newer APIs from `go.work`. When the
repository is private, Git must be authenticated for `github.com` access;
the Makefile exports the matching `GOPRIVATE` pattern so Go bypasses the public
module proxy and checksum database for these in-repository dependencies.

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
