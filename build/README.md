# Product build and development environment

The root Makefile is Proctor's product-level interface. It composes the four Go
modules, hosted Vite application, development infrastructure, observability,
runtime container, release archives, and certification gates. Module Makefiles
remain independently usable and do not depend on the product build.

Run `make help` for the current command surface. The main lifecycles are:

- `make run-server` validates the host toolchain, installs and compiles the
  hosted webapp, generates ignored development configuration and secrets,
  starts health-checked PostgreSQL, Redis, MinIO, Mailpit, Prometheus, Grafana,
  Loki, and the OpenTelemetry Collector, then runs the Go server in the
  foreground. Dependencies remain available after the server exits; use
  `make dev-down` to stop them or `make dev-reset` to remove their volumes.
- `make run-webapp` runs Vite with HMR and proxies API/WebSocket traffic to an
  independently running server.
- `make dev-seed` uses only the public HTTP API and locally captured Mailpit
  invitations to create a guarded synthetic Institution, administrator, Exam
  Manager, Candidate, academic structure, Exam Revision, and future Sitting.
  It refuses non-loopback servers, an Installation it did not initialize, and
  ambiguous partial state. Credentials and fixture identifiers are written
  mode `0600` below `.build/dev/seed`; a successful replay is read-only.
- `make run-cluster` builds the immutable runtime image, starts three active
  Proctor nodes plus shared dependencies and observability, puts HAProxy in
  front of their readiness endpoints, and follows application/gateway logs.
- `make cluster-smoke` certifies all three metrics targets, two peers per node,
  gateway readiness and build identity, then stops one node and verifies that
  the gateway remains usable before restoring full health.
- `make check`, `make integration`, and `make independent-modules` separate the
  hermetic, dependency-backed, and workspace-independence gates. `make ci`
  combines them with package verification.
- `make docs-start` serves the public documentation locally, while
  `make docs-check` validates its metadata, type-checks the site, synchronizes
  the generated OpenAPI artifact after proving it matches the human-authored
  YAML modules, and performs a strict static build. The root
  `make check` includes the docs gate.
- `make package`, `make dist`, and `make container` produce respectively a
  current-platform directory, deterministic Linux archives/checksums, and the
  minimal non-root runtime image.
- `make buildenv-check` runs the hermetic product gate inside the pinned Go and
  Node maintainer image; `make buildenv-shell` opens the same environment for
  diagnosis. Both run as the host uid/gid and mount the checkout at
  `/workspace`. Extra controlled Docker flags may be supplied through
  `PROCTOR_BUILDENV_RUN_FLAGS`.

Tracked defaults live in `build/make/config.mk`. Override one invocation with a
command-line value such as `make PROCTOR_POSTGRES_PORT=25432 run-server`, use an
environment value when no local override exists, or place persistent local
settings in the ignored root `config.override.mk`. Command-line values have
highest priority, followed by `config.override.mk`, environment values, and
tracked defaults.

All disposable state is below ignored `.build/dev`: generated full server
configuration, bootstrap and sealing keys, a development metrics certificate,
Prometheus target discovery, log files, collector checkpoints, and guarded
synthetic seed credentials and identifiers. No tracked configuration points at
an ignored file as an authority; the scripts recreate the state from the
canonical server example and tracked topology definitions. Configuration and
secret generation are idempotent and do not replace existing generated
secrets. `make dev-reset` removes the local seed files together with the
development containers and volumes.

The Compose files are deliberately layered:

- `dependencies.yaml` owns the development dependency lifecycle and persistent
  PostgreSQL, MinIO, and Mailpit volumes. Redis deliberately disables snapshots
  and AOF and uses tmpfs: Proctor's cache and authentication counters are
  disposable accelerators, while PostgreSQL remains the only authoritative
  recovery source. Persisting Redis would imply a durability guarantee the
  server architecture intentionally does not make;
- `observability.yaml` owns node-local scraping, dashboards, logs, and their
  persistent development volumes; and
- `cluster.yaml` adds only the three application nodes and HAProxy.

This differs from Mattermost where Proctor's product boundary differs. The
development stack does not add MySQL because PostgreSQL is Proctor's sole
authoritative store; it does not add Elasticsearch because server directory
search is PostgreSQL-backed and workspace-content search belongs to the
desktop product; and it does not run a local execution-host emulator because
the execution-host contract requires a real compatible `execenv` deployment.
Production examples likewise do not package a single-host database/cache/object
store bundle as “HA”: operators supply redundant infrastructure and a
redundant external load balancer for an active-active installation.

Go and Node base images are selected by exact version and immutable
multi-platform digest. Development-service images are also selected by exact
version and digest while remaining overridable for controlled upgrades. The
release archiver normalizes ordering, ownership, permissions, and timestamps;
`SOURCE_DATE_EPOCH` defaults to the source commit time. Release output must not
already exist, avoiding accidental reuse of a directory containing operator
configuration.
