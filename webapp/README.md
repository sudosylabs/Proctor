# Proctor webapp

This private Vite module contains the browser runtime for Proctor's
server-hosted access and onboarding pages. The module owns routing,
typed same-origin transport, browser credential handling, build output, the
generated token/theme system, and the document-level browser foundation.

Runtime-owned brand assets live in
[`src/assets/brand`](./src/assets/brand/README.md). They are reviewed copies or
derivatives of the repository masters and are referenced through Vite so
production files receive immutable fingerprints.

[`DESIGN_SYSTEM.md`](./DESIGN_SYSTEM.md) is the product interface contract.
Human-owned tokens live in [`design-system/tokens.mjs`](./design-system/tokens.mjs)
and generate [`src/styles/tokens.css`](./src/styles/tokens.css) plus the typed
[`src/generated/design-system/themes.ts`](./src/generated/design-system/themes.ts)
runtime catalog. The first visible slice is the server-hosted `/login` route;
its exact preimplementation behavior lives in the feature-local
[`login` contract](./src/features/login/CONTRACT.md). One-time installation
establishment and public local account admission live in the
[`setup`](./src/features/setup/CONTRACT.md) and
[`register`](./src/features/register/CONTRACT.md) contracts. Terminal
Session-confirmation behavior lives in the companion
[`authorization-complete` contract](./src/features/authorization-complete/CONTRACT.md).
Purpose-specific email-token consumption lives in the
[`verify-email` contract](./src/features/verify-email/CONTRACT.md). Those routes
and the password-recovery, Invitation acceptance, Desktop authorization, and
provider-connection feature contracts now complete the declared hosted route
family. Their shared
structural frame is governed beside
[`AccessPageShell`](./src/components/AccessPageShell/CONTRACT.md); feature
modules retain their own state, transport, content, and recovery behavior.
The shared [`auth` contract](./src/auth/CONTRACT.md) owns runtime validation and
canonical-origin pinning for public discovery; each consuming feature still
owns its admission decisions and visible states.
The [`application orchestration contract`](./src/app/CONTRACT.md) owns the
StrictMode-safe initial resource and retry lifecycle used by route modules.
The remaining focused access tasks share the narrow
[`AccessTaskIntro`](./src/components/AccessTaskIntro/CONTRACT.md) introduction
and [`Notice`](./src/components/Notice/CONTRACT.md) evidence contracts without
introducing decorative workflow steps.
Product icons use semantic names from the owned
[`Icon`](./src/components/Icon/CONTRACT.md) adapter; feature code never imports
the underlying icon library directly. Brand and provider marks remain governed
assets rather than product icons.
Repeated action styling, single-line field behavior, and inline state evidence
live beside the narrow
[`Button`](./src/components/Button/CONTRACT.md) and
[`InputField`](./src/components/InputField/CONTRACT.md), and
[`Notice`](./src/components/Notice/CONTRACT.md) contracts. Persistent
submission failures use the shared action-adjacent
[`FormFeedback`](./src/components/FormFeedback/CONTRACT.md) region. These
components remain domain-neutral; page modules still own values, copy,
validation, API orchestration, and recovery.
Visible browser copy uses the delegated `webapp.*` namespace in
`server/i18n`. `npm run i18n:generate` validates exact ownership against
browser source literals before producing the typed runtime catalog; the server
localization checker continues to own every other catalog namespace.
Route modules remain thin orchestrators. Feature presentation lives under
`src/components/` in PascalCase folders such as `Setup/` and `Registration/`,
with context, state, form, and styles split by responsibility. Transport
operations stay in the owning feature module and are passed into visual
components as typed functions.
Those feature action modules validate declared success responses and classify
Problem Details into bounded semantic outcomes. Presentation components never
receive server problem codes or select behavior from transport status.

The browser entry loads `reset.css`, the generated `tokens.css`, and `base.css`
in declared cascade-layer order. The root also owns document metadata and
bounded fatal recovery. The non-shipping fixture under `tests/fixtures` proves
those rules in pinned Chromium, Firefox, and WebKit without becoming a hosted
product route.

Use Node.js 22 and the committed npm lockfile:

```sh
npm ci
npx playwright install chromium firefox webkit
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
