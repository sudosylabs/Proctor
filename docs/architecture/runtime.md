# Runtime and operations

## Events, idempotency, and effects

Commit PostgreSQL before transient publication. Application events are
past-tense facts such as `ClassCreated` or `SessionRevoked` and carry only
minimal typed IDs, event time, safe revision, and approved metadata. They never
carry mutable entities, credentials, exam answers, arbitrary maps, or
transport-ready JSON.

Cache invalidation, WebSocket publication, and cluster fan-out use narrow ports
after commit. Use an atomic outbox only for confirmed durable delivery such as
queued mail, jobs, or external integrations—not ordinary realtime fan-out.

The application Realtime child module performs ordinary publication
synchronously and local-first, then attempts best-effort peer fan-out. It does
not buffer, retry, or make transient delivery durable. A peer-received message
applies only local effects and is never rebroadcast. Missing local delivery is
a no-op before attachment; missing peer fan-out is an ordinary delivery
failure for publication, while security invalidation applies every available
local effect and reports peer failure diagnostically.

For retryable commands, transport extracts and bounds the idempotency key, the
application command defines its meaning, and an atomic store operation records
principal, operation, request fingerprint, outcome, and expiry. Replaying the
same input returns the recorded outcome; different input with the same key is a
conflict. Persistence makes the behavior consistent across nodes and restarts.

Mutable aggregates use selective optimistic concurrency with explicit
revisions. Timestamps are not concurrency tokens.

## High availability

Several active application processes sharing one installation form an
active-active high-availability cluster. Full availability also requires
redundant database, storage, cache where used, provider, and load-balancer
infrastructure.

Nodes keep no durable business state locally. They use stable runtime IDs,
compatible versions/configuration, bounded graceful shutdown, schema migration
as a separate deployment step, and shared VFS in clustered production. Truly
singleton maintenance may use a PostgreSQL advisory lock; durable jobs prefer
database-backed work claiming over broad leader election.

## Cluster transport

`cluster/local` is the valid single-node adapter. `cluster/memberlist` is the
peer-to-peer multi-node adapter. Redis is optional disposable cache
infrastructure and is not required for clustering.

Memberlist mode:

- is explicitly selected rather than auto-detected;
- discovers bootstrap peers through short-lived PostgreSQL leases, combining
  configured static seeds first with compatible live discovery seeds;
  discovery uses a narrow store contract and never SQL adapter types or
  application-message payloads;
- requires authenticated gossip encryption, an explicit shared key, safe
  bind/advertise addresses, and key rotation through Memberlist's keyring;
- validates the key, addresses, discovery, shared VFS, and other prerequisites
  before readiness;
- advertises the server version and supported cluster-protocol range;
- carries a protocol version on every message, rejects incompatible peers
  before readiness, and tolerates safe unknown message types/fields between
  adjacent compatible versions for rolling upgrades; and
- provides best-effort, non-durable delivery.

The Memberlist adapter contains a private discovery-maintenance module. It owns
lease construction, initial advertisement and rollback, compatible discovery
seed selection, periodic renewal, expired-row cleanup, and graceful
withdrawal. The transport remains the sole lifecycle owner: it creates and
joins Memberlist, owns cancellation and the maintenance goroutine, and waits
for owned maintenance to terminate before persistence can close. The supplied
deadline bounds graceful leave and lease withdrawal and is reported when
exhausted; the concrete Memberlist shutdown call is synchronous and is not
abandoned merely to return at the deadline.

Startup creates Memberlist, advertises the local lease, selects seeds, attempts
the join, and validates joined node identities and protocol ranges before
readiness. Initial advertisement and seed-listing failures are fatal. A join
failure is diagnostic and nonfatal because discovery is eventual; this does
not imply a background rediscovery or rejoin loop. A failure after the initial
advertisement succeeds—including seed listing or admission—withdraws the local
lease best-effort and shuts Memberlist down before returning the primary error.
Maintenance renews before cleaning expired rows on each tick, continues after
either disposable-store failure, and does not determine readiness.

Shutdown first makes transport operations terminal, cancels and waits for
maintenance, leaves and shuts down Memberlist, and then withdraws the lease
while the supplied stop context remains usable. Deadline exhaustion is
returned after owned maintenance and synchronous Memberlist shutdown are safe;
graceful withdrawal remains best-effort.

One handler owns each typed event on a node. `Broadcast` sends to peers only;
`SendToNode` may target the current node. Messages are bounded and cloned for
handlers; panics are contained and payload data is not logged. Handlers are
idempotent and cluster-received realtime events are never rebroadcast.

