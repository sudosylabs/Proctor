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
  organizational membership, and effective-dated student enrollment.
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
  delivery deadline and does not extend it for paused time. Zero-Attempt
  Sittings close directly; resumable Attempt sealing remains a later slice.

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

Exam Resources and Starter Workspaces are distinct from mutable Attempt
Workspace files. PostgreSQL owns their logical identity and hierarchy while
VFS owns opaque bytes. Candidate access is protected in-application use, with
no candidate download/export surface. The complete accepted contract and
delivery order are in [Examinations](../architecture/examinations.md).

## Open decisions

- Choose and define the next external identity provider after CAS and OIDC:
  SAML/RENATER or LDAP.
- Define account linking, profile ownership, affiliation reconciliation, and
  provider-driven deprovisioning.
- Decide whether roles may bind directly to programme and programme-level
  scopes.
- Define any dedicated proctor-assignment role beyond Exam Managers, candidate
  accommodations, review appeals, exact retention periods, and future
  manager-controlled export/deletion policy.
- Set bounded close-work budgets in their implementing slices.
- Decide whether cross-node WebSocket reconnection transfers bounded replay
  queues or always performs authoritative HTTP resynchronization.
- Decide whether generated client SDKs belong in this monorepo and which
  desktop languages are required.
- Define the coderunner threat model, isolation boundary, resource limits,
  supported languages, artifact model, and deployment topology.

## Planned product work

- Continue Examination Core as complete vertical slices: immutable Submission,
  resumable Sitting sealing, and integrity evidence review with Student Result
  release.
- Extend server-owned file handling for validated IDE preferences alongside
  the examination-specific resource and workspace boundaries. Resource search
  is deferred because an Exam initially has at most ten active resources.

## Optional engineering follow-ups

- Expand the store cache allowlist only from measured need and after the
  documented staleness review.
- Tighten residual broad transport aggregates when a vertical slice provides a
  stable narrower interface.
