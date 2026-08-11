# Application

Use cases are direct methods with typed commands or queries and typed results:

~~~go
CreateAcademicUnit(ctx, invocation, command) (*model.AcademicUnit, error)
GetAcademicUnit(ctx, invocation, query) (*model.AcademicUnit, error)
~~~

`app.Invocation` is immutable and explicitly contains the principal and safe call metadata needed for authorization and audit. `context.Context` remains for cancellation, deadlines, and propagation, not hidden security dependencies.

Each actor-sensitive use case authorizes immediately before expensive work or mutation. HTTP and WebSocket establish credential and assurance requirements but do not preflight resource permissions or issue decision receipts.

Domain models own local invariants and transitions. Application services own use-case policy, authorization, multi-aggregate coordination, transaction intent, audit, and external effects. Focused service implementations are normally unexported behind `app.App` and consumer-owned interfaces.

Background jobs call application use cases with a system/service invocation. They do not manipulate stores directly.

The `app/job` child module owns the generic durable execution engine: immutable
descriptor validation, claiming, leases, fenced transitions, checkpoints,
work budgets, retry, cancellation observation, and bounded lifecycle. Domain
Job handlers remain beside their owning use cases and cross that seam through
typed, versioned documents and safe outcomes. The engine depends only on the
domain Job records and `store.JobStore`; it never imports its parent package or
selects infrastructure.

Operator Job use cases depend on the engine rather than a separate Job Store
and descriptor catalog. They retain Principal authorization, resource
resolution, durable audit ordering, and application-error translation; the
engine owns safe projections and descriptor-governed persistence mechanics.

Atomic store operations are explicit named aggregate operations such as bootstrap, enrollment transfer, or password-reset consumption. The application decides policy; the adapter owns locking, constraints, concurrency checks, and commit/rollback. Raw SQL transactions and generic `WithTransaction(func(Store))` callbacks do not cross into `app`.

The name and contract expose the complete atomic and race guarantee to callers
and reusable conformance tests. A generic transaction callback would leak
adapter mechanics while leaving the business guarantee undiscoverable.

## Interfaces and public surface

Interfaces normally live beside their consumer and expose only what it needs. Broad aggregates may exist at composition but are not daily dependencies. Prefer concrete types during construction and narrow interfaces at consumption.

Persistence is the exception: the bounded root `store.Store` and complete per-model or aggregate contracts live together in `store` for shared conformance testing.

Do not prefix interfaces with `I`. Add compile-time assertions where an adapter intentionally implements an important cross-package contract. Every substantive package has a package comment explaining what it owns, excludes, and may depend on.

Migrations are by vertical slice. A package or capability is introduced only when working code has a stable responsibility to inhabit it; bulk moves are prohibited.
