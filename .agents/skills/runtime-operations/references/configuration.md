# Deployment-configuration reference

Deployment configuration is operator-owned; institution application settings
are durable application data changed through authorized use cases.

Deployment configuration includes listeners/public URL, PostgreSQL, cache,
cluster, VFS, SMTP, execution hosts, external identity providers, logging,
secrets, and process limits. `Localization.DefaultLocale` selects the installed catalog fallback
and is validated against the embedded catalogs during root composition.
Application settings include institution presentation, branding,
academic policy, exam defaults, invitation rules, and other administrator
behavior.

`Server.TLS.Mode` selects `disabled`, `static`, or `lets_encrypt`. Static mode
requires `CertificateFile` and `PrivateKeyFile`. Let's Encrypt mode requires an
HTTPS `PublicURL` with one fully qualified DNS hostname, a private local
`LetsEncrypt.CacheDirectory`, and `ForwardHTTPToHTTPS`; `LetsEncrypt.Email` is
optional. Forwarding binds `HTTPListenAddress`, which must differ from the main
listener. Let's Encrypt is a single-node convenience and is invalid with the
Memberlist backend; clustered production terminates public TLS at its load
balancer. Static TLS remains valid in a cluster for an operator-managed
load-balancer-to-node encrypted hop.

The corresponding environment overrides are
`PROCTOR_SERVER_TLS_MODE`, `PROCTOR_SERVER_TLS_CERTIFICATE_FILE`,
`PROCTOR_SERVER_TLS_PRIVATE_KEY_FILE`,
`PROCTOR_SERVER_TLS_LETS_ENCRYPT_EMAIL`,
`PROCTOR_SERVER_TLS_LETS_ENCRYPT_CACHE_DIRECTORY`,
`PROCTOR_SERVER_TLS_FORWARD_HTTP_TO_HTTPS`, and
`PROCTOR_SERVER_TLS_HTTP_LISTEN_ADDRESS`. TLS mode, listener, certificate, and
forwarding changes require restart. HTTP certificates and Memberlist gossip
keys are independent deployment capabilities and never substitute for one
another.

