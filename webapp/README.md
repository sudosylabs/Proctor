# Proctor webapp

This private Vite module contains the browser runtime for Proctor's
server-hosted access and onboarding pages. The visual page and design-system
component implementation is deliberately deferred; the initial module owns
routing, typed same-origin transport, browser credential handling, build
output, and the generated token/theme foundation.

Runtime-owned brand assets live in
[`src/assets/brand`](./src/assets/brand/README.md). They are reviewed copies or
derivatives of the repository masters and are referenced through Vite so
production files receive immutable fingerprints.

[`DESIGN_SYSTEM.md`](./DESIGN_SYSTEM.md) is the product interface contract.
Human-owned tokens live in [`design-system/tokens.mjs`](./design-system/tokens.mjs)
and generate [`src/styles/tokens.css`](./src/styles/tokens.css) plus the typed
[`src/generated/design-system/themes.ts`](./src/generated/design-system/themes.ts)
runtime catalog. The stylesheet is the only global style imported by the empty
runtime; no visual page or component is implemented yet.

The document-level browser contract is specified in `DESIGN_SYSTEM.md` before
page work begins. Its reset, base styles, document controller, root boundary,
and browser fixture remain the next implementation slice; the fixture is test
infrastructure and will not become a hosted product route.

Use Node.js 22 and the committed npm lockfile:

```sh
npm ci
npm run check
npm run dev
```

After changing the token source, run `npm run design-system:generate`. The
normal verification gate rejects stale output, incomplete themes, contrast
regressions, forbidden authored CSS forms, and brand assets that drift from
their canonical masters.

For same-origin development with Vite, configure the server PublicURL as
`http://127.0.0.1:5173`; Vite proxies `/api` to the Go listener at
`http://127.0.0.1:8065`. Production packages contain `dist/` beside the server
binary and do not require Node.js.

The module is part of the combined Proctor server product and is licensed under
AGPL-3.0-only. See [`../server/LICENSE`](../server/LICENSE).
