# Transactional-mail reference

This reference owns the accepted Proctor transactional-mail design. It is a
delivery contract; code and tests expose current implementation state.

## Scope and ownership

Transactional mail communicates a bounded application or security fact to a
specific recipient. It is mandatory product communication rather than
marketing, so the initial contract has no user opt-outs, digests, reminders,
or entries in the portable `settings.json` document. A future optional
notification product requires its own preference model.

The reusable `packages/mail` module owns transport-neutral messages, MIME,
validation, transport outcome classification, SMTP, and sender conformance.
The server owns product meaning: the catalog, copy, templates, localization,
recipient resolution, authorization, encryption of recoverable payloads,
rate limits, durable intent, retries, suppression, retention, and operator
diagnostics. SMTP is the only production adapter in the initial slice.
Provider APIs, inbound mail, connection pooling, bounce or complaint webhooks,
and global address suppression are deferred.

The initial closed catalog contains these template keys:

- Identity and security: `identity.verify_email`,
  `identity.password_reset`, `identity.password_changed`,
  `identity.email_change_warning_old`,
  `identity.email_change_verify_new`,
  `identity.email_verified_by_admin`, `identity.account_disabled`,
  `identity.account_enabled`, `identity.sessions_revoked_by_admin`,
  `identity.mfa_enabled`, `identity.mfa_disabled`,
  `identity.mfa_recovery_codes_regenerated`,
  `identity.personal_access_token_created`,
  `identity.personal_access_token_enabled`,
  `identity.personal_access_token_disabled`, and
  `identity.personal_access_token_revoked`.
- Academic administration: `academic.class_enrolled`,
  `academic.class_enrollment_ended`, `academic.class_transferred`,
  `academic.academic_unit_assigned`,
  `academic.academic_unit_assignment_ended`,
  `authorization.scoped_role_assigned`,
  `authorization.scoped_role_ended`,
  `authorization.institution_role_assigned`, and
  `authorization.institution_role_ended`.
- Access and onboarding: `access.student_class_invitation`,
  `access.teacher_academic_unit_invitation`,
  `access.academic_unit_role_invitation`,
  `access.institution_role_invitation`, `access.invitation_accepted`, and
  `access.invitation_revoked`.
- Examination administration and candidate facts: `exam.manager_added`,
  `exam.manager_removed`, `exam.ownership_transferred_to_you`,
  `exam.ownership_transferred_from_you`, `exam.sitting_scheduled`,
  `exam.sitting_rescheduled`, `exam.sitting_cancelled`,
  `exam.sitting_assignment_removed`, `exam.submission_received`,
  `exam.submission_automatically_sealed`, and `exam.result_released`.
- Operations: `system.mail_test`.

New-login alerts, standalone affiliation notices, provider-directory
reconciliation mail, live Sitting lifecycle or correction mail, pre-release
integrity mail, Job-failure mail, reminders, and digests are outside the
initial catalog. Invitation purposes and their authoritative relationship
packages are owned by the
[`identity-and-access` reference](../../identity-and-access/references/access-and-onboarding.md). Live
examination changes continue to use the authenticated realtime contract rather
than email.

## Templates and localization

Authored MJML, generated tracked HTML, authored plain text, reusable partials,
and packaged inline assets live under `server/templates`. Human prose,
including subjects, headings, paragraphs, action labels, and footer copy,
lives under `server/i18n`. English is the only required initial catalog;
recipient locale falls back through the installation locale and then English.
Each locale file lives directly under that directory as a lexically sorted,
flat JSON array of `{ "id", "translation" }` records. IDs are stable dotted
meanings such as `mail.identity.verify_email.subject`; nested catalog objects
are forbidden. English is canonical and complete. Other locales may be partial,
but may contain neither unknown IDs nor placeholder signatures that differ
from English. `server/i18n` is a data directory, not a Go package. The root
composition package privately embeds its files and supplies them to the
general `server/localization` module, which validates and compiles every
catalog at startup and resolves each field with per-message fallback.
`server/app/mail` owns mail-specific rendering and assembles those fields into
one private typed presentation model before executing either alternative, so
HTML and text cannot diverge by performing independent lookups. Its renderer
accepts one closed typed request whose presentation value is limited to the
mail families owned by that package; adding a family extends that request
rather than adding a parallel rendering interface to the parent application.

