# Mattermost documentation site analysis for Proctor

> Status: research snapshot and design input, not an accepted Proctor
> architecture decision. This analysis covers the Mattermost monorepo at commit
> `a3e171f730781dc87e5eb0f36d556f9eb39fc22a` (2026-08-24). Source coordinates
> below are pinned permalinks to that revision.

> **Implementation-sequence update:** the focused
> [content and API pipeline analysis](./mattermost-docs-content-api-analysis.md)
> validates Proctor's compiled OpenAPI artifact with Mattermost's generator and
> records the later human-first YAML authoring foundation. It moves generated
> API reference work ahead of further visual-shell work. Its staged acceptance
> criteria supersede the phase ordering in this earlier broad survey where they
> differ.

## Executive summary

Mattermost's current documentation site is a new Docusaurus 3 application inside
its product monorepo. It separates authored content from presentation code,
publishes distinct user/operator, developer, and generated API sections, and
builds a static site through one orchestrated pipeline. Its best ideas for
Proctor are:

- keep the documentation source beside the product and make code, documentation,
  and API changes reviewable in one pull request;
- organize the public experience by reader intent while retaining a distinct
  generated API reference;
- derive reference material from canonical tracked sources rather than manually
  duplicating contracts;
- provide deterministic local builds, strict type checking, pull-request
  previews, and an independently repeated deployment build;
- publish immutable hashed assets separately from revalidated HTML; and
- use a small documentation design system for callouts, landing-page cards,
  availability or maturity notices, and diagrams.

Mattermost should be treated as evidence that this shape can work, not as a
template to copy. The inspected implementation is still carrying a large Sphinx
and Hugo migration. Broken links are warnings, sidebar generation has grown to
about 1,700 lines of manually enumerated legacy paths, the committed redirect
map cannot be regenerated from tracked inputs, style linting is not enforced in
CI, generated references have incomplete workflow triggers, and a separate PDF
subsystem contains stale paths and no tests. Some old automation still reads and
writes the separate Mattermost Sphinx repository rather than the new monorepo
site.

The recommended Proctor starting point is therefore deliberately smaller:

1. Use a private Docusaurus package under `docs/site/`, aligned with Proctor's
   existing Node 22 and React 19 toolchain.
2. Put task-oriented public prose under a new `docs/public/` authority while
   leaving the current glossary, architecture, contracts, status, module
   READMEs, and agent instructions in their existing authoritative locations.
3. Launch with one guide instance and one API instance generated from the
   drift-checked `server/openapi.json` artifact. Keep its human-owned YAML
   modules independent of the renderer. Use a small explicit sidebar; do not
   build a custom sidebar generator until content volume demonstrates the need.
4. Serve candidates, Exam Managers, institution administrators, operators and
   security reviewers, then developers. Publish only implemented behavior and
   mark planned material explicitly.
5. Fail CI on broken internal links, images, type errors, generated-reference
   errors, terminology violations, and accessibility smoke failures from the
   first release.
6. Self-host fonts and static search assets. Keep the output usable without
   third-party runtime requests so it can later ship with release archives for
   isolated institutions.

## Scope and method

The analysis used only tracked primary sources: Mattermost content, site
configuration, package manifests and lockfiles, generators, components,
styles, workflows, deployment scripts, and recent repository history. The
Mattermost worktree was clean. Ignored build output, generated API pages,
`node_modules`, local plans, and other ephemeral material were neither inspected
nor treated as evidence. No live-site or generated-output behavior is claimed
unless the tracked implementation establishes it.

The inspected `docs/` tree contains 1,527 tracked entries and roughly 203 MiB of
tracked data. Its authored content surface is 569 Markdown/MDX pages: 416 under
`docs/main`, 151 under `docs/develop`, and two hand-authored API pages. Generated
API endpoint pages are ignored. There are 726 tracked image assets under
`docs/site/static`. These counts were derived from `git ls-files` at the pinned
commit; they explain several design choices but are not targets for Proctor.

The tracked page metadata is uneven: 559 of the 569 content pages declare a
frontmatter title, only 28 declare a description, 151 declare a sidebar
position, and 28 declare a distinct sidebar label. Proctor can establish a
stricter page schema while its corpus is small.

Proctor currently has no separate research-notes convention. This report lives
under `docs/project/` because it informs future project work without changing a
durable architecture rule. It follows the authority and portability rules in
the [documentation system](../contributing/documentation.md): existing
architecture and component contracts remain authoritative, and this file has
no dependency on another developer's checkout.

## What Mattermost has built

### Repository and rendering architecture

The presentation layer is a private Node package under `docs/site/`; the prose
is outside that package. The site reads three content roots:

| Source | Public route | Purpose |
| --- | --- | --- |
| `docs/main/` | `/` | Product, deployment, administration, security, end-user, integration, and help content |
| `docs/develop/` | `/developers` | Contribution and extension documentation |
| `docs/api/` | `/api` | Hand-authored API introductions plus generated endpoint pages |

