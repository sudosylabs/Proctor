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

- Server: every collection answers `{"items": [...], "next_cursor": "..."}`
  and pages with `limit` (default 50, at most 200) and `cursor`. Academic
  Units, children, Programmes, Programme Levels, Academic Periods, Classes,
  Users, Roles, Role Bindings, Affiliations, Sessions, personal access tokens,
  Desktop Registrations, authentication providers, Browser sources, and Draft
  Resources and Starter Workspace entries no longer answer bare arrays or
  unbounded lists; the User directory drops `after_username`/`after_id`;
  Academic Unit and Class Members drop the `Link` header and always page;
  audit listing renames `events` to `items`; Browser receipts replace
  `first_sequence`/`next_sequence` with `cursor`/`next_cursor`; and the
  Workspace change journal replaces `after_cursor`, `entries` and `has_more`
  with `workspace_cursor`, `cursor`, `items` and `next_cursor`. Programme
  Level Classes are ordered by name (**breaking**).
- Server: optional instants are omitted instead of `null` everywhere. The
  delivery closure (`closed_at`, `upload_expires_at` and its other optional
  members), unretained-delivery summaries (`first_unretained_at`,
  `last_unretained_at`), and Browser integrity groups
  (`overflow_first_received_at`, `overflow_last_received_at`) leave absent
  members out; requests reject `null` for Desktop Compatibility Policy
  `retry_at` (omit it for none) and Academic Period `start_at`/`end_at`. The
  HTTP suspension object's `flag_id` becomes `integrity_flag_id`, matching
  the realtime event (**breaking**).
- Server: the remaining `cancelled` spellings become `canceled`. The Desktop
  authorization transaction `state` value and `cancelled_at` column, the
  Sitting mail `change_kind` value, the `exam.sitting_cancelled` mail template
  key, and its localization IDs are renamed in the pre-release SQL baseline;
  development databases must be reset (**breaking**).
- Server: every revision fence is the body member `expected_revision`.
  `expected_draft_revision`, `expected_exam_revision`,
  `expected_attempt_revision`, `expected_declaration_revision`,
  `expected_sitting_revision`, and the single-fence uses of
  `expected_review_revision` and `expected_policy_revision` become
  `expected_revision`; the Integrity decision's `expected_decision_revision`
  becomes `expected_revision` beside `expected_review_revision`. Academic
  archive `DELETE` takes `expected_revision` in a JSON body instead of the
  query string (**breaking**).
- Server: pagination cursors are unversioned. Every cursor rejects a
  `version` member, the audit, Job, Job attempt, mail delivery, and Exam
  catalog cursors no longer accept versionless or version-0 tokens, and every
  cursor issued by an earlier build is invalid (**breaking**).
- Server: internal persisted documents carry no version field. Job
  commands, checkpoints, and results lose `command_version`,
  `checkpoint_version`, and `result_version`; the pre-release SQL baseline
  drops those columns and development databases must be reset. Frozen mail
  payloads and Sitting fan-out bundles lose their `version` member and the
  version-one bundle and relative-logo readers. Command outcomes drop
  `fingerprint_version` and `outcome_version`, and the request fingerprint
  no longer mixes in a version tag, so earlier retained outcomes cannot
  replay.
- Server: every retained command outcome is a dedicated persisted document
  with explicit member names, decoded strictly. Outcomes that stored model
  values under Go field names (Exam Sittings, Exams and Exam Managers, Review
  and Records results, Retention Previews and Controls, live corrections,
  Starter Workspace, Exam Resources, Academic Units and Periods, security
  preflights, delivery summaries, and the policy replacements) change format,
  as does the prepared security preflight document. Earlier retained outcomes
  no longer replay; reset development databases (**breaking**).
- Server: Integrity Discrepancy responses drop the constant `schema_version`
  member, and Examination Export archives drop `schema_version` from
  `records.json` and `manifest.json`. Browser Policies no longer carry an
  internal schema version, so a zero policy is the disabled policy
  (**breaking**).
- Server: command operation names and digest, HMAC, and nonce
  domain-separation tags lose their version suffix and use one dot-separated
  snake_case spelling (`exam.attempt.reallow.v1` becomes
  `exam.attempt.reallow`, `proctor/invitation-claim/v1` becomes
  `proctor.invitation_claim`). Retained command outcomes, issued CSRF tokens,
  in-flight OIDC logins, and pinned review digests from earlier builds no
  longer match (**breaking**).
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
- Server: the administrative Session list (`GET /api/v1/users/{user_id}/sessions`)
  returns one page in an `items`/`next_cursor` envelope, newest first, and
  accepts `limit` and `cursor` (**breaking**).
- Server: the pre-release SQL baseline gained Session retention indexes;
  development databases must be reset (**breaking**).
- Build: `make container` packages Linux binaries with the same recipe as
  `make dist` and builds them with the release Dockerfile, so a local image
  matches a published one; `Dockerfile.runtime` is removed.
