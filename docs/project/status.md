# Project status

This document records capability-level implementation state and unresolved
decisions. It is deliberately not an endpoint, store, or test inventory; use
the code and component contracts for that detail.

## Implemented foundation

- Four independent Go modules are connected by the root workspace: reusable
  cache, mail, and VFS modules plus the Proctor server.
- The module-root `server.New` follows one inert ordered composition recipe:
  explicit acquisition transfers infrastructure atomically to lifecycle-only
  `platform.Service`, a discarded non-owning projection wires `app.App`, HTTP,
  WebSocket and Jobs, and `Server` alone owns milestone-based startup,
  readiness, listener handoff, bounded drain and reverse shutdown.
- Typed deployment configuration, bounded asynchronous structured logging with
  rotated targets and failure/drop diagnostics, health/readiness, graceful
  shutdown, and the shared `testlib` graph are operational.
- The execenv v0.2.0 server foundation is operational behind `app/execution`:
  a bounded static multi-host catalog supports TLS 1.3/mTLS or loopback-only
  development authentication, fail-closed readiness, reconnecting outbound
  clients, deterministic image/network/capacity placement, and durable
  PostgreSQL assignment/reassignment/release history. PostgreSQL and VFS remain
  authoritative for the Attempt Workspace; full verified snapshots and
  acknowledged IDE changes converge the ephemeral host tree, submission
  releases commit before host revocation, and bounded reconciliation repairs
  missed open, freeze, thaw, release, and revocation effects from authoritative
  Attempt and Sitting state. Applied Sitting state/revision, exact-grant host
  operations, and a connection-owned PostgreSQL advisory lease fence cross-node
  lifecycle races; a durable pre-effect pending marker makes process-loss
  uncertainty visible to reconciliation. Draft/Revision
  Execution Profiles, the authorized
  Attempt WebSocket terminal, guest-write acknowledgement, pause/resume/lease
  hooks, and periodic cleanup scheduling are implemented through the API
  boundary; the UI remains a separate product slice.
- General server localization keeps flat `{id, translation}` data directly
  under the data-only `server/i18n` directory; the root embeds it and the
  `server/localization` module provides installation/English fallback, strict
  catalog and placeholder validation, and HTTP Problem Details localization
  without changing stable machine codes.
- PostgreSQL schema management, the root/per-model store architecture, SQL
  conformance suites, and constrained timing, retry, and local-cache layers
  are implemented.
- The versioned HTTP API uses a sealed immutable routing catalog with narrow
  resource capabilities, explicit authentication classifications, transport
  DTOs, fail-closed Problem Details, OpenAPI agreement, request limits, and
  cursor pagination.
- Structural academic administration covers institution, academic units,
  programmes, programme levels, academic periods, classes, affiliations,
  organizational membership, and effective-dated student enrollment. Academic
  Periods have immutable Institution or Academic Unit ownership, owner-scoped
  canonical names and authorization, and Classes retain one exact Period that
  must apply to their Programme lineage.
- Identity includes local passwords, sessions and refresh rotation, account
  recovery, personal access tokens, TOTP MFA and recovery codes, administrative
  session management, direct CAS 3, and generic OIDC. Its application facade
  delegates to validated unexported focused services that receive exact Store
  contracts; it retains no persistence locator or mutable sibling callbacks.
- Authorization uses current scoped role bindings with institution and
  academic-unit inheritance, exact class scope, durable fail-closed decision
  auditing, protected built-in administration, and persistence-constrained
  scoped User and Role Binding visibility plus academic-only Audit Event
  history through one authoritative Access Control boundary. Scoped directory
  and audit projections omit account-security and request-security metadata.
- Realtime behavior includes authenticated WebSockets, authorized
  subscriptions, bounded local replay, explicit resynchronization, local and
  Memberlist cluster transports, continuous PostgreSQL-lease rediscovery,
  compiled protocol admission, rotatable gossip keyrings, and best-effort
  cross-node fan-out.
- The first server-owned file-management slice supports authorized custom
  profile-picture upload and retrieval, immutable file revisions and
  renditions, bounded upload leases, PostgreSQL-owned metadata, private
  ID-derived VFS keys, normalized 128/256/512 WebP representations,
  revision-preserving replacement, ETag-conditional removal, atomic mutation
  audit, archived custom-file retention eligibility, and bounded post-commit
  change events. Missing default pictures render deterministically without
  profile data and are persisted asynchronously as the same complete rendition
  set without changing the visible-picture timestamp.