This split is stated in the
[`docs/site/README.md:7-16`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/README.md#L7-L16).
The default Docusaurus classic preset owns the root documentation instance,
while two additional `plugin-content-docs` instances own `/developers` and
`/api`; the API instance uses the OpenAPI theme's page renderer
([`docusaurus.config.ts:84-160`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L84-L160)).

The core package is Docusaurus 3.10.1, React 19, MDX 3, TypeScript 6, Mermaid,
and `docusaurus-plugin-openapi-docs`. Node 20 is required
([`package.json:28-65`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/package.json#L28-L65)).
The output is a static site; the production workflow uploads the build directory
to object storage and serves it through a CDN rather than running a documentation
application server.

This is a sound separation of responsibilities:

- product contributors edit content without working inside the theme package;
- site code has its own locked dependencies and type check;
- generated content can be placed beside, but not confused with, authored
  prose; and
- multiple navigation contexts can share one deployment, brand, search index,
  and URL origin.

### Information architecture and navigation

The root page starts with reader personas—end users, administrators, developers,
security architects, SREs, compliance officers, and air-gapped operators—before
presenting the product narrative
([`docs/main/index.mdx:1-40`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/main/index.mdx#L1-L40)).
The developer and API home pages use a smaller card-based starting-point model
([`docs/develop/index.mdx:1-60`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/develop/index.mdx#L1-L60),
[`docs/api/index.mdx:1-75`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/api/index.mdx#L1-L75)).
The persona landing-page idea is reusable; Mattermost's defense-oriented product
copy and its commercial funnels are not.

Developer navigation follows the filesystem recursively. Directory index pages
become category links, `sidebar_position` controls order, filename is the stable
fallback, and empty categories are omitted
([`gen-developer-sidebar.mjs:38-94`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/gen-developer-sidebar.mjs#L38-L94)).
That is a reasonable pattern once a documentation tree becomes large enough.

The main navigation is different. Mattermost keeps many files physically flat
to preserve migrated URLs, then overlays manually defined category groupings at
render time. The generator explicitly says it has no common grouping engine and
copies a builder/regrouper shape for every exceptional section
([`gen-documentation-sidebar.mjs:25-60`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/gen-documentation-sidebar.mjs#L25-L60)).
Its useful safety feature is orphan detection: a page omitted from a manual
order is warned about and appended instead of disappearing
([`gen-documentation-sidebar.mjs:1111-1130`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/gen-documentation-sidebar.mjs#L1111-L1130)).
The warning does not fail the build, so accidental placement can still ship.

For Proctor, filesystem navigation or a concise explicit sidebar is sufficient.
URL-preserving regroupers are a migration solution, not a greenfield feature.

### Authoring model

Pages are Markdown or MDX with YAML frontmatter. The strongest convention is a
small stable metadata vocabulary: `title`, `description`, `slug`,
`sidebar_position`, `sidebar_label`, and occasional visibility fields. Landing
pages use a title, a one-sentence orientation, task or persona links, and a clear
next step. The use of short navigation cards is encoded as a typed component
([`CardGrid/index.tsx:5-40`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/CardGrid/index.tsx#L5-L40)).

Reusable MDX components are registered globally, so authors can write
`<Note>`, `<Security>`, `<CardGrid>`, diagrams, availability badges, or generated
reference components without imports
([`MDXComponents.tsx:1-58`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/theme/MDXComponents.tsx#L1-L58)).
The callout component uses semantic `aside` markup and a textual label rather
than communicating only through color
([`Callout/index.tsx:4-42`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/Callout/index.tsx#L4-L42)).

Global registration lowers authoring friction but makes prose dependent on a
React component API. Proctor should globally register only a tiny stable set:
callouts, navigation cards, and perhaps a maturity marker. Specialized or
interactive components should be imported by the few pages that use them.

Mattermost also renders product-plan, edition, deployment-mode, training, and
compliance status badges. The plan component hard-codes commercial tiers and
sales links
([`PlanAvailability/index.tsx:24-87`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/PlanAvailability/index.tsx#L24-L87));
the deployment component includes shared cloud, dedicated cloud, government,
self-hosted, air-gapped, and tactical-edge modes
([`DeploymentAvailability/index.tsx:5-35`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/DeploymentAvailability/index.tsx#L5-L35)).
These are Mattermost product-model components. Proctor is one open-source,
self-hosted product and must not import commercial edition gates or imply that
horizontal scaling is a paid capability.

The query-driven `DeploymentOnly` component is presentation filtering, not
access control: all variants are server-rendered and only hidden in the browser
([`DeploymentOnly/index.tsx:17-22`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/DeploymentOnly/index.tsx#L17-L22),
[`DeploymentOnly/index.tsx:49-75`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/DeploymentOnly/index.tsx#L49-L75)).
Any Proctor scope or maturity filter must likewise be treated only as a reading
aid; it must never hide secrets, unsupported security conditions, or license
restrictions.

### Design system and accessibility intent

Mattermost separates brand tokens from Docusaurus/Infima overrides. The token
layer defines palette, typography, semantic surfaces, dark-mode variants, and
documented contrast choices
([`tokens.css:11-99`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/css/tokens.css#L11-L99)).
The theme maps those tokens onto the framework, provides explicit focus and
active states, distinguishes links from body text, and styles navigation,
sidebars, code, callouts, and print surfaces
([`custom.css:9-71`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/css/custom.css#L9-L71),
[`custom.css:117-196`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/css/custom.css#L117-L196)).

The reusable lesson is the layering and semantic naming, not the palette,
fonts, military voice, or component shapes. Proctor should establish its own
brand tokens and validate them automatically. Comments that claim a contrast
ratio are helpful evidence but do not replace an accessibility audit.

The Mattermost site has no tracked accessibility test. One interactive example,
the upgrade-note filter, polls the DOM every 100 milliseconds without an
attempt or time limit until a table appears
([`UpgradeNotesFilter/index.tsx:82-119`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/UpgradeNotesFilter/index.tsx#L82-L119)).
This is the kind of behavior a small component test or browser smoke test should
catch before a site accumulates custom interactions.

### Search, internationalization, and runtime dependencies

Algolia search is conditional. The config includes it only when both repository
variables exist, so local builds remain valid and simply omit the search box
([`docusaurus.config.ts:15-32`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L15-L32)).
This is a good degradation pattern. The site currently declares only English
([`docusaurus.config.ts:65-68`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L65-L68)).

Live pages load three font families from Google
([`docusaurus.config.ts:70-81`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L70-L81)).
That is a privacy, availability, and offline dependency. It is particularly
unsuitable as a Proctor default because institutions may operate constrained or
isolated environments and a downloadable documentation build should not make
unnecessary third-party requests.

Proctor's server already has localized presentation infrastructure and stable
locale-independent protocol codes, while the hosted UI remains under active
development ([project status](./status.md), lines 58-73). The documentation site
can start English-only, but its paths, component strings, and search strategy
should not make a future locale structure prohibitively expensive.

## Build and generated reference pipeline

### One entry point, many generators

Mattermost's `npm start` and `npm run build` are orchestration entry points. Npm
lifecycle hooks stage vendored Agents documentation, generate two sidebars,
build or reuse an OpenAPI bundle, generate endpoint MDX, and generate three
plugin SDK datasets before Docusaurus runs
([`package.json:5-26`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/package.json#L5-L26)).
Generated sidebars, API pages, OpenAPI output, plugin datasets, staged Agents
content, and build output are ignored
([`.gitignore:187-205`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.gitignore#L187-L205)).

The strong pattern is one documented entry point that reconstructs every
derived input from a fresh checkout. The weak consequence is that a bare
`docusaurus start` cannot work, and the quick start omits the Agents submodule
initialization required by `prestart`. The staging script deletes old output
and fails if the submodule is absent
([`stage-agents-docs.mjs:114-125`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/stage-agents-docs.mjs#L114-L125));
the README's quick start only lists `npm ci` and `npm start`
([`README.md:18-31`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/README.md#L18-L31)).

Several scripts use POSIX shell operators, `mkdir -p`, `mv`, and `[ -f ... ]`
inside `package.json`
([`package.json:14-19`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/package.json#L14-L19)).
Proctor's site scripts should be portable Node programs or Make targets if
native Windows contribution is expected.

### OpenAPI generation

Mattermost delegates to the canonical API Makefile, then reparses and modifies
the result to work around MDX parsing. It changes straight-quoted phrases to
curly quotes and escapes some `<` characters in every title, summary, and
description
([`build-openapi.mjs:42-82`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/build-openapi.mjs#L42-L82)).
That transform is specific to Mattermost's source and generator combination; it
could alter exact syntax in Proctor descriptions and has no tracked tests.

The delegated build also runs `npm install`, Go code, and Swagger validation
([`api/Makefile:72-83`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/Makefile#L72-L83)).
It fetches the Playbooks OpenAPI file from an unpinned `master` URL during the
build
([`api/playbooks/extract.js:24-33`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/playbooks/extract.js#L24-L33)).
Consequently, the same Mattermost commit can produce different docs and a full
docs build needs network access beyond locked package installation.

Proctor has a cleaner authoring and consumer boundary: resource-oriented YAML
modules under `server/openapi/` are the human source, while
`server/openapi.json` is their deterministic reviewed artifact. Agreement tests
require route authentication, DTO fields, success schemas, and public errors to
agree with the compiled contract
([HTTP contract](../../server/httpapi/CONTRACT.md), lines 1-24). The docs build
should prove the artifact matches the source, then consume those exact bytes.
It should never parse transport code, fetch another repository's `main` branch,
or mutate API descriptions to make the renderer succeed. If a renderer cannot
safely accept the compiled specification, change or wrap the renderer and test
the output.

The generated API UI can add Proctor-specific presentation for
`x-proctor-auth`, assurance requirements, idempotency, and declared Problem
Details without making the documentation generator authoritative for those
facts.

The current Proctor specification is structurally organized for generation. It
contains 172 paths, 218 operations, 268 component schemas, and 16 stable public
product-area tags. Every operation has an `operationId`, summary, one tag,
explicit security, and Proctor authentication, error, and idempotency metadata.
A representative set also carries deeper behavioral descriptions and redacted
examples; that editorial standard should expand in domain-sized passes. Keep
the metadata beside its route in [`server/openapi/`](../../server/openapi/),
under the same compiler and agreement review as the rest of the contract.

### Source-derived SDK documentation

Mattermost generates three datasets directly from product source: the Go plugin
API, the web-app plugin registry, and the plugin manifest schema
([`README.md:145-161`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/README.md#L145-L161)).
The Go generators deliberately parse syntax and documentation without type
checking, reducing toolchain coupling but relying on Mattermost-specific source
shapes
([`gen-plugin-godocs/main.go:1-8`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/gen-plugin-godocs/main.go#L1-L8)).

The React renderers inject repository-derived HTML through
`dangerouslySetInnerHTML`
([`PluginGoDocs/index.tsx:51-75`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/PluginGoDocs/index.tsx#L51-L75),
[`PluginManifestDocs/index.tsx:57-80`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/PluginManifestDocs/index.tsx#L57-L80)).
The current trust boundary is reviewed repository source comments, not arbitrary
runtime data, but raw HTML still reaches production without an explicit
sanitizer.

The docs CI/CD workflow triggers on `docs/**`, `api/v4/source/**`, and
`api/playbooks/**`
([`docs-ci.yaml:3-15`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-ci.yaml#L3-L15)).
It does not trigger for the Go plugin interfaces, manifest model, or web-app
registry that feed the three generators. Those references can therefore remain
stale until another docs-triggering change occurs.

The reusable rule is dependency completeness: every generated page must name a
tracked authoritative input; builds must be deterministic; CI paths must cover
every input and generator; and generated HTML must have a reviewed sanitization
boundary. Proctor does not yet need source-parsed SDK pages. Its OpenAPI
reference and existing module READMEs provide a useful first developer surface.

### Vendored documentation and partials

Mattermost pins a plugin repository as a git submodule and stages only its docs
subtree. The transformer strips a leading HTML license comment, extracts a
title, rewrites relative links and images, creates partials for inline inclusion,
and marks most pages unlisted
([`stage-agents-docs.mjs:2-31`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/stage-agents-docs.mjs#L2-L31),
[`stage-agents-docs.mjs:73-110`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/stage-agents-docs.mjs#L73-L110)).

This is valid when an independently versioned product owns documentation that
must appear in a unified site, but it creates submodule, transform, provenance,
and link-rewrite obligations. Proctor's four Go modules and server already share
one monorepo. Their READMEs should remain in place and be linked or rendered
deliberately; no staging layer is justified now.

### PDF pipeline

Mattermost has a separate Puppeteer package for page and multi-page PDF capture.
It is not called by the site package or docs workflows, and its only `test`
script intentionally fails
([`docs/pdf/package.json:1-14`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/pdf/package.json#L1-L14)).
The README labels future table-of-contents, watermarking, indexing, and
Cloudflare rendering work as deferred
([`docs/pdf/README.md:69-118`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/pdf/README.md#L69-L118)).

The book-spine generator still resolves the old `docs-site/` directory rather
than `site/`
([`gen-main-docs-books.mjs:20-24`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/pdf/scripts/gen-main-docs-books.mjs#L20-L24)).
The book builder says it includes Docusaurus CSS and calculates a `stylesUrl`,
but does not insert that URL into its composed HTML
([`build-book-pdf.mjs:147-168`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/pdf/scripts/build-book-pdf.mjs#L147-L168)).
The proposed URL-to-PDF service would also need strict origin allowlisting to
avoid server-side request forgery because the primitive navigates to a supplied
URL
([`build-page-pdf.mjs:21-43`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/pdf/scripts/build-page-pdf.mjs#L21-L43)).

Proctor should first produce a downloadable static offline bundle. PDF books are
a later reader need, not a prerequisite for a documentation site.

## Quality, review, preview, and deployment

### Current Mattermost gates

Mattermost's docs CI uses Ubuntu 24.04, checks out submodules, pins actions by
commit, installs with `npm ci`, runs strict TypeScript checking, and completes a
production build under read-only repository permissions
([`docs-ci.yaml:17-56`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-ci.yaml#L17-L56)).
The production workflow independently repeats type checking and the build so a
separate CI failure or bypass cannot deploy unvalidated code
([`docs-cd.yml:45-65`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-cd.yml#L45-L65)).
That repetition is a good release boundary.

The site explicitly downgrades broken links, Markdown links, and images to
warnings during migration
([`docusaurus.config.ts:50-63`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L50-L63)).
There is no unit, browser, accessibility, visual regression, external-link, or
generator snapshot test command; the package's validation surface is TypeScript
plus the static build
([`package.json:5-26`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/package.json#L5-L26)).

Vale is configured for Markdown, MDX, and RST with Mattermost-specific heading,
terminology, brand voice, and sentence-length rules
([`.vale.ini:7-20`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/.vale.ini#L7-L20),
[`Terminology.yml:1-50`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/styles/Mattermost/Terminology.yml#L1-L50)).
The site README documents a manual command, but the docs CI and CD workflows do
not run Vale. The `.vale.ini` comment describing CI is stale.

Proctor should start stricter while the content is small. Its existing
repository gate already checks tracked Markdown links, fragments, HTTPS,
ignored/untracked dependencies, and machine-specific paths
([`documentation_test.go:25-49`](../../server/architecture/documentation_test.go),
[`documentation_test.go:92-185`](../../server/architecture/documentation_test.go)).
That validator currently selects only `.md` files at lines 39-42, so MDX support
must be added before `docs/public/` becomes an authority.

### Pull-request previews

Mattermost builds same-repository pull requests under a PR-specific `BASE_URL`,
serializes previews per PR, type-checks, performs the full production generation
pipeline, and synchronizes the result into a repository-scoped object-storage
prefix
([`docs-preview-template.yml:24-69`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-preview-template.yml#L24-L69),
[`docs-preview-template.yml:78-97`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-preview-template.yml#L78-L97)).
Closed same-repository pull requests remove their prefix
([`docs-preview-cleanup.yml:3-31`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-preview-cleanup.yml#L3-L31)).

The preview workflow uses long-lived AWS access keys and publishes an HTTP S3
website URL
([`docs-preview-template.yml:71-76`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-preview-template.yml#L71-L76),
[`docs-preview-template.yml:99-105`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-preview-template.yml#L99-L105)).
The manual fork flow accepts a commit SHA and then invokes the same secret-bearing
build
([`docs-preview-fork.yml:3-47`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-preview-fork.yml#L3-L47)).
Because dependency installation and npm build scripts execute code from the
selected revision, manual approval does not make that code safe to receive cloud
credentials.

Proctor previews should separate the untrusted build from privileged publishing:

1. build and test pull-request content without deployment credentials;
2. upload an ordinary CI artifact;
3. let a trusted workflow deploy that exact artifact with a short-lived OIDC
   identity scoped to one preview prefix; and
4. expose only HTTPS preview URLs and expire prefixes automatically.

### Production deployment

Mattermost's production workflow uses GitHub OIDC rather than persistent AWS
keys
([`docs-cd.yml:18-25`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-cd.yml#L18-L25),
[`docs-cd.yml:67-71`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-cd.yml#L67-L71)).
It uploads hashed assets with a one-year immutable cache without deleting old
objects, then uploads HTML and unhashed files with `no-cache` and deletes stale
paths before invalidating the CDN
([`docs-cd.yml:73-102`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-cd.yml#L73-L102)).
This is a robust generic static-deployment policy, though an object lifecycle is
needed so old hashed assets do not accumulate forever.

The exact host is not the architectural point. Proctor should require a static
artifact, immutable deployment by digest, HTTPS, short-lived deployment
identity, correct cache classes, atomic promotion or rollback, and a tracked
description of any CDN redirect/header behavior.

### Documentation impact automation

Mattermost has an AI-assisted workflow that analyzes code changes against
documentation personas and paths. The conceptual mapping from product areas to
audiences is useful. The implementation is currently part of the migration
split: it checks out the separate `mattermost/docs` repository and describes
Sphinx/RST content
([`docs-impact-review.yml:30-64`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-impact-review.yml#L30-L64)).
Another workflow writes generated drafts to that separate repository and targets
versioned `*-documentation` branches
([`docs-needed.yml:24-36`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-needed.yml#L24-L36),
[`docs-needed.yml:147-178`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.github/workflows/docs-needed.yml#L147-L178)).

Proctor should begin with deterministic ownership signals: pull-request
checklists, path-based reviewers, OpenAPI agreement, and a required docs-impact
field. Automated drafting is not a substitute for an authority model and is not
needed to launch the site.

## Mattermost migration debt and gaps

The following findings should not be mistaken for Docusaurus limitations. Most
are migration residue or incomplete integration work in this particular
snapshot.

| Finding | Consequence | Proctor response |
| --- | --- | --- |
| Broken links and images are warnings | A green build can publish missing navigation or assets | Set all internal link/image policies to fail from the first page |
| Main sidebar uses extensive manual URL-preserving regrouping | High maintenance cost, duplicate logic, warning-only orphan handling | Use an explicit or filesystem sidebar; add generation only after measured need |
| Redirect metadata reports 691 active, 217 missing-target, and 1,113 dropped anchored sources out of 2,021 internal redirects ([`active-redirects.json:1-8`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/sidebars/active-redirects.json#L1-L8)) | A substantial legacy URL surface is unresolved or needs CDN rules | Establish stable Proctor URLs early; test every redirect and keep the source map tracked |
| Redirect generator expects an absent migration JSON input ([`gen-active-redirects.mjs:21-26`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/gen-active-redirects.mjs#L21-L26)) | Committed redirects are not reproducible from a fresh checkout | Never commit derived routing data whose authority is absent |
| Comments and pages refer to a nonexistent `PLAN.md` and old `docs-site/` paths | Rationale and maintenance instructions are stale or inaccessible | Put accepted rules in tracked Proctor architecture/contributing docs; delete superseded plans |
| Config comment says the main source is `../docs`, while the actual path is `../main` ([`docusaurus.config.ts:9-12`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L9-L12), [`docusaurus.config.ts:91-97`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L91-L97)) | Maintainer comments no longer match behavior | Test examples and treat comments as part of documentation review |
| The sidebar entry point contains a duplicate unreachable `end-user-guide` branch ([`gen-documentation-sidebar.mjs:1713-1725`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/gen-documentation-sidebar.mjs#L1713-L1725)) | Manual-growth debt is already visible | Keep generators small, typed, and unit tested |
| Vale is local-only | Terminology and voice can drift while CI stays green | Add a Proctor vocabulary and enforce it in CI |
| Page descriptions are sparse | Search and social/SEO summaries are inconsistent | Require `title` and `description` in public frontmatter |
| Generated reference inputs are missing from CI path filters | Reference pages can lag product code | Maintain and test a complete generator dependency manifest |
| OpenAPI generation mutates documentation strings and fetches unpinned remote content | Non-reproducible output and possible semantic drift | Render the tracked Proctor spec unchanged and keep generation offline |
| Raw generated HTML is injected without an explicit sanitizer | Reviewed source comments become an HTML trust boundary | Prefer structured data-to-React rendering or sanitize with tests |
| Site code has no component/generator/accessibility tests | Nontrivial custom interactions rely on manual review | Add focused unit tests plus one browser accessibility smoke suite |
| Search and fonts need third-party runtime services | Local/offline experience differs from production | Self-host fonts and use a static or explicitly self-hostable search index |
| Only English is configured and no Docusaurus versioning is present | Future locale/release routing will require a design change | Reserve a version and locale policy before the first stable release |
| Preview build and deployment share long-lived credentials | Selected PR code executes in a privileged job | Separate untrusted build artifacts from trusted OIDC deployment |
| Preview URLs use HTTP | Review traffic has no transport protection | Require HTTPS |
| PDF tooling is manual, untested, and partly stale | It is not a dependable release artifact | Defer PDF; ship a static offline bundle first |
| Footer section links point to generic roots instead of named destinations ([`docusaurus.config.ts:205-237`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L205-L237)) | Footer navigation does not perform the task its labels imply | Test every navigation landmark and use exact destinations |
| `editUrl` uses a repository tree URL ([`docusaurus.config.ts:91-125`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L91-L125)) | “Edit this page” may open browsing rather than an edit form | Generate exact edit links and test them |

## Constraints specific to Proctor

### Preserve existing authorities

The public site must extend, not replace, Proctor's documentation system:

- [the glossary](../../CONTEXT.md) remains the implementation-free authority
  for Institution, Academic Unit, Programme, Programme Level, Academic Period,
  Class, Exam, Exam Revision, Exam Sitting, Exam Attempt, Exam Manager, and
  related terms;
- [architecture](../architecture/) remains the owner of durable
  cross-component design and rationale;
- exact behavioral contracts remain beside their components;
- [project status](./status.md) remains the capability and open-decision
  authority;
- module setup and commands remain in module READMEs and Makefiles; and
- `docs/README.md` remains the repository navigation hub.

Public task guides may explain how to achieve an outcome, but should link to or
derive exact contract facts instead of creating a parallel inventory. The docs
site should render existing files directly only when their repository-relative
links and intended audience are compatible with publication. Otherwise, the
site should link to the source on GitHub and keep the public task guide focused.

### Use Proctor's domain, not Mattermost's information architecture

Proctor represents exactly one institution per installation. Public docs must
not inherit Mattermost workspace/team terminology or introduce tenant routing.
`Class`, not Group, is the roster. Membership and affiliation do not themselves
grant permissions. An Exam Manager is the creator or a teacher with equal
exam-management authority and is not called a proctor or grader
([glossary](../../CONTEXT.md), lines 3-51 and 194-197).

The examination model is Exam → mutable Draft → immutable Revisions → Sittings
→ Attempts, with workspaces, optional execution environments, integrity evidence,
submission, and review
([examinations](../architecture/examinations.md), lines 3-37). Candidate guidance
must not imply grading, scoring, rubrics, guilt determinations, offline
participation, or other deferred features; those are explicitly outside or
deferred in the current model
([examinations](../architecture/examinations.md), lines 599-619).

### Separate implemented guidance from product intent

Proctor already has a substantial server and API foundation, but visual account,
invitation, desktop authorization, candidate IDE, and some other product slices
remain incomplete or separate work ([project status](./status.md), lines
694-720). A polished docs page can accidentally make planned behavior look
available. Public pages should carry one of three machine-checked states:

- **available** — implemented and verified in a released product;
- **preview** — implemented but explicitly pre-release or unstable; or
- **planned** — excluded from default navigation and clearly non-operational.

Do not copy Mattermost's edition badges. A small maturity notice can be useful,
but the authority remains project/release status rather than hand-maintained
marketing copy.

### Design for self-hosting and isolated use

Proctor's product build supports single-node and active-active deployments,
observable infrastructure, deterministic release archives, and a minimal
runtime image ([build guide](../../build/README.md), lines 1-34). The
documentation site need not be bundled into the application server, but its
output should be a standalone static artifact that can later be included in a
release archive or mirrored internally without Google Fonts, hosted search,
analytics, or remote images.

### Align the toolchain without coupling the products

Proctor's web application already uses Node `22.22.0`, React 19, TypeScript,
ESLint, Vitest, and locked npm dependencies
([`webapp/package.json`](../../webapp/package.json), lines 1-42;
[`webapp/.nvmrc`](../../webapp/.nvmrc)). A Docusaurus package is therefore a
lower operational burden than introducing another language ecosystem. It should
still have its own lockfile, scripts, and package boundary. The public docs build
must not become part of the server module's independent Go build.

### Settle licensing and provenance before copying code

Mattermost's top-level policy licenses source under AGPL unless an identified
exception applies
([`LICENSE.txt:9-20`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/LICENSE.txt#L9-L20)).
Conceptual patterns do not require copying its CSS, TypeScript, prose, assets, or
brand. Any direct or substantial adaptation must record the exact revision,
path, license, required notices, and Proctor-specific review as required by
Proctor's existing provenance policy.

Proctor's current [licensing document](../../LICENSING.md) explicitly assigns
the server/webapp to AGPL and reusable packages to Apache-2.0, but does not yet
assign a license to public documentation content or a future `docs/site`
package. That is a decision to make before publication. This report does not
provide legal advice.

## Recommended Proctor information architecture

Use reader outcomes as the primary entry point and the domain model as the
secondary hierarchy. Avoid reproducing Mattermost's product-overview,
integrations, commercial-plan, and broad collaboration taxonomy.

| Route | Primary reader | Initial scope | Authority or source |
| --- | --- | --- | --- |
| `/` | Everyone | What Proctor is, what it is not, choose a role, current release status | Public overview plus links to status and glossary |
| `/candidate/` | Student taking an exam | Prepare, join a Sitting, understand connectivity and participation, use workspace/terminal, respond to suspension or correction, submit, get help | Released candidate UI and examination contracts |
| `/exam-manager/` | Exam creator or delegated Exam Manager | Create Draft, publish Revision, attach resources/starter workspace, choose execution profile, schedule Sitting, monitor participation, correct, suspend/re-allow, review submission/integrity | Examination model and released manager UI |
| `/institution-admin/` | System or scoped academic administrator | Bootstrap, Institution, academic hierarchy, periods/classes, onboarding, identity providers, roles/bindings, access policy, audit | Access, identity, authorization, domain, HTTP contracts |
| `/operator/` | Installer, SRE, platform operator | Install, configure, TLS, PostgreSQL, Redis, VFS/object storage, mail, execution hosts, HA, health/readiness, metrics, backup/restore, upgrade, troubleshooting | Build/server READMEs and runtime/configuration architecture |
| `/security/` | Security and privacy reviewer | Threat boundaries, credentials, audit, data classes, examination containment, execution isolation, retention status, deployment hardening | Security, identity, authorization, files, execution architecture |
| `/developers/` | Contributor or integrator | Repository setup, module boundaries, contribution workflow, reusable modules, HTTP/WebSocket integration, links to exact architecture/contracts | Existing contributor docs, module READMEs, contracts |
| `/api/` | API consumer | Generated endpoint pages, auth/assurance, idempotency, errors, examples | `server/openapi.json` and agreement tests |
| `/releases/` | Operators and integrators | Supported versions, upgrade notes, compatibility and deprecations | Release process and tagged artifacts, once defined |

Candidate and Exam Manager guides should launch only when the corresponding UI
slice is usable. Until then, the first valuable public site can focus on
operators, institution administrators, security reviewers, developers, and the
API.

Every landing page should answer four questions without marketing detours:

1. Who is this for?
2. What can they accomplish in the current release?
3. What must they know before starting?
4. Where do they go next or get help?

## Recommended source and build shape

```text
docs/
├── README.md                    # existing repository documentation hub
├── architecture/               # existing durable design authority
├── contributing/               # existing contribution authority
├── project/                    # status and design-input research
├── public/                     # new task-oriented public documentation
│   ├── index.mdx
│   ├── candidate/
│   ├── exam-manager/
│   ├── institution-admin/
│   ├── operator/
│   ├── security/
│   ├── developers/
│   └── releases/
└── site/                       # private Docusaurus package
    ├── package.json
    ├── package-lock.json
    ├── docusaurus.config.ts
    ├── sidebars.ts
    ├── scripts/
    ├── src/
    ├── static/
    └── .generated/             # ignored, rebuilt from tracked sources
        └── api/
```

Recommended initial composition:

- one ordinary docs instance reads `docs/public/` at `/`;
- one OpenAPI docs instance reads generated endpoint MDX at `/api`;
- an explicit typed sidebar names only the small initial section set;
- exact architecture and component contracts stay in place and are linked to
  GitHub where direct rendering would break their repository context;
- brand fonts, icons, screenshots, and search index are local assets;
- the site package produces one static build directory and one manifest
  recording source commit, product version, build time, and content version;
- the build checks `server/openapi/`, reads the generated
  `server/openapi.json` artifact, and writes ignored output without rewriting
  either contract source or artifact; and
- every generator receives local paths explicitly and has a unit test plus a
  reproducibility check.

Do not use multiple Docusaurus instances merely for symmetry. Add a separate
developer instance only when its navigation and route lifecycle genuinely
diverge from the public guides. Do not publish current architecture documents by
copying them into `docs/public/`; either render the original source through a
tested adapter or link to it.

## Minimum quality and security gate

The first docs-site pull request should define one command, such as
`make docs-check`, that runs all of the following from a fresh checkout:

1. `npm ci` with the docs lockfile and the repository's pinned Node version.
2. Compile and drift-check tracked `server/openapi/`, then generate API pages
   only from the resulting tracked `server/openapi.json` artifact.
3. Type-check the site and generator code.
4. Run unit tests for generators and custom MDX components.
5. Run the Docusaurus production build with broken links and images set to
   `throw`.
6. Extend Proctor's documentation portability test to `.mdx`, frontmatter, and
   site asset rules, then run `make -C server architecture`.
7. Run Vale with a Proctor vocabulary derived from `CONTEXT.md`; fail on
   forbidden domain substitutions such as tenant, Group for Class, or proctor
   for Exam Manager where the canonical meaning is intended.
8. Validate required `title`, `description`, maturity, and audience frontmatter.
9. Run an accessibility browser smoke test over the landing pages, navigation,
   theme toggle, search, callouts, and generated API pages.
10. Check that the built site makes no unintended third-party requests and that
    all external links use HTTPS. Check external link reachability on a scheduled
    job rather than making ordinary builds network-dependent.
11. Enforce asset size, filename, and alternative-text rules; keep screenshots
    near their owning page where practical.
12. Build twice from the same source and compare a normalized artifact manifest.

The docs workflow must trigger when any of these change:

- `docs/public/**`, `docs/site/**`, or the documentation style configuration;
- `server/openapi/**`, `server/openapi.json`, or the compiler/code/tests that
  own their agreement;
- a tracked source consumed by any future reference generator;
- the Node version, lockfile, build container, or docs workflow itself; and
- release metadata used for versioned output.

Preview builds must run without repository or cloud secrets. A separate trusted
job may deploy their exact artifact through OIDC. Production deployment should
rebuild or verify the artifact in a protected context, publish hashed and
unhashed files with distinct cache policies, support rollback, and keep CDN
redirects/security headers in tracked infrastructure.

## Phased proposal

### Phase 0 — settle the documentation contract

Decide and record:

- the license for public prose, screenshots, examples, and `docs/site` code;
- canonical hostname and repository/hosting ownership;
- whether the first release publishes only `latest` or a versioned route;
- the public maturity vocabulary and the rule for planned content;
- English-only launch versus a locale-ready path strategy;
- self-hosted/static search versus an external service;
- privacy posture for analytics, fonts, search, and embedded media;
- supported contributor operating systems; and
- which existing maintainer documents are public, linked to GitHub, or omitted
  from the public navigation.

Exit when these are tracked under the authority that owns them, not in an
ephemeral plan.

### Phase 1 — create a strict static skeleton

- Add the private Docusaurus package and lock it to Proctor's Node toolchain.
- Add `docs/public/index.mdx` plus operator, administrator, security, developer,
  and API landing pages with substantive but narrow content.
- Define Proctor tokens, typography, callouts, cards, code blocks, and dark mode
  without copying Mattermost brand assets or CSS.
- Use an explicit sidebar and local assets.
- Add `docs-check`, required metadata validation, strict broken-link/image
  behavior, MDX portability validation, tests, and a browser accessibility
  smoke.
- Build a static artifact in CI; do not deploy yet.

Exit when a fresh checkout can produce the same site artifact through one
documented command and every internal navigation path is checked.

### Phase 2 — publish operator, administrator, and security journeys

- Write install, first boot/bootstrap, configuration, dependencies, TLS,
  storage, mail, execution-host, observability, HA, backup/restore, upgrade, and
  troubleshooting journeys from released behavior.
- Write institution structure, onboarding, identity, role, access-policy, and
  audit journeys.
- Write security/privacy pages that distinguish product guarantees,
  installation responsibilities, and open decisions.
- Add a concise “Mattermost users: important differences” page only if migration
  demand exists; it must emphasize one Institution, PostgreSQL authority,
  Proctor's academic model, and the absence of Mattermost commercial tiers.
- Add HTTPS pull-request previews with build/deploy privilege separation.
- Publish the static site with OIDC and tracked cache/header behavior.

Exit when a new operator can deploy an implemented release without relying on
the internal architecture guide as a runbook.

### Phase 3 — generated API and developer reference

- Generate endpoint pages from the drift-checked `server/openapi.json`
  artifact.
- Present authentication, assurance, idempotency, stable errors, examples, and
  WebSocket discovery using structured contract data.
- Test that generated routes and navigation cover every operation exactly once.
- Add developer setup, module-boundary, contribution, and integration guides
  that link to exact contracts and package READMEs.
- Add deterministic configuration reference generation only if a canonical
  schema or metadata source exists; do not infer it from incidental Go syntax.

Exit when an API change that passes agreement tests cannot leave the published
reference stale.

### Phase 4 — candidate and Exam Manager journeys

- Add candidate preparation, Sitting entry, participation, connection-loss,
  workspace/terminal, correction, suspension, submission, and help flows after
  the candidate UI is released.
- Add Exam Manager authoring, revision, resources, starter workspace, execution
  profile, Sitting, monitoring, correction, enforcement, and review flows after
  the corresponding manager UI is released.
- Use screenshots only for stable interaction landmarks; prefer text and
  semantic diagrams for invariant behavior.
- Validate every page against the glossary and examination architecture, and
  keep integrity flags explicitly distinct from findings of guilt.

Exit when both roles can complete their released end-to-end journey and the
docs make all interruption, privacy, and authority boundaries explicit.

### Phase 5 — releases, offline distribution, search, and localization

- Define release snapshots and redirects only when the product has a stable
  documentation compatibility policy.
- Include the static site or a compressed offline variant in release artifacts
  for isolated institutions.
- Add a locally built search index; external search remains optional rather than
  required for navigation.
- Add translated navigation/component strings and locale builds when product
  support and maintainership justify them.
- Evaluate PDF only after a concrete printable runbook, examination manual, or
  compliance-package need exists. Treat URL rendering as an SSRF-sensitive
  service and test print output visually.

Exit when each additional distribution is reproducible from the same canonical
content and does not fork the documentation authority.

## Decisions and non-goals

The analysis supports Docusaurus as the pragmatic default because Mattermost
demonstrates the required static, multi-audience, MDX, OpenAPI, and preview
capabilities, and Proctor already operates a compatible Node/React toolchain.
This is not yet an architecture decision. A short prototype should confirm
build time, OpenAPI rendering, offline behavior, accessibility, and integration
with the existing documentation gate before adoption.

The initial site should not include:

- commercial plans, editions, tenant routing, hosted-cloud variants, or
  scale-gating;
- a copied Mattermost theme, prose corpus, sidebar generator, PDF system, or
  automation workflow;
- generated SDK references without a stable public SDK contract;
- AI-authored drafts as an authority or required release step;
- runtime analytics, fonts, images, or search calls that are not explicitly
  reviewed; or
- documentation for planned UI and domain features presented as available.

The durable lesson from Mattermost is the three-layer system—authored task
guides, deterministic source-derived references, and a static
presentation/deployment package. Proctor can adopt that shape now while avoiding
the migration and commercial complexity that made Mattermost's implementation
much larger than Proctor needs.
