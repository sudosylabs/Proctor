# Runtime and operations reference

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

## Examination runtime

Exam scheduling, closing, workspace sealing, and integrity settlement follow
the durable Job and effect rules above. Jobs open a Scheduled Sitting, enter
Closing at `ScheduledEndAt`, and resume bounded finalization after
interruption. A sealing occurrence pages 100 Attempt identities at a time and
reserves at most 1,000 units; a permanent successor continues a larger Sitting.
The checkpoint records the stable Attempt cursor and bounded counts after each
seal. A durable reservation observed ahead of its checkpoint is consumed as
uncertain work on retry rather than reused, preserving the hard occurrence cap
across process loss. Daily lifecycle recovery also recreates missing Closing
work. Participation expiry instead uses a bounded, non-durable periodic runtime
task owned by the application Job engine lifecycle. It creates no Job
occurrence or permanent deduplication ledger. Both mechanisms call application
use cases with fenced durable state; neither infers authority from an in-memory
connection or cluster notification.

WebSocket liveness, authenticated Attempt Participation renewal, and native
process health are separate signals. A successful connection response gives
the privileged external client coordinator the server-owned renewal interval.
The coordinator schedules explicit authenticated renew requests for that
Participation generation and waits for their acknowledgements; neither
transport ping nor a server timer renews it automatically. The initial contract
is renewal every 5 seconds, a 20-second PostgreSQL-time lease, and a 2-second
expiry scan. A single failed request does not prove loss. A late renewal or the
bounded periodic runtime task instead claims an expired lease through the same
idempotent operation, permanently fences the generation, records the Connection
Loss Flag and automatic suspension, and only then publishes transport and
manager effects.

Every committed Attempt Connection open or close and every created Integrity
Flag produces a bounded manager-facing realtime fact after PostgreSQL commit.
The event contains safe identifiers and state only. Delivery is best-effort;
authorized managers refetch authoritative state after missed, duplicate, or
resynchronization events. A newly committed Focus Loss warning targets only the
candidate; a policy suspension additionally publishes the durable Connection
close and separate manager/candidate suspension facts. Replayed Focus Loss
outcomes publish nothing, and transient publication failure cannot roll back or
repeat the committed policy decision. Live instruction/resource correction
likewise commits the new immutable Revision and Sitting retarget before
notifying candidates. The complete lifecycle and correction contract is
the [`exam-lifecycle` skill](../../exam-lifecycle/SKILL.md).

## Client-facing HTTP and TLS

The root HTTP-serving module owns the complete client-facing listener lifecycle
behind one `Serve`/`Shutdown`/`Close` interface. Deployment selects `disabled`,
`static`, or `lets_encrypt` TLS mode. Static mode loads an operator-provided
certificate and private key. Let's Encrypt mode uses one exact DNS hostname
from `Server.PublicURL`, accepts the ACME terms, and persists account and
certificate material in a node-local directory that must be private and
writable before readiness. Built-in TLS requires TLS 1.2 or newer and never
reuses Memberlist key material. Automatic-certificate maintenance starts only
inside HTTP serving; shutdown cancels issuance and renewal and waits for its
owned background work before the serving lifecycle returns.

Optional HTTP-to-HTTPS forwarding is part of the same module rather than an
untracked sibling process. The HTTP listener binds before the HTTPS listener is
accepted for readiness, serves ACME HTTP-01 challenges when applicable, and
otherwise returns a permanent redirect to the configured public authority
while preserving the request path and query. It never derives the redirect
authority from the untrusted request `Host`. Startup failure of either listener
closes the other; graceful and forced shutdown stop both.

Let's Encrypt cache and issuance are deliberately node-local and are not
coordinated through PostgreSQL, Memberlist, or shared storage. Consequently,
`lets_encrypt` is rejected in Memberlist mode. An active-active installation
terminates public TLS and performs HTTP forwarding at its redundant load
balancer. Operators may use static TLS independently on each application node
when they require encryption from the load balancer to the node; certificate
distribution and trust remain operator infrastructure concerns.

## High availability

Several active application processes sharing one installation form an
active-active high-availability cluster. Full availability also requires
redundant database, storage, cache where used, provider, and load-balancer
infrastructure.

