---
name: docs-site
description: Author, restructure, validate, or build Proctor's Docusaurus public and API documentation. Use for docs/public, docs/api, docs/site, navigation, search, glossary presentation, or site visual-system work.
---

# Work on the documentation site

## Workflow

1. Identify whether the change owns public guidance, API guidance, generated
   presentation, or site implementation. Completion: authored behavior remains
   in `docs/public/` or `docs/api/`, while rendering and generators remain in
   `docs/site/`.
2. Invoke [`$glossary`](../glossary/SKILL.md) before changing public domain
   terminology. For visual-system work, also read
   [the visual-system reference](references/design-system.md). For a governed
   diagram, illustration, or screenshot, invoke
   [`$docs-site-visual-assets`](../docs-site-visual-assets/SKILL.md). Completion:
   the change uses canonical terms, current semantic tokens, and the governed
   asset registry when it contains a visual.
3. Preserve the authority boundary: public guides explain released outcomes;
   OpenAPI owns public HTTP shapes; component contracts own exact behavior;
   Docusaurus code owns presentation only. Completion: no public page links to
   an internal skill or becomes a second engineering authority.
4. Regenerate only the views affected by the authored input. Completion:
   glossary, search, design-system, or OpenAPI outputs are current as relevant.
5. Run the focused checks, then `make docs-check` for a site-wide change.
   Completion: metadata, generated adapters, assets, types, and the production
   build pass.

The package-local commands and generated-file boundaries live in
[`docs/site/README.md`](../../../docs/site/README.md). Treat that README,
`package.json`, and the generator tests as the command authority rather than
copying their inventories here.

Public pages contain task-oriented information for their declared audience and
maturity. They use synthetic examples, never credentials, student data, Exam
answers, operational hostnames, or local-machine values. Curated `<Term>`
markup is limited to a useful first occurrence and stays out of headings, code,
API paths, and illustrations.

Search remains a deterministic local adapter over authored MDX and
`server/openapi.json`; it sends no reader query to an external service. The API
reference is generated from the reviewed OpenAPI artifact and does not provide
a credential-bearing request console.
