# Security and privacy

Proctor stores sensitive educational and identity data. Security properties
are explicit contracts backed by tests, not assumptions inherited from an
upstream reference.

## Boundary safeguards

- Require TLS outside explicit local development.
- Trust forwarding headers only from configured proxies.
- Bound request bodies, headers, URLs, provider responses, uploads, queues, and
  serialized diagnostic fields.
- Use established password hashing and cryptographic libraries and
  constant-time credential comparisons where applicable.
- Rate-limit login, bootstrap, token, invitation, and recovery operations and
  prevent account enumeration with uniform public responses.
- Persist credentials as hashes when they need only comparison; encrypt secrets
  only when the server must recover them.
- Authorize file access from semantic metadata rather than guessed paths; bound
  uploaded type/size and retain a malware/content-scanning boundary.
- Use least-privilege database, storage, mail, and provider credentials.
- Define retention and legal-preservation behavior before destructive student
  data workflows.
- Test horizontal privilege escalation and cross-academic-unit isolation.

## Logging and observability

The Proctor-owned `mlog` subsystem supports independently filtered text or JSON
targets, safe contextual fields, bounded serialization, atomic dynamic
reconfiguration, test capture, flushing, and shutdown. `platform.Service` owns
its lifecycle.

Unexpected failures are logged once at the outer operational boundary.
Request-driven application services return failures rather than logging them;
long-lived workers receive a narrow logging port only for otherwise
unobservable operational events. Context may include component, request ID,
node ID, and safe entity IDs—never complete users, sessions, configuration,
credentials, tokens, exam answers, provider payloads, or raw claims.

Metrics and tracing instrument HTTP, WebSocket, store, and outbound-adapter
boundaries through explicit wrappers. Application use cases may expose named
outcomes or events but do not call a global telemetry facade.

Durable security audit behavior is specified in
[Authorization and audit](./authorization.md#audit).