One complete mail definition registry owns the occurrence meaning, delivery
Job class, default lifetime, action-link requirement, and presentation family
for every template key. Application use cases request semantic outcomes such
as email verification, password reset, account-state change, or Sitting
schedule communication. They do not select a template key, occurrence kind,
delivery Job class, or lifetime for those outcomes. Family-specific operations
which legitimately select among related meanings accept only that bounded
family choice and validate it against the same registry.

Every message key has a typed data model. Templates do not receive arbitrary
maps, construct routes, or look up application state. Each MJML and text source
starts with a non-rendering comment that names the exact properties it
receives. Generated HTML is parsed with Go `html/template`; plain text uses
`text/template`. The function allowlist is pure and bounded. Raw-HTML bypasses,
filesystem or environment access, network calls, and template-selected headers
are forbidden. User-controlled values remain contextually escaped.

MJML generation uses a repository-pinned toolchain and produces committed
HTML. CI rejects stale generated output and renders every production template
with representative typed data. A deterministic preview command renders all
templates and an index into a caller-selected directory without production
data or mail delivery. `server/templates` is likewise an asset and
build-tooling directory, not a Go package. The root privately embeds generated
HTML and authored text, then `server/app/mail` parses and validates them when
its renderer is constructed; production composition constructs that renderer
before readiness. Edits require regeneration, rebuild, and restart. The exact
source layout, property contract, generation, freshness, and preview commands
are maintained in the
[transactional-mail template workflow](../../../../server/templates/README.md).
Template-local install, generation, freshness, and test targets live in
`server/templates/Makefile`; the server Makefile provides delegating aliases.

Messages use UTF-8 multipart alternatives with authored text first and HTML
second. Only versioned Proctor-owned inline Content-ID assets are permitted.
External images, remote fonts, tracking pixels, ordinary attachments, read
receipts, and unsubscribe headers are forbidden. Generated mail includes
`Auto-Submitted: auto-generated` and `X-Auto-Response-Suppress: All`.
Presentation retains semantic reading order, descriptive links, meaningful alt
text, readable type, sufficient contrast, no color-only meaning, responsive
layout, and a complete text equivalent.

Mattermost was inspected as a behavioral reference for authored MJML,
generated tracked HTML, reusable partials, and build-time freshness checking:
repository `https://github.com/mattermost/mattermost`, revision
`8ce3c54a5ed76b2aa39a46cf8a1b517ea53ec0cc`, principally
`server/templates` under its stated Apache-2.0 exception. Proctor implements
the behavior independently and does not copy Mattermost's loader or visual
templates. Any later direct or substantial adaptation must follow the root
licensing and provenance rules.

## Durable model and atomicity

A `MailOccurrence` is one immutable logical notification caused by a domain
transition. A `MailDelivery` is one frozen recipient delivery and its current
lifecycle. A direct occurrence has one delivery; a bounded fan-out occurrence
has many. Generic Jobs execute expansion and delivery but do not replace this
mail-domain state.

Delivery states are Queued, Sending, Accepted, Failed, Suppressed, Canceled,
and their validated transitions. Accepted means that SMTP accepted the DATA
operation; Proctor never claims inbox delivery. Every delivery has a stable
Message-ID that survives automatic and operator retry.

The `server/app/mail` child module prepares a validated occurrence, encrypted
payload and required Job for every catalog family. Shared payload freezing owns
address validation, bounded serialization, sealing, digest, and stable
Message-ID construction for both direct and fan-out child deliveries. The
originating named Store mutation inserts them with the business state and audit
in one PostgreSQL transaction. A standalone mail or
Job enqueue after the business commit is forbidden. If enabled mail cannot be
prepared or persisted, the originating mutation rolls back. A missing or
ineligible recipient instead records an operator-visible terminal suppression;
one recipient never blocks the other children of a fan-out.

