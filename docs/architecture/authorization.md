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
- `PrincipalHasPermissionTo*` predicates compose policy without auditing;
  `AuthorizePrincipalTo*` boundaries durably record allow or deny and fail
  closed.
- List/search queries constrain results by authorized scope in persistence;
  they do not fetch everything and filter in memory.
- WebSocket commands/subscriptions and background jobs use the same application
  policy boundary and explicit `Invocation` as HTTP callers.
- Role and binding changes invalidate affected authentication or connection
  state across nodes, while correctness remains grounded in PostgreSQL.

Authorization results are not cached. Each decision resolves active bindings
and non-deleted roles from authoritative state. Adding such a cache requires a
separate bounded-staleness, invalidation, and recovery design.

User visibility is contextual: self-view does not imply self-management;
cross-user access needs `user.view`/`user.manage` at institution scope or the
implemented teacher-to-student class relationship. Audit records keep the
target user and the academic authorization scope distinct.

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
