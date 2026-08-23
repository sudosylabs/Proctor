# Proctor documentation

This is the entry point for maintainers and agents. Follow the section that
matches the work instead of loading every document.

## Architecture

- [Architecture guide](./architecture/) — system boundaries and durable
  engineering decisions
- [Domain language](../CONTEXT.md) — implementation-free canonical glossary
- [HTTP API contract](../server/httpapi/CONTRACT.md) — exact public API rules
- [Cluster guarantees](../server/cluster/GUARANTEES.md) — delivery and recovery
  contract

## Project

- [Implementation status](./project/status.md) — capability-level status,
  active work, and open decisions
- [Licensing](../LICENSING.md) — repository licensing split

## Contributing

- [Build and development environment](../build/README.md) — product lifecycle,
  HA development topology, observability, packaging, and overrides
- [Documentation system](./contributing/documentation.md) — authority,
  placement, links, decision rationale, and validation
- [Root agent guide](../AGENTS.md) — always-loaded repository guardrails and
  task routing
- [Server development](../server/README.md) — configuration, commands, and
  verification

Additional categories are created only when substantive documents need them.