- Durable Jobs run through one application Job engine with immutable type and
  recurrence registration, versioned typed contracts, append-preserving
  Attempt history, database-clock PostgreSQL claims, token-fenced heartbeats
  and completion, expired-lease recovery, bounded root-owned lifecycle, and an
  idempotent default-profile-picture generator. Every user-creation transaction
  now records that generation intent atomically. Daily occurrence-keyed work
  reconciles missed defaults, purges metadata-selected expired file content,
  and applies bounded per-type Job-history retention without leader election.
  Institution operators can inspect safe Job and Attempt projections, request
  cooperative cancellation, and explicitly retry descriptor-approved failures
  through audited, cursor-paginated HTTP operations. Clustered recovery tests
  cover concurrent publication and scheduling, parallel claims, worker loss
  around an idempotent commit, bounded shutdown, stale fencing, and retained
  Attempt history. Local and S3-compatible VFS gates exercise the same complete
  rendition and referenced-content cleanup boundaries.
- Client-command idempotency is implemented as an explicit route capability.
  Academic Period and root Academic Unit creation accept optional bounded
  keys; PostgreSQL atomically records user-and-operation-scoped semantic
  fingerprints, successful application outcomes, and audit identity. Matching
  retries reauthorize and audit without repeating mutations or transient
  publication, conflicting reuse fails closed, safe unknown commits may be
  retried internally, and a permanently deduplicated daily Job removes bounded
  pages after the 24-hour minimum replay window.
- Examination Core authoring now creates one Academic-Unit-owned Exam, its
  single mutable Draft, creator Owner/Manager relationship, and strict typed
  Proctor-shipped policy defaults in one audited idempotent PostgreSQL
  operation. The parent application and explicit HTTP routes expose bounded
  create/get views, authorize ordinary Academic Unit members and Exam Managers
  separately from explicit administrator overrides, and never hydrate an
  unbounded manager set. Authorized managers can edit the Draft title and
  authored Markdown through presence-aware, revision-fenced, audited,
  idempotent updates. They can also replace only the typed Focus Loss policy
  through the same guarded mutation path while Connection Loss remains fixed;
  unchanged updates do not mutate or publish. A visibility-aware, keyset-
  paginated catalog lists bounded active or archived Exam summaries without
  hydrating authored content. Ordinary discovery requires current exact
  Academic Unit membership, current Manager relationship, and applicable role
  scope; explicit override stays separate and audited. Revision-fenced,
  idempotent archive records an immutable archive time without deleting state;
  archived Exams remain available to authorized exact reads and reject new
  authoring mutations. Authorized Managers can list bounded relationship
  provenance, add and remove eligible Managers, and transfer ownership through
  revision-fenced idempotent commands; target eligibility and the protected
  owner-manager invariant are rechecked atomically. Complete validated Drafts
  can be published through one audited idempotent operation as immutable,
  monotonically numbered Revisions that become the future default and rebase
  the Draft. Bounded Revision Get/list HTTP projections expose publication
  metadata and digests without exposing authored instructions, raw policy,
  resource details, Starter Workspace paths, opaque object identities, or
  source bytes. Authorized Managers can now schedule a sealed same-Exam
  Revision for one eligible Class, retrieve and list bounded Sitting
  projections, change the complete pre-open selection, and cancel a Scheduled
  Sitting through audited, revision-fenced, idempotent commands. PostgreSQL
  atomically rechecks the current Manager relationship, exact Academic
  Unit/Class lineage, Academic Period containment, and a strictly future start
  at its own decision time; application authorization separately requires the
  current scoped permission, and administrator overrides remain distinct and
  audited. Durable deduplicated lifecycle Jobs now open due Sittings, reject
  structurally invalid openings, preserve the original end when opening late,
  and enter Closing at the PostgreSQL-enforced deadline. Authorized managers
  can pause, resume, extend only to a later contained end, or close early
  through strict idempotent HTTP commands with private reasons. Deadline races
  converge on the scheduled-end cause, archive still permits pause and early
  close to reduce capability, and committed non-replay transitions publish
  bounded lifecycle events. Version 1 uses `ScheduledEndAt` as the sole
  delivery deadline and does not extend it for paused time. Closing now queues
  durable bounded Attempt sealing; zero-Attempt Sittings close through the same
  authoritative completion check.

## Architecture migration acceptance

The required architecture migration was accepted on 2026-08-08. At acceptance,
the dependency-debt ledger was empty, the module-root composition and inward
dependency graph were enforced, OpenAPI agreed with runtime routes and errors,
and the hermetic server gate plus independent module checks passed. The
remaining work below is product development or optional tightening, not an
uncompleted architecture migration.

## Accepted Examination Core design

