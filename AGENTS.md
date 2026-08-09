# Proctor Agent Guide

This guide applies to the entire repository. A more specific `AGENTS.md` may
add rules for its subtree; it must contain local differences rather than copy
this file or the architecture guide.

## Mission

Proctor is an open-source, self-hosted examination and proctoring platform.
One logical installation represents one educational institution. Several
application nodes sharing authoritative state form one installation, not
separate tenants.

The repository is a monorepo with four independently versioned Go modules:

- `github.com/sudosylabs/proctor/packages/cache`
- `github.com/sudosylabs/proctor/packages/mail`
- `github.com/sudosylabs/proctor/packages/vfs`
- `github.com/sudosylabs/proctor/server`

The root `go.work` connects them for repository development. Each module must
also build and test independently.

## Sources of truth

Start at [`docs/README.md`](docs/README.md). Load only the material relevant to
the task:

- Read [`CONTEXT.md`](CONTEXT.md) when domain terminology or invariants are in
  scope. It is the single implementation-free glossary.
- Read [`docs/architecture/`](docs/architecture/) when changing boundaries,
  dependencies, security behavior, persistence, transports, or runtime design.
- Read [`docs/project/status.md`](docs/project/status.md) when selecting work,
  checking implemented capabilities, or resolving an open decision.
- Read [`docs/contributing/documentation.md`](docs/contributing/documentation.md)
  before creating or reorganizing documentation or agent instructions.
- Read the affected module README and the nearest component contract before
  changing a public or behavioral contract.

The code and tests are the source of truth for discoverable implementation
detail. Documentation records domain language, durable rules, rationale,
contracts, status, and non-obvious workflow—not inventories that can be
recovered cheaply from the tree.

## Architectural guardrails

1. Business logic is independent of HTTP, WebSocket, SQL, Redis, SMTP, VFS,
   Memberlist, and concrete adapters.
2. Reusable modules never import the Proctor server or expose server types.
3. The module-root `server.New` is the sole composition root and the only place
   that selects concrete infrastructure.
4. `platform.Service` owns infrastructure lifecycle and health; it is not an
   application service locator.
5. PostgreSQL is authoritative for durable application state. Cache entries
   and cluster messages are disposable and reconstructible.
6. Authentication establishes an immutable principal. Application use cases
   perform the authoritative action/resource authorization check.
7. Every route explicitly declares its credential and assurance requirement.
8. Security-sensitive state changes and authorization decisions are durably
   auditable and fail closed where the architecture contract requires it.
9. Credentials, secrets, student data, exam answers, and unbounded
   user-controlled content never enter ordinary logs or unsafe audit fields.
10. A single-node installation is the degenerate form of the same clustered
    architecture; horizontal scaling is never commercially gated.
11. `server/model` remains a cohesive domain package, not a home for wire,
    persistence, infrastructure, or general utility contracts.
12. Add packages and abstractions for stable ownership boundaries, not for
    symmetry, file size, or speculative reuse.
13. Persistence uses the bounded root `store.Store` and per-model contracts;
    cross-model transactions use named aggregate operations rather than raw
    transaction callbacks exposed to the application.
14. Durable state commits before transient cache invalidation, cluster fan-out,
    WebSocket publication, or other best-effort effects.
15. One installation contains one institution. Do not add tenant columns or
    tenant routing without a new, explicit architecture decision.

The detailed dependency graph and rationale live in
[`docs/architecture/dependencies.md`](docs/architecture/dependencies.md).

## Domain and product guardrails

- Use `Institution`, hierarchical `Academic Unit`, `Programme`, `Programme
  Level`, `Academic Period`, and `Class` as defined in the glossary.
- `Class`, not `Group`, is the concrete student roster.
- A student has at most one active class membership in an academic period;
  progression and transfers retain history.
- Affiliations describe relationships. Roles and scoped bindings grant
  permissions. Membership alone grants no unrestricted access.
- The exact exam ownership, targeting, lifecycle, proctor assignment, and
  violation-review model remain open decisions. Do not invent them.

## Licensing and provenance

Reusable modules under `packages/` use Apache-2.0; the server uses AGPL-3.0.
[`LICENSING.md`](LICENSING.md) explains the split.

[Mattermost](https://github.com/mattermost/mattermost) is an upstream behavior
and eligible source reference. Direct or substantial adaptations must identify
the exact upstream revision, repository path, and governing license; preserve
required notices; record provenance immediately in the server notice; and pass
Proctor-specific architecture and security review. Never import from the old
`github.com/prctr/prctr/server` module path. Source Available or commercial
Mattermost code requires explicit permission.

Optional local reference material is never an authority. `.scratch/` is an
ignored, ephemeral workspace; tracked documentation and tests must not link to
or depend on it.

## Working procedure

Before changing files:

1. Read this file, the nearest scoped `AGENTS.md`, and the routed documents
   relevant to the task.
2. Inspect `git status` and distinguish existing user work from the requested
   change.
3. Inspect the affected tests, contracts, and module documentation.
4. Surface any disagreement between code and documentation; determine whether
   it is stale documentation, a bug, or an incomplete migration.

While working:

- Preserve unrelated work and make the smallest coherent change.
- Keep dependency direction intact and complete one vertical slice at a time.
- Add tests proportional to risk and use existing conformance suites.
- Record copied or substantially adapted upstream source immediately.
- Update the glossary, architecture topic, status, or component contract in
  the same change when its authority has changed.

Before handing off:

1. Run the smallest relevant unit, race, vet, integration, and conformance
   checks described by the affected module.
2. Run `make -C server architecture` for architecture or documentation changes;
   run `make -C server check` when the server change warrants the full hermetic
   gate.
3. Review the diff for secrets, generated noise, stale references, and
   unrelated changes.
4. Report what was verified and what remains unresolved.

Do not modify, stage, delete, or depend on ignored local material unless the
user explicitly places it in scope.