Logical occurrence identity derives from the originating transition, role,
and recipient rather than message content. Examples include a recovery token
identity, a security audit transition, a Sitting revision and notification
role, or a Submission identity and seal kind. PostgreSQL uniqueness prevents
duplicate children after process loss. Operator retry reuses the same delivery
and Message-ID; it does not clone or edit a frozen recipient.

One user-visible command normally creates one semantic notification even when
it has internal side effects. Account disable does not also send a session
revocation notice; password reset completion sends password-changed rather
than a revocation notice; Class transfer does not send separate ended and
enrolled messages; and automatic sealing does not claim voluntary submission.
Email change intentionally creates an old-address warning and a new-address
verification, while ownership transfer creates separate role-appropriate
messages for its two recipients.

Exam Manager addition and removal each create one direct occurrence for the
affected User. Ownership transfer creates exactly two direct occurrences: the
new Owner receives the resulting Owner relationship and the previous Owner is
told that they remain a Manager. The frozen payload contains only the safe Exam
title, resulting relationship, and action time; it excludes the actor, other
Managers, and private authorization detail.

Accepting an invitation for a new User sends one semantic
`access.invitation_accepted` message rather than separate notices for every
membership and role-binding side effect. Acceptance by an already authenticated
User does not send a redundant welcome message. A teacher invitation applies
the Academic Unit membership and invitation-package-origin role binding as one
business transition and therefore also produces only one acceptance message.

Class enrollment, explicit ending, and transfer each commit one direct notice
with the membership transition and successful audit. A transfer is one
`academic.class_transferred` occurrence, not an enrollment-ended occurrence
plus a new-enrollment occurrence. The frozen copy contains only the affected
Class display names and exact effective bounds; it excludes the actor, roster,
authorization grants, and private audit detail. The same transaction advances
the affected Classes' mail-audience revisions so bounded Sitting
reconciliation adds, updates, or removes candidate schedule projections.

Ordinary direct Academic Unit membership creation and ending commit
`academic.academic_unit_assigned` and
`academic.academic_unit_assignment_ended` respectively for the affected User.
Ordinary Role Binding creation and ending select the institution templates for
Institution scope and the scoped templates for every narrower scope. The
relationship, successful audit, occurrence, delivery, and Job commit in the
same named PostgreSQL transaction after a recipient-revision and eligibility
fence. Disabled or ineligible delivery is retained as terminal suppression;
exact replay or an idempotent no-op creates no second occurrence. Invitation
acceptance remains one semantic `access.invitation_accepted` message and does
not also emit notices for its internal membership or Role Binding package.

## Recipient and fan-out rules

Each delivery has exactly one recipient. Proctor never exposes roster
recipients through CC or BCC. Direct operations freeze User identity, address,
display name, locale, timezone, safe template data, message date, and headers
when the occurrence is created. Delivery never re-resolves the address. Email
change explicitly freezes the old warning address and the new verification
address. Its named aggregate accepts bounded lifetimes rather than caller-clock
deadlines and derives the User-token, occurrence, delivery, and Job timestamps
from one PostgreSQL clock sample inside the committing transaction.

Sitting audience is the set of active students whose effective Class
membership contains the Sitting's scheduled start. The implemented transition
aggregate records one encrypted render bundle and an ordinary, bounded
expansion Job atomically with the Sitting revision and audit. The worker pages
the authoritative roster, commits each unique recipient independently, and
destroys the bundle after completion. Bounded periodic reconciliation detects
enrollment, ending, transfer, and other audience drift for upcoming Sittings;
multiple nodes converge through the same one-active-fan-out and recipient
projection fences. Moving a Sitting between Classes sends removal to the
removed audience, update to the retained audience, and schedule to newly
eligible candidates.

