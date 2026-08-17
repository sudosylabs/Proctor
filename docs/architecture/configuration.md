# Configuration

Deployment configuration is operator-owned; institution application settings
are durable application data changed through authorized use cases.

Deployment configuration includes listeners/public URL, PostgreSQL, cache,
cluster, VFS, SMTP, external identity providers, logging, secrets, and process
limits. Application settings include institution presentation, branding,
academic policy, exam defaults, invitation rules, and other administrator
behavior.

Provider issuer/service URLs, client credentials, certificates, claim mapping,
and SMTP remain deployment configuration. The revisioned `AccessPolicy` is
application data: it selects local-login, credential-enrollment, invitation,
desktop-authorization, and per-provider admission behavior only among
configured capabilities. Ordinary administrators never read or replace
provider or mail secrets. The exact transition and mismatch behavior is in
[Access and onboarding](./access-and-onboarding.md#access-policy-and-deployment-configuration).

The one-time public bootstrap is protected by
`authentication.bootstrap.secret`, with the environment override
`PROCTOR_AUTHENTICATION_BOOTSTRAP_SECRET`. A non-empty value is 32–512 bytes
and is always redacted. An uninitialized network-accessible installation fails
composition without it. `authentication.bootstrap.development_mode` (or
`PROCTOR_AUTHENTICATION_BOOTSTRAP_DEVELOPMENT_MODE`) permits generation only
when both the TCP listener and public origin are loopback; the generated value
is displayed once to the controlling terminal outside structured logs only
while the installation is pristine. After
initialization the durable marker prevents replacement or removal of deployment
configuration from reopening setup.

Recoverable transactional-mail payloads use the independent
`mail.secret_sealing` deployment key ring. `encryption_key` is the primary key
for new envelopes and `decryption_keys` is a bounded fallback ring for rotation;
all entries are canonical standard-base64 encodings of exactly 32 bytes. The
ring may be absent only while mail delivery is disabled; enabling mail
activates the durable delivery workflow and requires a primary key.
Configuring fallbacks without a primary is invalid, and every configured ring
is constructed and collision-checked during server composition. Its
environment overrides are
`PROCTOR_MAIL_SECRET_SEALING_ENCRYPTION_KEY` and
`PROCTOR_MAIL_SECRET_SEALING_DECRYPTION_KEYS`. Configuration display redacts
every entry, and neither MFA nor Memberlist keys may substitute for it.

Configuration precedence is defaults, typed configuration backing, then
`PROCTOR_` environment overrides. Unknown fields are rejected, validation
errors are aggregated where possible, URLs/durations are parsed at the
boundary, secrets are explicitly redacted, and the schema is versioned.

One concurrency-safe `config.Store` separates persisted configuration from
cloned effective snapshots. Environment overrides are never persisted.
Backings implement a shared conformance contract; memory and atomic-file
backings exist. Successful changes notify listeners with cloned old/current
values.

The composition root translates configuration into small immutable application
policies or explicit dynamic ports. Application services never receive the
whole `config.Config` or `config.Store`.

Runtime reconfiguration is capability-specific. Logging and the external
provider registry reconfigure dynamically; listener addresses, HTTP limits,
cluster backend, and node identity require restart. Structural validation and
external connectivity diagnostics remain separate.

A User-owned portable presentation document is neither deployment
configuration nor an institution application setting. Its exact-source,
revision, self-access, and client-interpretation boundaries are defined in
[User settings](./user-settings.md); it never enters `config.Store`.