- Build: `make module-pins-check`, run in CI, fails when `server/go.mod` pins a
  `packages/*` module at a commit whose content differs from the workspace
  module.
- Build: the root `make check` verifies transactional-mail template freshness,
  and `make -C server integration-postgres` runs every PostgreSQL integration
  test in its packages instead of a fixed list.

### Added

- Server: Exam Managers can manually suspend an active Attempt.
- Server: failed password checks against one account, counted across all
  sources and both its username and email address, delay further logins for
  that account progressively
  (`Authentication.LoginRateLimit.AccountFailuresBeforeDelay` and
  `MaximumAccountFailureDelay`). The delay never refuses a correct password,
  so other people's failures cannot lock an account out.
- Server: Exam Managers can manually suspend an active Attempt before the
  Sitting's scheduled end. A kick after the candidate's lease lapsed records
  that Connection Loss instead and fails with `exam.attempt.connection_lost`.
- Server: local login attempts are also bounded per account across all
  sources (`Authentication.LoginRateLimit.MaximumIdentityAttempts`).
- Server: revoked and expired Sessions and superseded Session credentials are
  deleted after `Authentication.Sessions.RetiredRetention` (default 30 days)
  by an hourly Job.
- Server: startup refuses to serve when a stored MFA secret is sealed with a
  key missing from `Authentication.MFA`, instead of stranding those
  credentials.
- Server: `Server.TrustedProxies` names the reverse proxies whose forwarded
  client address and request ID are accepted.
- VFS: `FileSystem.CheckRoot` checks that the backend root (the local root
  directory or the S3 bucket) is reachable without listing it. Every
  `FileSystem` implementation must add it (**breaking**).

### Fixed