The daily mail-maintenance aggregate uses one PostgreSQL clock sample and a
bounded page to terminalize expired expansion, permanently failed expansion,
or an expansion whose retained Job has disappeared. It cancels live expansion
work, destroys the shared bundle and its key reference, releases the active
fan-out fence, and suppresses child deliveries in bounded follow-up pages.
Reconciliation treats those terminal reasons as an absent desired fact, so a
current audience can converge without waiting for every old child to be swept.

Each candidate has a last-communicated Sitting projection. Unaccepted schedule
changes coalesce to the latest relevant fact. An unsent schedule followed by
cancellation becomes obsolete; a candidate who received the schedule receives
the cancellation. Direct security notices, Class transitions, Submission
receipts, and released-result notices preserve history instead of coalescing.
Personal Access Token create, enable, disable, and revoke transitions are
ordinary historical security notices. Before rendering, a named aggregate
locks the per-User PAT mutation fence and persists a bounded, Session-bound
preparation whose action time comes from PostgreSQL. The terminal aggregate
reuses that action time for PAT state and the occurrence, delivery deadline,
and Job lifecycle; it records the terminal audit with the preparation time as
creation and a fresh PostgreSQL completion time, then removes the preparation.
An authoritative replay removes only its preparation and emits no audit or
mail. Bounded non-durable periodic maintenance terminalizes abandoned
preparations as failed audits under `SKIP LOCKED`. Copy is limited to the
supplied description, exact committed expiry, action time, a localized
Institution/Academic Unit scope label, and bounded action count. The one-time
credential, stored hash, and complete action list never enter mail content or
delivery metadata.

A fan-out occurrence freezes a bounded encrypted render bundle containing the
rendered text and HTML alternatives for every supported locale, the
installation default locale, schema version, and sender. Recipient expansion
selects exact locale, language base, installation default, then English from
that immutable bundle; it never consults live localization or template assets.
Every child therefore uses one release without losing recipient-localized
copy. Version-one English-only bundles remain readable during an upgrade. The
bundle is destroyed when expansion terminates. Later roster reconciliation
creates a new occurrence using the then-current template release.

## Security and privacy

Deferred credential delivery creates a recoverable-secret requirement. A deep
server-owned secret-sealing module owns versioned AES-256-GCM envelopes, random
nonces, authenticated domain separation, safe errors, bounds, key selection,
and key identifiers. It is an in-process module, not a replaceable cryptography
port. Mail has an independent primary encryption key and fallback decryption
ring. MFA uses the same cryptographic module through its own purpose-bound
envelopes and independent key ring; Memberlist retains its protocol-specific
ring. Key material is never reused across those domains.

The complete frozen delivery payload is encrypted: full recipient name and
address, subject, text, HTML, and sensitive headers. Plain durable metadata is
limited to safe identity and routing fields such as occurrence and delivery
IDs, target User ID where applicable, template key and digest, masked address,
state, safe timestamps, deadline, Message-ID, and public failure code. A
rendered delivery is bounded to 1 MiB and a frozen fan-out bundle to 4 MiB.

Ciphertext is destroyed atomically after acceptance, suppression,
cancellation, or expiry. Retryable deliveries retain it only until their
deadline. Rotation distributes a new readable key to every worker node,
promotes it to primary, durably re-encrypts active payloads, proves no active
row uses the retiring key, and only then removes it. Key-ring configuration is
immutable for a process and coordinated node restarts are required. A node may
not run a mail worker when an active payload references an unavailable key.

The durable rekey operation installs a PostgreSQL primary-key fence before it
queues work. From that point, nodes still configured with the old primary may
read and deliver values through their fallback ring, but cannot introduce a
new old-key payload. Every transaction that creates an encrypted delivery or
frozen fan-out bundle holds a shared lock on that fence through commit; fence
promotion takes the exclusive lock. An insertion is therefore wholly before
promotion and included by rekey, or wholly after promotion and required to use
the new primary. One operator-visible Job pages delivery payloads and
frozen fan-out bundles in stable identity order, authenticates the original
domain binding, re-seals under the fenced primary, and checkpoints after each
idempotent replacement. Its Job identity fences stale work if a later rotation
starts. A node whose configured primary does not match the command relinquishes
the claim with bounded backoff and cannot reclaim that Job under the same stable
node identity; the incompatible Attempt remains visible without consuming the
failure-attempt budget. Such a node may continue ordinary delivery through its
fallback ring while a node with the fenced primary completes rekeying.

