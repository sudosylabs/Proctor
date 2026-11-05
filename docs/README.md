# Proctor documentation

This directory contains only the public Docusaurus authoring and build
workspace.

- [Public guide sources](./public/) — task-oriented operator, administrator,
  security, developer, and API guidance
- [Developer guide](./public/developers/) — local setup, repository boundaries,
  server and webapp workflows, reusable modules, HTTP and OpenAPI, persistence,
  testing, documentation, and contribution review
- [API guide sources](./api/) — authored integration guidance and generated
  endpoint reference workspace
- [Public glossary](./public/reference/glossary.mdx) — generated presentation of
  Proctor's canonical domain terms
- [Documentation site](./site/) — local preview, validation, static build, and
  generated-reference workflow

## Publishing

The `Publish documentation` workflow builds the site when a server release tag
(`server/v*`) is pushed, and on manual dispatch; it does not run on pushes to
`main`, where CI's `make check` already builds and validates the site. It
deploys to GitHub Pages only after maintainers opt in; until then each run is a
build check that publishes nothing. The site URL otherwise stays
the `https://docs.proctor.invalid` placeholder.

To activate publication:

1. Decide that the documentation may be public. A GitHub Pages site is
   readable by anyone even when the repository is private, unless the
   organization's plan provides access-controlled Pages.
2. In the repository's Pages settings, select **GitHub Actions** as the source.
   Restrict the `github-pages` environment's deployment branches and tags to
   the `main` branch (for manual runs) and `server/v*` tags, and add required
   reviewers if each deployment should be approved.
3. Choose the public origin. For a custom domain, configure it in the Pages
   settings and its DNS first.
4. Set repository variable `DOCS_SITE_URL` to that HTTPS origin with no path
   or trailing slash, such as `https://docs.example.org`. When the site is
   served from a path, as a project page on `github.io` is, also set
   `DOCS_BASE_URL` to that path with a trailing slash, such as `/Proctor/`;
   it defaults to `/`.
5. Set repository variable `DOCS_PUBLISH_ENABLED` to `true` and run the
   workflow manually once to confirm the deployment.

Removing either variable, or setting `DOCS_PUBLISH_ENABLED` to anything else,
stops deployment without affecting the build. The workflow refuses to publish
an origin that is not HTTPS or still ends in `.invalid`. "Edit this page" links
always point at `main` in the `sudosylabs/Proctor` repository.
