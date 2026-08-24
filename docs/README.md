# Proctor documentation

This is the entry point for maintainers and agents. Follow the section that
matches the work instead of loading every document.

## Architecture

- [Architecture guide](./architecture/) — system boundaries and durable
  engineering decisions
- [Domain language](../CONTEXT.md) — implementation-free canonical glossary
- [HTTP API contract](../server/httpapi/CONTRACT.md) — exact public API rules
- [OpenAPI authoring](../server/openapi/) — human-first route, description,
  example, and schema workflow
- [Cluster guarantees](../server/cluster/GUARANTEES.md) — delivery and recovery
  contract

## Project

- [Implementation status](./project/status.md) — capability-level status,
  active work, and open decisions
- [Mattermost documentation site analysis](./project/mattermost-docs-site-analysis.md)
  — research input for Proctor's public documentation site
- [Mattermost content and API pipeline analysis](./project/mattermost-docs-content-api-analysis.md)
  — source architecture, generated-reference proof, content gaps, assets, and
  staged acceptance criteria
- [Licensing](../LICENSING.md) — repository licensing split

## Public documentation

- [Public guide sources](./public/) — task-oriented operator, administrator,
  security, developer, and API guidance
- [Documentation site](./site/) — local preview, validation, static build, and
  generated-reference workflow

## Contributing

- [Build and development environment](../build/README.md) — product lifecycle,
  HA development topology, observability, packaging, and overrides
- [Documentation system](./contributing/documentation.md) — authority,
  placement, links, decision rationale, and validation
- [Governed visual assets](./contributing/visual-assets.md) — registry,
  diagram, privacy, and deterministic screenshot workflow
- [Root agent guide](../AGENTS.md) — always-loaded repository guardrails and
  task routing
- [Server development](../server/README.md) — configuration, commands, and
  verification

Additional categories are created only when substantive documents need them.
