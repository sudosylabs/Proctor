# Mattermost documentation content and API pipeline analysis

## Purpose and scope

This report examines the tracked Mattermost documentation implementation at
commit [`a3e171f730781dc87e5eb0f36d556f9eb39fc22a`](https://github.com/mattermost/mattermost/tree/a3e171f730781dc87e5eb0f36d556f9eb39fc22a).
It focuses on the content and data needed to produce a professional
documentation experience, with particular attention to the generated REST API
reference. It then compares that system with Proctor's current documentation
site and reviewed OpenAPI contract.

The source analysis is based on tracked files at the pinned Mattermost revision
and tracked Proctor sources. Counts below are reproducible tracked-source
inventories at that revision; they are not marketing claims or live-site
measurements. A separately identified local compatibility probe used generated
output only to test the renderer boundary; that output is temporary and is not
treated as documentation authority.

## Executive summary

Mattermost's professional documentation experience comes from four systems
working together:

1. **A large, deliberately organized content corpus.** The audited revision has
   569 authored Markdown/MDX pages: 416 product/operator pages, 151 developer
   pages, and two authored API landing pages. The site exposes them as three
   documentation instances with separate routes, navigation, and edit links,
   rather than forcing every audience into one hierarchy
   ([site configuration](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L84-L128)).
2. **A substantial visual and interaction layer.** The tracked site contains
   726 image assets: 536 PNG, 73 JPG, 63 GIF, and 54 SVG files. Two hundred
   authored pages contain image markup, with 1,768 detected image references.
   Landing pages combine audience-specific cards, diagrams, screenshots, and
   product narrative; for example, the main page moves from persona entry points
   into illustrated product workflows
   ([main landing page](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/main/index.mdx#L10-L107)).
3. **A small MDX design system.** Authors can use globally registered semantic
   callouts, availability badges, diagrams, cards, upgrade filters, tabs, and
   generated reference components without local imports
   ([MDX component registry](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/theme/MDXComponents.tsx#L1-L58)).
   Brand tokens and component CSS make those patterns look coherent
   ([design tokens](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/css/tokens.css#L11-L72)).
4. **A deterministic-looking but not fully hermetic reference pipeline.** Rich
   OpenAPI YAML fragments are concatenated, augmented with Go examples, and
   validated. A site script sanitizes the result for MDX, and
   `docusaurus-plugin-openapi-docs` emits endpoint pages and a sidebar. Most
   generated files are ignored. The architecture is reusable; the unpinned
   Playbooks download, POSIX concatenation, and broad text rewriting are not.

Proctor should preserve its stronger runtime agreement while adopting
Mattermost's human-first authoring lesson. The resource-oriented
[`server/openapi/`](../../server/openapi/) YAML modules are the maintained
source; a deep compiler discovers and collision-safely merges them, validates
OpenAPI 3.1 and Proctor metadata, and emits the deterministic reviewed
[`server/openapi.json`](../../server/openapi.json) artifact. The contract has
172 paths, 218 operations, 268 schemas, explicit security and product-area tags
on every operation, and complete `x-proctor-auth`, `x-proctor-error-codes`, and
`x-proctor-idempotency` coverage. Phase 2 also completed behavioral descriptions,
parameter and request-body purpose prose, and mutation request examples across
the entire contract. A local compatibility probe confirmed that Mattermost's
OpenAPI renderer accepts the generated artifact unchanged and emits one page
per operation.

The recommendation is therefore:

- author routes, definitions, descriptions, and examples in small domain and
  resource YAML modules;
- keep generated `server/openapi.json` tracked and reviewed as the exact
  consumer/runtime-agreement artifact;
- generate browsable endpoint pages and their sidebar into ignored build input;
- keep task-oriented API guides in `docs/api/` as authored content, separate
  from the main docs plugin root so each MDX file has one renderer;
- introduce images only after the current image-processing dependency gate is
  resolved, with a tracked asset registry and freshness/privacy checks; and
- begin feedback with edit/issue links, adding a minimal privacy-preserving
  helpfulness event only when there is an owner and retention policy.

Mattermost's YAML organization is **not a requirement of the renderer**; it is
an authoring convention. Proctor adopts that human-facing Seam without adopting
raw concatenation: every fragment is a normal partial OpenAPI document, the
compiler hides ordering and indentation, duplicate ownership fails closed, and
artifact drift is checked in CI.

## 1. What makes the Mattermost experience feel professional

### 1.1 Content depth, not just site chrome

The visual shell is only the final layer. Mattermost has enough material to
answer questions at different levels:

- role-oriented landing pages help an end user, administrator, developer,
  security architect, SRE, compliance officer, or air-gapped operator choose a
  starting point
  ([persona navigation](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/main/index.mdx#L12-L24));
- conceptual product narrative explains why capabilities exist before linking
  to task-level guides
  ([product overview](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/main/index.mdx#L26-L44));
- task pages contain concrete procedures, screenshots, constraints, version
  notes, and troubleshooting;
- developer documentation has its own generated filesystem hierarchy rather
  than sharing the product sidebar
  ([developer sidebar generator](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/gen-developer-sidebar.mjs#L38-L94)); and
- API overview and quick-start pages provide a curated path into the much larger
  generated reference
  ([API overview](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/api/index.mdx#L9-L75),
  [curl recipes](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/api/examples.mdx#L9-L84)).

This produces a useful three-level information architecture:

```text
audience or job
  -> task/concept/troubleshooting guide
      -> exact generated API or configuration reference
```

Proctor's current role split under [`docs/public/`](../public/) already follows
the first level. The next quality increase will come more from filling the
operator, institution-administrator, security, developer, and API journeys than
from adding more visual effects.

### 1.2 Multiple documentation instances

Mattermost renders one static site but configures three independent Docusaurus
docs instances:

| Source | Route | Purpose |
| --- | --- | --- |
| `docs/main` | `/` | Product, user, operator, administration, security |
| `docs/develop` | `/developers` | Contributor and integration material |
| `docs/api` | `/api` | Authored API entry pages plus generated reference |

Each instance has its own sidebar and GitHub edit base
([configuration](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L84-L128)).
This is valuable at Mattermost's scale. It is not necessary for Proctor's
current six public entry pages. Proctor should keep one docs instance until
sidebar size, versioning policy, or audience-specific release cadence makes a
second instance materially simpler.

### 1.3 Screenshots, illustrations, and assets

The pinned Mattermost tree has a large static image corpus. A representative
landing page uses product screenshots with explanatory alt text after each
capability section, not decorative images detached from the prose
([messaging and Playbooks images](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/main/index.mdx#L46-L71),
[Calls, Boards, and Agents images](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/main/index.mdx#L73-L107)).

Observed asset roles include:

- product screenshots that establish UI orientation;
- diagrams that explain architecture or workflow;
- SVG icons and brand marks used by the shell and reusable components;
- animated GIFs for interaction sequences; and
- illustrations used on landing and product-overview pages.

There is no evidence of a single tracked asset registry in the inspected
Mattermost site. The static tree and page references are the practical index.
That scales operationally only when editorial ownership, naming, and screenshot
refresh work are already mature. Proctor should make those obligations explicit
before accumulating hundreds of assets.

Proctor also has a current, deliberate constraint: its site README says authored
images are blocked until a fixed transitive `image-size` release is available
([site README](../site/README.md#current-dependency-constraint)). The initial
typography/CSS system is therefore the correct present implementation, not an
incomplete imitation. An asset program belongs after that dependency gate is
resolved. Resolution does not have to mean waiting indefinitely: a dedicated
asset slice may either upgrade to a fixed dependency or prove a constrained
static-asset path that bypasses the vulnerable parser, permits only reviewed
formats, and still validates dimensions, bytes, alternative text, and registry
ownership. That path must be tested before the blanket image prohibition is
relaxed.

### 1.4 Reusable MDX components

Mattermost's component registry exposes content primitives globally
([registry](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/theme/MDXComponents.tsx#L30-L58)).
The most reusable ideas are:

- semantic `Note`, `Tip`, `Important`, `Warning`, and `Security` callouts with a
  predictable accessible wrapper
  ([implementation](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/Callout/index.tsx#L4-L42));
- `CardGrid` as a data-driven navigation primitive with title, description,
  destination, optional icon, and optional metadata
  ([implementation](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/src/components/CardGrid/index.tsx#L5-L41));
- availability and scope components for repeated product constraints;
- diagrams for stable conceptual models; and
- tabs for platform- or language-specific instructions.

The risk is that MDX prose becomes coupled to React component APIs. Proctor
should keep the component vocabulary small, semantic, and versioned by content
need. Its existing `Maturity`, `RoleGrid`, and `LifecycleRibbon` components are a
good bounded start. Add a component only when at least three pages need the same
semantic pattern or when accessibility is safer when centralized.

## 2. Mattermost's REST API reference pipeline, end to end

### 2.1 Authored YAML fragment tree

Mattermost tracks 56 YAML files in `api/v4/source`:

- `introduction.yaml` starts the document with `openapi`, rich `info`, the
  top-level tag catalog, server variables, and the `paths:` key;
- 54 resource files contribute indented path entries such as users, channels,
  posts, files, system, LDAP, audits, and agents; and
- `definitions.yaml` contributes `components`, including security schemes,
  shared responses, and schemas.

Mattermost's own contribution guide routes endpoint changes to the matching
resource file, tag changes to the introduction, and schema changes to the
definitions tail
([API contribution guide](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/README.md#L13-L22)).
At the pinned revision, a tracked-source lexical inventory finds 479 path keys
(220 unquoted and 259 quoted), 600 HTTP operation blocks, 597 explicit
`operationId` values, and 40 top-level tags before any remote Playbooks input is
merged. These counts expose content drift in the authored API landing page: its
stat strip says 549 endpoints and 38 groups, while its generation example says
435 paths and 550 operations
([landing metrics](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/api/index.mdx#L11-L18),
[generation claims](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/api/index.mdx#L77-L86)).
Proctor should generate such counts from the canonical document at build time,
not type them into MDX.

The introduction is substantive documentation, not boilerplate. It embeds API
conventions, authentication, rate limits, WebSocket behavior, support, and
contribution guidance before defining the title/version
([end of the API guide](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/v4/source/introduction.yaml#L360-L388)).
It then declares named tags with human descriptions and a parameterized server
URL
([tags and servers](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/v4/source/introduction.yaml#L389-L485)).

Resource fragments are rich OpenAPI prose. A users operation can include:

- tag, summary, description, and stable `operationId`;
- permission and feature requirements;
- field descriptions and required properties;
- request-body and response descriptions;
- shared schema/response references;
- deprecation state and migration direction; and
- multi-step authentication flow and error guidance.

The opening login operations demonstrate the ordinary structure
([users fragment](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/v4/source/users.yaml#L1-L85));
the SSO and Intune operations show deprecation and long-form workflow/error prose
([users fragment](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/v4/source/users.yaml#L120-L230)).

Shared response descriptions and schemas live in `definitions.yaml`, beginning
with bearer authentication and reusable error responses
([definitions](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/v4/source/definitions.yaml#L1-L67)).

#### YAML is an organization choice, not a rendering requirement

The resource files are not individually valid complete OpenAPI documents. They
depend on exact indentation and concatenation beneath the `paths:` key opened by
`introduction.yaml`. That choice makes a very large specification easier for
Mattermost authors to partition by resource and preserve legacy tooling.

Neither Docusaurus nor `docusaurus-plugin-openapi-docs` inherently requires this
layout. The plugin is configured with one assembled `specPath`, not the fragment
directory
([plugin configuration](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L137-L154)).
OpenAPI JSON and YAML are equivalent serialized inputs at that boundary.

For Proctor, converting the existing JSON into 50 YAML files would provide no
rendering benefit. It would also weaken the current relationship between the
reviewed contract and its agreement tests. Fragmentation should be considered
only if authoring collisions or reviewability become a demonstrated problem,
and then implemented with a schema-aware deterministic bundler rather than raw
indentation-sensitive concatenation.

### 2.2 Makefile assembly

The API Makefile constructs `api/v4/html/static/mattermost-openapi-v4.yaml` in a
fixed sequence:

1. use merged Playbooks tags if present, otherwise start with
   `introduction.yaml`;
2. append every Mattermost resource file in a hand-authored order;
3. append externally extracted Playbooks paths if present;
4. use merged component definitions if present, otherwise append
   `definitions.yaml`;
5. run a Go program that injects code samples;
6. validate the resulting document with `swagger-cli`; and
7. copy the legacy server-rendered HTML template.

The complete sequence is visible in the
[`api/Makefile`](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/Makefile#L10-L77).
Dependencies are installed with `npm install`, not the more reproducible
`npm ci`
([Makefile](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/Makefile#L79-L83)).

The Playbooks input is downloaded during the build from the `master` branch of
another repository, without a pinned revision
([extractor](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/playbooks/extract.js#L24-L50)).
Consequently the same Mattermost commit can produce different API bundles at
different times. Proctor should not copy this behavior. Every documentation
build input must be in the checkout or pinned by immutable digest/revision.

### 2.3 Go code-sample injection

After concatenation, Mattermost's Go generator:

1. parses the assembled OpenAPI model;
2. walks all operations;
3. scans functions under `server/public/model`;
4. matches `ExampleClient4_<operationId>` to the operation's exact
   `operationId`;
5. extracts the example body and imports with the Go AST;
6. formats a standalone `main` program; and
7. writes it into the operation as an `x-codeSamples` entry with `lang: Go`.

The matching contract is explicit in the generator
([operation matching](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/server/main.go#L76-L113)),
as is the `x-codeSamples` insertion
([sample rendering and extension](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/server/main.go#L114-L164)).
The source directory scan is hard-coded
([model scan](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/server/main.go#L171-L190)).

There are only 33 matching example functions in the tracked model package, so
the explicit Go coverage is selective. The operation walk also calls GET, POST,
DELETE, OPTIONS, HEAD, PATCH, and TRACE but omits PUT
([operation walk](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/api/server/main.go#L82-L90)).
At the same revision the OpenAPI fragments contain 63 PUT operation blocks, so
a correctly named PUT example cannot be attached by this generator. Proctor
should test every supported HTTP method if it adopts convention-based sample
injection.

This is not a general extraction of code comments. It is a convention-based
join between OpenAPI operation IDs and compilable Go example functions. It is
valuable because a sample can exercise the real public Go client. Proctor has no
equivalent client-example corpus today, so it should begin with explicit
OpenAPI examples and generated HTTP snippets, then add checked client examples
only when a supported client library exists.

### 2.4 Site-side bundle sanitization

`docs/site/scripts/build-openapi.mjs` calls `make -C api build`, reads the raw
YAML output, and walks every value whose key is `description`, `summary`, or
`title`. It replaces quoted phrases with curly quotes and escapes selected `<`
characters to avoid generated MDX/frontmatter parse failures. It writes the
sanitized document to `docs/site/openapi/mattermost-openapi-v4.yaml`
([script contract and paths](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/build-openapi.mjs#L1-L27),
[rewrite and output](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/scripts/build-openapi.mjs#L42-L82)).

This sanitizer is a compatibility workaround, not part of the OpenAPI data
model. It may change exact quoted values in prose or examples and has no
targeted test in the site package. Proctor should first test its JSON unchanged
with the selected renderer. If a renderer exposes an MDX bug, fix or narrowly
escape only the failing generated surface and preserve a fixture proving that
JSON/code text is unchanged.

### 2.5 Endpoint page generation and `ApiItem`

The site declares:

- the `docusaurus-plugin-openapi-docs` generator;
- `specPath: openapi/mattermost-openapi-v4.yaml`;
- generated page output at `docs/api/reference`;
- path grouping by OpenAPI tag; and
- category pages sourced from tag metadata.

See the
[`docusaurus.config.ts` API generator block](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L137-L160).

The separate API docs instance sets `docItemComponent: '@theme/ApiItem'`, which
selects the endpoint renderer supplied by `docusaurus-theme-openapi-docs`
([API docs instance](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L118-L127)).
That renderer, rather than Mattermost-authored endpoint MDX, lays out method and
path, prose, parameters, request and response schemas, authentication, and code
samples from the generated page metadata.

The npm lifecycle makes the generation order explicit:

```text
build:openapi:spec
  -> build-openapi.mjs
  -> api/Makefile
  -> assembled and sanitized OpenAPI bundle

build:openapi:docs
  -> docusaurus gen-api-docs mattermost
  -> generated endpoint/tag MDX plus generated sidebar

prebuild
  -> sidebars + OpenAPI + other source-derived references
  -> docusaurus build
```

See the
[`package.json` scripts](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/package.json#L5-L26).

### 2.6 Code sample sources

Mattermost configures five displayed language tabs: curl, PowerShell, Python,
Node, and Go
([language tabs](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/docusaurus.config.ts#L280-L288)).
There are two distinct data sources:

- the OpenAPI renderer can derive HTTP snippets for configured languages from
  method, URL, parameters, request media type, schemas, examples, and security;
- the earlier Go generator injects explicit repository-tested Go samples as
  `x-codeSamples` when a matching example function exists.

The tracked YAML fragments contain no authored `x-codeSamples` nodes; the
extension is added to the generated bundle. They do contain a small number of
ordinary OpenAPI `example`/`examples` fields, which improve generated request
values. The language tab configuration alone does not guarantee semantically
useful samples: good examples still require realistic schema examples, server
URLs, authentication metadata, and request bodies.

### 2.7 Descriptions, comments, tags, categories, and sidebars

These terms refer to different mechanisms:

| Term | Mattermost meaning | API-page effect |
| --- | --- | --- |
| OpenAPI `summary` | Short authored YAML operation label | Endpoint title/sidebar label |
| OpenAPI `description` | Authored Markdown prose on info, tag, operation, parameter, body, response, schema, or property | Explanations rendered in the reference |
| `tags` on operations | Resource taxonomy | Groups endpoint pages |
| Top-level tag `description` | Group-level authored prose | Category/tag landing content with `categoryLinkSource: 'tag'` |
| `operationId` | Stable machine join key | Page identity/generation and Go example matching |
| `x-codeSamples` | Generated explicit code sample data | Language-specific sample panel |
| Go source comments | Used by separate plugin SDK generators | Not REST endpoint prose |
| MDX/JavaScript comments | Author/build notes | Not user-visible documentation |
| Page feedback | A user response about a page | Separate analytics/editorial workflow |

The API sidebar is hybrid. A tracked TypeScript wrapper places authored
`Overview` and `Examples` pages before a `Reference` category, then imports the
generated sidebar array
([API sidebar](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/sidebars/api.ts#L1-L27)).
This is a strong pattern: a generator should organize exhaustive reference
material, while humans curate the first steps and common workflows.

### 2.8 Tracked versus generated artifacts

| Artifact | Mattermost status | Why |
| --- | --- | --- |
| `api/v4/source/*.yaml` | Tracked, authored | API reference source prose and schemas |
| `api/Makefile`, merge scripts, Go sample injector | Tracked | Generation logic |
| `api/v4/html/static/mattermost-openapi-v4.yaml` | Generated | Raw assembled bundle |
| `docs/site/openapi/mattermost-openapi-v4.yaml` | Generated, ignored | Renderer input after sanitization |
| `docs/api/index.mdx`, `examples.mdx` | Tracked, authored | Curated API onboarding |
| `docs/api/reference/**` | Generated, ignored | Endpoint/tag pages and generated sidebar |
| `docs/site/sidebars/api.ts` | Tracked | Curated wrapper around generated sidebar |
| `docs/site/build`, `.docusaurus`, `node_modules` | Generated, ignored | Build/dependency output |

The ignore policy is visible in the site and repository ignore files
([site generated data](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/site/.gitignore#L1-L17),
[API/build output](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/.gitignore#L187-L199)).

Ignoring generated endpoint pages avoids noisy commits, but CI must always run
the generator from a clean checkout and watch every generator input. Proctor's
architecture gate should continue forbidding tracked documentation from linking
to ignored generated source paths; routes in the built site are not repository
file dependencies.

## 3. Is Proctor's current OpenAPI document sufficient?

### 3.1 What is already strong

The [`server/openapi/`](../../server/openapi/) source and compiled
[`server/openapi.json`](../../server/openapi.json) artifact are not a sketch.
They form the reviewed public contract. The adjacent
[`server/httpapi/CONTRACT.md`](../../server/httpapi/CONTRACT.md) defines its
relationship to route registration, request/response DTOs, stable errors, and
agreement tests.

Tracked-source inventory:

| Measure | Proctor today |
| --- | ---: |
| OpenAPI version | 3.1.0 |
| Paths | 172 |
| Operations | 218 |
| Component schemas | 268 |
| Operations with `operationId` | 218 / 218 |
| Operations with `summary` | 218 / 218 |
| Operations with explicit `security` | 218 / 218 |
| Operations with `x-proctor-auth` | 218 / 218 |
| Operations with `x-proctor-error-codes` | 218 / 218 |
| Operations with `x-proctor-idempotency` | 218 / 218 |
| Operations with one declared product-area tag | 218 / 218 |
| Operations with behavioral descriptions | 218 / 218 |
| Operation parameter occurrences with descriptions | 357 / 357 |
| Operation request-body occurrences with purpose descriptions | 101 / 101 |
| Mutations with synthetic request examples | 136 / 136 |
| Operations with `x-codeSamples` | 56 / 218 |
| Shared response components with descriptions | 153 / 153 |

This is more robust than many generated references because the security
semantics are explicit and tested. The agreement suite reads the checked-in
document and compares it with runtime routes rather than generating one side
from the other
([agreement module](../../server/httpapi/openapi_agreement_module_test.go),
[catalog-wide agreement](../../server/httpapi/openapi_agreement_test.go)). The
schema validator also requires the Proctor auth/error extensions
([schema validation](../../server/httpapi/openapi_schema_validation_test.go)).

### 3.2 Phase 2 editorial depth

| Metadata/content | Current coverage | Result if rendered unchanged |
| --- | ---: | --- |
| Top-level tag catalog | 16 described tags | Stable generated grouping is ready |
| Operation tags | 218 / 218 | Every operation has exactly one product-area owner |
| Operation behavior descriptions | 218 / 218 | Every endpoint explains behavior beyond its summary |
| Parameter occurrences with descriptions | 357 / 357 | Path, query, and header inputs explain their operational meaning |
| Request-body occurrences with purpose descriptions | 101 / 101 | Every payload explains why and when it is supplied |
| Mutations with request examples | 136 / 136 | JSON examples are schema checked; multipart and bodyless commands use reviewed shell samples |
| Schemas with descriptions | 109 / 268 | Lifecycle- and security-sensitive models have targeted purpose prose |
| Schema properties with descriptions | 306 / 1,275 | Units, redaction, concurrency, and identifier semantics are documented where they are not obvious |
| Operations with checked code samples | 56 / 218 | Every mutation has a media example or executable-style sample without requiring redundant snippets on reads |
| Product areas with Problem Details examples | 16 / 16 | Safe stable-code failures render throughout the reference |
| Product areas with body-bearing success examples | 15 / 15 | Every applicable area has a schema-valid representative response; Realtime succeeds with a `101` handshake |
| Deprecated operations | 0 | Fine if none are deprecated; no migration presentation is exercised |
| Authored API entry paths | 16 / 16 tags | Integration guides and recipes lead into the generated groups |

The response count still requires nuance: most operation responses are shared
`$ref`s, and all 153 shared response components have descriptions. The
operation-owned `x-proctor-error-codes` provide the route-specific stable codes,
while shared safe examples demonstrate the public RFC 9457 shape without
duplicating it hundreds of times.

### 3.3 Sufficiency decision

The current contract is:

- **sufficient as a human-maintainable wire contract and downloadable generated specification;**
- **sufficient as input to the checked Proctor endpoint-page generator;** and
- **sufficient as the reference foundation beneath authored integration guides
  and end-to-end recipes, while broader product documentation remains ongoing.**

The original 15-operation pilot defined the metadata standard across public,
principal-required, session-plus-CSRF, idempotent, multipart/upload,
binary/inline content, pagination, submission, and WebSocket variants. Phase 2
has now applied that standard to all operations in coherent product-area passes
and made the complete coverage a regression-tested build gate.

### 3.4 Local renderer compatibility probe

The same `docusaurus-plugin-openapi-docs` 5.0.2 installation used by the pinned
Mattermost checkout was run in an isolated temporary Docusaurus configuration
against Proctor's then-current OpenAPI 3.1 JSON artifact. Generation exited successfully
and produced:

- 218 endpoint `.api.mdx` pages, exactly one for every Proctor operation;
- one API information page;
- the request, parameter, and status-code datasets used by `ApiItem`; and
- 818 generated files in total.

At the time of that probe the source had no tags, so the generated sidebar
placed all 218 operations in one `UNTAGGED` category. Phase 0 has since added 16
described tags and tagged every operation. The historical result remains useful
evidence that JSON versus YAML and OpenAPI 3.1 parsing were never renderer
blockers. Phase 1 subsequently completed renderer integration, and Phase 2
completed the reference-content and task-guide foundation.

The probe proves ingestion and deterministic page generation, not the final
Proctor package integration. Installing the plugin and theme into Proctor,
wiring the generated sidebar, preserving the `x-proctor-*` extensions, and
passing Proctor's production build remain Phase 1 acceptance work.

## 4. Recommended Proctor source-of-truth architecture

### 4.1 Four explicit content classes

```text
product/domain authorities
  CONTEXT.md + architecture + component contracts + code/tests
        |
        +--> authored public guidance
        |      docs/public/**/*.mdx
        |
        +--> human-authored wire contract
        |      server/openapi/**/*.yaml
        |          |
        |          +--> compiled + reviewed server/openapi.json
        |                    |
        |                    +--> downloadable copied JSON
        |                    +--> generated endpoint/tag pages + sidebar
        |
        +--> tracked visual assets + registry (after dependency gate)
        |
        +--> page feedback events (operational data, never content authority)

presentation only
  docs/site/**
```

This extends the authority split already documented in
[`docs/contributing/documentation.md`](../contributing/documentation.md): public
guidance belongs in `docs/public`, while presentation and build logic belong in
`docs/site`.

### 4.2 Authored pages

Keep task-oriented content under `docs/public/`:

- `operator/`: install, configure, upgrade, backup/restore, high availability,
  health, troubleshooting;
- `institution-admin/`: bootstrap, academic structure, identity, roles,
  invitations, exam administration;
- `security/`: threat model, credentials/assurance, audit, data handling,
  deployment hardening, incident response;
- `developers/`: local development, module boundaries, extension points,
  integration patterns; and
- `api/`: API overview, authentication recipes, errors, pagination,
  idempotency, uploads/content, WebSocket/realtime, and worked workflows.

These pages explain tasks and consequences. They should link to generated
endpoint pages for exact fields rather than duplicating the endpoint inventory.
Their existing frontmatter contract should gain fields only when validation or
navigation needs them, for example `owner`, `last_reviewed`, and
`product_area`; avoid speculative metadata.

### 4.3 Generated API reference

Keep [`server/openapi/`](../../server/openapi/) as the canonical human source
and [`server/openapi.json`](../../server/openapi.json) as its tracked,
deterministic contract artifact. The present `sync-openapi.mjs` correctly
copies the artifact unchanged to an ignored download path
([sync script](../site/scripts/sync-openapi.mjs)). Evolve that build into two
consumers:

1. **publication consumer:** copy the exact bytes to
   `docs/site/static/openapi/openapi.json`;
2. **reference consumer:** feed the exact document to a proven OpenAPI plugin
   and generate endpoint/tag pages plus a sidebar into ignored directories.

The generator must not rewrite the source JSON, infer behavioral promises from
handlers, or strip `x-proctor-*` fields. Add a renderer component or small
adapter that presents:

- `x-proctor-auth` as a first-class credential/assurance panel;
- `x-proctor-error-codes` beside status responses;
- `x-proctor-idempotency` as a command contract;
- CSRF/session/PAT distinctions; and
- Problem Details examples.

If `docusaurus-plugin-openapi-docs` does not preserve those extensions or prove
OpenAPI 3.1 behavior, select another generator or implement a thin generated
MDX adapter. Do not distort the wire contract to fit a renderer.

Recommended generated layout:

```text
docs/api/index.mdx                        tracked authored landing
docs/api/guides/*.mdx                    tracked authored workflows
docs/api/reference/                      ignored generated endpoint/tag pages and sidebar
docs/site/static/openapi/openapi.json     ignored byte-for-byte publication copy
```

The exact paths can change to match the plugin, but tracked and generated
ownership must remain obvious.

### 4.4 API authoring format

Use the resource-oriented YAML modules under `server/openapi/fragments/`.
Routes and exclusively used definitions are co-located; product-area and root
`shared.yaml` modules contain definitions with real shared consumers.
`base.yaml` owns document metadata and the stable tag taxonomy. The compiler
recursively discovers files, so authors add a well-named resource module
without editing a central inventory.

The compiler is an in-process Module with a single filesystem-based Interface.
It hides deterministic ordering, merge mechanics, duplicate detection,
reference resolution, Proctor metadata checks, OpenAPI validation, and JSON
encoding. The CLI is a thin local-filesystem Adapter. This keeps authoring
local and reviewable while preserving the existing generated artifact and
runtime agreement boundary. See the
[`OpenAPI authoring guide`](../../server/openapi/README.md).

### 4.5 Asset ownership and registry

After the image dependency gate is resolved, divide assets by ownership:

- content assets should be colocated with, or under a clearly owned asset
  directory in, `docs/public/`;
- site identity assets such as logos and favicons belong in
  `docs/site/static/`; and
- generated diagrams belong in ignored build output, while their source
  definition remains tracked.

Add a tracked `docs/public/assets.json` registry validated by the site build.
Each entry should contain only fields with an operational use:

```json
{
  "id": "institution-admin.create-academic-unit",
  "file": "institution-admin/assets/create-academic-unit.webp",
  "kind": "screenshot",
  "alt": "Academic Unit creation form with parent and name fields",
  "owner": "institution-administration",
  "used_by": ["institution-admin/academic-structure.mdx"],
  "captured_version": "1.0",
  "last_verified": "2026-08-24",
  "privacy_reviewed": true,
  "license": "project-authored"
}
```

Validation should fail for a missing file, unregistered content image,
duplicate ID, empty alt text for informative images, unused registry item,
unreviewed screenshot, unsupported format, or excessive dimensions/bytes.
Screenshots must use synthetic institutions, people, exam content, and results;
real student or examination data must never enter the repository.

### 4.6 Comments and feedback

"Comments" must not become an ambiguous pipeline requirement:

1. **API descriptions** are explicit OpenAPI fields and belong in
   the resource modules under `server/openapi/`. General Go comments must not
   silently become public REST guarantees.
2. **Go example functions** may later supply checked code samples, but that is a
   naming/AST convention, not comment extraction.
3. **Editorial comments** belong in GitHub pull-request review and are not
   published.
4. **MDX/JavaScript comments** explain authoring or build choices and are not
   user content.
5. **Page feedback** is operational input, not documentation authority.

Mattermost's contributor guide says product pages expose a bottom-right “Did
you find what you were looking for?” prompt
([contributor guide](https://github.com/mattermost/mattermost/blob/a3e171f730781dc87e5eb0f36d556f9eb39fc22a/docs/develop/contribute/why-contribute/index.md#L88-L100)).
However, the inspected tracked Docusaurus site contains no implementation or
data contract for that widget. It may describe the legacy/live property. The
new site does configure GitHub edit links. Therefore Mattermost is evidence for
the value of a feedback affordance, not a reusable feedback backend.

For Proctor:

- Phase 1: keep `Edit this page` and add a prefilled `Report a docs issue` link
  carrying only the stable page path and source commit;
- Phase 2: optionally add a two-state helpful/not-helpful event with reason
  enums such as `missing-step`, `unclear`, `outdated`, `broken`, and `other`;
- do not collect free text initially;
- never send URL query strings, credentials, institution identifiers, exam
  identifiers, candidate data, answers, or arbitrary selected page content;
- use fields such as `page_id`, `docs_commit`, `docs_version`, `locale`,
  `helpful`, `reason`, and coarse timestamp;
- publish retention, access, and deletion policy before collection; and
- keep feedback storage outside the documentation source-of-truth graph.

## 5. Staged implementation and data inventory

### Phase 0 — Establish the API data foundation

**Status (2026-08-24): complete.** Human-owned resource YAML modules now contain
the complete contract, including one stable product-area tag and explicit
authentication, error, and idempotency metadata for every operation. A deep,
tested compiler produces the reviewed JSON artifact and rejects invalid
fragments, duplicate ownership, schema errors, metadata gaps, and artifact
drift. The representative 15-operation pilot has behavioral and input prose
plus redacted shell examples. `npm run audit:openapi` emits exact
machine-readable coverage and tests the required regression failures.

**Work**

- Establish `server/openapi/` as the human authoring authority and
  `server/openapi.json` as its deterministic reviewed artifact.
- Create a machine-readable audit command for operation/tag/description/example
  coverage.
- Define an initial tag taxonomy of roughly 12–20 durable domains, not one tag
  per transport file. Candidate groups include System, Authentication,
  Institutions, Academic Structure, People and Membership, Roles and Access,
  Invitations, Examinations, Sittings, Attempts, Submissions, Integrity Review,
  Audit, Jobs, Mail, and Realtime.
- Select 10–15 representative pilot operations across the security and payload
  variants described above.

**Acceptance criteria**

- There is one human source tree and one generated, drift-checked artifact; the
  renderer owns no parallel annotations or contract copy.
- A clean test reports the exact current coverage counts and fails on duplicate
  operation IDs, unknown tags, missing summaries, or missing Proctor
  extensions.
- Every pilot operation has a tag, behavioral description, parameter/body
  descriptions, success and important failure context, and a redacted example.
- `make -C server architecture` and existing OpenAPI agreement tests pass.

### Phase 1 — Generate the browsable API reference

**Status (2026-08-24): complete.** The locked OpenAPI plugin and theme now
generate one deep-linkable page for each of the 218 operations and one category
page for each of the 16 product areas under a separate `/api` documentation
root. A tracked sidebar wrapper joins the authored overview to the ignored,
reproducible reference. Every endpoint exposes its Proctor authentication,
idempotency, and stable error-code contract, while the underlying renderer owns
security schemes, parameters, bodies, response schemas, examples, and focused
curl, JavaScript Fetch, and Python Requests snippets. Generation and clean
installation have regression tests, and the strict production build is part of
`make docs-check`.

**Work**

- Add the selected OpenAPI generator/theme with exact locked versions.
- Prove its full Proctor production build while preserving every
  `x-proctor-*` extension. OpenAPI 3.1 endpoint generation is already proven by
  the local compatibility probe.
- Extend the current generation script from download-only publication to
  endpoint/tag page and sidebar generation.
- Compose a tracked API sidebar wrapper from authored Overview, Authentication,
  Errors, Pagination, Idempotency, Uploads, Realtime, and Examples pages plus the
  generated Reference category.
- Implement presentation for `x-proctor-auth`, `x-proctor-error-codes`, and
  `x-proctor-idempotency`.
- Configure two or three useful languages initially. Prefer curl plus one
  officially supported language; do not advertise five placeholder languages
  merely because a renderer can synthesize them.

**Acceptance criteria**

- A clean checkout can run the documented install/check flow; generation reads
  no unpinned remote content.
- All 218 operations produce a stable deep-linkable page exactly once.
- Every operation appears in exactly one generated tag group.
- The published OpenAPI download is byte-for-byte equal to
  `server/openapi.json`.
- Generated pages/sidebar/build output are ignored and reproducible.
- The renderer displays security, CSRF/assurance, idempotency, errors, schemas,
  and examples without changing the compiled contract artifact.
- Deleting generated output and rebuilding yields the same route manifest.
- Strict broken-link and broken-image behavior remains enabled.
- CI watches the OpenAPI document, generator, custom renderer, authored API
  guides, lockfile, and sidebar wrapper.

### Phase 2 — Enrich reference content and task guides

**Status (2026-08-24): complete.** All 218 operations have substantive behavior
descriptions; all 357 parameter occurrences and 101 request-body occurrences
have purpose prose; and all 136 mutations have a schema-valid media example or
reviewed executable-style sample. The version 2 audit fails on any regression
and also requires representative safe Problem Details examples for all 16
product areas and success examples for all 15 areas with body-bearing success
responses. Authored integration guides and recipes now give every generated
tag group a human entry path. Targeted schema/property prose covers lifecycle,
units, redaction, concurrency, and opaque identifiers without duplicating
cheaply discoverable wire shape.

**Work**

- Apply the metadata standard across all operations in tag-sized slices.
- Add top-level API guidance for base URL/versioning, credential types,
  assurance/CSRF, Problem Details, stable error codes, pagination, idempotency,
  rate/size limits, uploads/content, realtime, and compatibility.
- Add realistic, synthetic examples for request bodies and representative
  responses. Make error examples prove that no sensitive/internal data leaks.
- Add end-to-end authored recipes: bootstrap an installation, model an academic
  structure, invite or link a user, author/publish an exam, manage a sitting,
  start/continue/submit an attempt, review integrity, and consume audit data.
- Add schema/property descriptions where units, state transitions, redaction,
  nullability, or identifier semantics are not obvious.

**Acceptance criteria**

- 100% operation tag, summary, and behavior-description coverage.
- 100% parameter descriptions except self-evident shared headers explicitly
  allowlisted by policy.
- 100% request-body purpose descriptions.
- Every tag has a useful top-level description and at least one authored entry
  path or recipe.
- Every mutation has at least one valid synthetic request example; every major
  resource has a representative success and Problem Details example.
- Examples contain no real institution, student, exam, answer, token, email,
  object key, or local filesystem data.
- Agreement and schema-validation tests remain the gate for wire correctness.

### Phase 3 — Introduce a governed visual corpus

**Status: in progress.** `image-size` 2.0.2 still has no fixed release for the
relevant advisories. The first slice therefore proves the alternative already
required by section 1.3: reviewed SVG and PNG files are served from a constrained
static directory, authored MDX supplies only a registry ID, and Docusaurus'
vulnerable authored-image parser never reads the content asset. The registry,
validator, deterministic screenshot contract, hash-bound desktop/mobile visual
acceptance record, installation-authority diagram, protected-request
authentication/assurance diagram, academic-structure ownership map, and
exam-publication/delivery lifecycle are implemented. The remaining prioritized
diagrams and UI-specific screenshot fixtures are later Phase 3 slices.

**Work**

- Add the asset registry and validator.
- Establish screenshot fixtures with synthetic seeded data and a documented
  capture viewport/theme.
- Prioritize diagrams where relationships are hard to explain linearly:
  installation topology, authentication/assurance, academic hierarchy, exam
  lifecycle, attempt continuity, integrity/review, and durable-versus-transient
  effects.
- Add screenshots only to UI procedures where orientation materially reduces
  ambiguity.

**Acceptance criteria**

- Every informative asset is registered, owned, licensed, privacy-reviewed,
  alt-described, and referenced.
- No unregistered or orphaned content asset passes validation.
- Screenshot diffs are reviewed when the relevant UI/version changes.
- Core procedures remain understandable without images and in print/high
  contrast.
- The production build performs no external font, image, or asset fetch needed
  for core documentation.

### Phase 4 — Add feedback and documentation operations

**Work**

- Add issue/edit affordances first.
- If an owner and policy exist, add bounded helpfulness events.
- Create a monthly triage view by stable page ID and reason.
- Define stale-page and stale-screenshot review intervals by risk.
- Add accessibility, broken external link, and representative visual/browser
  checks proportional to the site surface.

**Acceptance criteria**

- Feedback collection has a named owner, privacy notice, retention limit,
  access controls, and deletion path.
- Payload tests reject query strings and arbitrary text/content.
- No feedback failure blocks page use or leaks into the Proctor application
  audit/log domain.
- A documented triage process converts evidence into tracked issues or content
  changes; the feedback database never becomes a second docs backlog.
- Critical operator/security/API pages have owners and review dates.

## 6. Initial Proctor data backlog

The following inventory is the minimum useful content backlog, not a proposal to
copy Mattermost's volume immediately.

### Authored public pages

| Audience | First durable pages |
| --- | --- |
| Operator | Installation, configuration, health, backup/restore, upgrade, HA, troubleshooting |
| Institution administrator | Bootstrap, academic structure, identity/invitations, roles/bindings, exam operations |
| Security reviewer | Trust boundaries, credential/assurance matrix, audit, data classification, logging, hardening |
| Developer | Local setup, repository/module architecture, testing, HTTP contract workflow, contribution |
| API consumer | Overview, authentication, errors, pagination, idempotency, uploads/content, realtime, examples |

### OpenAPI metadata

For every operation:

- one durable tag;
- stable operation ID and concise imperative summary;
- behavior description including authority, preconditions, important
  concealment/fail-closed behavior, and durable/transient effects where useful;
- credential and assurance presentation derived from `x-proctor-auth` and
  `security`;
- parameter and request-body purpose;
- bounded errors derived from `x-proctor-error-codes` plus recovery guidance;
- idempotency semantics where declared;
- at least one safe request example for mutations; and
- lifecycle/deprecation/version notes when applicable.

For every schema/property where relevant:

- semantic purpose, units/format, nullability, bounds/default, mutability,
  lifecycle state, redaction/concealment, and identifier opacity.

### Visual assets

Prioritize assets in this order:

1. architecture and lifecycle diagrams;
2. UI orientation screenshots for the highest-frequency admin/operator tasks;
3. annotated security/assurance flows;
4. troubleshooting decision diagrams; and
5. decorative illustrations only when the brand system has a clear need.

### Feedback data

Start with no collected data. The first useful implementation is source-aware
edit and issue links. If event collection is later justified, collect only the
bounded fields listed in section 4.6 and never free-form student/exam context.

## 7. Patterns to reuse and patterns to reject

### Reuse

- authored landing/guide pages plus generated exhaustive reference;
- one build entry point that generates all derived content before Docusaurus;
- OpenAPI tags as the generated reference taxonomy;
- top-level tag descriptions as category-page content;
- stable operation IDs as generator/example join keys;
- a tracked sidebar wrapper around an ignored generated sidebar;
- globally registered semantic MDX primitives;
- strict TypeScript and production-build validation;
- ignored, reproducible generated endpoint pages; and
- edit links from rendered pages to the exact authored source.

### Reject or redesign

- unpinned remote specifications during the build;
- raw indentation-sensitive YAML concatenation;
- `npm install` in generation instead of the locked install flow;
- broad quote/angle-bracket rewriting without fixtures;
- copying hundreds of images without ownership/freshness/privacy metadata;
- presenting generated placeholder snippets as supported SDK examples;
- treating general code comments as API contract prose;
- a feedback widget without a tracked data contract and owner;
- warning-only broken links/images; and
- Mattermost's large migration-specific manual sidebar and redirect machinery
  before Proctor has a legacy URL estate.

## 8. Immediate decision proposal

Approve the following as Proctor's next documentation/API slice:

1. Keep the current single Docusaurus instance and strict link/image policy.
2. Keep `server/openapi/` as the human source and generated
   `server/openapi.json` as the reviewed consumer artifact.
3. Integrate a locked OpenAPI renderer against the generated OpenAPI 3.1
   document and make its full Proctor production build pass.
4. Continue expanding rich prose and examples beyond the completed
   representative pilot.
5. Render Proctor auth, assurance, idempotency, and error extensions explicitly.
6. Add authored API convention and workflow pages around the generated
   reference.
7. Leave generated endpoint pages and bundles ignored and rebuild them in CI.
8. Defer screenshots until the existing dependency gate is cleared, then add a
   registry before the first screenshot lands.
9. Use edit/issue links for feedback; defer telemetry until ownership and
   privacy requirements are settled.

This preserves Proctor's strongest architectural property—one reviewed and
tested contract—while adopting the part of Mattermost's documentation system
that creates the most user value: rich authored context around deterministic,
deep-linkable reference material.
