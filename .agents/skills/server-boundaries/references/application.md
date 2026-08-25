# Application-boundary reference

Use cases are direct methods with typed commands or queries and typed results:

~~~go
CreateAcademicUnit(ctx, invocation, command) (*model.AcademicUnit, error)
GetAcademicUnit(ctx, invocation, query) (*model.AcademicUnit, error)
~~~

`app.Invocation` is immutable and explicitly contains the principal and safe call metadata needed for authorization and audit. `context.Context` remains for cancellation, deadlines, and propagation, not hidden security dependencies.

Each actor-sensitive use case authorizes immediately before expensive work or mutation. HTTP and WebSocket establish credential and assurance requirements but do not preflight resource permissions or issue decision receipts.

Domain models own local invariants and transitions. Application services own use-case policy, authorization, multi-aggregate coordination, transaction intent, audit, and external effects. Focused service implementations are normally unexported behind `app.App` and consumer-owned interfaces.

Background jobs call application use cases with a system/service invocation. They do not manipulate stores directly.

The `app/idempotency` leaf owns the shared validation, canonical JSON, and
SHA-256 preparation algorithm for durable command idempotency. It does not own
any business operation. Each command owner decides whether a key is required,
defines its stable operation name, normalizes its own semantic inputs, and
constructs the semantic document whose compatibility is preserved across
retries. Exam parent facades forward the bounded raw key; the owning Exam
service prepares the persistence contract beside the command policy.

The `app/job` child module owns the generic durable execution engine: immutable
descriptor validation, claiming, leases, fenced transitions, checkpoints,
work budgets, retry, cancellation observation, and bounded lifecycle. Domain
Job handlers remain beside their owning use cases and cross that seam through
typed, versioned documents and safe outcomes. The engine depends only on the
domain Job records and `store.JobStore`; it never imports its parent package or
selects infrastructure.

The `app/realtime` child module owns transport-neutral realtime events and
connection-close reasons, local-first delivery, peer propagation codecs,
loop prevention, attachment synchronization, and propagation of session
revocation and authentication or authorization invalidation. It is inert after
construction: it owns no goroutine, queue, retry loop, or infrastructure
lifecycle. The parent application retains use-case effect timing, Principal
validation, WebSocket subscription authorization, and translation between
delivery failures and public application errors.

The `app/mail` child module owns all server-specific mail composition:
the complete template-definition registry, semantic preparation operations,
closed typed presentation requests, safe render inputs, disabled/ineligible
suppression, shared frozen-payload mechanics, stable Message-ID construction,
and locale-indexed Sitting fan-out bundles. It receives rendering and sending
capabilities at construction and returns validated `MailOccurrence`,
`MailDelivery`, and Job records; it does not send during a business mutation or
persist anything itself. Parent use cases retain authorization, audit,
idempotency, recipient lookup, and named transaction coordination through
narrow consumer-owned preparation ports. Production code in the parent uses
the child package's owned contracts directly rather than redeclaring aliases;
the child never imports the parent application, transports, platform, SQL, or
deployment configuration.

The examination capability is a cohesive `app/exam` boundary.
It owns authoring and publication policy, manager relationships, Sitting and
Attempt lifecycle, Participation fencing, Submission coordination, integrity
evaluation, authorization timing, audit intent, and post-commit effects.
Selective `app/exam/resource`, `app/exam/workspace`, `app/exam/attempt`,
`app/exam/correction`, and `app/exam/review` children are justified by their distinct stable
mechanics: published read-only supporting material, Draft Starter Workspace
authoring, Attempt admission/continuity, bounded Focus Loss evaluation, and
acknowledged live Workspace coordination over opaque VFS objects, plus bounded
live-correction staging and atomic application policy, and post-Submission
integrity decisions/finalization/release. The small `app/exam/safemarkdown`
module owns the one shared candidate-facing sanitizer used by protected Exam
presentation and released Review remarks; it contains no authorization or
transport policy. `app/exam/attempt`
consumes separate bounded Store contracts for the Attempt lifecycle and mutable
Workspace aggregate and narrow audit, content, and realtime ports. None imports
the parent application package or selects SQL, VFS, WebSocket, Jobs, or other
infrastructure.

The Attempt child also owns voluntary and actorless automatic Submission use
cases plus protected manager inspection queries. Voluntary Submission crosses
one named Store aggregate seam that revalidates current membership and
continuity, seals lifecycle and integrity state with the immutable manifest,
and commits audit and idempotent outcome together. Bounded Sitting-close work
pages Attempts in its parent Job use case, then invokes the child once per
Attempt; the child records system-audit intent and calls a separate natural-
identity sealing operation without importing or scheduling Jobs. The
application publishes only safe manager and candidate facts and requests exact
Attempt-Connection unbinding after commit; a replay returns the retained safe
receipt and repeats neither effect. Manager queries authorize the canonical
Submission resource before concealing any nested ownership mismatch, and
stream retained file bytes only through the narrow content port.