The Examination Core domain and architecture are decided. Its Exam creation,
retrieval, catalog, archive, manager administration, Draft text and Focus Loss
authoring, protected Exam Resources, and logical Starter Workspace slices are
implemented. An Exam belongs to one Academic Unit and has one mutable Draft
and immutable published Revisions. Each Sitting selects one Revision and
exactly one Class in that Academic Unit. Urgent
instructions or resource correction creates another immutable Revision and
atomically retargets only the affected open or paused Sitting; it does not
introduce a Sitting Amendment entity or force candidates to rejoin.

Every candidate Attempt has a protected IDE Workspace. Attempts are created on
first eligible connection, use sequential fenced Participation generations,
and produce at most one immutable Submission. Manual kick and policy suspension
share blocking behavior but retain distinct provenance and may be reversed by
an authorized manager without erasing evidence. Integrity policy is a typed,
frozen Exam Policy Set copied from Proctor-shipped defaults rather than
institution configuration. Confirmed Participation lease expiry always creates
neutral Connection Loss evidence, flags, and suspends; Focus Loss is the first
teacher-configurable typed policy. Server-owned evaluation turns bounded client
claims and server observations into evidence, enforcement, and a final
integrity review. Grading, scores, rubrics, pass/fail decisions, and academic
outcomes are outside the initial product boundary.

The Attempt-admission vertical slice is implemented. It provides atomic logical
copy-on-write Workspace bootstrap, hash-only Session-bound Participation and
Connection selectors, current-membership admission and reconnect, correction-
aware sanitized candidate presentation, bounded manager and Workspace
projections, protected inline content, WebSocket attachment and teardown, and
PostgreSQL conformance across direct and decorated Store layers.

Participation continuity enforcement is implemented. Explicit authenticated
WebSocket renewals use PostgreSQL-time 20-second leases and monotonic sequence
fences; a bounded installation-wide two-second runtime scan converges with late
renewals on one atomic expiry transition. Confirmed expiry closes any still-open
Connection, retains neutral Connection Loss evidence and a flag, suspends the
Attempt, and emits safe manager and candidate effects. Authorized managers can
re-allow the exact suspension without erasing evidence; fresh admission then
creates the next Participation generation. The external privileged coordinator
is responsible for sending renewals at the advertised five-second cadence;
transport ping and server timers cannot renew a Participation.

Focus Loss evaluation is implemented. Candidate presentation projects only
whether collection is enabled, and the authenticated Attempt WebSocket accepts
strict generation-scoped monotonic claims without client-selected severity or
outcome. PostgreSQL receipt time drives bounded rolling-window evaluation,
exact duplicate replay, gap uncertainty, one open Flag per generation,
100-episode evidence retention plus bounded overflow, and atomic flag, warning,
or suspension enforcement. Disabled policy records only a bounded diagnostic.
Committed non-replay outcomes publish separated safe manager and candidate
facts; suspension closes the durable Connection and removes the live binding,
while re-allow preserves evidence and resets only the causal window.

Acknowledged Attempt Workspace operation is implemented. Active candidates can
list and read a cursor-pinned manifest, recover through its bounded ordered
journal, and create, replace, move, rename, or delete stable logical entries.
Every write rechecks the Open Sitting, current Class membership, active fenced
Participation, credential, Connection, and Session; Attempt-scoped idempotency
survives reconnect and re-allow. PostgreSQL owns the hierarchy, cursors, quotas,
and object reachability while VFS stores path-independent opaque bytes. Durable
cleanup retains referenced or replay-recoverable objects and reclaims only safe
staged or superseded objects. The HTTP, targeted realtime, local/S3 VFS,
multi-node PostgreSQL, race, and full server gates are verified.

Immutable Submission sealing and protected inspection are implemented. An
active candidate can seal exactly the acknowledged Workspace Cursor once; the
same PostgreSQL transaction snapshots the authoritative manifest, settles
Focus Loss sequence uncertainty, marks the Attempt Submitted, ends its active
Participation and Connection, completes audit, and retains the idempotent
receipt. The manifest pins immutable entry and object metadata without copying
VFS bytes, and Submission references fence Attempt-object reclamation.
Voluntary submission rechecks current exact-Class membership and denies paused,
suspended, disconnected, Closing, or terminal Attempts. Candidates receive
only a safe receipt and lose Workspace access; currently authorized Exam
Managers can inspect the immutable header, bounded manifest, and protected
content through purpose-specific Submission authorization. Unknown-commit,
multi-node, manifest-integrity, retained-content, HTTP/OpenAPI, race, and full
PostgreSQL gates are verified.