Every replacement checkpoint publishes only processed and total counts plus
the closed `reencrypting` stage. The mail-specific status projection decodes
the typed checkpoint and, after success, the typed final proof; raw Job
commands, checkpoints, result documents, ciphertext, payload identities, and
recipient data remain private. Completion uses the bounded payload-key
reference aggregate to prove
that every active value uses the primary and that the named retiring key has
zero references. Key IDs and aggregate counts are safe diagnostics; key
material, payload owners, and ciphertext are not. Removing a fallback before
that proof remains a startup failure rather than a partial worker mode.

After a rotation Job succeeds with a valid zero-reference proof, the current
fence remains authoritative while the next primary is staged. A restarted node
may use a different configured primary only when it can still read the fenced
primary and that completed proof remains durable; its new-primary writes are
rejected until the next rekey command atomically advances the fence. Nodes on
the previously required primary may continue writing until that command. A
failed, canceled, corrupt, missing, or retention-deleted proof never authorizes
promotion, and the Job carrying the current proof is retained until a later
rotation replaces it.

Ordinary mail may identify the necessary Class, Exam, Sitting, effective date,
PAT description or expiry, and safe receipt identity. It never contains exam
answers, Workspace content or paths, instructions or resources, scores,
academic outcomes, review evidence or private rationale, recovery codes,
credentials, IP addresses, session tokens, or complete authorization scopes.
Released-result mail says only that a result is available. Administrative
notices describe the actor as an administrator rather than disclosing their
identity or reason.

## Links and recovery-page dependency

The server owns canonical semantic link construction; templates receive only
an already validated optional action URL. They never concatenate identifiers
into routes.

The current verification and password-reset messages construct HTTPS URLs at
`/account/verify-email#token=...` and
`/account/reset-password#token=...`. The packaged browser runtime now owns
these exact routes and the shared fragment-removal and security foundation,
but their visual flows are not implemented. They remain deferred to the
server-hosted design-system phase, when they will submit the captured
credential through the existing JSON completion operation. This dependency
must remain visible until those flows exist; the mail phase must not claim
that the recovery journey is end-to-end complete.

Invitation mail uses the corresponding server-hosted join route
`/join#token=...`. The invitation carries a random 256-bit credential only in
the URL fragment; the nonvisual browser bootstrap removes the fragment before
rendering and passes the claim as purpose-specific in-memory state to the
future visual flow. The supporting API exchanges it for a short-lived server
transaction and HttpOnly proof. Resend rotates the credential rather than
reproducing the old link.

Production mail action URLs are absolute HTTPS URLs. The composition-owned
loopback HTTP development proof may relax only the scheme for `localhost` or a
literal loopback address so the tracked local Mailpit workflow remains usable;
the renderer rechecks the host and never accepts user information or a
non-loopback HTTP destination.
Acceptance must still recheck the Invitation's pending state, expiry, target
email, purpose package, and the current authorization of the relationship it
will establish.

There is no accepted desktop custom scheme, universal-link association, or
navigable Class, Sitting, Submission, result, or settings route grammar.
Ordinary transactional mail therefore has no action button initially and tells
the recipient to open the installed Proctor client. Protected API paths are
never email destinations. Desktop intents, signed-out replay, reauthorization,
candidate-active restrictions, and browser fallback require a separate client
navigation design.

## Delivery, retries, and disabled mode

Transport classification belongs to `packages/mail`: temporary, permanent,
or acceptance-uncertain while preserving portable error matching. SMTP 4xx
and transient network failures retry; 5xx and invalid messages fail
permanently. Failure after possible remote acceptance is uncertain and may
retry with the stable Message-ID. Exactly-once SMTP is not promised.