Nodes keep no durable business state locally. They use stable runtime IDs,
compatible versions/configuration, bounded graceful shutdown, locked automatic
forward schema migration, and shared VFS in clustered production. Truly
singleton maintenance may use a PostgreSQL advisory lock; durable jobs prefer
database-backed work claiming over broad leader election.

Startup requires an operator-owned JSON configuration. Resolution is explicit
path, then `PROCTOR_CONFIG`, then `config/config.json`; no active file is
generated or silently replaced by in-memory defaults. Release bundles carry
`config/config.example.json` for the operator to copy and edit. Deployment JSON
uses PascalCase field names while environment overrides retain stable
`PROCTOR_` names.

The disposable application cache is either a bounded per-process memory LRU or
Redis. The independent store read-through cache is always a small bounded local
LRU and uses cluster messages only for best-effort invalidation after durable
commits. Neither cache is authoritative. Startup logs the selected application
cache, store cache, VFS, cluster, mail, execution-host, external-authentication,
configuration-source, and schema-migration state without emitting secrets.

## Cluster transport

`cluster/local` is the valid single-node adapter. `cluster/memberlist` is the
peer-to-peer multi-node adapter. Memberlist does not use Redis as a transport,
but Proctor requires the Redis application-cache backend in Memberlist mode so
installation-wide disposable authentication counters remain coherent across
nodes.

Memberlist mode:

- is explicitly selected rather than auto-detected;
- discovers bootstrap peers through short-lived PostgreSQL leases, combining
  configured static seeds first with compatible live discovery seeds;
  discovery uses a narrow store contract and never SQL adapter types or
  application-message payloads;
- requires authenticated gossip encryption, an explicit primary key, safe
  bind/advertise addresses, and a bounded fallback decryption ring for rolling
  key rotation;
- validates the key, addresses, discovery, Redis cache, shared VFS, and other
  prerequisites before readiness;
- advertises the server version and the cluster-protocol range compiled into
  the binary; operators cannot claim wire versions the binary does not encode;
- carries a protocol version on every message, rejects incompatible peers
  before readiness, and tolerates safe unknown message types/fields between
  adjacent compatible versions for rolling upgrades; and
- provides best-effort, non-durable delivery.

The configured advertise address may use a stable private DNS name. At startup
the adapter resolves that name to the concrete IP required by Memberlist while
retaining the stable configured name in PostgreSQL discovery. Resolution is
fail-closed and prefers IPv4 when DNS returns both families. This keeps
orchestrator-assigned container IPs out of the deployment contract without
changing seed discovery semantics.

The Memberlist adapter contains a private discovery-maintenance module. It owns
lease construction, initial advertisement and rollback, compatible discovery
seed selection, periodic renewal, expired-row cleanup, rediscovery, and
graceful withdrawal. The transport remains the sole lifecycle owner: it
creates and joins Memberlist, owns cancellation and the single maintenance
goroutine, and waits for owned maintenance to terminate before persistence can
close. Rediscovery re-lists compatible live leases and attempts a rotating
batch of at most three candidate addresses per tick; configured seeds take
precedence and the combined candidate set is bounded at 64. The supplied
deadline bounds graceful leave and lease withdrawal and is reported when
exhausted; the concrete Memberlist shutdown call is synchronous and is not
abandoned merely to return at the deadline.

Startup creates Memberlist, advertises the local lease, selects seeds, attempts
the join, and validates joined node identities and protocol ranges before
readiness. Initial advertisement and seed-listing failures are fatal. A network
join failure is diagnostic and nonfatal because periodic rediscovery retries
candidate peers. An admission rejection remains fatal during startup. A failure
after the initial advertisement succeeds—including seed listing or
admission—withdraws the local lease best-effort and shuts Memberlist down
before returning the primary error. Maintenance renews before cleaning expired
rows, re-lists compatible peers, then attempts bounded rejoin on each tick.
Disposable-store and rejoin failures are diagnosed independently and later
ticks continue; they do not determine readiness.

Peer admission is continuous rather than startup-only. Memberlist alive and
merge callbacks reject malformed metadata, identity mismatches, duplicate
remote identities in a merge, blank server versions, and protocol ranges that
do not include a wire version supported by this binary. Memberlist's
incarnation logic resolves a stable node ID's old address during immediate
restart; conflicting live addresses are refused and diagnosed without exposing
peer metadata. After a crash, a dead node name becomes reclaimable at a new
address only after one full discovery TTL; periodic rediscovery retries during
that safety window. Local metadata that cannot fit Memberlist's fixed bound is
rejected during construction and is never truncated into malformed JSON.

