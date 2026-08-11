# Transport

## HTTP

`app/api` owns routing, request/response DTOs, strict decoding, authentication/assurance wrappers, Problem Details, and OpenAPI agreement.

`api.New` compiles and seals one immutable route catalog before exposing the HTTP handler. Cohesive resource modules declare typed paths, operations, authentication requirements, DTO mappings, and allowed public errors without receiving mutable router types. All ordinary HTTP resources use this kernel. The WebSocket upgrade is the sole remaining private compatibility-bridge route pending completion of the catalog migration; the bridge is not an extension seam. Every route has an explicit authentication classification.

Ordinary handlers return a typed status/body result and `error`. Central code validates the complete result before writing JSON, headers, cookies, or Problem Details. Redirects, bounded uploads, binary downloads, and upgrades use named, protocol-specific result types recorded in the route manifest; resource modules never receive an unrestricted response writer.

Resource paths use kernel-owned literal and parameter constructors. Canonical IDs and provider IDs are closed parameter kinds; resource modules do not supply arbitrary regular expressions. The catalog rejects invalid authentication requirements, unmapped or duplicate public errors, and duplicate normalized method/path shapes before serving.

Mutable domain entities never double as wire DTOs. Command decoding:

- applies body limits first;
- accepts exactly one JSON value;
- rejects unknown fields and trailing data;
- uses `Optional[T]` for omitted, zero, and explicit-null PATCH states;
- permits unknown keys only inside a named bounded extension object.

Dedicated DTOs prevent domain/persistence evolution from silently changing the
public API or exposing a newly added sensitive field. This is a deliberate
boundary even when an upstream design serializes shared model objects.

Use plural kebab-case paths and `snake_case` JSON, query, and path-variable names. Collections return an object with non-null `items` and optional `next_cursor`. Cursors are opaque, versioned, untrusted keysets; growing offsets are not used for unbounded collections.

A reviewed, checked-in OpenAPI document is the public contract. CI compares it with routes, authentication classifications, DTOs, and error mappings. Generate clients and documentation from OpenAPI, never handlers or domain models.

Stable API versions evolve additively. Existing routes, fields, meanings, and
error codes are not removed or repurposed; clients tolerate unknown response
fields; deprecations are documented before removal. New required inputs or
changed semantics require a new version. Pre-stable behavior may change only
when release notes explicitly state that compatibility is not yet promised.

The transport extracts and bounds idempotency keys, while the application
command defines their meaning and persistence atomically records the outcome.
This keeps retry behavior consistent across nodes and process restarts.

The exact route, authentication, DTO, error, and OpenAPI agreement rules live
in the component-owned [HTTP API contract](../../server/app/api/CONTRACT.md).

## WebSocket

The sibling `websocket` package owns the hub, connection state, upgrade handler, wire DTOs, versioned errors, sequencing, replay, liveness, and backpressure. HTTP mounts its handler but does not own its protocol.

WebSocket reuses stable application error codes and safe fields inside its own versioned envelope, not HTTP Problem Details. It maps transport-neutral application events into versioned wire events.

Each connection belongs to one node and maintains an immutable principal,
connection ID, monotonic sequence, bounded outbound/replay queues, liveness,
subscriptions, and backpressure policy. Reconnection can replay only state
retained by the current node; otherwise the server creates a fresh connection
and instructs the client to resynchronize authoritative state over HTTP.

Cookie-authenticated upgrades require an exact configured public-origin match;
bearer-authenticated native clients may omit `Origin`. Subscriptions carry an
explicit action/resource pair and pass through the application authorization
boundary. Role, binding, assurance, account, and session changes invalidate
affected connections. Cluster-received events are delivered locally without
rebroadcast, preventing loops.

## Errors and validation

Application methods return standard `error`. Expected public failures use `*app.Error` with a stable dotted domain code, explicitly safe fields, and an optional wrapped cause. They contain no protocol status, localization, request ID, SQL detail, or stack trace.

~~~text
driver failure
    ↓
typed store/port failure
    ↓
domain/application interpretation
    ↓
app.Error for an expected public failure
    ↓
HTTP Problem Details or WebSocket protocol error
~~~

HTTP and WebSocket each maintain exhaustive mappings. Unknown failures become generic correlated internal errors. Unexpected failures preserve their cause and are logged once at the outer operational boundary.

Validation ownership is divided:

- transport: encoding, shape, required wire fields, and size;
- application: use-case prerequisites and authorization;
- domain: local invariants and transitions;
- atomic store/database: cross-row and concurrency invariants.

`panic` is reserved for impossible initialization invariants and test-only `Must*` helpers. Long-running boundaries recover unexpected panics without exposing diagnostics.
