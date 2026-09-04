# Product build and development environment

The root Makefile is Proctor's product-level interface. It composes the four Go
modules, hosted Vite application, development infrastructure, observability,
runtime container, release archives, and certification gates. Module Makefiles
remain independently usable and do not depend on the product build.

Run `make help` for the current command surface. The main lifecycles are:

- `make bootstrap` validates the host and installs the repository-pinned Go
  tools below `.build/bin` plus both locked npm dependency trees. The separate
  `make tools` and `make tools-check` targets install and verify only the Go
  toolchain described by `build/tools/go.mod` and
  `build/tools/gopls/go.mod`.
- `make fmt`, `make fmt-check`, and `make lint` apply or verify versioned and
  unignored Go source against the formatting and new-finding static-analysis
  policy. `make lint-config` verifies only the linter configuration;
  `make lint-full` reports the pre-existing analyzer backlog without weakening
  the changed-line gate. `make quality` combines read-only formatting and lint gates;
  `make vulncheck` queries the current Go vulnerability database, and
  `make security` combines linting with that live scan.
- `make run-server` validates the host toolchain, installs and compiles the
  hosted webapp, generates ignored development configuration and secrets,
  starts health-checked PostgreSQL, Redis, MinIO, Mailpit, Prometheus, Grafana,
  Loki, and the OpenTelemetry Collector, then runs the Go server in the
  foreground. Dependencies remain available after the server exits; use
  `make dev-down` to stop them or `make dev-reset` to remove their volumes.
- `make run-webapp` runs Vite with HMR and proxies API/WebSocket traffic to an
  independently running server.
- `make debug-server`, `make debug-server-dap`, and
  `make debug-server-headless` run the same local server environment through
  interactive Delve, loopback DAP, or loopback Delve API v2 respectively.
  `make debug-test`, `make profile-test`, and `make trace-test` provide focused
  package diagnostics below `.build/dev/diagnostics` without exposing a
  production profiling endpoint.
- `make dev-seed` uses only the public HTTP API and locally captured Mailpit
  invitations to create a guarded synthetic Institution, administrator, Exam
  Manager, Candidate, academic structure, Exam Revision, and future Sitting.
  It refuses non-loopback servers, an Installation it did not initialize, and
  ambiguous partial state. Credentials and fixture identifiers are written
  mode `0600` below `.build/dev/seed`; a successful replay is read-only.
- `make dev-doctor` performs a bounded, read-only check of the host toolchain,
  Docker daemon, expected Compose containers, generated local artifacts, and
  loopback service, server, and hosted-webapp health. It prints closed check
  names only and never reads configuration, credentials, mail, or application
  records into its output.
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
tracked defaults. The Go version, `GOTOOLCHAIN`, `GOWORK`, `GOFLAGS`,
pinned-tool module and install directories, and individual pinned-tool paths
are repository authority: they ignore ambient and `config.override.mk` values.
Only an explicit Make command-line assignment can override those values for a
deliberate experiment.

All disposable runtime state is below ignored `.build/dev`: generated full server
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

## CI reports and failure diagnosis

Pull requests and `main` run the hermetic, independent-module, integration,
and three-node gates before building Linux archives. CI installs the exact
Playwright browsers matching the webapp lockfile, including their Linux system
dependencies. The existing browser suite checks the hosted interface against
test fixtures; it is not production deployment or upgrade acceptance testing.
`make integration` also runs the reusable cache Redis and mail SMTP conformance
suites, each with its own dependency lifecycle.

Ordinary module tests still use `go test` without a Node dependency. To collect
the same Go reports locally, set an absolute report directory and explicitly
select the runner, for example from the repository root:

```sh
PROCTOR_TEST_REPORT_DIR="$PWD/.build/ci" \
  make -C server test GO_TEST="node $PWD/build/ci/go-test.mjs"
```

Each invocation retains its command, raw Go JSON events, stderr, JUnit report,
atomic coverage profile, and summary in a separate directory. The runner
preserves the test process's failure status and disables test-result caching.
It also randomizes test order; the raw output retains the shuffle seed for
reproduction. Setting `PROCTOR_TEST_REPORT_DIR` for the webapp enables Vitest
JUnit/coverage and Playwright JUnit/HTML reports and failure traces. Coverage
is reported without inventing an unmeasured minimum coverage gate.

Failed CI runs retain the bounded report directory for 14 days, and failed
cluster runs retain bounded service diagnostics for 7 days. Download these
from the run's artifact panel. They may contain synthetic fixture data; treat
them as internal diagnostics. Never expand uploads to the whole `.build`
tree, which also contains development credentials and configuration. The
separate vulnerability workflow runs on relevant changes, weekly, manually,
and as a required release gate.

## Activating signed releases