Resumable Sitting sealing is implemented. Entering Closing immediately fences
candidate mutation and queues durable work that pages unfinished Attempts by
stable identity, seals each Active or Suspended Attempt in its own actorless-
audited transaction, checkpoints after every unit, and continues large rosters
under a 1,000-unit occurrence cap. Natural one-Submission convergence, durable
reservation recovery, permanent successor work, and the daily lifecycle scan
recover process loss and terminal Job failure without an unbounded transaction.
Already submitted Attempts are skipped; automatic sealing retains acknowledged
Workspace/VFS references, preserves prior suspension or disconnection causes,
and records unresolved integrity as Gapped. A Sitting closes only after every
created Attempt is Submitted with a sealed Submission. The manager no-show
view separately derives membership active at `OpenedAt` and never fabricates an
Attempt or Submission. PostgreSQL multi-node, mixed lifecycle, crash-recovery,
Job-history, HTTP/OpenAPI, realtime, race, and bounded-index gates are verified.

Integrity Review and explicit student-result release are implemented. Current
Exam Managers with Submission review/release authority can page bounded safe
Flag summaries, purpose-specific evidence, and explicit post-collection
discrepancies; create or revision-fence one non-academic decision per Flag;
and maintain private notes plus optional student-facing Markdown. One audited,
idempotent finalization freezes the complete bounded evidence, decision, and
discrepancy inventory and digest only after terminal collection and complete
decisions. A separate audited, revision-fenced release exposes only sanitized
approved remarks and safe identities to the candidate. Pre-release reads are
concealed; private rationale, notes, evidence, Workspace content, and academic
outcomes never enter the candidate projection or realtime facts. PostgreSQL
multi-node/replay, late-record, authorization, privacy, Markdown, HTTP/OpenAPI,
race, and architecture gates are verified.

Exam Resources and Starter Workspaces are distinct from mutable Attempt
Workspace files. PostgreSQL owns their logical identity and hierarchy while
VFS owns opaque bytes. Candidate access is protected in-application use, with
no candidate download/export surface. The complete accepted contract and
delivery order are in [Examinations](../architecture/examinations.md).

## Transactional mail

