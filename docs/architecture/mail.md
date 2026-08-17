# Transactional mail

This document owns the accepted Proctor transactional-mail design. It is a
delivery contract, not an implementation claim; current capability status
lives in [Project status](../project/status.md).

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
packages are owned by [Access and onboarding](access-and-onboarding.md). Live
examination changes continue to use the authenticated realtime contract rather
than email.

## Templates and localization

Authored MJML, generated tracked HTML, authored plain text, reusable partials,
and packaged inline assets live under `server/templates`. Human prose,
including subjects, headings, paragraphs, action labels, and footer copy,
lives under `server/i18n`. English is the only required initial catalog;
recipient locale falls back through the installation locale and then English.
The localization module resolves a complete typed copy model before rendering,
so HTML and text cannot diverge by looking up translations independently.

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
data or mail delivery. Templates are embedded, parsed, and validated when the
mail renderer is constructed; production composition must construct that
renderer before readiness once delivery is wired. Edits require regeneration,
rebuild, and restart. The exact source layout, property contract, generation,
freshness, and preview commands are maintained in the
[transactional-mail template workflow](../../server/templates/README.md).

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

The application mail module prepares a validated occurrence, encrypted payload
and required Job. The originating named Store mutation inserts them with the
business state and audit in one PostgreSQL transaction. A standalone mail or
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

Accepting an invitation for a new User sends one semantic
`access.invitation_accepted` message rather than separate notices for every
membership and role-binding side effect. Acceptance by an already authenticated
User does not send a redundant welcome message. A teacher invitation applies
the Academic Unit membership and invitation-package-origin role binding as one
business transition and therefore also produces only one acceptance message.

## Recipient and fan-out rules

Each delivery has exactly one recipient. Proctor never exposes roster
recipients through CC or BCC. Direct operations freeze User identity, address,
display name, locale, timezone, safe template data, message date, and headers
when the occurrence is created. Delivery never re-resolves the address. Email
change explicitly freezes the old warning address and the new verification
address.

Sitting audience is the set of active students whose effective Class
membership contains the Sitting's scheduled start. A bounded expansion Job
pages the authoritative roster and creates per-recipient deliveries. Class
enrollment, ending, and transfer reconcile upcoming Sittings. Moving a Sitting
between Classes sends removal to the removed audience, update to the retained
audience, and schedule to newly eligible candidates.

Each candidate has a last-communicated Sitting projection. Unaccepted schedule
changes coalesce to the latest relevant fact. An unsent schedule followed by
cancellation becomes obsolete; a candidate who received the schedule receives
the cancellation. Direct security notices, Class transitions, Submission
receipts, and released-result notices preserve history instead of coalescing.

A fan-out occurrence freezes a bounded encrypted render bundle containing its
template sources, localized copy, schema version, and digest. Every child uses
that bundle so one fan-out cannot mix releases during a deployment. The bundle
is destroyed when expansion terminates. Later roster reconciliation creates a
new occurrence using the then-current template release.

## Security and privacy

Deferred credential delivery creates a recoverable-secret requirement. A deep
server-owned secret-sealing module owns versioned AES-256-GCM envelopes, random
nonces, authenticated domain separation, safe errors, bounds, key selection,
and key identifiers. It is an in-process module, not a replaceable cryptography
port. Mail has an independent primary encryption key and fallback decryption
ring; MFA and Memberlist keys are never reused. MFA migration to the common
module is separate work.

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
`/account/reset-password#token=...`. The corresponding hosted pages are not
implemented. They are deliberately deferred to the server-hosted page and
design-system phase, when they will read and immediately remove the fragment,
submit the credential through the existing JSON completion operation, and
apply strict no-store, no-referrer, and Content Security Policy safeguards.
This dependency must remain visible until those pages exist; the mail phase
must not claim that the recovery journey is end-to-end complete.

Invitation mail uses the corresponding server-hosted join route
`/join#token=...`. The invitation carries a random 256-bit credential only in
the URL fragment; the page must remove the fragment before submitting it to the
server. Resend rotates the credential rather than reproducing the old link.
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
revoked, is accepted, or is superseded by a resend. Expiry does not itself send
mail. Revocation sends `access.invitation_revoked` only when the earlier
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

Safe mail projections contain masked recipient, target User ID where present,
template key, state, safe timestamps, attempts, deadline, Message-ID, and a
closed public failure code. They omit full addresses, copy, rendered bodies,
template data, credentials, SMTP configuration, and raw provider responses.
Succeeded, suppressed, and canceled metadata is retained for 90 days; failed
metadata for 180 days. A durable bounded cleanup Job enforces those cutoffs and
catches abandoned ciphertext without changing independent security-audit
retention.

Unsafe template, encryption, or configuration state prevents server startup.
SMTP outage and queue delay degrade the mail subsystem without making general
HTTP readiness fail. Metrics expose bounded counts, age, latency, attempts,
acceptance, suppression, expiry, and public failure class by template key.
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
