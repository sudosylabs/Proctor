# Configuration

Deployment configuration is operator-owned; application settings are durable
application data changed through authorized use cases.

Deployment configuration includes listeners/public URL, PostgreSQL, cache,
cluster, VFS, SMTP, external identity providers, logging, secrets, and process
limits. Application settings include institution presentation, branding,
academic policy, exam defaults, invitation rules, and other administrator
behavior.

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