The transactional-mail service is implemented for the complete closed 43-key
catalog. One dedicated `server/app/mail` child module now owns composition for
all system, identity/security, access/onboarding, academic administration,
Exam-management, Sitting, Submission, and Result-release families.
Its complete definition registry owns occurrence meaning, delivery Job class,
default lifetime, action-link policy, and presentation family for every key.
Parent use cases call semantic preparation methods through direct child-package
contracts; they no longer select generic template/kind/Job combinations or
mirror the mail package's production types. Rendering crosses one closed typed
request, and direct and fan-out children share the same frozen-payload
construction.
The transactional-mail product and delivery architecture are accepted. The
transport now exposes portable temporary, permanent, and acceptance-uncertain
outcomes; the server has an independently configured versioned secret-sealing
module; and the complete initial catalog has localized English copy, authored
MJML and text, tracked generated HTML, freshness checks, and deterministic
previews. The first durable tracer is implemented: a recent institution
operator with `mail.manage` can enqueue the fixed `system.mail_test` message to
their own verified address, atomically persisting its immutable occurrence,
encrypted frozen payload, audit event, and delivery Job. Any node can deliver
it with a stable Message-ID, record SMTP acceptance, destroy accepted
ciphertext, and expose only `mail.view`-protected safe projections. Institution
operators can now page a bounded delivery work queue with state, template, and
time filters; inspect one delivery; and use a recent interactive Session with
`mail.manage` to cancel queued work or retry a failed, unexpired, still-relevant
delivery. These controls revision-fence and update the same Delivery and Job,
preserve occurrence and Message-ID identity, destroy canceled ciphertext, and
complete payload-free audit atomically. PATs cannot perform the mutations.
Delivery lifecycle hardening is also implemented: disabled installations
converge outstanding work to terminal suppression without ciphertext or later
resurrection; deadline and typed relevance fences run before sends; automatic
retry uses the bounded jittered Job policy and a PostgreSQL-shared 10-per-second
token bucket with burst and credential reserve; and a daily durable bounded
cleanup enforces the 90/180-day metadata cutoffs without touching security
audit. SMTP outage and queue delay now degrade only the mail subsystem, while
bounded safe telemetry reports template, state, public outcome, attempts,
latency, queue age, count, and health code. Strong recently authenticated
operators can now start one audited durable rekey Job after staged primary
promotion. A PostgreSQL fence rejects old-primary writes; stable bounded pages
re-encrypt active delivery payloads and frozen fan-out bundles; Job checkpoints,
idempotent reference-counted replacement, stale-attempt fencing, and a final
zero-reference proof allow the named fallback to be retired safely across
nodes. Email-verification and password-reset issuance now commit the hashed
credential, successful security audit, encrypted frozen delivery, occurrence,
and reserved credential-delivery Job atomically. Reissue invalidates the prior
credential and suppresses its unsent delivery. Password-reset completion now
atomically changes the password, revokes Sessions, consumes the reset token,
records its audit, and queues only the password-changed security notice.
Implemented product-transition mail also covers typed Invitation issuance and
acceptance, explicit resend, accepted-delivery revocation notices, explicit
email transitions, account enable and
disable, explicit administrative Session revocation, and MFA enable, disable,
recovery-code regeneration, and Personal Access Token create, enable, disable,
and revoke. Exam Manager addition, removal, and ownership transfer now commit
their exact role-specific notices atomically with the relationship change;
ownership transfer tells the previous Owner that they remain a Manager. PAT
notices use ordinary security-delivery work and contain only the safe
description, exact expiry, action time, localized scope context, and bounded
action count; credentials, hashes, and complete scopes are excluded. Sitting
scheduling and Class administration now form one convergent notification
slice. Ordinary Academic Unit membership and Role Binding creation/ending now
commit their six scope-appropriate notices with the relationship and audit,
including disabled/ineligible terminal suppression and replay-safe no-op
behavior. Invitation acceptance deliberately remains its single semantic
acceptance message rather than duplicating those internal relationship notices.
Enrollment, explicit ending, and transfer commit their exact direct
student notice atomically with membership and audit; transfer emits only its
single semantic message. Each transition advances the affected Classes'
durable audience revisions so bounded multi-node reconciliation adds, updates,
or removes candidate projections for upcoming Sittings. Sitting
scheduling, rescheduling, cancellation, and assignment removal now use
an atomic locale-indexed frozen fan-out bundle plus bounded expansion Job. The
bundle freezes every supported locale and the installation default; expansion
selects exact locale, language base, installation default, then English without
consulting mutable assets. Legacy English-only bundles remain readable during
upgrade. Per-candidate
last-communicated projections coalesce unsent changes, authoritative pre-send
fences suppress stale or post-start work, and bounded multi-node
reconciliation converges later Class-audience changes without loading a roster
in the request transaction. Daily bounded maintenance now terminalizes
expired, permanently failed, or orphaned expansion, destroys its encrypted
bundle, suppresses remaining child work, and releases reconciliation; terminal
retention preserves only the last-communicated candidate projection. The
Submission sealing now records one candidate-safe direct receipt atomically
with the Submission and audit. Voluntary and automatic sealing use distinct
wording and templates; both freeze only the published Exam title, safe Sitting
and Submission receipt identities, and a UTC seal time. Exact and crash
replays recover the retained Submission without rendering or recording a
second message, while disabled or ineligible recipients retain a terminal
suppression. Explicit one-way Student Result release now records one canonical
candidate availability notice in the same transaction as Review state and
audit. The message freezes only the published Exam title and PostgreSQL release
time; it contains no score, outcome, remarks, evidence, rationale, Submission
content, or invented result link. Exact release replay records no second
message, and disabled or ineligible candidates retain terminal suppression.
The phase gate certifies the full hermetic server gate, PostgreSQL Store and
application integration, independent module builds/tests, all 43 English
template triplets and previews, reusable SMTP conformance, and a real
application-through-Mailpit credential, security, and Sitting-fan-out flow.

The closed initial catalog includes identity, security, access-and-onboarding,
academic, examination, candidate, and controlled operator-test messages.
Recovery, Invitation, Class, Exam-management, Submission, and Result-release
transitions record encrypted per-recipient delivery intent and durable Jobs
atomically. Academic Unit and Role transitions use the same contract, with
bounded fan-out, relevance fencing, deadline-aware retries, safe operator
control, and no user opt-outs. The complete contract is in
[Transactional mail](../architecture/mail.md).

The current verification and reset links name server-hosted routes whose pages
do not exist. Those pages are explicitly deferred to the server-hosted page
and design-system phase and must remain visible as an incomplete recovery
journey. Ordinary transactional mail will not invent protected API links or a
desktop URL scheme while the client navigation contract remains undefined.

## Access and onboarding

The access and onboarding server phase is implemented and certified. Existing
installations reconcile the protected `system_admin` Role with every current
grantable action before serving traffic, preserving unknown downgrade actions
and all custom Roles and bindings. The real-graph phase gate covers bootstrap,
policy, Desktop protocol, local and external credentials, Invitations,
JSON/CSV administration, progression, mail, multi-node recovery,
authorization, privacy, PostgreSQL conformance, HTTP/OpenAPI, race, vet, and
architecture checks. Server-hosted design-system pages and the Desktop
LaunchWindow remain explicitly deferred client/hosted-page work.