Memberlist `EncryptionKey` is the primary key used for new gossip traffic and
`DecryptionKeys` contains at most eight fallback keys. Rotation is a staged,
restart-required deployment operation: first add the new key as a fallback on
every node; then make it primary while retaining the old key as a fallback on
every node; finally remove the old fallback after the fleet is converged.
Skipping the overlap stage partitions nodes that cannot decrypt one another.

Shutdown first makes transport operations terminal, cancels and waits for
maintenance, leaves and shuts down Memberlist, and then withdraws the lease
while the supplied stop context remains usable. Deadline exhaustion is
returned after owned maintenance and synchronous Memberlist shutdown are safe;
graceful withdrawal remains best-effort.

One handler owns each typed event on a node. `Broadcast` sends to peers only;
`SendToNode` may target the current node. Messages are bounded and cloned for
handlers; panics are contained and payload data is not logged. Handlers are
idempotent, perform bounded local work, and do not place durable or network
work on Memberlist's receive path. Cluster-received realtime events are never
rebroadcast. Broadcast is an O(peer-count) best-effort loop with context
cancellation checked between peers; there is no generic outbound queue or hard
membership limit. The transport is intended for modest institutional node
counts, and larger topologies require capacity testing against their actual
event rate and network.

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
and recovery contract is
[Cluster delivery guarantees](../../../../server/cluster/GUARANTEES.md).

## Lifecycle and observability

The composition root starts dependencies before consumers and marks readiness
only after mandatory dependencies are usable. Shutdown reverses ownership under
bounded deadlines. Every goroutine, client, queue, channel, and closer has an
owner and shutdown path; request/event fan-out is bounded with an explicit
backpressure, drop, or disconnect policy.

Operational logging and telemetry follow
the [`authorization-audit` security reference](../../authorization-audit/references/security.md#logging-and-observability). Liveness says
the process is functioning; readiness says it can safely receive traffic;
detailed dependency diagnostics are authorized/operator-only.

The `metrics` module owns one private Prometheus registry and one optional,
node-local scrape listener. It starts after shared infrastructure and before
public readiness, reports readiness as zero during startup and shutdown, and
is closed after Jobs and public transports but before `platform.Service`.
Binding, TLS-identity, and unexpected serving failures are terminal to
`Server.Run`; metrics never run as an untracked sibling process. The endpoint
exposes only `GET /metrics`, never profiling handlers. Loopback is the safe
default. A non-loopback bind is rejected unless static TLS and bearer
authentication are both configured. Prometheus scrapes each node separately;
Memberlist does not aggregate or forward metrics.

The registry covers Go/process/build/readiness and scrape health; HTTP route
templates, sizes, SQL pools, complete store timing/retries, logging drops, and
local store-cache decisions; WebSocket messages, publication fan-out, replay,
subscriptions, and backpressure; Memberlist message flow, membership,
discovery/rejoin, and admission; durable Job claim, queue, lease, checkpoint,
completion, recurrence, and periodic work; execution-host state/capacity and
streams; VFS outcomes, sizes, streams, and bytes; shared-cache/Redis outcomes,
latency, and bytes; SMTP stages and durable mail delivery/queue/health; and
named authentication, authorization, realtime, and examination outcomes.
Each subsystem owns a narrow recorder or transparent wrapper. The application
has no Prometheus dependency and there is no global telemetry service locator;
its one-method recorder accepts only application-constructed bounded events.
Labels come from sealed route templates, closed operation and outcome sets,
configured backend names, registered cluster events, registered Job/periodic
work names, mail template/state vocabularies, and named application events.
Resource IDs, principals, addresses, paths, cache keys, message data, mail
recipients, and error text are forbidden labels.

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

The root product build is the stable repository interface for complete local
runtime, three-node certification, observability, container construction, and
release packaging; module Makefiles remain independently runnable. Generated
development state stays below ignored `.build`, while tracked Compose,
Prometheus/Grafana/Loki/collector, gateway, Docker, and deployment definitions
remain reproducible inputs. Release archives normalize file order, ownership,
permissions, and source-commit timestamps and refuse to replace an existing
output directory.

The hermetic `make -C server check` gate covers unit/race/vet, production import
boundaries, OpenAPI/route/error agreement, and portable documentation links.

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
