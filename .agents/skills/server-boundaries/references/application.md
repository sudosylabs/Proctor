# Application-boundary reference

Use cases are direct methods with typed commands or queries and typed results:

~~~go
CreateAcademicUnit(ctx, invocation, command) (*model.AcademicUnit, error)
GetAcademicUnit(ctx, invocation, query) (*model.AcademicUnit, error)
~~~

A use case takes `(ctx, invocation, command)` with an `XCommand` for a
mutation or an `XQuery` for a read, passed by value, and returns a domain model
or an `XResult`. Ports to infrastructure or another module that are not use
cases name their argument for that port, such as a renderer `RenderRequest`.

`invocation.Invocation` is immutable and explicitly contains the principal and safe call metadata needed for authorization and audit. `context.Context` remains for cancellation, deadlines, and propagation, not hidden security dependencies.

The `app/invocation` leaf owns that one call type so the parent and every Exam
child module share it without a parent import, and transports import it
directly. Every use case and consumer-owned port takes
`invocation invocation.Invocation`, and no module declares a local call type,
alias, or conversion between call types.
Actorless work carries `invocation.System` in the same position, which names
either the durable Job execution (`invocation.Job`) or a non-durable runtime
task (`invocation.Task`) such as the Attempt expiry scan, startup
reconciliation, or an offline host-operator command, and cannot impersonate a
User. An anonymous transport use case (installation status, access discovery,
external provider list, Desktop compatibility) still takes an Invocation: a
zero Principal with its request metadata. `TestUseCasesTakeAnInvocationFirst`
checks every exported App method; the few that are not use cases (credential
resolution, realtime propagation, composition attachment, child-module ports)
are listed there with their reasons.

Each actor-sensitive use case authorizes immediately before expensive work or mutation. HTTP and WebSocket establish credential and assurance requirements but do not preflight resource permissions or issue decision receipts.

Domain models own local invariants and transitions. Application services own use-case policy, authorization, multi-aggregate coordination, transaction intent, audit, and external effects. Focused service implementations are normally unexported behind `app.App` and consumer-owned interfaces.

Background jobs call application use cases with a system/service invocation. They do not manipulate stores directly.

The `app/idempotency` leaf owns the shared validation, canonical JSON, and
SHA-256 preparation algorithm for durable command idempotency, and reports its
failures as application errors: `Require` rejects a missing key with
`idempotency.key_required`, `Prepare` treats the key as optional, and both
report an invalid acting user as `idempotency.key_invalid` and an unencodable
semantic document as `request.invalid`. No module wraps it in a local
preparation helper. It does not own any business operation. Each command owner decides whether a key is required,
defines its stable operation name, normalizes its own semantic inputs, and
constructs the semantic document whose compatibility is preserved across
retries. The owning Exam service receives the bounded raw key and prepares
the persistence contract beside the command policy.

The `app/job` child module owns the generic durable execution engine: immutable
descriptor validation, claiming, leases, fenced transitions, checkpoints,
work budgets, retry, cancellation observation, and bounded lifecycle. Domain
Job handlers remain beside their owning use cases and cross that seam through
strict typed documents and safe outcomes. The engine depends only on the
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

Every application service, parent or child, is constructed the same way:
`New(Dependencies) (*Service, error)` in a child package, `NewX(XDependencies)
(*X, error)` for each service of the root `app/exam` package, and
`newX(xDependencies)` for the parent's unexported services. The constructor
validates every collaborator once and the service keeps them in a single
`deps` field, beside any state it derives; no service takes positional
collaborators. A type is a service when it holds a collaborator: an
interface, a function, or a pointer to a type with methods.
`TestServicesTakeOneDependencies` enforces the shape; only `app.New`, which
distributes its Dependencies to the services it embeds, keeps no `deps`. `app.App` embeds each Exam
service by its concrete type, through a parent alias such as
`examSittingService` that gives the embedded field a distinct name. A parent
adapter or Job runner names a narrow port only where its test substitutes a
fake.

Each Exam use case is the exported method on its owning service, named as the
public use case (`ScheduleExamSitting`), with the command, query, and result
shapes that the transports' ports require. Those shapes belong to the service, and
transports name them there (`sitting.ScheduleCommand`); the parent neither
aliases nor repeats them. The service method does all the boundary work: defensive input
copies, input normalization and defaults, deliberately narrower results, and
error concealment. Each Exam module declares one `publicFailures`
`apperror.Concealment`; a use case defers `publicFailures.Conceal` when its
target's existence is protected, or `Present` when it is not, so a denial or a
module's not-found code becomes `resource.not_found`. A use case whose outcome
the operations seam observes records it through the module's optional
`OperationalRecorder` after concealment.

Every application module, parent or child, reports expected public failures
with the one `*apperror.Error` type from the `app/apperror` leaf. Children
construct it directly with `apperror.New(code)` or `apperror.Invalid(code,
field)`; they declare no local fault type or per-package error helpers. A
use case's public failures pass through its module's declared
`apperror.Concealment`, and the parent adds nothing.
Typed Store failures are translated only through a declared
`apperror.StoreTable` keyed by Store error kind, entity, and constraint name;
a use case adds a small function before its table only for a typed Store
error that carries safe detail, such as a current-revision hint.