Before every attempt, a typed relevance check runs. Recovery mail requires an
active, unexpired token bound to the frozen target. Sitting mail requires a
still-relevant communicated projection. Obsolete deliveries are suppressed
rather than sent. Historical security facts and receipts remain relevant.

Credential delivery is bounded by token validity; Sitting mail stops when its
fact is stale or the Sitting begins; security mail uses a 24-hour deadline;
Class, Manager, Submission, and result mail use 72 hours. Retry allows at most
eight attempts with exponential jitter beginning near 30 seconds and capped
near 30 minutes, always inside the message deadline.

Invitation delivery additionally stops when the Invitation expires, is
revoked, accepted, or superseded by an explicit replacement. Resend preserves
the pending Invitation and rotates its claim while suppressing every earlier
unsent credential delivery. Expiry does not itself send mail. Revocation sends
`access.invitation_revoked` only when the earlier
invitation delivery reached Accepted; otherwise the pending delivery is merely
suppressed. The initial slice has no invitation reminders.

Credential, ordinary transactional, and fan-out expansion work use separate
bounded pools so a large roster cannot starve recovery. Initial per-node
concurrency is 4 credential deliveries, 8 ordinary deliveries, and 2 fan-out
workers; expansion pages contain 200 recipients. A shared installation limiter
allows 10 sends per second with burst 20 while reserving credential capacity.
These values are immutable process configuration.

Mail remains disabled by default so development and deliberately mail-free
installations can operate. Verification and reset request operations report
mail unavailable while disabled. Other domain mutations commit a terminal
`suppressed_disabled` occurrence. Starting with mail disabled suppresses and
cancels outstanding nonterminal delivery work, destroys its ciphertext, and
does not resurrect it after re-enablement.
For Sitting fan-out, each enrollment, ending, or transfer advances a bounded
Class audience revision and stamps the affected membership row. A disabled
aggregate records the exact Sitting revision and Class audience revision
outside retained mail history. Reconciliation treats only membership facts at
or before that watermark as converged, even after the 90-day occurrence
cleanup; a later audience mutation or schedule revision remains independently
eligible without resurrecting the earlier suppressed audience. The Sitting
also records a transactional singleton User-eligibility revision. Email
verification and account enablement or disablement advance and stamp that
chronology, while a disabled fan-out reads it under the same PostgreSQL row
lock; either commit order is therefore exact without retaining a per-Sitting
roster snapshot. Reconciliation considers a membership or User eligibility
fact newer than its corresponding watermark. It retains only the last
reconciliation actor ID: a due scan selects an active current Exam Manager,
then the aggregate locks that User before the eligibility singleton and before
Class/hierarchy state, and rechecks current authority before replacing the
provenance. The installation-wide system-administrator authentication-path
fence, where required, precedes every User row. Every affected transaction then
uses the canonical User, eligibility singleton, Class/hierarchy order:
account-state and email transitions lock the target User before advancing the
singleton; Invitation acceptance locks the inviter and target User before
verification advances it; and disabled Sitting fan-out captures it before the
lifecycle fence. Reconciliation therefore depends on neither retained mail
history nor stale authority and cannot form a cross-aggregate lock cycle.

An installation may not activate invitation-required onboarding while mail is
disabled or unhealthy. It must not reveal raw invitation links through an
operator response, audit record, log, or CSV result as a fallback. Development
uses the same durable flow with the in-memory sender or a local capture SMTP
server so the secret remains available only through the captured message.

Rendering and durable enqueue happen before returning from a request, but SMTP
does not. Verification and reset return after token, audit, occurrence,
payload, and Job commit. Password reset retains its uniform public response.
After SMTP acceptance, the worker records Accepted and destroys ciphertext
before completing its Job. A retry that observes terminal mail state completes
without another send; a crash before that database transition retains the
documented uncertain-acceptance duplicate risk.

## Operations, authorization, and retention