The Attempt Terminal bridge is a focused unexported parent-application service.
It coordinates the Attempt and Execution modules through narrow consumer-owned
ports because neither child owns the complete cross-module workflow. Workspace
mutation origin is an explicit closed command value: candidate mutations may
synchronize to the Execution Environment, while execution-host mutations still
publish realtime results but never echo back to the host. Context remains only
for cancellation and deadlines, not mutation provenance. Origin is deliberately
excluded from the retained version-one Workspace fingerprint: both origins
commit the same durable change, and replay repeats neither post-commit effect.

The package is introduced with the first working vertical slice, not as an
empty architectural placeholder. Its `doc.go` must define the Exam, Draft,
Revision, Sitting, Attempt, Participation, Resource, Starter Workspace,
Workspace, Submission, Integrity, and Review vocabulary; state what the
package owns and excludes; and state its allowed inward dependencies. The
parent `app.App` remains the public facade and `app.New` remains the sole
application constructor. Detailed examination boundaries are in
[the `exam-lifecycle` reference](../../exam-lifecycle/references/examinations.md).

Realtime receives its required authentication invalidator and diagnostics at
construction. Its sink and peer fan-out are attached once before readiness
because the composition graph constructs the application before its WebSocket
and cluster adapters. The child depends only on the domain model and narrow
consumer-owned ports; it never imports its parent package or transport
packages. The parent does not redeclare the child's event, close-reason, sink,
fan-out, codec, or propagation contracts; its thin publication facade exists
only to translate typed delivery failures into application errors.

Operator Job use cases depend on the engine rather than a separate Job Store
and descriptor catalog. They retain Principal authorization, resource
resolution, durable audit ordering, and application-error translation; the
engine owns safe projections and descriptor-governed persistence mechanics.

Atomic store operations are explicit named aggregate operations such as bootstrap, enrollment transfer, or password-reset consumption. The application decides policy; the adapter owns locking, constraints, concurrency checks, and commit/rollback. Raw SQL transactions and generic `WithTransaction(func(Store))` callbacks do not cross into `app`.

The name and contract expose the complete atomic and race guarantee to callers
and reusable conformance tests. A generic transaction callback would leak
adapter mechanics while leaving the business guarantee undiscoverable.

Browser Authentication application services consume purpose-specific Store
facts rather than persisted transaction aggregates. Creation, Invitation
resolution, code issue, cancellation, and exchange expose only the identities,
callbacks, deadlines, Session, and credential expiries needed for the next
application decision. Persistence retains ownership of locking, PostgreSQL
time, proof destruction, and atomic terminal transitions.

## Interfaces and public surface

Interfaces normally live beside their consumer and expose only what it needs. Broad aggregates may exist at composition but are not daily dependencies. Prefer concrete types during construction and narrow interfaces at consumption.

Persistence is the exception: the bounded root `store.Store` and complete per-model or aggregate contracts live together in `store` for shared conformance testing.

Do not prefix interfaces with `I`. Add compile-time assertions where an adapter intentionally implements an important cross-package contract. Every substantive package has a package comment explaining what it owns, excludes, and may depend on.

Migrations are by vertical slice. A package or capability is introduced only when working code has a stable responsibility to inhabit it; bulk moves are prohibited.

`app.App` is the public application facade, not a persistence locator. Focused
services receive exact Store contracts during composition and App methods
delegate to them. The facade retains neither the root Store nor a Store
accessor; architecture tests reject restoration of either that locator or
production `App.Store()` traversal.

`app.New` remains the sole application constructor and exposes its fail-fast
order directly: validate shared dependencies, construct shared mechanics,
construct Identity, construct access and Academic Structure, construct
Examination authoring, construct profile and file behavior, construct optional
Jobs, then construct administration and bootstrap behavior before assembling
the facade. Private same-package recipes
retain the projection and wiring knowledge for those cohesive slices. Their
result values exist only during construction and are not runtime locators or
alternate application interfaces.

Readable ordered construction and selective child modules are useful
structural patterns, but another project's platform-service taxonomy is not a
package template. A child package is introduced only after the responsibility
is stable, can obey inward dependencies without a parent-package cycle, and
passes the deletion test. Concrete focused implementations remain unexported
unless a real cross-package consumer requires their type.

Internal interfaces are sized around one cohesive consumer need or an existing
atomic contract, not mechanically around every method and not as broad getter
aggregates. Test fakes remain beside their consuming focused service; Store
conformance and the real composition graph provide shared persistence and
wiring evidence.
