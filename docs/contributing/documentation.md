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
| Public HTTP shapes and API reference metadata | [`server/openapi/`](../../server/openapi/) sources, generated [`server/openapi.json`](../../server/openapi.json), and the [`httpapi` contract](../../server/httpapi/CONTRACT.md) |
| Capability status and unresolved decisions | [`docs/project/status.md`](../project/status.md) |
| Public task-oriented guidance | [`docs/public/`](../public/) and authored [`docs/api/`](../api/) overview |
| Public site presentation and build | [`docs/site/`](../site/) |
| Documentation visual language and tokens | [`docs/contributing/design-system.md`](./design-system.md) and [`docs/site/design-system/tokens.mjs`](../site/design-system/tokens.mjs) |
| Public visual-asset metadata and files | [`docs/public/assets.json`](../public/assets.json) and [`docs/public/static/assets/`](../public/static/assets/) |
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

Public guides under `docs/public/` and the authored API overview under
`docs/api/` explain released outcomes for operators, institution
administrators, security reviewers, developers, API consumers, and eventually
examination participants. They link to or derive exact facts from
the existing glossary, architecture, component contracts, configuration, and
OpenAPI sources instead of becoming a second authority. The private
`docs/site/` package owns rendering, navigation, metadata validation, and the
static build, not product behavior.

## OpenAPI reference data

Edit the human-owned YAML modules under `server/openapi/`; never edit generated
`server/openapi.json` directly. `base.yaml` owns document metadata and the tag
taxonomy. Recursively discovered product-area/resource fragments own paths and
co-located definitions; area and root `shared.yaml` files own definitions with
real shared consumers. Stable tags, summaries, descriptions, security,
idempotency, error codes, parameter and request-body prose, and examples travel
with the operation or definition they explain.

The OpenAPI compiler hides fragment discovery, collision-safe merging,
reference resolution, universal metadata checks, schema validation, and
deterministic JSON generation. There is no fragment manifest or
indentation-sensitive concatenation contract. Run `make -C server
openapi-build` after an intentional source change and `make -C server
openapi-check` to prove the reviewed artifact is current. The complete author
workflow and placement rules live in
[`server/openapi/README.md`](../../server/openapi/README.md).

Every operation declares exactly one top-level product-area tag and explicitly
sets `x-proctor-auth`, `x-proctor-error-codes`, and
`x-proctor-idempotency`. Use `x-codeSamples` for reviewed executable-style
examples. Examples are synthetic, use obvious placeholders for credentials and
secrets, and never contain real student, Institution, Exam, answer, mail,
object-store, or local-machine data.

Run `npm run audit:openapi` from `docs/site` to audit the generated artifact and
obtain the machine-readable coverage report. The normal `make docs-check` gate
runs that audit and its failure-mode tests, regenerates the ignored endpoint
pages under `docs/api/reference/`, and proves every operation and required
Proctor extension survived rendering before building the site. The server OpenAPI validation
independently enforces the universal operation metadata; its runtime agreement
tests remain authoritative for route behavior.

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

## Visual assets

Public diagrams and screenshots use the governed registry and static serving
boundary described in the [visual-asset workflow](./visual-assets.md). MDX pages
reference a stable asset ID through `GovernedFigure`; they do not own file
paths, alt descriptions, dimensions, or captions. Preserve the direct image and
image-import prohibitions until the documented dependency constraint changes.

The [documentation design system](./design-system.md) owns color semantics,
typography, spacing, geometry, and illustration grammar. Authored stylesheets
consume its generated semantic properties rather than introducing local color
or font scales.

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

The architecture gate checks every repository Markdown and MDX candidate for
local link targets, heading fragments, ignored/untracked dependencies, and
machine-specific paths. It does not contact external sites.
