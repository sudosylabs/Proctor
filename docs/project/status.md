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
- Typed deployment configuration, structured logging, health/readiness,
  graceful shutdown, and the shared `testlib` graph are operational.
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
  scoped user visibility through one authoritative Access Control boundary.
- Realtime behavior includes authenticated WebSockets, authorized
  subscriptions, bounded local replay, explicit resynchronization, local and
  Memberlist cluster transports, and best-effort cross-node fan-out.
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

## Transactional-mail foundation

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
ciphertext, and expose only a `mail.view`-protected safe status projection.
Product-transition mail, operator retry/cancel/rekey, lifecycle cleanup, and
bounded catalogs remain unimplemented.

The closed initial catalog includes identity, security, access-and-onboarding,
academic, examination, candidate, and controlled operator-test messages.
Later application transitions will record encrypted per-recipient delivery intent
and durable Jobs atomically, with bounded fan-out, relevance fencing,
deadline-aware retries, safe operator control, and no user opt-outs. The
complete contract and remaining delivery order are in
[Transactional mail](../architecture/mail.md).

The current verification and reset links name server-hosted routes whose pages
do not exist. Those pages are explicitly deferred to the server-hosted page
and design-system phase and must remain visible as an incomplete recovery
journey. Ordinary transactional mail will not invent protected API links or a
desktop URL scheme while the client navigation contract remains undefined.

## Access and onboarding

The access and onboarding architecture is accepted. Existing installations
now reconcile the protected `system_admin` Role with every current grantable
action before serving traffic, preserving unknown downgrade actions and all
custom Roles and bindings. Access Policy administration and discovery, browser
authorization, identity reconciliation, Invitation, and batch workflows are
not implemented.

Bootstrap now requires a deployment-owned secret, rate-limits public proof attempts,
and atomically creates the unverified first local administrator, protected
Role and binding, initial User Settings and profile-picture Job, installation
marker, audit, and conservative Access Policy revision 1. PostgreSQL fences
concurrent nodes, retains an exact replay outcome, and rejects conflicting
reuse. Production bootstrap requires an explicit secret; explicit loopback
development may generate and display a temporary value once while pristine,
outside logs.
Access Policy administration, transition history, provider selection, and
public discovery remain unimplemented.

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
CSV imports are asynchronous validated batches over the same invitation and
progression commands, with row-level results and no raw invitation secrets in
exports. Academic Periods now have explicit Institution or Academic Unit
ownership and dedicated `academic_period.view` and `academic_period.manage`
actions; Academic Unit scope applies to its subtree. The complete contract and
implementation order are in
[Access and onboarding](../architecture/access-and-onboarding.md).

Invitation-required policy cannot be activated until the transactional-mail
foundation can durably deliver invitation credentials. The hosted `/join` and
account pages are deliberately deferred to the same future server-hosted design
system phase as the recovery pages; this dependency does not block the domain,
persistence, discovery, or authorization design.

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

## Accepted execution-environment design

The Execution Environment design is accepted but not implemented. An
Execution Profile is authored on the Draft, frozen in the Revision, and
defaults to off. The Attempt Workspace remains the durable authority; the
environment is a Firecracker-isolated projection with one Attempt Terminal.
`packages/guest` is the exam-blind client contract; the host binary lives in
a separate repository; only the installation talks to hosts. The complete
contract is in [Execution environments](../architecture/execution.md).

## Planned product work

- Implement the accepted access-and-onboarding architecture in its documented
  order after scoped Academic Period ownership and protected initial policy:
  Access Policy administration and discovery, browser authorization
  transactions, local and external credential
  reconciliation, durable typed Invitations, then bounded CSV batch workflows.
  Invitation-required activation and usable invite links remain gated on the
  mail foundation and hosted-page design system respectively.
- Implement the accepted transactional-mail architecture as verified vertical
  slices, beginning with the durable operator test-mail tracer and migration of
  verification and password-reset delivery. The recovery landing pages remain
  a separately visible dependency of the future server-hosted design system.
- Resource search remains deferred because an Exam initially has at most ten
  active resources.

## Optional engineering follow-ups

- Expand the store cache allowlist only from measured need and after the
  documented staleness review.
- Tighten residual broad transport aggregates when a vertical slice provides a
  stable narrower interface.