- Server: concurrent first TLS handshakes for the automatic-certificate
  hostname no longer crash the process (golang/go#80119); the embedded ACME
  client is rebased on `golang.org/x/crypto` v0.55.0.
- Server: a Let's Encrypt renewal attempt that times out is retried instead of
  ending renewal for the life of the process, and each failed attempt is
  logged.
- Server: profile-picture and Exam Resource JPEGs with more than 32 scans are
  rejected before decoding, so a small progressive file can no longer hold a
  content permit for minutes.
- Server: every SQL statement reports a unique, foreign-key, or rejected-value
  violation as a Store conflict, reference, or invalid-input error, including
  writes such as browser and native delivery appends that did not translate
  them, which previously surfaced as unavailable.
- Server: Job history cleanup no longer creates a successor every few seconds
  after an incomplete batch, and skips Jobs that onboarding imports or mail
  deliveries still reference instead of failing on them.
- Server: a mail delivery left Queued or Sending after its Job failed can be
  retried by an operator, and the last Job attempt fails its delivery instead
  of requeuing it with no Job left.
- Server: a Job whose lease expired is requeued behind a backoff, and a Job
  canceled while its handler failed retryably completes as canceled instead of
  staying running until its lease expires.
- Server: one failed Job heartbeat no longer abandons the attempt while the
  lease has slack, and a handler that succeeds as its timeout elapses is not
  re-run.
- Server: a database constraint rejection during Sitting sealing or lifecycle
  work is retried instead of failing that Sitting's Job permanently.
- Server: adding an Exam Manager or granting a Role Binding locks the User
  before the Attempt admission fence, so it no longer deadlocks with an
  Invitation acceptance for the same User.
- Server: retiring a Submission finds the retry outcomes that cite it through
  indexes, so retirement no longer scans every audit event per Submission.
- Server: a retention policy preview counts audit expiry in one set-based pass
  instead of probing every side table per audit event, so previews finish
  within the query timeout at any audit volume.
- Server: cluster messages too large for one UDP packet are queued per peer
  instead of sent inline, so a slow or partitioned peer no longer blocks the
  publishing request for up to 10 seconds per peer. A full peer queue drops
  its oldest message and counts it as `dropped`.
- Server: a `SIGHUP` during startup or migration no longer terminates
  `proctor serve`; the reload is held until the node exists.
- Server: configuration reload is all-or-nothing. When logging or the
  external-provider registry cannot apply a change, the other is restored, the
  running configuration stays in force, and the reload reports failure instead
  of "configuration reloaded". Changed settings that need a restart keep their
  running values and are named in a warning instead of changing silently.
- Server: `proctor migrate status` only reads; it no longer creates the
  migration tables or takes the exclusive migration lock.
- Release image: packaged binaries and webapp are owned by root and read-only
  to the runtime user, and the image declares a `HEALTHCHECK` that runs
  `proctor-healthcheck` against readiness.
- Server: the serving lease keeps renewing during graceful shutdown until HTTP
  has stopped, and a lease failure during the drain force-stops HTTP, so a node
  can no longer keep serving after its fence may have expired.
- Server: re-allowing a focus-loss suspension no longer reports a committed
  re-allow as unavailable.
- Server: a client that disconnects mid-mutation no longer leaves its audit
  attempt open, and an hourly Job fails attempts still open after 24 hours
  with `audit.attempt_abandoned` so retention can expire them.
- Server: a failed audit completion is joined with, not substituted for, the
  original failure.
- Server: rows written with a node's clock are never later stamped earlier by
  PostgreSQL time (or the reverse), which removes intermittent check-constraint
  failures and unreadable Sessions when node and database clocks differ.
- Server: a refused cross-account Desktop approval is audited, and a Desktop
  Session withdrawn after its audited exchange gets its own withdrawal record.
- Server: audit list cursors keep microsecond precision, so rows created in
  the same millisecond are no longer skipped between pages.
- Server: Desktop approval audit binds to the approving account, local password
  login outcomes are durably audited, and result-release audit begins before
  release time is reserved.
- Server: manager Attempt operations authorize before reading Attempts.
- Server: password reset looks up the token before hashing the new password.
- Server: forwarded client identity is trusted only from configured proxies,
  HSTS is sent when the server terminates TLS, and transport refusals answer
  with Problem Details.
- Server: Workspace file content is served with `Content-Security-Policy:
  sandbox`, and HTML, XML, and SVG media types as plain text, so authored
  content cannot render as an application page.
- Server: the inbound WebSocket frame budget is shared by every connection of
  one Session on a node, so reconnecting or opening more connections does not
  grant a fresh burst.
- Server: every inbound WebSocket frame is rate-limited, and HTTP and WebSocket
  share origin normalization.
- Server: candidate answers are no longer spooled to the temporary directory,
  and transfer deadlines are sized from route declarations and, for uploads,
  from the declared request length.
- Server: manager re-allow, reschedule, and cancel use database time; voluntary
  seal lifecycle times stay monotonic; and late focus-loss discrepancies
  invalidate review waivers.
- Server: live renewals queue at the worker cap instead of being refused; the
  live lane validates requests before admission, limits each User to twice the
  renewal cadence, and keeps per-User limits when its identity table is full
  by evicting the least recently admitted User.
- Server: every SQL statement is bounded by the query timeout, rejected-value
  SQL errors translate to invalid input, and the Attempt candidate is locked
  before enrollment fences.
- Server: readiness probes mandatory dependencies, serving-lease renewal
  retries within its slack, oversized cluster envelopes use the reliable
  stream, offline recovery commands no longer migrate the schema, and requested
  shutdown no longer reports closed-listener errors.
- Server: administrator MFA reset notices and retention grace notices are
  delivered instead of failing every attempt.
- Server: profile names containing control characters (including CR, LF and
  tab) are rejected as invalid input on profile update, external identity
  provisioning, onboarding import, and invitation suggestions, instead of later
  blocking every mail-preparing mutation for that User; the pre-release SQL
  baseline enforces the same rule (**breaking** for development databases). A
  Sitting mail fan-out skips an unaddressable recipient instead of stopping.
- Server: an SMTP relay certificate, STARTTLS, or authentication failure
  retries and reports `mail.smtp_outage` instead of permanently failing every
  in-flight message.
- Server: Personal Access Token and retention notice mail honor the mail
  primary-key fence, so a rolling key rotation cannot strand an old-key payload.
- Server: disabled-mail and expiry suppression update a Retention Notice's
  delivery state instead of leaving it queued.
- Server: transactional mail keeps its Outlook conditional comments, so the
  layout renders in desktop Outlook.
- Server: Browser source refusals keep their meaning, so a spent source budget
  reports `exam.browser.source_budget_exhausted` instead of the generic Attempt
  conflict; `errors-check` rejects Store tables that map constraints the Store
  never produces.
- Cache: counter expirations are fixed windows.
- Server: `server/go.mod` pins the current cache, mail, and VFS modules, so
  `GOWORK=off` builds include the fixed-window counters and the other module
  fixes instead of August snapshots.
- VFS: a local listing stops once its page is full instead of walking and
  sorting the whole tree under the lock, and the server readiness probe checks
  the VFS root instead of listing, so a large local tree no longer times out
  readiness or stalls writes.
- VFS: S3 part size is bounded for unsized writes.
- VFS: delimited S3 listings return common prefixes in key order among
  objects, so a page and its cursor no longer skip prefixes that sort before
  the page's last object.
- Mail: relay TLS, STARTTLS, and authentication failures classify as
  temporary rather than permanent.
- Server: file purge shares each batch between the FileRevision, Starter
  Workspace, and Attempt Workspace phases and continues through successor
  Jobs while work remains, instead of 50 objects a day in which a FileRevision
  backlog starved workspace cleanup.
- VFS: the local backend fsyncs parent directories after publishing, moving,
  or removing a file and after creating directories, so a completed operation
  survives a crash.

[Unreleased]: https://github.com/sudosylabs/proctor/commits/main
