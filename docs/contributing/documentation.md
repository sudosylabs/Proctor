# Documentation system

Proctor documentation is a portable, tracked interface for maintainers and
agents. A fresh checkout must contain everything required to understand its
domain, architecture, contracts, status, and development workflow.

## Authorities

Each kind of information has one owner:

| Information | Authority |
| --- | --- |
| Domain vocabulary | Root [`CONTEXT.md`](../../CONTEXT.md) |
| Durable cross-component design and rationale | [`docs/architecture/`](../architecture/) |
| Exact component behavior | Contract beside the component, linked from architecture docs |
| Capability status and unresolved decisions | [`docs/project/status.md`](../project/status.md) |
| Module setup and commands | The module README and Makefile |
| Cross-repository agent procedure | Root [`AGENTS.md`](../../AGENTS.md) |
| Subtree-only agent differences | The nearest concise `AGENTS.md` |
| Licensing and adapted-source provenance | [`LICENSING.md`](../../LICENSING.md) and the applicable notice |

Code, configuration, and tests remain authoritative for cheaply discoverable
implementation detail. Do not duplicate inventories that can be recovered from
the repository tree.

## Placement

Use `docs/README.md` as the navigation hub. Create a category only when it has
substantive content. Cross-component architecture belongs under
`docs/architecture/`; a precise contract that changes with one component may
remain beside its code when the architecture guide links to it.

Keep `CONTEXT.md` implementation-free. A `CONTEXT-MAP.md` and additional
glossaries are justified only when bounded contexts give the same terms
genuinely different meanings or ownership. Package boundaries alone are not
bounded contexts.

Keep root `AGENTS.md` small enough to load on every task. Inline universal
guardrails and procedures; route branch-specific detail through links with
clear triggers. A nested guide contains local differences, not copied
narrative.

## Decisions

Integrate a durable decision beneath the architecture topic it governs. State
the resulting rule and preserve concise rationale beside it. Add alternatives
or consequences only when they explain a real trade-off.

Update a superseded rule in place and rely on Git history for chronology. Do
not create a parallel ADR directory or append an unstructured decision log.
Create a new architecture topic only when no existing topic owns the decision.

## Portable references

- Use repository-relative Markdown links for repository content.
- Link only to files that will be tracked in the same change.
- Stable external HTTPS URLs are allowed. Source adaptations include the exact
  upstream revision and path in the applicable provenance record.
- Machine-absolute paths, `file://` URLs, ignored content, deleted files, and
  another developer's checkout are not documentation dependencies.
- `.scratch/`, `docs/adr/`, and `docs/adrs/` are ignored local workspaces. Their
  contents are never authoritative and tracked files never link to them.

Plain code identifiers and illustrative paths are acceptable when they do not
claim a local file dependency. Prefer links when a real tracked source or
contract is the intended evidence.

## Maintenance workflow

When a change affects domain language, architecture, a public/component
contract, capability status, or commands, update the corresponding authority
in the same change. Remove stale references instead of leaving redirects or
links to deleted history files.

For documentation or architecture changes, run:

```sh
make -C server architecture
```

The architecture gate checks every repository Markdown candidate for local
link targets, heading fragments, ignored/untracked dependencies, and
machine-specific paths. It does not contact external sites.