Provider issuer/service URLs, client credentials, certificates, claim mapping,
and SMTP remain deployment configuration. The revisioned `AccessPolicy` is
application data: it selects local-login, credential-enrollment, invitation,
desktop-authorization, and per-provider admission behavior only among
configured capabilities. Ordinary administrators never read or replace
provider or mail secrets. The exact transition and mismatch behavior is in
the [`identity-and-access` reference](../../identity-and-access/references/access-and-onboarding.md#access-policy-and-deployment-configuration).

The one-time public bootstrap is protected by
`Authentication.Bootstrap.Secret`, with the environment override
`PROCTOR_AUTHENTICATION_BOOTSTRAP_SECRET`. A non-empty value is 32–512 bytes
and is always redacted. An uninitialized network-accessible installation fails
composition without it. `Authentication.Bootstrap.DevelopmentMode` (or
`PROCTOR_AUTHENTICATION_BOOTSTRAP_DEVELOPMENT_MODE`) permits generation only
when both the TCP listener and public origin are loopback; the generated value
is displayed once to the controlling terminal outside structured logs only
while the installation is pristine. After
initialization the durable marker prevents replacement or removal of deployment
configuration from reopening setup.

Recoverable TOTP secrets use the independent `Authentication.MFA` key ring
through the server-owned `secretseal` module. `EncryptionKey` selects the
primary key for new purpose- and User-bound envelopes; `DecryptionKeys`
contains at most eight fallbacks for values written before key promotion. All
entries are canonical standard-base64 encodings of exactly 32 bytes. Enabling
MFA requires a primary key, configuring fallbacks without one is invalid, and
every configured ring is constructed during server composition. The
environment overrides are `PROCTOR_AUTHENTICATION_MFA_ENCRYPTION_KEY` and
`PROCTOR_AUTHENTICATION_MFA_DECRYPTION_KEYS`. Configuration display redacts the
complete ring.

Recoverable transactional-mail payloads use the independent
`Mail.SecretSealing` deployment key ring. `EncryptionKey` is the primary key
for new envelopes and `DecryptionKeys` is a bounded fallback ring for rotation;
all entries are canonical standard-base64 encodings of exactly 32 bytes. The
ring may be absent only while mail delivery is disabled; enabling mail
activates the durable delivery workflow and requires a primary key.
Configuring fallbacks without a primary is invalid, and every configured ring
is constructed and collision-checked during server composition. Its
environment overrides are
`PROCTOR_MAIL_SECRET_SEALING_ENCRYPTION_KEY` and
`PROCTOR_MAIL_SECRET_SEALING_DECRYPTION_KEYS`. Configuration display redacts
every entry, and neither MFA nor Memberlist keys may substitute for it.

Memberlist gossip uses its own `Cluster.Memberlist` key ring. `EncryptionKey`
is the primary key and `DecryptionKeys` contains at most eight distinct
fallbacks for rolling rotation; entries decode from standard base64 to 16, 24,
or 32 bytes. Its environment overrides are
`PROCTOR_CLUSTER_MEMBERLIST_ENCRYPTION_KEY` and
`PROCTOR_CLUSTER_MEMBERLIST_DECRYPTION_KEYS`. Every entry is cloned and
redacted. The cluster protocol range is compiled into the binary rather than
operator-configurable. Cluster key changes require restart and use the staged
overlap procedure in [runtime](runtime.md#cluster-transport).

Normal startup requires one operator-owned JSON file and never creates it.
Path precedence is an explicit CLI path, `PROCTOR_CONFIG`, then
`config/config.json` relative to the process working directory. Release
bundles carry `config/config.example.json`; operators copy it to the active
path and edit it. That canonical file renders every deployment field with its
built-in default, including empty secret fields, so the supported schema is
discoverable without reading Go types. Empty structured lists stay empty in
the canonical file; complete validated entry objects for execution hosts and
CAS/OIDC providers ship under `config/examples/`. Configuration serialization
never omits zero-valued fields, and tests fail when the schema, defaults, or
examples drift. Deployment JSON field names are PascalCase. Value precedence
is built-in field defaults, the required typed file, then `PROCTOR_`
environment overrides. Unknown fields are rejected, validation errors are
aggregated where possible, URLs/durations are parsed at the boundary, secrets
are explicitly redacted, and the schema is versioned.

`Database.MaxOpenConnections` is at least two because Morph holds one
connection for migration work and a second for its refreshed named lock during
startup convergence.

`Cache.Backend` selects either Redis or the process-local encoded LRU. The
memory adapter enforces both `Cache.Memory.MaxEntries` and
`Cache.Memory.MaxBytes`; keys and encoded values count toward the byte limit,
while runtime object overhead does not. Memberlist mode requires Redis because
installation-wide disposable authentication counters must be coherent across
nodes. Redis remains non-authoritative and is not the Memberlist message
transport. The memory limits may be overridden with
`PROCTOR_CACHE_MEMORY_MAX_ENTRIES` and `PROCTOR_CACHE_MEMORY_MAX_BYTES`.

One concurrency-safe `config.Store` separates persisted configuration from
cloned effective snapshots. Environment overrides are never persisted.
Backings implement a shared conformance contract; memory and atomic-file
backings exist. Memory backing supports tests and explicit embedding but is not
the normal server fallback. Successful changes notify listeners with cloned
old/current values.

The composition root translates configuration into small immutable application
policies or explicit dynamic ports. Application services never receive the
whole `config.Config` or `config.Store`.

Runtime reconfiguration is capability-specific. Logging and the external
provider registry reconfigure dynamically; listener addresses, HTTP limits,
cluster backend, node identity, and the execution-host catalog require restart. Structural validation and
external connectivity diagnostics remain separate.

`Execution.Enabled` activates the bounded outbound execenv host directory.
Each entry under `Execution.Hosts` has a stable `ID`, a TCP `Address`, and
either production `tls` security or loopback-only `insecure_local` security.
TLS requires a verified `ServerName` and either a token or client certificate;
an optional CA file extends the system roots. Client certificate and key files
must be configured together. Cleartext development requires a token and
rejects all non-loopback addresses. Host tokens are redacted. Dial and
operation timeouts have environment overrides
`PROCTOR_EXECUTION_DIAL_TIMEOUT` and
`PROCTOR_EXECUTION_OPERATION_TIMEOUT`; enablement has
`PROCTOR_EXECUTION_ENABLED`. The host list itself remains structured file
configuration so credentials and stable identities are reviewed together.

Logging configuration bounds the engine and per-target queues, enqueue/flush/
shutdown deadlines, field size, target level/format, and file rotation. A
configuration change becomes visible only after every replacement target has
initialized successfully.

A User-owned portable presentation document is neither deployment
configuration nor an institution application setting. Its exact-source,
revision, self-access, and client-interpretation boundaries are defined in
the [`user-settings` skill](../../user-settings/SKILL.md); it never enters `config.Store`.