Realtime peer event names and JSON payloads are stable application-propagation
contracts carried inside the versioned cluster envelope. The Realtime child
module owns those codecs and verifies their exact compatibility; it does not
add a competing payload-version field. Registering its complete handler set is
part of node construction. Any registration failure is terminal for that
construction attempt, and the composition root unwinds the partially built
node before readiness rather than retrying attachment on the same instance.

Cluster delivery is an accelerator, not a correctness authority. PostgreSQL,
bounded cache TTLs, periodic revalidation, and client resynchronization recover
missed or duplicate notifications. The detailed delivery, authentication-cache,
and recovery contract is [Cluster delivery guarantees](../../server/cluster/GUARANTEES.md).

## Lifecycle and observability

The composition root starts dependencies before consumers and marks readiness
only after mandatory dependencies are usable. Shutdown reverses ownership under
bounded deadlines. Every goroutine, client, queue, channel, and closer has an
owner and shutdown path; request/event fan-out is bounded with an explicit
backpressure, drop, or disconnect policy.

Operational logging and telemetry follow
[Security and privacy](./security.md#logging-and-observability). Liveness says
the process is functioning; readiness says it can safely receive traffic;
detailed dependency diagnostics are authorized/operator-only.

## Naming and files

- Packages are short, lowercase, singular responsibilities.
- Avoid vague `util`, `common`, `shared`, `base`, `core`, `services`, and
  `repositories` packages.
- Protocol names such as `oidc`, `cas`, and `memberlist` are valid concrete
  adapter package names.
- Organize files by responsibility or vertical slice, not arbitrary size.
- Avoid catch-all `helpers.go`, `utils.go`, `common.go`, and `types.go`.
- Preserve `ID`, `URL`, `HTTP`, `API`, `SQL`, `MFA`, `VFS`, `OIDC`, and `CAS`.
- Use `New` for the primary exported construction, precise `New<Type>` only for
  several public constructions, and `new<Type>` for internal services.
- Use options only when absence has defined semantics; prefer domain verbs to
  vague `Process`, `Handle`, `Execute`, or `Manage`.

Application capabilities start as cohesive files in `app`. Extract a child
package only after it has substantial structure, a stable responsibility,
several collaborators, and a narrow facade; it never imports its parent.

## Testing and CI

- Domain tests prove constructors, transitions, and invariants.
- Application tests use small handwritten fakes/spies for consumer-owned ports.
- Store and infrastructure adapters share conformance suites.
- HTTP and WebSocket tests prove their DTO, authentication, authorization,
  error, sequencing, replay, and backpressure contracts.
- Integration tests use the real `server.New` graph through `testlib`.

External-package tests are preferred for exported contracts. Same-package
tests are reserved for important unexported logic. Tests name observable
behavior, and pure isolated tests use `t.Parallel()`.

Ordinary `go test ./...` is network-free. PostgreSQL, Redis, SMTP, S3, and
multi-node suites use the `integration` build tag and dedicated Make targets;
an invoked integration target fails rather than silently skipping a missing
dependency. Docker test environments pin images, bind loopback ports, isolate
names and state, wait for health, and clean up on failure.

The hermetic `make -C server check` gate covers unit/race/vet, production import
boundaries, OpenAPI/route/error agreement, and portable documentation links.

## Architecture migration acceptance

The required architecture migration was accepted on 2026-08-08. The acceptance
run verified the module-root composition graph, an empty dependency-debt
ledger, inward production imports, OpenAPI/runtime agreement, the hermetic
server gate, and independent module checks. This dated result is historical
evidence; current status and unresolved product work live in
[`docs/project/status.md`](../project/status.md).

The migration deliberately used vertical slices rather than a big-bang package
rewrite. Conceptual boundaries, consumer-owned ports, named atomic operations,
transport DTOs, root-selected infrastructure, constrained store layers,
versioned APIs, built-in Memberlist clustering, and persisted idempotent
outcomes are now the rules documented across this guide.

## Illustrative boundary examples

~~~go
// Correct: transport depends on a narrow application capability.
type academicUnits interface {
    CreateAcademicUnit(context.Context, app.Invocation, app.CreateAcademicUnitCommand) (*model.AcademicUnit, error)
}

// Incorrect: transport reaches persistence and bypasses policy/audit.
type API struct {
    store store.Store
}
~~~

~~~go
// Correct: the durable guarantee is explicit.
result, err := enrollmentStore.Transfer(ctx, transfer)

// Incorrect: the business guarantee is hidden behind adapter mechanics.
store.WithTransaction(ctx, func(tx store.Store) error { return nil })
~~~