The mail-domain operator surface provides bounded list and detail with state,
template, and time filters; retry and cancellation; explicit rekey; and a
controlled test message. It never exposes arbitrary message creation. The test
operation is rate-limited and audited, requires a recent interactive operator,
and can target only that principal's verified address. The initial controlled
test limit is three request attempts per operator in one hour.

Institution-scoped `mail.view` protects safe list and detail. `mail.manage`
protects retry, cancel, and test mail and requires a recent interactive Session;
PATs cannot perform those mutations. `mail.rekey` requires a strong, recent
interactive Session. Generic `job.view` and `job.manage` remain independent and
show only payload-free execution state. Adding these grantable actions requires
versioned, idempotent reconciliation of the protected built-in
`system_admin` role for existing installations; custom roles remain unchanged.

Operators may cancel only Queued or retry-waiting delivery. They may retry a
Failed delivery only while its deadline and relevance contract remain valid.
Sending and terminal races return conflict; operators cannot edit recipients,
retry Accepted or Suppressed delivery, or clone an occurrence. Human retry,
cancellation, suppression override, rekey, and privileged verification
override are durably audited. Automated attempts remain in mail and Job
history rather than flooding security audit.

Starting rekey is a critical attempt-to-terminal audit transition. The attempt
commits before the named mail mutation; its successful completion commits in
the same PostgreSQL transaction as the Job and primary-key fence. Active
operation or unproven-promotion conflicts and unexpected persistence failures
complete that attempt as failure through the shared audit service. If failure
completion cannot be persisted, the operation fails closed.

`POST /api/v1/mail/rekey` accepts only the retiring key ID and returns the safe
Job identity, primary and retiring key IDs, and creation time. It never accepts
or changes deployment key material. Operators inspect progress and the final
zero-reference proof through the existing Job operations; configuration
promotion and fallback removal remain explicit deployment actions.

Safe mail projections contain masked recipient, target User ID where present,
template key, state, safe timestamps, attempts, deadline, Message-ID, and a
closed public failure code. They omit full addresses, copy, rendered bodies,
template data, credentials, SMTP configuration, and raw provider responses.
Succeeded, suppressed, and canceled metadata is retained for 90 days; failed
metadata for 180 days. A durable bounded cleanup Job enforces those cutoffs and
catches abandoned ciphertext without changing independent security-audit
retention. Sitting cleanup clears only the exact retired desired-delivery
reference, preserves the last-communicated projection, and retires the
completed fan-out before its now-orphaned occurrence.

Unsafe template, encryption, or configuration state prevents server startup.
Every mail-enabled node validates the bounded key-reference aggregate for
active encrypted deliveries against its configured decryption ring before
constructing workers. A disabled node may start without the ring because its
workers suppress ciphertext before any decrypt attempt.
SMTP outage and queue delay degrade the mail subsystem without making general
HTTP readiness fail. Metrics expose bounded counts, age, latency, attempts,
acceptance, suppression, expiry, and public failure class by template key.
The authorized `mail.view` metrics projection is the operator-facing read path;
its dimensions contain no delivery IDs or recipient/content data.
Logs contain only safe IDs and closed codes, never recipients, bodies, template
data, ciphertext, or SMTP dialogue.

## Delivery order and verification

Implementation proceeds as narrow vertical slices: reusable transport outcome
classification, secret sealing, and the template toolchain; one complete
operator test-mail tracer; operator controls, rekey, and lifecycle hardening;
identity and security notifications; Exam administration; Sitting fan-out and
Class reconciliation; Submission receipts and result release; and final
application-through-SMTP certification.

Each slice updates its owning contract and capability status. Verification
includes secret-sealing and rotation tests, Store conformance, safe rendering
and escaping, generation freshness, locale fallback, atomic business-plus-mail
commit, occurrence idempotency, crash and fencing behavior, transport outcome
classification, fan-out races, authorization and assurance, safe projections,
retention, disabled-mail suppression, and an application-through-SMTP Mailpit
test. The server architecture gate and affected independent module checks are
required before the phase is declared implemented.