Bootstrap now requires a deployment-owned secret, rate-limits public proof attempts,
and atomically creates the unverified first local administrator, protected
Role and binding, initial User Settings and profile-picture Job, installation
marker, audit, and conservative Access Policy revision 1. PostgreSQL fences
concurrent nodes, retains an exact replay outcome, and rejects conflicting
reuse. Production bootstrap requires an explicit secret; explicit loopback
development may generate and display a temporary value once while pristine,
outside logs.
Access Policy administration is implemented with a revision-fenced singleton,
bounded transition history, safe deployment-capability preflight, atomic
last-System-Administrator and invitation-mail guards, durable audit, and a
content-free realtime revision signal. Public `GET /api/v1/discovery` exposes
only the canonical origin, installation and Institution presentation, enabled
capabilities and providers, policy revision, and desktop protocol bounds.
Provider removal disappears from discovery without changing durable policy;
restoring the stable provider ID makes it available again.
Offline administrator recovery is implemented through the host-only
`proctor administrator recover` command while every node is stopped. It
confirms the exact Institution and active `system_admin`, reads a rotated
password only from private input, shares the policy/credential administrator
fence, preserves MFA and Sessions, and writes a secret-free pending record in
the same transaction. Startup reconciles that record into actor-free ordinary
audit before workers or transports. A local-login repair advances the policy
revision and records exact from/to revisions in this dedicated host history;
it is never falsely attributed to the target User in ordinary replacement
history.
Public local registration is implemented as the strict, public
`POST /api/v1/auth/register` API. Shared private attempt accounting precedes
account preparation, while one PostgreSQL-clocked named transaction rechecks
both public registration and local credential enrollment and atomically
creates the unverified User, encoded password, initial settings and
profile-picture work, safe audit, target-bound verification token, frozen
encrypted delivery, and credential Job. Duplicate valid requests retain the
same empty accepted response, failures leave no partial account, and no
academic relationship or Role Binding is implied. The hosted `/register` page
remains explicitly deferred to the server design-system phase.
Existing local-password login and password-recovery issuance/completion now
fail closed against the current policy, with generic public outcomes.
External initiation, identity resolution or auto-provisioning, and Session
issuance require the exact selected provider ID. Each terminal operation
rechecks the authoritative PostgreSQL policy in its committing transaction;
protocol names are never used as provider-identity fallbacks. Detailed
provider admission is now enforced: `linked_only` resolves only an existing
immutable subject link, ordinary `invitation_required` login fails safely when
it has no purpose-bound Invitation claim, and a claimed invitation flow now
terminally links the immutable subject and accepts the exact frozen package
without creating an ordinary Web Session. `auto_provision` creates only an
eligible relationship-free User.
Email never merges accounts, changed provider
profile claims never overwrite an established User, and a provider omitted by
the current node fails closed while its durable policy identity and links are
preserved. A different verified provider mailbox is accepted only when it is
unowned; an owned mailbox or subject linked to another User fails as a bounded
conflict without partial User, relationship, Role, mail, or audit state.
This is a reset-only pre-release baseline: there is no supported upgrade path
for development rows whose external Sessions lack an exact provider ID and
`ExternalIdentityID`.

The server-side Proctor Desktop Authorization protocol is implemented. A
purpose-bound PostgreSQL transaction pins installation, exact local/external
authentication path, IP-literal loopback callback, state, S256 challenge, and
Desktop client/device metadata while persisting bearer values only as hashes.
Any node can approve through an existing Web Session, cancel, or atomically
exchange the short one-use code for an ordinary rotating Desktop Session;
mix-up, concurrent exchange, replay, expiry, and current-policy/provider checks
fail closed. The `/authorize/desktop` hosted page and Desktop Launch Window/UI
remain explicitly unimplemented design-system/client work, so the repository
does not yet claim an end-to-end user journey.

Authentication-method lifecycle APIs are implemented. A strong, recent Web
Session can enroll a policy-permitted password only for a verified mailbox,
start a provider connection pinned to the exact current User, remove the local
password, or unlink one external identity. Provider callbacks link only the
proved immutable subject; profile email and username never merge accounts.
Named PostgreSQL transactions recheck current policy, configured provider
capabilities, active User state, another usable method, and the shared
last-system-administrator fence before committing the audit and mutation.
Removal archives the exact method and revokes only Sessions carrying the exact
removed identity or local password provenance; a second identity at the same
provider is unaffected. Provider-connection rejection, invalid assertion, and
post-consumption failure terminalize the original critical audit, while bounded
periodic reconciliation fails abandoned expired attempts and purges their safe
state metadata after 24 hours. Public method listings and audit projections omit provider
subjects, hashes, claims, and credentials. The hosted
`/account/connect-provider` page remains deferred with the other server-hosted
design-system pages.

