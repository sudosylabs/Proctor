# Authorization and audit

Authorization evaluates a principal, stable action, and actual resource.
Authentication alone never grants resource access.

## Roles and scopes

Roles are additive and access is denied by default. Bindings may apply at
institution, academic-unit, or class scope; programme, programme-level, and
exam scopes remain product decisions. Academic-unit permissions inherit to
descendant resources only when the action's inheritance rule allows it. Do not
introduce explicit deny rules without a documented precedence model.

Action names are stable domain contracts such as `class.members.view`, not
HTTP verbs or route names. The built-in system-administrator role contains
every recognized action and is protected as described in
[Identity](./identity.md#installation-bootstrap).

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

User visibility is contextual: self-view does not imply self-management;
cross-user access needs `user.view`/`user.manage` at institution scope or the
implemented teacher-to-student class relationship. Audit records keep the
target user and the academic authorization scope distinct.
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
[Examinations](./examinations.md#authorization-effects-and-persistence).

## Audit

Operational logs and audit records are separate. PostgreSQL audit events are
authoritative and include the safe actor/principal, action, resource, academic
scope, outcome, request/node context, time, and approved prior/result data.

Authorization decisions fail closed when their durable record cannot be
written. A critical mutation writes an `attempt` before state changes and that
attempt transitions once to `success` or `fail`. If terminal completion fails
after commit, return an internal failure and retain the attempt for operator
reconciliation.

Audit parameters and prior/result projections are each bounded to 16 KiB and
exclude secrets, credentials, exam answers, and unbounded user content. Direct
peer addresses may be recorded; forwarded client addresses remain untrusted
until trusted-proxy configuration exists. Retention is indefinite until an
administrator-visible retention and legal-preservation policy is decided.
