# Proctor server architecture

This directory is the canonical developer guide to Proctor's boundaries and
durable engineering decisions. Each rule keeps its concise rationale beside
it; Git history carries chronology and superseded wording.

Start with [Overview](./overview.md), then load only the topics relevant to the
change:

- [Dependencies](./dependencies.md) — modules, package direction, and ports
- [Composition and lifecycle](./composition.md) — `server.New`, platform, and
  configuration construction
- [Domain](./domain.md) — academic invariants, model ownership, identifiers,
  time, and validation
- [Examinations](./examinations.md) — authoring, publication, sittings,
  participation, workspaces, integrity, and review
- [Application](./application.md) — use cases, invocation, orchestration, and
  atomic operations
- [Identity and authentication](./identity.md) — accounts, sessions, tokens,
  MFA, CAS, and OIDC
- [Access and onboarding](./access-and-onboarding.md) — bootstrap, access
  policy, hosted authentication, desktop handoff, invitations, and batches
- [Authorization and audit](./authorization.md) — actions, resources, scopes,
  enforcement, and durable decisions
- [Configuration](./configuration.md) — deployment configuration and
  application settings
- [User settings](./user-settings.md) — exact portable presentation source,
  revisions, validation, persistence, and self-service access
- [Transport](./transport.md) — HTTP, WebSocket, errors, compatibility, and
  validation
- [Persistence](./persistence.md) — stores, layers, schema, and migrations
- [File management](./files.md) — application metadata, VFS content, search,
  and live workspaces
- [Execution environments](./execution.md) — attempt terminals, host
  contract, projection, and deployment topology
- [Durable jobs](./jobs.md) — finite background work, claiming, retries,
  cancellation, and traceability
- [Transactional mail](./mail.md) — templates, durable delivery, recipients,
  encryption, retries, and operator control
- [Runtime and operations](./runtime.md) — effects, clustering, observability,
  naming, testing, and migration acceptance
- [Security and privacy](./security.md) — data handling and operational
  safeguards

Domain terms are defined only in [`CONTEXT.md`](../../CONTEXT.md). Current
implementation state and unresolved choices live in
[`docs/project/status.md`](../project/status.md). Documentation placement and
portability rules live in
[`docs/contributing/documentation.md`](../contributing/documentation.md).
