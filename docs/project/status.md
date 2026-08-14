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
  idempotent updates; unchanged edits do not mutate or publish, and archived
  Exams reject new authoring mutations.

## Architecture migration acceptance

The required architecture migration was accepted on 2026-08-08. At acceptance,
the dependency-debt ledger was empty, the module-root composition and inward
dependency graph were enforced, OpenAPI agreed with runtime routes and errors,
and the hermetic server gate plus independent module checks passed. The
remaining work below is product development or optional tightening, not an
uncompleted architecture migration.

## Accepted Examination Core design

The Examination Core domain and architecture are decided, and its initial Exam
creation, retrieval, and Draft text-authoring slices are implemented. An Exam
belongs to one Academic Unit and has
one mutable Draft and immutable published Revisions. Each Sitting selects one
Revision and exactly one Class in that Academic Unit. Urgent instructions or
resource correction creates another immutable Revision and atomically
retargets only the affected open or paused Sitting; it does not introduce a
Sitting Amendment entity or force candidates to rejoin.

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
- Set bounded workspace quotas and close-work budgets in their implementing
  slices.
- Decide whether cross-node WebSocket reconnection transfers bounded replay
  queues or always performs authoritative HTTP resynchronization.
- Decide whether generated client SDKs belong in this monorepo and which
  desktop languages are required.
- Define the coderunner threat model, isolation boundary, resource limits,
  supported languages, artifact model, and deployment topology.

## Planned product work

- Continue Examination Core as complete vertical slices: manager mutation;
  resources, Starter Workspace, and publication; Sitting delivery
  and live correction; Attempt admission and Participation; mutable Workspace
  and immutable Submission; then integrity policy, evidence, flags, and
  Submission Review.
- Extend server-owned file handling for validated IDE preferences alongside
  the examination-specific resource and workspace boundaries. Resource search
  is deferred because an Exam initially has at most ten active resources.

## Optional engineering follow-ups

- Expand the store cache allowlist only from measured need and after the
  documented staleness review.
- Tighten residual broad transport aggregates when a vertical slice provides a
  stable narrower interface.
