# Authorization and audit reference

Authorization evaluates a principal, stable action, and actual resource.
Authentication alone never grants resource access.

## Roles and scopes

Roles are additive and access is denied by default. Bindings may apply at
institution, academic-unit, or class scope. Programme, Programme Level,
Academic Period, Exam, and other resources resolve to those binding scopes
rather than introducing additional Role Binding kinds. Academic-unit
permissions inherit to descendant resources only when the action's inheritance
rule allows it. Do not introduce explicit deny rules without a documented
precedence model.

Action names are stable domain contracts such as `class.members.view`, not
HTTP verbs or route names. The built-in system-administrator role contains
every recognized action and is protected as described in
[identity](../../identity-and-access/references/identity.md#installation-bootstrap).

`system_admin` remains the only built-in Role. Institutions construct Teacher,
Faculty Administrator, Reviewer, Roster Manager, and other roles from the
closed action catalog. Role definitions are Institution-wide and reusable;
their bindings provide scope. Adding a grantable action requires versioned,
idempotent reconciliation of the protected built-in role for existing
installations and never mutates custom roles.

Role-definition administration and Role Binding administration are separate.
`role.view` exposes only safe roles usable at the caller's scope;
Institution-scoped `role.manage` creates, changes, or archives definitions.
`role_binding.view` and `role_binding.manage` operate within the caller's
authorized scope. A caller cannot create a broader binding, delegate an action
they do not possess, or delegate protected system, role-management,
access-policy, or administrative-delegation authority without the separately
required parent or Institution authority.

## Records completion and preservation

`exam.records.complete` grants the separate completion and waiver actions
through a current Exam Manager relationship and current membership in the
Exam's exact Academic Unit. `exam.records.complete.override` is an explicit
scoped permission, not an administrator-only shortcut. Waivers and their
private read projection exclude the Submission's own candidate; a denial is
durably audited. Neither path can waive an undecided Flag.

`exam.records.hold` and its scoped `.override` follow those same manager/scope
rules for placing and inspecting Exam, Sitting and Submission holds.
`retention_hold.release` additionally requires the active protected
system-administrator Role and strong recent interactive authentication.
Ordinary custom roles cannot release a hold merely by including that action.
All of these actions require Session credentials and forbid PATs.

The owning Store operations recheck credential validity, current role and
scope, ordinary manager membership, and release assurance while holding the
affected domain fences. State, critical audit and retained retry outcome
commit atomically. Every replay rechecks current authority. Closed operational
reason codes and immutable IDs/counts enter ordinary audit; private
preservation/release rationale and review text never do.

## Academic and onboarding actions

The accepted granular catalog distinguishes:

- `academic_unit.view` and `academic_unit.manage`;
- `academic_unit.members.view` and `academic_unit.members.manage`;
- `academic_period.view` and `academic_period.manage`;
- `programme.view` and `programme.manage`;
- `programme_level.view` and `programme_level.manage`;
- `class.view` and `class.manage`;
- `class.members.view` and `class.members.manage`;
- `academic.progression.manage`;
- `access_policy.view` and `access_policy.manage`;
- `invitation.view`, `invitation.create`, and `invitation.manage`;
- `onboarding_batch.view` and `onboarding_batch.manage`;
- `external_identity.manage`;
- `role.view`, `role_binding.view`, and `role_binding.manage`.

Programme, Programme Level, and Academic Period are actual authorization
resources. The scope resolver maps each to its owning Academic Unit or
Institution. An Institution-owned Academic Period applies everywhere; a
unit-owned period applies to that unit's subtree. A Class authorizes through
its exact Programme lineage and applicable Period without granting a sibling
or parent scope.

Invitation and batch permissions never imply their packaged effects. Issuing
or executing one also requires every Affiliation, membership, progression,
Role Binding, and User action represented by the row. Generic `job.view` and
`job.manage` do not grant onboarding authority or domain payload visibility.
Ordinary student/teacher automation may use an appropriately scoped PAT;
Access Policy, identity reconciliation, administrative Role Binding, and
credential intervention require a strong recent interactive Session.

## Enforcement

- HTTP and WebSocket establish credentials and assurance but do not preflight
  resource permissions or issue authorization receipts.
- Every actor-sensitive application use case performs its authoritative check
  immediately, before avoidable expensive work or mutation.
- `App.Can` composes policy without auditing; `App.Authorize` durably records
  allow or deny and fails closed. Focused authorizers provide the same split at
  owning use-case boundaries.
- List/search queries constrain results by authorized scope in persistence;
  they do not fetch everything and filter in memory.
- WebSocket commands/subscriptions and background jobs use the same application
  policy boundary and explicit `Invocation` as HTTP callers.
- Role and binding changes invalidate affected authentication or connection
  state across nodes, while correctness remains grounded in PostgreSQL.

Authorization results are not cached. Each decision resolves active bindings
and non-deleted roles from authoritative state. Adding such a cache requires a
separate bounded-staleness, invalidation, and recovery design.

## Application ownership

Access Control is a cooperating application boundary rather than part of one
large Identity service. It owns authorization evaluation, non-audited
predicates, authoritative audited decisions, action/resource compatibility,
scope inheritance, credential ceilings, and authorization-specific visibility
interpretation. Role administration and Role Binding administration remain
focused mutation services grouped beside—but not merged into—the evaluator.

The evaluator receives exact Role, Role Binding, and resource-resolution
contracts rather than the root `store.Store`. One focused scope resolver owns
the repeated interpretation from a typed Resource to its authoritative
institution, academic-unit, class, or relationship context. It may read
academic relationships but does not own their lifecycle or expose general
academic lookup operations.

The resolver validates the action/resource combination before resolving
current authoritative state. Missing, inactive, incompatible, or out-of-scope
relationships deny access without a cached or inferred fallback. Persistence
or other resolution failures fail the authorization operation closed. The
owning use case decides whether its safe public response is forbidden or
not-found without disclosing unauthorized resource existence.

Durable decision audit, general mutation audit, audit listing, and realtime
fan-out remain sibling capabilities exposed through narrow ports. Role and
binding changes commit before best-effort invalidation; authorization
correctness never depends on those effects or on an authorization-result
cache.

Transport permission preflight, decision receipts, and request-shaped
permission helpers are not compatibility surfaces. HTTP and WebSocket provide
authentication context only; real application use cases call the generic or
focused Access Control boundary themselves.

The retained entry points are non-audited generic evaluation for policy
composition, authoritative audited authorization, and focused authorizers for
specific use cases. List and search use cases derive authorized scope
constraints before querying; purpose-specific Store operations apply those
constraints with bounded keyset pagination rather than filtering unauthorized
rows in application memory.

User visibility is contextual: self-view does not imply self-management.
Institution-scoped `user.view` and `user.manage` retain full administrative
meaning. Academic-unit-scoped `user.view` reaches only Users with a current
Academic Unit membership, Class membership, or Role Binding in the authorized
subtree, and its projection omits relationships outside that subtree. Because
an Affiliation is institution-wide and carries no Academic Unit, it cannot
anchor subtree visibility by itself; its history becomes readable only after
another current relationship establishes contextual User visibility. Unit
scope never reveals whether a global User is disabled: disabled Users are
absent from scoped collection, exact-profile, and profile-picture reads, and
an `include_disabled` request is effective only for Institution-wide
visibility. Academic Unit administrators end relationships within scope rather
than disabling the global User, changing canonical identity, or intervening in
credentials. Audit records keep the target User and academic authorization
scope distinct.
The collection-level `user.search` audit-event action records a bounded
User-search decision against the Institution; it is not a permission action.
Authority is derived from current `user.view` and `class.members.view` grants
before persistence applies the resulting constraints.

Profile-picture mutation uses the narrow `user.profile_picture.manage` action.
Users have intrinsic access to that action on themselves, subject to their
credential ceiling; `user.manage` authorizes administrative mutation of
another user's picture. This self-service exception grants no authority over
other User fields.

Durable Job inspection uses institution-scoped `job.view`; cancellation and
explicit retry use `job.manage`. These operator actions do not authorize a
generic create operation or expose unfiltered payloads.

Institution-wide `audit.view` remains distinct from `academic_audit.view`.
The latter lists only academic mutations, Invitations, batches, and decisions
whose scope resolves to an authoritative Academic Unit or Class. An Academic
Unit grant is restricted to that unit's subtree; an Institution grant spans
all such academic scopes without becoming unrestricted audit visibility. Both
forms apply the closed academic-action catalog in persistence, so unrelated
identity, provider, and security events remain excluded. Onboarding domain
actions likewise expose actor/scope-filtered Job projections without granting
generic Job inspection.

Examination authorization begins with Exam, Exam Sitting, Exam Attempt, and
Submission resources. Drafts and Revisions authorize through their Exam;
supporting material, workspace state, flags, evidence, and reviews authorize
through their owning Exam, Sitting, Attempt, or Submission path. The closed
action vocabulary is introduced with each working vertical slice rather than
invented from HTTP verbs.

Ordinary Exam management requires both a current Exam Manager relationship and
the appropriate role permission at the Exam's Academic Unit. The immutable
creator is provenance; one current owner is protected, and ownership transfer
is an audited operation. A system administrator may use an explicit permission
override without becoming an Exam Manager, but no permission bypasses the
exact Academic Unit/Class lineage, lifecycle, or immutable-publication rules.
Sitting creation authorizes `exam.sitting.create` on the owning Exam; exact
Sitting reads and pre-open schedule/cancel mutations authorize
`exam.sitting.view` and `exam.sitting.manage` on the Exam Sitting. Sitting lists
use the owning Exam's view decision. Each ordinary path also rechecks the
current Exam Manager relationship and exact Academic Unit membership. The
corresponding `.override` actions are explicit administrator paths, remain
audited, and bypass neither Revision ownership nor Class lineage, Academic
Period, archive, state, or optimistic-revision constraints. Unauthorized exact
Sitting access is concealed as not found. Candidate participation is decided
from current exact-Class membership and Attempt/Sitting state on every
connection; it is not a reusable role grant.
After admission, every protected candidate HTTP read requires the immutable
authenticated Session principal plus the exact open Attempt Connection identity
and continuity credential. The application hashes that credential immediately;
persistence matches the hash and requires the Connection's candidate and
Session identities to equal the current principal. Membership is not
continuously re-polled for those established reads, but a fresh connection or
reconnect always rechecks exact current Class membership. Missing, invalid,
expired, fenced, or cross-Session selectors are concealed without exposing
whether the Attempt exists.

Submission inspection rechecks the current Exam Manager relationship plus
`submission.view` or the explicit override at the Submission's Academic Unit.
Draft decisions, notes, and finalization require `submission.review`; explicit
student-result release requires the distinct `submission.release` action.
Their `.override` variants are separate audited administrator paths. Every
operation resolves the canonical Submission first, audits that resource and
resolved Academic Unit scope, and conceals a denied or mismatched target as not
found. An identical idempotent mutation replay repeats current authorization
and audit before returning its retained result.
Further lifecycle and visibility rules are in
[examinations](../../exam-lifecycle/references/examinations.md#authorization-effects-and-persistence).

## Audit

Operational logs and audit records are separate. PostgreSQL audit events are
authoritative and include the safe actor/principal, action, resource, academic
scope, outcome, request/node context, time, and approved prior/result data.

Authorization decisions fail closed when their durable record cannot be
written. A critical mutation writes an `attempt` before state changes and that
attempt transitions once to `success` or `fail`. If terminal completion fails
after commit, return an internal failure and retain the attempt for operator
reconciliation.

Self-service Session revocation and logout follow the same critical attempt
and atomic-success rule as administrative revocation. Their ownership-based
authority, separate audit operations, no-op outcomes, and notice exclusion are
defined in the [Session contract](../../identity-and-access/references/identity.md#sessions-browser-transport-and-desktop-handoff).

Audit parameters and prior/result projections are each bounded to 16 KiB and
exclude secrets, credentials, exam answers, and unbounded user content. Direct
peer addresses may be recorded; forwarded client addresses remain untrusted
until trusted-proxy configuration exists.

Audit and minimal retirement receipts follow the current approved Institution
Retention Policy's audit period. Zero preserves them indefinitely. An audit's
terminal event and a receipt's own retirement or cancellation event define
their ages; completed offline-recovery evidence and released hold history must
also satisfy their own event ages. Eligibility begins a fresh positive grace
period. Saving a policy never approves expiry, and policy or control changes
cancel pending grace atomically.

Unfinished audit attempts and records still referenced by durable owners are
protected. Relevant examination holds, unfinished records completion, export
construction, and uncompleted physical purge are explicit preview blockers.
Adding a durable audit reference cancels its expiry grace even if that
reference disappears before the next worker scan. Existing FK owners are a
closed, conformance-tested inventory; a new reference requires a deliberate
protection and cancellation rule. Ordinary audit listing and visibility
checks do not grant expiry authority.

The named Retention Store operation locks policy/control and the current
examination owners, rechecks dependencies at the PostgreSQL clock, and commits
the bounded removal with a separate content-free system audit. Neither its
pending schedule nor a retirement receipt references that cleanup audit;
cleanup history therefore receives its own age rather than retaining detailed
audit forever. Reconciled offline recovery evidence may expire with its audit.
Released hold history may expire only after both audit events and the release
have aged and no retained command outcome references them. Other referenced
histories remain visible blockers until their owning workflow removes them.

Terminal system cleanup audits use a separate bounded aggregate pass, never
one new audit for each old cleanup audit. The same per-record age, grace,
holds and reference checks apply. A changing batch reserves its actorless
critical attempt inside the named transaction before effects, then completes
one scalar-count summary atomically. A no-change scan, including protected
and future-grace records, emits no audit. The summary itself follows the same
batch lifecycle; repeated generations therefore do not multiply history.
Broad Exam audit records inherit unfinished protection from descendant Sittings.

A receipt cannot expire until its permanent Submission markers exist and
all exact-key purge writers are known finished with independently confirmed
absence. Unknown writers retain their reconciliation references indefinitely.
Deleting eligible receipt history also removes its notices, but never clears
permanent category markers or restores content.

`retention_policy.view` reads Institution policy through an authorized Session
and may be granted at Institution scope. `retention_policy.manage` is reserved
for the active protected system-administrator Role and additionally requires
a strong recent interactive Session. Neither action accepts Personal Access
Tokens. Startup reconciliation adds both actions to the protected Role on
existing installations. Policy replacement requires critical audit, expected
revision, and idempotency; success commits with the policy and retained outcome.

`exam.records.export` and `exam.records.export_override` grant no standalone
read access. Creation and retrieval also require Submission read; Sitting
exports require Sitting read, and integrity requires Browser Activity read,
all under the same ordinary/override scope. SQL rechecks credentials,
bindings, Exam Manager/exact-unit membership, nested ownership and requester
under authoritative locks. Critical creation audit, the exact ID-only retry
outcome, source protections and build Job commit atomically. Archive bytes,
paths, private Review text, hashes and structured student records never enter
ordinary audit or Job diagnostics.
