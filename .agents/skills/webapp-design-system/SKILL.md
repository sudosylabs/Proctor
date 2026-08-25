---
name: webapp-design-system
description: Design, implement, or review Proctor's server-hosted product interface. Use for webapp tokens, themes, brand assets, reset or base styles, typography, focus, motion, components, page shells, responsive behavior, or interaction accessibility; not for the Docusaurus documentation site.
---

# Build the server-hosted interface

## Workflow

1. Invoke [`$glossary`](../glossary/SKILL.md) before naming visible domain
   concepts or product states. Read the complete
   [webapp design-system contract](../../../webapp/DESIGN_SYSTEM.md) and the
   [package README](../../../webapp/README.md). Completion: every visual or
   interaction decision follows the current contract or updates that contract
   in the same change.
2. Inspect the real hosted route, bootstrap boundary, API or domain contract,
   states, and tests that force the requested UI. For foundation work, name the
   first real page slice that requires the new rule. Completion: the change
   introduces no speculative component, token, navigation, or persistence API.
3. Place each decision in its owning source. Completion: durable meaning lives
   in `DESIGN_SYSTEM.md`; tokens live in `design-system/tokens.mjs`; generated
   CSS and TypeScript remain generated; brand provenance lives beside
   `src/assets/brand`; feature modules retain domain and transport behavior;
   and promoted shared components receive a narrow adjacent contract.
4. Implement one vertical slice through semantic tokens and the established
   CSS layers. Completion: global CSS owns only reset, tokens, and document
   defaults; component styles remain local; and product code imports neither
   Docusaurus tokens nor public-documentation assets.
5. Exercise every state the slice can enter using the interaction, theme,
   viewport, zoom, localization, motion, and accessibility acceptance matrix
   in `DESIGN_SYSTEM.md`. Completion: evidence covers behavior, not only a
   static screenshot.
6. Run the affected generators and package checks documented in the webapp
   README. Run `make -C server architecture` when a contract or repository
   document changed. Completion: tracked adapters are current, the production
   build emits only server-owned paths, and unrelated pre-existing failures are
   reported without being folded into the UI change.

## Boundaries

The webapp and documentation site may share the accepted Proctor identity, but
they are separate implementations. Product presentation is governed by
`webapp/DESIGN_SYSTEM.md`; documentation presentation is governed by
[`$docs-site`](../docs-site/SKILL.md). A product asset is copied into the
webapp package under its local provenance contract. A public-documentation
figure or screenshot instead invokes
[`$docs-site-visual-assets`](../docs-site-visual-assets/SKILL.md) and its asset
registry.

Grow the system from observed reuse. Start a component inside its first real
feature and promote it only when another consumer proves shared semantics and
behavior. Pages own current authority, API orchestration, URL-addressable
state, and recovery; presentation primitives remain domain-neutral.