The `app/exam/manageraccess` leaf owns the shared current Exam Manager and exact
Academic Unit membership rule used to select ordinary or explicit override
Actions. It receives the caller's access projection and time, performs the
current membership lookup through its narrow port, and returns the selected
Action or lookup failure. It does not grant permission: each use case retains
input and projection validation, its precise Resource and authoritative
authorization call, error presentation, audit timing, and named Store command.
The same exact-unit membership check supports Exam creation and Manager-target
eligibility without implying either role permission or a Manager relationship.

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

The package is introduced with the first working vertical slice, not as an
empty architectural placeholder. Its `doc.go` must define the Exam, Draft,
Revision, Sitting, Attempt, Participation, Resource, Starter Workspace,
Workspace, Submission, Integrity, and Review vocabulary; state what the
package owns and excludes; and state its allowed inward dependencies. The
parent `app.App` embeds its services as the public surface and `app.New`
remains the sole application constructor. Detailed examination boundaries are in
[the `exam-lifecycle` reference](../../exam-lifecycle/references/examinations.md).

Realtime receives its required authentication invalidator and diagnostics at
construction. Its sink and peer fan-out are attached once before readiness
because the composition graph constructs the application before its WebSocket
and cluster adapters. The child depends only on the domain model and narrow
consumer-owned ports; it never imports its parent package or transport
packages. The parent does not redeclare the child's event, close-reason, sink,
fan-out, codec, or propagation contracts; its thin publication service exists
only to translate typed delivery failures into application errors. WebSocket
admission, principal revalidation and subscription authorization, is one
focused service that composes the authentication, Exam, Sitting, and Access
Control decisions.

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

An interface exists only where something substitutes behind it: a second
production implementation, a fake that the consumer's tests pass in, a
type-assertion or `errors.As` capability probe, or a consumer-owned port across
a module or dependency boundary that the consumer cannot name concretely, such
as a child-module port the parent implements, a transport port the application
implements, or a File Content capability. Otherwise the consumer holds the
concrete type. A local port that would repeat a Store contract or a child
module's owned contract uses that contract directly, and identical ports in
one package are declared once. Each port carries only the methods its
consumer calls or must pass on.

An adapter translates one concrete service, Store, or infrastructure value at
one seam. It never holds, embeds, or delegates to another adapter: when two
ports need the same translation, one adapter satisfies both, and port method
names converge so that it can. A type is an adapter by shape, not by name:
a struct that implements a port and whose methods mostly forward, in a few
statements, to one field it holds. Holding a port counts as holding an
adapter when every other production implementation of that port is one, so a
decorator over an adapter is a chain too. Supply a backend's lifecycle as
values to the one adapter (as `platform.CacheBackend` does) rather than as a
second adapter beneath it. Authorization ports share the single
`Authorize(ctx, Invocation, Action, Resource)` shape that the Access Control
service satisfies directly.

Persistence is the exception: the bounded root `store.Store` and complete per-model or aggregate contracts live together in `store` for shared conformance testing.

Do not prefix interfaces with `I`. Add compile-time assertions where an adapter intentionally implements an important cross-package contract. Every substantive package has a package comment explaining what it owns, excludes, and may depend on.

Every production function is reachable from a server binary. Code only tests
call is dead: delete it, or, when it is a seam its own package's tests need,
declare it in a `_test.go` file of that package. A seam that other packages'
tests construct through (the `server.NewForTesting` hook, configuration and
log fakes, fixture constructors) stays in production code only with a reason
in the architecture test's `reachableOnlyFromTests` list, which
`TestProductionFunctionsAreReachable` checks by rapid type analysis from every
`main` package.

Migrations are by vertical slice. A package or capability is introduced only when working code has a stable responsibility to inhabit it; bulk moves are prohibited.

## One application surface

Every use case is declared once, as an exported method on the focused service
that owns its policy, under its public use-case name (`CreateClass`, not
`Create`). `app.App` embeds each such service, so the App method set is the
union of their exported methods and the transports' consumer-owned ports are
satisfied through promotion. App declares no use-case method of its own:
neither a forwarder nor a wrapper that adds logic around a service call.

Work a wrapper used to add belongs in the owning service method: operational
outcome recording, error concealment, input normalization, defensive copies,
and result narrowing. A service whose construction is optional, such as Mail
or Job operations when the graph has no Job Store, answers its own
unavailability with a nil-receiver guard. A use case that needs several
services lives on the service that owns its policy and receives the others as
collaborators; it never reads another service's fields.

Every exported method of an embedded service joins the public surface. A
service therefore exports only its use cases and the methods that a
child-module port requires; ports inside `app` use unexported methods. Two
embedded services never export the same name, because Go silently drops an
ambiguous promoted method. App keeps a named field only for a runtime handle
that composition borrows, such as the Job engine, or a collaborator whose
methods must not join the surface.

This shape was chosen over inlining every service into App methods. The
transports already consume narrow ports that `*app.App` satisfies, and the
Exam children are separate services. Embedding removes the doubled
declaration while keeping those ports, each service's focused dependency set,
and the fakes its unit tests use.

`app.App` is not a persistence locator. Focused services receive exact Store
contracts during composition. App retains neither the root Store nor a Store
accessor; architecture tests reject restoring either that locator or
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
