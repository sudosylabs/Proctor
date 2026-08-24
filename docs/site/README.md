# Proctor documentation site

This private Docusaurus package renders the task-oriented content in
[`../public/`](../public/). Existing architecture, component contracts, module
READMEs, and project status remain authoritative in their current locations.

The current local build is a development scaffold for task-oriented
navigation, public content validation, and later generated API reference pages.
Its visual presentation remains provisional. Publication, the production
hostname, content licensing, versioning, analytics, and hosting remain explicit
decisions to settle before deployment.

The guide finder intentionally searches the current top-level guide catalog.
Replace that bounded catalog with a generated body-content index when the
public guide set is large enough to justify the dedicated search slice.

## Commands

From the repository root:

```sh
make docs-start
make docs-check
```

`docs-start` serves a local preview. `docs-check` validates frontmatter,
proves the generated OpenAPI artifact matches its human-authored YAML modules,
audits the reference data, tests the audit's failure modes, type-checks the
site, synchronizes the artifact, and performs a strict production build.

To inspect the OpenAPI data without building the site:

```sh
cd docs/site
npm run audit:openapi
```

The command writes a versioned JSON report containing exact operation, schema,
tag, description, code-sample, and Proctor-extension coverage. It fails on
duplicate operation IDs, unknown or multiple tags, missing summaries or
required extensions, and incomplete data in the representative reference
pilot.

## Generated files

The human authoring workflow lives in
[`server/openapi/README.md`](../../server/openapi/README.md). Run `make -C
server openapi-build` after changing a route, description, example, or schema.

`scripts/sync-openapi.mjs` copies the generated and reviewed
`server/openapi.json` contract to
the ignored `static/openapi/openapi.json` publication path. The generated copy
is never edited or treated as an authority.

The OpenAPI source modules now own the complete product-area taxonomy and explicit
authentication, error-code, and idempotency metadata for every operation. A
representative pilot covers public and authenticated JSON, strong recent
assurance, optional and required idempotency, keyset pagination, raw image
upload, protected binary download, candidate multipart upload, submission, and
WebSocket upgrade. These operations also include behavioral descriptions,
parameter and body prose, synthetic request data, and redacted shell examples.

Browsable endpoint and tag page generation is the next API slice. Until that
renderer is integrated, the site publishes the unchanged downloadable contract
but does not claim that the reference UI is complete.

## Current dependency constraint

Docusaurus 3.10.2 currently resolves `image-size` 2.0.2, whose ICNS, JXL, and
HEIF parsers have high-severity denial-of-service advisories and no published
fixed release. Public pages therefore cannot add authored images or image
imports yet; `npm run validate` enforces that boundary. Revisit it when a fixed
dependency is available. The site uses typography and CSS for its initial
visual system.