The first durable Invitation vertical slice is implemented for
`student_class`. Issue freezes the normalized mailbox, exact Class and Academic
Period, effective bounds, safe profile suggestions, inviter/scope, seven-day
expiry, and only a domain-separated claim hash. One transaction commits the
Invitation, successful audit, encrypted HTML/text delivery intent, and
credential-delivery Job. Local acceptance rechecks current policy, database
time, inviter authority, Class/Period state, mailbox/account conflicts, and
relationship invariants, then atomically creates or resolves the User,
password and verified mailbox, initial User Settings and default-picture Job,
student affiliation and exact Class membership, Invitation consumption,
successful audit, and semantic acceptance mail. Exact claim replay is
idempotent. Neither API responses nor audit/report projections contain the raw
claim, target mailbox, or generated link.

The `teacher_academic_unit` Invitation slice is also implemented. Issue
freezes one exact active Academic Unit, selected non-built-in Role, canonical
action snapshot, effective bounds, inviter, and normalized mailbox only after
the inviter passes both membership-management authorization and the
scope/action delegation ceiling. Acceptance repeats those checks under the
academic-hierarchy, User, Role, and Role-Binding fences, then atomically
creates or resolves the verified local User and initial settings work, teacher
Affiliation, Academic Unit membership, package-origin Role Binding, audit, and
semantic acceptance mail. Compatible packages reuse relationships and add
only missing bindings; existing Users do not receive another welcome. Exact
replay returns the originally committed relationship IDs, and ending the
package membership ends only bindings carrying Invitation provenance.

The first administrator always bootstraps one local account with a one-use
deployment secret before mutable access policy can take effect. Thereafter a
PostgreSQL-owned singleton Access Policy composes local sign-in, invitation-
only local onboarding, self-registration, and configured external providers;
deployment configuration continues to own provider and mail secrets.

The server will host discovery and every sign-in, registration, recovery,
invitation, linking, and provider-selection page. The desktop Launch Window
will accept and verify an installation URL, open only the discovered
authorization endpoint in the system browser, and complete a short-lived
single-use authorization-code flow through an exact loopback callback with
state and PKCE. It will not render authentication forms or receive upstream
provider credentials or tokens.

One User may have an optional local password and multiple explicitly linked
external identities. Email equality never merges accounts automatically. A
durable pre-User Invitation owns its target email, typed purpose, relationship
package, expiry, lifecycle, delivery identity, and atomic acceptance. Student
invitations establish exact Class membership; teacher invitations establish
Academic Unit membership plus their invitation-package-origin role binding;
institution and Academic Unit role invitations use explicit typed purposes.
Those scoped Role Invitations are implemented as existing-User claim flows:
Academic Unit issue checks exact Role delegation, Institution issue additionally
requires a strong/recent interactive Session and rejects PATs, and atomic
acceptance adds or reuses only the compatible Role Binding without changing the
canonical User, creating a relationship, or sending redundant welcome or
acceptance mail. Same-User replay is exact and cross-User replay fails closed.
Authorized Invitation administration is also implemented. Bounded keyset list
and detail apply Institution, Academic Unit subtree, or exact Class visibility;
their safe projections omit claims, rendered payloads, provider and transport
internals. Revision-fenced resend rotates the claim and atomically suppresses
older unsent credential delivery, revocation terminalizes immediately and
notifies only after an SMTP-Accepted credential delivery, and replacement
supersedes the old immutable package while rechecking the new package and
authority in the committing PostgreSQL transaction.
External-provider Invitation acceptance is implemented for CAS and OIDC. The
short-lived browser-bound state stores only the Invitation ID, provider, and
purpose; callback completion rechecks current policy, capability, package,
inviter authority, canonical mailbox ownership, and immutable-subject
uniqueness before one named transaction links the identity and accepts the
package. New and existing canonical Users are supported, provider mailbox
differences never select an account, losing races fail closed, and the closed
Invitation purpose creates no ordinary Session.
Bounded JSON Invitation batches are implemented for one typed create, resend,
or revoke operation and exact target scope at a time. Up to 200 ordered rows
reauthorize and commit independently through the ordinary Invitation use cases;
stable per-row keys and retained outcomes recover unknown commits or row
reordering without duplicate Invitations or mail, duplicate rows are explicit,
and PATs remain limited to ordinary scoped student/teacher onboarding. Larger
validated CSV Invitation imports are also implemented: bounded private uploads
produce asynchronous immutable previews, explicit idempotent commits queue one
resumable per-row execution Job, and safe seven-day reports expose only row
references, outcomes, created Invitation IDs, and closed public codes.
Bounded JSON and CSV existing-User academic administration batches now cover
the closed Affiliation, Academic Unit membership, Class membership/transfer,
Role Binding, account-state, and selected-User Session operations. Rows reuse
the ordinary use cases and named PostgreSQL aggregates, retain minimal
idempotent outcomes atomically, reauthorize at terminal mutation, and expose
only safe resource IDs and closed outcomes.
Student progression is implemented as an exact source/destination Period and
Class workflow. Its asynchronous dry-run reports each bounded-roster conflict,
an explicit content/revision-fenced commit executes resumable independent rows,
same-Period transitions preserve transfer history, and cross-Period
transitions create destination history without rewriting the source. Current
two-scope authority, target/enrollment revisions, transactional Class notices,
cancellation, safe reports, and retained execution outcomes remain enforced by
the ordinary named PostgreSQL aggregates.
The closed authorization registry now distinguishes Academic Unit
membership, Programme, Programme Level, Academic Period, Class, Class
membership, progression, Access Policy, Invitation, onboarding batch,
external-identity, Role, and Role Binding operations. Existing academic HTTP
use cases authorize Programme, Programme Level, Academic Period, and Class
resources through their actual Institution or Academic Unit lineage, including
period applicability, subtree inheritance, sibling denial, and exact Class
scope. Role Binding creation cannot exceed the caller's current action/scope
authority, protected administrative delegation requires parent or Institution
authority, and PAT creation applies the same current-role ceiling while
excluding interactive-only actions. Existing installations receive the new
grantable actions only through protected `system_admin` reconciliation; custom
Roles remain unchanged. The complete contract and implementation order are in
[Access and onboarding](../architecture/access-and-onboarding.md).

