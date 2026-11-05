# Proctor documentation site

This private Docusaurus package renders the task-oriented content in
[`../public/`](../public/) and the API reference rooted at
[`../api/`](../api/). Repository skills, component contracts, code, and tests
remain authoritative for internal behavior.

The current local build provides task-oriented navigation, public content
validation, contextual guide navigation, and a deterministic
generated API reference. A GitHub Pages deployment workflow exists but stays
inert until maintainers activate it as described in
[the documentation workspace guide](../README.md#publishing). The production
hostname, content licensing, versioning, and analytics remain explicit
decisions to settle before activation.

The search dialog uses a generated local index covering authored public and API
guides, canonical glossary terms, OpenAPI product areas, and every operation.
It performs no remote request and sends no query off the page.

## Navigation and reading layout

Each guide owns one sidebar in `sidebars.ts`. Keep pages in task order within
that guide; do not add another audience's page as a `doc` item. Cross-guide
links belong in prose or global navigation so pagination stays within a guide.
The guide title links to its overview and cannot be collapsed; deeper sections
may collapse and Docusaurus opens the active page's ancestors automatically.

`navigation.mjs` holds the shared guide destinations, short descriptions,
reference links, and audience labels used by the homepage, navbar, search, and
article metadata. Add a guide there and give it a sidebar owner in the same
change. `npm run test:navigation` checks that every public page has exactly one
owner, entry points resolve, and no audience falls back to an internal key.

The layout adapter uses Docusaurus' configured base URL to distinguish the
homepage from the reader. Scoped CSS owns reading widths; avoid selectors
against generated CSS-module names. The homepage is a compact guide directory,
not a second description of product architecture.

The navigation and footer retain the flat Proctor Docs lockup. Browser icons
use the 2.5D mark and follow the browser's color preference independently of the
site's reading theme. These are reviewed package-local assets; their tests
validate the bundled copies without reading repository brand masters.

The tracked [documentation visual-system reference](../../.agents/skills/docs-site/references/design-system.md)
owns the brand palette, semantic colors, IBM Plex typography, spacing,
geometry, and illustration grammar. Human changes begin in
`design-system/tokens.mjs`; `src/css/tokens.css` is generated:

```sh
cd docs/site
npm run generate:design-system
npm run check:design-system
npm run test:design-system
npm run test:icons
npm run generate:glossary
npm run check:glossary
npm run test:glossary
npm run generate:search
npm run check:search
npm run test:search
npm run test:navigation
npm run generate:technical-reference
npm run check:technical-reference
```

The check rejects stale generated CSS, literal colors outside the token module,
retired color names, unsupported font weights, insufficient standard-token
contrast, and illustration palettes that do not match their declared visual
system.

`src/components/DocIcon` owns the bounded documentation icon vocabulary. Its
typed names, three optical sizes, shared two-pixel geometry, and accessibility
contract keep domain and utility symbols consistent without an external icon
library. Add a name only when the concept recurs and update the icon contract
and its test in the same change.

The repository [`glossary` skill](../../.agents/skills/glossary/SKILL.md) is the
single domain-language authority. `generate:glossary` produces the public
glossary page and the typed runtime lookup used by explicit `<Term>` tooltips.
The check rejects stale generated views, unknown identifiers, repeated
annotations, and tooltip markup inside headings or code.

The search module reads human-authored MDX plus `server/openapi.json` and hides
route derivation, text extraction, duplicate detection, and TypeScript adapter
generation behind `generate:search`. Regenerate after public content or OpenAPI
changes; `check:search` rejects a stale index.

`scripts/technical-reference.mjs` generates the complete environment-variable
inventory from `server/config/environment_overlay.go` and checks the authored
command-line reference against the command constructors and flags in
`server/cmd/proctor/commands`. Regenerate after adding, removing, or renaming an
environment override. The command-line page remains human-authored because it
must explain intent, consequences, and recovery rather than merely repeat Cobra
help; `check:technical-reference` catches drift in both sources. Never edit the
generated environment-variable page by hand.

`validate:routes` checks every Proctor `/api/v1` path written in authored public
or API guidance against the reviewed OpenAPI path and method set. It understands
documentary placeholders and deliberately ignores API-shaped paths on explicit
external origins such as the local Prometheus API. Generated endpoint pages are
excluded because their paths already come directly from OpenAPI.

`documentation-coverage.json` is the human-edited routing table from every
OpenAPI product area and named multi-operation workflow to its authored entry
page. A workflow can remain `planned` only with its delivery phase recorded.
`validate:coverage` rejects missing tags, pages, and operation IDs; update the
map and the validator's explicit expected-workflow inventory when a contract
change adds, removes, or moves a workflow. Keeping the inventory separate from
the routing table means deleting a complete workflow record cannot silently
reduce the claimed coverage.

## Commands

From the repository root:

```sh
make docs-start
make docs-check
```

`docs-start` serves a local preview. `docs-check` validates the documentation
design system, frontmatter, and governed visual-asset registry, proves
the generated OpenAPI artifact matches its human-authored YAML modules, audits
the reference data, tests the audit and renderer failure modes, regenerates and
verifies endpoint pages, type-checks the site, synchronizes the artifact, and
performs a strict production build.

To inspect the OpenAPI data without building the site:

```sh
cd docs/site
npm run audit:openapi
```

The command writes a versioned JSON report containing exact operation, schema,
tag, description, parameter, request-body, mutation-example, response-example,
code-sample, and Proctor-extension coverage. It fails on duplicate operation
IDs, unknown or multiple tags, short behavior or tag descriptions, missing
parameter/body purpose prose, mutations without synthetic request examples,
product areas without representative success or Problem Details examples, and
missing required extensions. The parameter-description exception list is
explicit and currently empty.

## Generated files

The human authoring workflow lives in
[`server/openapi/README.md`](../../server/openapi/README.md). Run `make -C
server openapi-build` after changing a route, description, example, or schema.

`scripts/sync-openapi.mjs` copies the generated and reviewed
`server/openapi.json` contract to
the ignored `static/openapi/openapi.json` publication path. The generated copy
is never edited or treated as an authority.

`docusaurus-plugin-openapi-docs` renders that same contract into the ignored
`docs/api/reference/` directory. `scripts/finalize-openapi-reference.mjs` adds
the Proctor contract panel between each operation introduction and its request
details. The local `ApiExplorer/MethodEndpoint` theme adapter presents the
exact OpenAPI path without joining it to a display-only server URL. These
adapters own presentation only; they never rewrite route or contract data.
The `ApiExplorer/CodeTabs` adapter synchronizes the generator with the active
language panel after Docusaurus restores a saved tab. Verify a page reload after
selecting JavaScript or Python: the highlighted language must match the sample,
not only after a fresh click. The configured variants are cURL, Fetch, and
Requests; unsupported extra variants do not appear in the reader.
`scripts/verify-openapi-reference.mjs` then proves that all operations,
product-area tags, sidebar entries, and `x-proctor-*` declarations are present
exactly once.
The authored `docs/api/index.mdx` overview and generator template remain small;
route descriptions and schemas stay with their human-owned OpenAPI YAML
fragments.

The OpenAPI source modules now own the complete product-area taxonomy and
explicit authentication, error-code, idempotency, behavior, parameter, request
body, and example metadata for every operation. The full-content gate covers
public and authenticated JSON, strong recent assurance, optional and required
idempotency, keyset pagination, raw image upload, protected binary download,
candidate multipart upload, submission, and WebSocket upgrade. Representative
success and redaction-safe Problem Details examples exist across every
applicable product area.

The reference UI deliberately disables request sending. It documents the
contract and produces client-ready examples without turning a documentation
deployment into a credential-bearing API console.

The package-local `.npmrc` disables dependency lifecycle scripts. The OpenAPI
theme's `postman-code-generators` dependency otherwise detects and launches
foreign package managers inside `node_modules`, making a clean install depend
on ambient developer tooling and network timing. Its published runtime already
contains the generator registry and all required dependencies; the codegen
smoke test exercises curl, JavaScript Fetch, and Python Requests output after a
script-free install, and the production build is the final guard. Re-enable
lifecycle scripts only after reviewing every dependency that would execute.

## Dependency advisories

Every package in this site runs at build time; the published output is static
files. `npm audit --omit=dev` still covers the toolchain because a
compromised or crashing build dependency affects what is published.

The `package.json` overrides hold patched transitive releases that the
upstream declarations do not yet admit:

- `js-yaml` 4.3.2 and `yaml` 1.10.3 under the OpenAPI converter
  (`openapi-to-postmanv2`);
- `tinypool` 2.2.0 for Docusaurus' static-rendering worker pool, the version
  line the next Docusaurus major already declares; and
- `postcss-selector-parser` 7.1.7 under the cssnano 6 minifier chain, which
  otherwise resolves an unpatched 6.x release.

Remove an override once its upstream declares a patched range. The snippet
smoke test and the production build, which renders through the worker pool and
minifies every stylesheet, cover their runtime compatibility.

Two high-severity advisories have no compatible fix and are accepted:

- [`braces` GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm)
  has no patched release. It reaches the build through `micromatch`,
  `fast-glob`, and the development-server file watcher, which expand only
  glob patterns written in this repository; no reader input reaches them.
- [`@faker-js/faker` GHSA-qxc2-j82w-r537](https://github.com/advisories/GHSA-qxc2-j82w-r537)
  affects `helpers.fake`. The OpenAPI theme's `postman-collection` pins faker
  5.5.3 for Postman dynamic variables and never calls `helpers.fake`; the
  patched faker major removed the API it uses, so it cannot be overridden.

Every other high advisory npm reports stems from these two. The vulnerability
workflow audits this lockfile with `build/ci/npm-audit.mjs`, which fails on
any high or critical advisory except these two IDs; accepting another one
means listing it here and in that workflow. Re-run the audit after each
Docusaurus or OpenAPI plugin upgrade and drop an accepted advisory as soon as
a compatible fix exists.

## Image boundary

Public pages cannot add Markdown images, raw `img` elements, or image imports;
`npm run validate` enforces that boundary. It was introduced while
Docusaurus' authored-image parser depended on an unpatched `image-size`
release. That dependency now resolves to a patched version, but the boundary
also keeps every content image inside the governed review below, so it stays
until maintainers decide otherwise.

Reviewed SVG and PNG content assets instead use the constrained static route
governed by the repository
[`docs-site-visual-assets` skill](../../.agents/skills/docs-site-visual-assets/SKILL.md).
MDX supplies only a registry ID to `GovernedFigure`, so Docusaurus' authored
image parser never reads those files. The asset gate independently permits only
the two reviewed formats and checks inventory, ownership, provenance, license
state, privacy review, dimensions, size, safe SVG structure, references, and
review triggers. Each visual approval is bound to the asset's SHA-256 and a
desktop/mobile acceptance checklist, so an edited illustration cannot inherit
an earlier review silently. Do not remove the governed asset boundary.
