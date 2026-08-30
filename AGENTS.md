# Proctor Agent Guide

This guide applies to the entire repository. A more specific `AGENTS.md` may
add rules for its subtree; it must contain local differences rather than copy
this file or task skills.

## Mission

Proctor is an open-source, self-hosted examination and proctoring platform.
One logical installation represents one educational institution. Several
application nodes sharing authoritative state form one installation, not
separate tenants.

The repository is a monorepo with four independently versioned product Go
modules:

- `github.com/sudosylabs/proctor/packages/cache`
- `github.com/sudosylabs/proctor/packages/mail`
- `github.com/sudosylabs/proctor/packages/vfs`
- `github.com/sudosylabs/proctor/server`

The root `go.work` connects the product modules for repository development.
Each product module must also build and test independently. Non-product
developer-tool pins live in the isolated `build/tools` and `build/tools/gopls`
modules; they stay outside `go.work` so tool dependencies cannot affect product
module graphs.

## Sources of truth

Load only the material relevant to the task:

- Invoke [`$glossary`](.agents/skills/glossary/SKILL.md) before work that
  creates, changes, persists, transports, authorizes, documents, or publicly
  presents Proctor domain concepts. Honor skill prerequisites in order;
  unrelated tooling work does not load the glossary.
- Load a task-specific skill from `.agents/skills/` when its description
  matches the work; skills contain branch-specific workflow and reference.
- Invoke
  [`$documentation-design`](.agents/skills/documentation-design/SKILL.md)
  before creating or reorganizing documentation, skills, or agent instructions.
- Invoke
  [`$webapp-design-system`](.agents/skills/webapp-design-system/SKILL.md)
  before changing product presentation under `webapp/`. Docusaurus presentation
  uses [`$docs-site`](.agents/skills/docs-site/SKILL.md); its governed figures
  and screenshots use
  [`$docs-site-visual-assets`](.agents/skills/docs-site-visual-assets/SKILL.md).
- Read the affected module README and the nearest component contract before
  changing a public or behavioral contract.

The code and tests are the source of truth for discoverable implementation
detail. Skills and component contracts record domain language, durable rules,
rationale, and non-obvious workflow—not inventories recoverable from the tree.

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

The exact dependency graph and rationale live in the
[`server-boundaries` dependency reference](.agents/skills/server-boundaries/references/dependencies.md).

## Domain and product guardrails

Invoke `$glossary` before every domain-sensitive skill and honor each skill's
ordered prerequisites. Implement neither stale summaries nor unspecified
behavior.

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
- Update the owning skill, component contract, or public documentation in the
  same change when its authority has changed.

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