The release workflow is deliberately inert until maintainers configure its
protected `release` GitHub environment. No credentials or signing keys belong
in the repository. Before the first release:

1. Protect `main`, require the CI gates, and restrict creation, modification,
   and deletion of release tags to release maintainers. Protect the `release`
   environment with required reviewers and deployment-tag restrictions for
   the four module prefixes below. Confirm that the repository's GitHub plan
   supports those controls for its visibility; do not enable unattended
   publication without equivalent protections.
2. Provision an encrypted Cosign signing key outside CI. Store the private PEM
   as environment secret `COSIGN_PRIVATE_KEY`, its nonempty password as
   `COSIGN_PASSWORD`, and the trusted public PEM as environment variable
   `COSIGN_PUBLIC_KEY`. Distribute that public key through an independently
   trusted channel; a key downloaded alongside an artifact is not by itself
   a trust anchor. Keep a backed-up key and a documented rotation owner.
3. Permit the release job to publish GitHub Releases and GHCR packages with
   its job token. Review the intended package visibility explicitly; the
   workflow does not change repository or package visibility. Set environment
   variable `RELEASES_ENABLED` to `true` only after these controls are ready.

The current signing mode uses a stored key and a signing configuration with
no public transparency-log, certificate, or timestamp services. This avoids
publishing private repository release metadata into a public transparency
log. It provides key-based integrity/authenticity, not keyless identity,
trusted signing-time evidence, or public transparency. Moving to keyless
signing is a separate policy choice. The pinned
[Cosign signing and verification interfaces](https://docs.sigstore.dev/cosign/signing/signing_with_blobs/)
are used to sign and verify a Sigstore bundle for the checksum manifest.

## Cutting and consuming a release

Release only a reviewed commit reachable from `main`, using one explicit,
previously unused module tag:

| Tag form | Published output |
| --- | --- |
| `server/v0.1.0` | Linux amd64/arm64 bundles, corresponding repository source, GHCR multi-platform image |
| `packages/cache/v0.1.0` | Cache module source release and Go module version |
| `packages/mail/v0.1.0` | Mail module source release and Go module version |
| `packages/vfs/v0.1.0` | VFS module source release and Go module version |

Versions are independent, not a coordinated monorepo version. Normal Go
subdirectory tags make each module version addressable; private repositories
still require consumer authentication and are not thereby published to the
public Go proxy. A pushed Git tag is fetchable before its workflow completes;
consumers must check the completed, signed release rather than treating tag
existence as evidence that the gates passed. When changing a server dependency
to a new reusable-module version, release that module first, then review the server's `go.mod`/`go.sum`
update before tagging the server. Tags currently accept major 0 or 1 because
none of these module paths has a `/v2` suffix. SemVer prereleases such as
`server/v0.1.0-rc.1` are supported; moving aliases and build-metadata tags are
not. The workflow never creates a Git tag or updates `latest`.

The tag's exact commit passes the same CI and current vulnerability gates.
For server releases, the image copies the already-built Linux archives,
including the healthcheck helper, instead of recompiling the application.
BuildKit attaches image provenance and an SBOM. The versioned image is
`ghcr.io/sudosylabs/proctor:VERSION`; consumers should pin the immutable
reference recorded in `image-digest.txt`.

Every release includes corresponding source, `provenance.json`, `SHA256SUMS`,
and `SHA256SUMS.sigstore.json`. The signed manifest covers every payload and
the provenance file, which records the source commit and exact workflow run.
This is signed release provenance, not a claim of a particular SLSA level.
After downloading all assets, verify with the independently trusted public key
before extracting or executing anything (Cosign v3):

```sh
cosign verify-blob --key /trusted/path/proctor-release.pub \
  --insecure-ignore-tlog --bundle SHA256SUMS.sigstore.json SHA256SUMS
sha256sum --check --strict SHA256SUMS
# Server releases also publish a signature for the immutable image index:
cosign verify --key /trusted/path/proctor-release.pub \
  --insecure-ignore-tlog "$(cat image-digest.txt)"
```

The explicitly named transparency-log exception is necessary for this
private, key-based policy; it does not disable cryptographic signature or
checksum verification. Compare the signed provenance's repository, tag,
commit, and run with the release you intended to install.

A draft GitHub Release reserves the tag before registry publication; the draft
becomes visible as a release only after signing and verification succeed.
There is no cross-service transaction: a later failure can leave a draft and
a registry image. Reruns deliberately refuse an existing draft/release and
never clobber its assets. Inspect partial publication before taking recovery
action; once an image or release has been distributed, use a new version
instead of reusing its tag. Do not treat an image as trusted until its
signature and the published manifest verify. Deployment acceptance, database
rollback policy, and Linux self-upgrade orchestration are separate work.
