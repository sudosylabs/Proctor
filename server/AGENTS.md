# Server agent guide

This file applies to the `server` module. Root [`AGENTS.md`](../AGENTS.md)
remains authoritative for repository-wide rules.

## Scope

The server owns Proctor-specific domain models, application use cases,
authorization, identity, transports, persistence, clustering, configuration,
and runtime composition. Reusable cache, mail, and VFS behavior belongs in the
independent modules under `packages/`.

Before changing the server, load the relevant architecture topic from
[`docs/architecture/`](../docs/architecture/) and any exact component contract:

- HTTP routes, DTOs, errors, or OpenAPI:
  [`app/api/CONTRACT.md`](app/api/CONTRACT.md)
- cluster delivery or recovery: [`cluster/GUARANTEES.md`](cluster/GUARANTEES.md)
- current capabilities and open decisions:
  [`docs/project/status.md`](../docs/project/status.md)

## Local rules

- Preserve the inward production graph documented in
  [`docs/architecture/dependencies.md`](../docs/architecture/dependencies.md),
  including the selective `app/job` and `app/realtime` child modules. Child
  modules never import their parent package or concrete transports.
- Keep concrete adapter selection in module-root package `server`.
- Keep `platform.Service` out of application services and use narrow
  consumer-owned ports; store contracts are the deliberate grouped exception.
- Keep ordinary `go test ./...` network-free. External services use the
  `integration` build tag and dedicated Make targets.
- Use the real `server.New` graph through `testlib` for wiring confidence; do
  not create a second composition path.
- Preserve AGPL notices and record exact upstream provenance in
  [`NOTICE`](NOTICE) when adapting source.

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
