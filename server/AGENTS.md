# Server agent guide

This file applies to the `server` module. Root [`AGENTS.md`](../AGENTS.md)
remains authoritative for repository-wide rules.

## Scope

The server owns Proctor-specific domain models, application use cases,
authorization, identity, transports, persistence, clustering, configuration,
and runtime composition. Reusable cache, mail, and VFS behavior belongs in the
independent modules under `packages/`.

Before changing the server, load the matching repository skill and any exact
component contract:

- HTTP routes, DTOs, errors, or OpenAPI:
  [`httpapi/CONTRACT.md`](httpapi/CONTRACT.md)
- cluster delivery or recovery: [`cluster/GUARANTEES.md`](cluster/GUARANTEES.md)

## Local rules

- Preserve the inward production graph documented by the
  [`server-boundaries` skill](../.agents/skills/server-boundaries/SKILL.md),
  including the selective `app/job` and `app/realtime` child modules. Child
  modules never import their parent package or concrete transports.
- Keep concrete adapter selection in module-root package `server`.
- Keep `platform.Service` out of application services and use narrow
  consumer-owned ports; store contracts are the deliberate grouped exception.
- Keep ordinary `go test ./...` network-free. External services use the
  `integration` build tag and dedicated Make targets.
- Use the real `server.New` graph through `testlib` for wiring confidence; do
  not create a second composition path.
- Preserve the applicable file-level license header and upstream notices
  described by [`LICENSING.md`](../LICENSING.md), and record exact upstream
  provenance in [`NOTICE`](NOTICE) when adapting source.

## Conventions

Each row is the one answer for its question. Follow it in new and changed
code; the linked authority holds the detail, and the named check enforces it.

| Question | Answer | Authority / check |
| --- | --- | --- |
| Application error type | One `*apperror.Error`, built in application code with `apperror.New(code)` or `apperror.Invalid(code, field)`; Store failures become codes only through a declared `apperror.StoreTable`. Transports mint only kernel codes. | [http-api](../.agents/skills/http-api/SKILL.md); `make -C server errors-check` |
| Error code spelling and status | `<domain>[.<subdomain>].<condition>`, lowercase dot-separated `snake_case`, listed once in `httpapi/error_catalog.yaml`; the condition word decides the HTTP status and `error_mapping.go` is generated. | [`httpapi/CONTRACT.md`](httpapi/CONTRACT.md); `errors-check` |
| Entity identifiers | A typed model ID (`model.UserID`, …) from the point it is parsed; path IDs go through `Params.Require<Entity>ID`. A plain `string` checked with `model.IsValidOpaqueID` only for references whose entity type is chosen at runtime. | [persistence](../.agents/skills/persistence/SKILL.md) |
| Wire timestamps | RFC 3339 strings formatted by `model.FormatRFC3339`, in bodies and query parameters; absent optional instants are omitted. Lifecycle members are `<verb>ed_at` (`created_at`, `updated_at`, `archived_at`); scheduled bounds keep `start_at`, `end_at`, `expires_at`. | [`httpapi/CONTRACT.md`](httpapi/CONTRACT.md); OpenAPI agreement tests |
| Create request members | Never server-owned `id` or timestamps, nor parent identifiers the route path already fixes; strict decoding rejects them. | [`httpapi/CONTRACT.md`](httpapi/CONTRACT.md) |
| Use case invocation and idempotency | Every use case takes `invocation.Invocation` (actorless work takes `invocation.System`); command idempotency is prepared only by `app/idempotency` `Require` or `Prepare`. | [server-boundaries](../.agents/skills/server-boundaries/SKILL.md) |
| Service construction | `New(Dependencies)` with one `deps` field; no positional constructors. | server-boundaries |
| Application surface | Each use case is declared once, as an exported method on the service that owns its policy; `app.App` embeds the services and declares no use case of its own. | server-boundaries; `TestApplicationSurfaceIsTheEmbeddedServices` |
| Interfaces | Only where something substitutes behind it: a second implementation, a fake the consumer's tests pass in, a capability probe, or a consumer-owned port across a module boundary. Otherwise hold the concrete type. | server-boundaries |
| Adapters | An adapter wraps one concrete service, Store or infrastructure value and never another adapter. | server-boundaries; architecture adapter test |
| Authorization ports | One shape, `Authorize(ctx, Invocation, Action, Resource) error`, plus named helpers. | [authorization-audit](../.agents/skills/authorization-audit/SKILL.md) |
| Commands and queries | Use cases take `XCommand`/`XQuery` by value and return a model or `XResult`. Store named operations take one pointer input named for entity and operation (`ClassUpdate`); revision fencing is `ExpectedRevision`; idempotency is a trailing `*store.CommandIdempotency`. | persistence |
| Audit | A security-sensitive mutation has one Store operation that requires its audit input, using the attempt protocol (`AuditEventID` + `AuditAt`, completed in the same transaction) or the draft protocol (`AuditEvent`, inserted once). Action names exist only as `model.Action` constants. | authorization-audit; action catalog tests |
| Time authority | PostgreSQL time decides validity, expiry, deadlines and leases; a mutation never records an instant earlier than the lifecycle instants it supersedes on the row (`GREATEST`). | persistence ("Time authority") |
| Where types live | `model` holds domain types; it carries json tags only for a canonical serialization it owns (digest-bound documents, delivery agreements, verbatim replay outcomes, Job payloads). DTOs live in `httpapi`/`websocket`, persisted spellings in `sqlstore`, query-shaped results in `store`. | persistence; server-boundaries |
| Model failures | A package sentinel matched with `errors.Is`, or a `*ValidationError` from `invalidModelError`. Closed vocabularies answer `IsValid() bool`; structured values answer `Validate() error`. | `model` package doc |
| File layout | `store/store.go` holds only `Catalog` and `Store`; each contract lives in its topic file. `config/config.go` holds the root; each section has its own file. A file is named for the singular concept it holds. | architecture naming test |
| Naming | One 1-3 letter receiver per type; Go identifiers spell `ID`; Store implementations are exported `SQL<Model>Store`; `server/app` is imported as `app`; `app/job` is the Job engine and `app/jobhandler` the handlers. | architecture naming test |
| SQL idiom | `?` placeholders (`$N` only for positional reuse); `errors.Is(err, sql.ErrNoRows)`; advisory locks only through `lockAdvisory` on an `advisoryLockKey`. | persistence ("SQL idiom") |
| Tests | Assert through testify `require` (`assert` only where continuing is useful or off the test goroutine), one assertion per property, expected value first, called directly; run case tables as named `t.Run` subtests; assert outcomes; a fake's recorded call log (`events`, `order`, `calls`) is asserted whole with `require.Equal` and a comment naming the ordering contract, never by membership or subsequence. | [`README.md`](README.md) "Write tests"; `architecture` test idiom test; `testifylint` |
| Exported documentation | Every exported identifier in `app`, `httpapi` and `store/sqlstore` has a doc comment. | `.golangci.yml` revive `exported` (new and changed code) |
| Wire DTO members | Snake_case json names on every exported member; instants are RFC 3339 `string` (or `time.Time`). | architecture wire-name test |
| Foreign keys | Every baseline foreign key is led by an index on its columns, or is listed with a reason. | `store/sqlstore` foreign-key index test |

## Verification

Use the narrowest target that covers the change. The hermetic full gate is:

```sh
make -C server check
```

Architecture and documentation changes use:

```sh
make -C server architecture
```

PostgreSQL, provider, Redis, and realtime integration targets are documented in
[`server/README.md`](README.md).
