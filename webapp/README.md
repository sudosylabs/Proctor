# Proctor webapp

This private Vite module contains the browser runtime for Proctor's
server-hosted access and onboarding pages. The visual page and design-system
implementation is deliberately deferred; the initial module owns only routing,
typed same-origin transport, browser credential handling, and build output.

Use Node.js 22 and the committed npm lockfile:

```sh
npm ci
npm run check
npm run dev
```

For same-origin development with Vite, configure the server PublicURL as
`http://127.0.0.1:5173`; Vite proxies `/api` to the Go listener at
`http://127.0.0.1:8065`. Production packages contain `dist/` beside the server
binary and do not require Node.js.

The module is part of the combined Proctor server product and is licensed under
AGPL-3.0-only. See [`../server/LICENSE`](../server/LICENSE).