The transactional-mail foundation can now durably deliver the implemented
student Invitation credential and semantic acceptance message. The hosted
`/join` page is deliberately not implemented in this slice and remains deferred
to the future server-hosted design-system phase alongside the recovery pages.
The issue and acceptance APIs therefore do not by themselves constitute a
complete human onboarding journey.

## Implemented user settings

The server-owned User Settings Document is implemented as one exact bounded
JSONC source document per User behind session-only self read and conditional
replacement. It provides opaque revision fencing, command idempotency,
structural validation, safe mutation audit, forward-compatible read-only
access, and content-free refetch events. PostgreSQL, not VFS or an Attempt
Workspace, owns the source; the desktop registry alone interprets recognized
presentation preferences. The complete contract is in
[User settings](../architecture/user-settings.md).

The initial server slice excludes profiles, keybindings, device/restoration
state, client registry and UI work, server-visible source history, and every
Exam Attempt integration. In particular, Candidate Settings Baseline capture
is deferred to a separately designed Attempt-admission phase and must not
widen the User Settings module.

## Open decisions

- Choose and define the next external identity provider after CAS and OIDC:
  SAML/RENATER or LDAP.
- Define any future provider-directory synchronization for profile fields,
  affiliations, relationship reconciliation, and deprovisioning. Interactive
  sign-in and explicit linking do not grant a provider general directory
  authority.
- Define any dedicated proctor-assignment role beyond Exam Managers, candidate
  accommodations, review appeals, exact retention periods, and future
  manager-controlled export/deletion policy.
- Decide whether cross-node WebSocket reconnection transfers bounded replay
  queues or always performs authoritative HTTP resynchronization.
- Decide whether generated client SDKs belong in this monorepo and which
  desktop languages are required.

## Execution-environment foundation

The execenv server integration is implemented through the API boundary.
Execution Profiles are authored on Drafts and frozen into immutable Revisions;
the authenticated Attempt WebSocket exposes bounded terminal open, input,
resize, close, output, and closed messages; guest writes are acknowledged
through the existing Workspace commands; and pause, resume, lease loss,
submission, Sitting close, and periodic cleanup converge host state. The UI is
still a separate product slice. The profile remains off by default, the Attempt
Workspace remains durable authority, and the environment is a
Firecracker-isolated projection with one Attempt Terminal.
execenv v0.2 advertises remaining slots while enforcing configured memory,
disk, CPU, and process limits inside `Ensure`; a separate remaining-memory
advertisement is deferred until the reusable execenv readiness contract grows
that field.
[`execenv`](https://github.com/sudosylabs/execenv) is the exam-blind
client contract and the host daemon repository; only the installation talks
to hosts. The complete contract is in
[Execution environments](../architecture/execution.md).

## Planned product work

- Implement the deferred server-hosted account, Invitation, and Desktop
  authorization pages with the design system, plus the Desktop LaunchWindow.
- Resource search remains deferred because an Exam initially has at most ten
  active resources.

## Optional engineering follow-ups

- Expand the store cache allowlist only from measured need and after the
  documented staleness review.
- Tighten residual broad transport aggregates when a vertical slice provides a
  stable narrower interface.
