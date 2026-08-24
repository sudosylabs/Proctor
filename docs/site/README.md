# Proctor documentation site

This private Docusaurus package renders the task-oriented content in
[`../public/`](../public/) and the API reference rooted at
[`../api/`](../api/). Existing architecture, component contracts, module
READMEs, and project status remain authoritative in their current locations.

The current local build provides task-oriented navigation, public content
validation, and a deterministic generated API reference. Its visual
presentation remains provisional. Publication, the production hostname,
content licensing, versioning, analytics, and hosting remain explicit decisions
to settle before deployment.

The guide finder intentionally searches the current top-level guide catalog.
Replace that bounded catalog with a generated body-content index when the
public guide set is large enough to justify the dedicated search slice.

The tracked [documentation design system](../contributing/design-system.md)
owns the brand palette, semantic colors, IBM Plex typography, spacing,
geometry, and illustration grammar. Human changes begin in
`design-system/tokens.mjs`; `src/css/tokens.css` is generated:

```sh
cd docs/site
npm run generate:design-system
npm run check:design-system
npm run test:design-system
npm run generate:glossary
npm run check:glossary
npm run test:glossary
```

The check rejects stale generated CSS, literal colors outside the token module,
retired color names, unsupported font weights, insufficient standard-token
contrast, and illustration palettes that do not match their declared visual
system.

`CONTEXT.md` is the single glossary authority. `generate:glossary` produces the
public glossary page and the typed runtime lookup used by explicit `<Term>`
tooltips. The check rejects stale generated views, unknown identifiers, repeated
annotations, and tooltip markup inside headings or code.

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
the Proctor contract panel at the generator's documented MDX seam, and
`scripts/verify-openapi-reference.mjs` proves that all operations, product-area
tags, sidebar entries, and `x-proctor-*` declarations are present exactly once.
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

The `package.json` overrides also keep the OpenAPI converter on patched
`js-yaml` and `yaml` patch releases while its upstream dependency declarations
lag behind. Keep those pins until `openapi-to-postmanv2` adopts the patched
versions directly; the snippet smoke test and production build cover their
runtime compatibility.

## Current dependency constraint

Docusaurus 3.10.2 currently resolves `image-size` 2.0.2, whose ICNS, JXL, and
HEIF parsers have high-severity denial-of-service advisories and no published
fixed release. Public pages therefore cannot add Markdown images, raw `img`
elements, or image imports; `npm run validate` enforces that boundary.

Reviewed SVG and PNG content assets instead use the constrained static route
documented in [`../contributing/visual-assets.md`](../contributing/visual-assets.md).
MDX supplies only a registry ID to `GovernedFigure`, so Docusaurus' authored
image parser never reads those files. The asset gate independently permits only
the two reviewed formats and checks inventory, ownership, provenance, license
state, privacy review, dimensions, size, safe SVG structure, references, and
review triggers. Each visual approval is bound to the asset's SHA-256 and a
desktop/mobile acceptance checklist, so an edited illustration cannot inherit
an earlier review silently. Revisit the parser prohibition when a fixed
dependency is available; do not remove the governed asset boundary.
