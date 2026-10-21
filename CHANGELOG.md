# Changelog

Notable changes to Proctor's four product modules are recorded here. The
format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
each module is versioned under the
[versioning policy](build/README.md#versioning-policy). Entries name their
module when a change is not server-only. No module has been released yet.

## [Unreleased]

### Removed

- Server: execution hosts and interactive terminals, including their APIs,
  settings, metrics, and persistence (**breaking**).

### Changed

- Server: wire timestamps are RFC 3339 strings. `create_at`, `update_at`, and
  `delete_at` become `created_at`, `updated_at`, and `archived_at` (omitted
  while active), Job `complete_at` and `start_at` become `completed_at` and
  `started_at`, timestamp query parameters take RFC 3339, and absent optional
  timestamps are omitted instead of `null` (**breaking**).
- Server: cancellation is spelled `canceled` on the wire. Retention Notice
  `cancelled_at` becomes `canceled_at`, and the Retention Notice and Record
  Retirement `state` value `cancelled` becomes `canceled`, matching Sitting
  `canceled_at` and every other `canceled` state. The pre-release SQL baseline
  renames the matching retention columns and state; development databases
  must be reset (**breaking**).
- Server: error codes follow one grammar, and the HTTP status derives from the
  code. Several codes were renamed (for example
  `authentication.invalid_credentials` became
  `authentication.credentials_invalid`, `internal` became `server.internal`,
  and `not_found` became `route.not_found`). Every `*.unavailable` code is 503,
  and Problem Details `type` URIs end in the code (**breaking**).
- Server: an application result the server cannot present returns 500
  `server.internal` instead of a resource family's `*.unavailable` code, and
  unparseable list query parameters return `request.invalid` (**breaking**).
- Server: create requests reject members that the route path already owns,
  such as `parent_id` on Academic Unit create or `class_id` on Class Member
  enrollment, with `request.invalid` (**breaking**).
- Server: production is the untagged build default. Development builds must
  pass `-tags dev`; `-tags production` no longer has an effect (**breaking**).
- Server: the pre-release SQL baseline changed (new indexes and manual Attempt
  suspension). Development databases created from an earlier baseline must be
  reset before use (**breaking**).
- Server: static TLS certificates and configuration reload on `SIGHUP`, and the
  systemd unit gains `ExecReload`.
- Server: Let's Encrypt mode requires
  `Server.TLS.LetsEncrypt.AcceptTermsOfService` to be `true` (**breaking**).
- Server: named advisory locks hash with one key scheme, so a node on an
  earlier build does not exclude a node on this one; upgrade every node of a
  cluster together (**breaking**).
- Server: Store timer metric labels drop the `_with_audit` suffix, and every
  security-sensitive Store mutation requires its audit record.
- Server: when the success audit of a login cannot be recorded, no Session is
  issued at all.
- Build: `make container` packages Linux binaries with the same recipe as
  `make dist` and builds them with the release Dockerfile, so a local image
  matches a published one; `Dockerfile.runtime` is removed.
- Build: the root `make check` verifies transactional-mail template freshness,
  and `make -C server integration-postgres` runs every PostgreSQL integration
  test in its packages instead of a fixed list.

### Added

- Server: Exam Managers can manually suspend an active Attempt.
- Server: local login attempts are also bounded per account across all
  sources (`Authentication.LoginRateLimit.MaximumIdentityAttempts`).
- Server: `Server.TrustedProxies` names the reverse proxies whose forwarded
  client address and request ID are accepted.

### Fixed

- Server: re-allowing a focus-loss suspension no longer reports a committed
  re-allow as unavailable.
- Server: a failed audit completion is joined with, not substituted for, the
  original failure.
- Server: rows written with a node's clock are never later stamped earlier by
  PostgreSQL time (or the reverse), which removes intermittent check-constraint
  failures and unreadable Sessions when node and database clocks differ.
- Server: Desktop approval audit binds to the approving account, local password
  login outcomes are durably audited, and result-release audit begins before
  release time is reserved.
- Server: manager Attempt operations authorize before reading Attempts.
- Server: password reset looks up the token before hashing the new password.
- Server: forwarded client identity is trusted only from configured proxies,
  HSTS is sent when the server terminates TLS, and transport refusals answer
  with Problem Details.
- Server: every inbound WebSocket frame is rate-limited, and HTTP and WebSocket
  share origin normalization.
- Server: candidate answers are no longer spooled to the temporary directory,
  and transfer deadlines are sized from route declarations.
- Server: manager re-allow, reschedule, and cancel use database time; voluntary
  seal lifecycle times stay monotonic; and late focus-loss discrepancies
  invalidate review waivers.
- Server: live renewals queue at the worker cap instead of being refused.
- Server: every SQL statement is bounded by the query timeout, rejected-value
  SQL errors translate to invalid input, and the Attempt candidate is locked
  before enrollment fences.
- Server: readiness probes mandatory dependencies, serving-lease renewal
  retries within its slack, oversized cluster envelopes use the reliable
  stream, offline recovery commands no longer migrate the schema, and requested
  shutdown no longer reports closed-listener errors.
- Server: administrator MFA reset notices and retention grace notices are
  delivered instead of failing every attempt.
- Cache: counter expirations are fixed windows.
- VFS: S3 part size is bounded for unsized writes.

[Unreleased]: https://github.com/sudosylabs/proctor/commits/main
