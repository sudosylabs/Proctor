---
name: docs-site-visual-assets
description: Add or revise governed diagrams, illustrations, and screenshots for Proctor's Docusaurus documentation. Use for docs/public/assets.json, privacy review, deterministic capture, or visual acceptance; not for webapp brand or UI assets.
---

# Govern documentation visuals

## Workflow

1. Confirm that a visual materially clarifies a relationship that prose does
   not make equally legible. Completion: the intended claim and owning public
   page are explicit.
2. Read the shared
   [visual-system reference](../docs-site/references/design-system.md) for a
   diagram, illustration, or site-facing visual change. Completion: palette,
   geometry, typography, and interaction decisions use its current tokens.
3. Create or update one registry record and its exact asset bytes under the
   governed paths below. Completion: ownership, provenance, privacy, review
   triggers, accessibility text, and dimensions are complete.
4. Perform the visual acceptance review against the final bytes. Completion:
   all five checks and the final SHA-256 are recorded.
5. Run the asset checks listed below. Completion: validation and failure-mode
   tests pass without weakening the registry contract.

Visuals in the public documentation are evidence, not decoration. Add one only
when it makes an architecture, lifecycle, spatial relationship, or UI procedure
materially easier to understand than prose alone. The surrounding prose must
remain sufficient in print, high contrast, and environments where the asset is
unavailable.

## Authoring boundary

Every content asset has one entry in
[`docs/public/assets.json`](../../../docs/public/assets.json)
and one file beneath `docs/public/static/assets/`. Authored Markdown and MDX do
not import files, use Markdown image syntax, create raw `img` elements, or write
direct `/assets/` paths. Reference the stable registry ID instead:

```mdx
<GovernedFigure asset="installation-authority-topology" />
```

`GovernedFigure` resolves the public path, alt description, dimensions, kind,
and caption from the registry. This keeps page authors away from transport and
presentation details while the validator enforces the asset contract.

Only deterministic SVG diagrams and PNG screenshots or illustrations are
allowed. The validator rejects unregistered and orphaned files, duplicate IDs,
missing ownership or provenance, unapproved privacy reviews, unsafe SVG
features, dimension and size drift, visual-review hash drift, unknown
references, and missing review triggers. SVG files may not contain active
content, linked or embedded images, external references, event handlers,
doctypes, or entities.

The constrained static path deliberately bypasses Docusaurus' authored-image
parser while its transitive `image-size` dependency has unresolved security
advisories. Do not relax the Markdown-image and image-import prohibition when
adding an asset.

## Registry record

Use lowercase kebab-case IDs and paths grouped by role, such as
`static/assets/diagrams/`, `static/assets/screenshots/`, or
`static/assets/illustrations/`. A record owns:

- the source and public paths, kind, intrinsic dimensions, and maximum bytes;
- the documentation owner, provenance, and licensing state;
- a privacy decision with its evidence;
- useful alt text and a concise visible caption;
- the intended theme and review date;
- the visual-system version used by a diagram or illustration;
- repository files whose changes require the asset to be reviewed; and
- a visual acceptance record tied to the exact asset SHA-256.

An original asset may record `license.status` as `pending` only while Proctor's
documentation-license production decision remains open. The note must explain
that state. Do not assign a license expression by assumption, and do not publish
the documentation site until that decision is resolved. Adapted work must
instead identify its exact source revision and satisfy the repository's
licensing and notice rules.

Run the asset checks from `docs/site`:

```sh
npm run validate:assets
npm run test:assets
```

The normal `npm run check` and root `make docs-check` workflows include both.

## Diagram standard

The complete palette, typography, geometry, connector, density, and annotation
rules live in the shared
[visual-system reference](../docs-site/references/design-system.md). The asset
registry records the exact visual-system version used by every diagram or
illustration.

New diagrams use Proctor purple for the authoritative path, teal for disposable
or reconstructible effects, amber for human attention, and coral only for
blocked or fail-closed outcomes. Labels, line styles, ordering, and shapes must
communicate the same distinction without color. Keep diagrams on a white
technical plate so light, dark, print, and high-contrast contexts retain the
same evidence. The retired `legacy-cobalt-v0` system is no longer accepted by
the registry or palette validator.

Normal text must retain at least 4.5:1 contrast against its actual plate. Use
the current `#655f69` muted-ink token or darker for labels on white and
near-white surfaces. The SVG validator
enforces the approved palette so one-off colors cannot silently fragment the
visual system.

Use the documented IBM Plex stack and fallbacks. Preserve a numeric `width`,
`height`, and matching `viewBox`; keep primary labels legible at the desktop
content width.
At mobile width, the complete structure must remain recognizable and the
full-size inspection link must remain visible. If a procedure depends on
reading every label without opening that view, author a narrower companion
figure instead of compressing the desktop diagram. The SVG title and
description are useful source metadata, while the registry alt text remains the
rendered accessibility authority.

## Visual acceptance review

An asset is not approved merely because its SVG or PNG parses. Review the exact
bytes recorded by `visual_review.source_sha256` in a browser, both at full size
and inside the documentation page that uses it. The registry requires the
standard 1440 × 1024 desktop and 390 × 844 mobile viewports.

1. Confirm every visible word remains inside its intended card, label, or plate.
2. Trace every connector from source boundary to target boundary. Branches must
   actually join their spine, and arrowheads must terminate at a meaningful
   target rather than in whitespace.
3. Read the figure at the rendered desktop documentation width. Split or
   simplify a dense figure instead of relying on browser zoom; the full-size
   link is a secondary inspection aid.
4. Confirm the narrow page has no unintended horizontal overflow and that its
   overall structure, caption, and full-size link remain usable.
5. Inspect print or print emulation so meaning survives without color, shadow,
   animation, or a dark-theme background.

Record all five checklist identifiers and the review method in the registry,
then compute the SHA-256 from the final asset. `npm run validate:assets` rejects
any later byte change until a maintainer repeats the review and updates the
approval record. The hash proves which exact artifact was inspected; it does
not replace the browser review.

## Screenshot fixtures

Screenshots use seeded synthetic data only. Never capture a real Institution,
User, Exam, answer, credential, email address, hostname, object key, or local
filesystem path. A screenshot's review triggers must include both the UI source
or component contract and the tracked seed fixture that produced its visible
state.

Use these deterministic capture defaults unless the procedure requires a
different viewport and the registry provenance explains why:

| Setting | Default |
| --- | --- |
| Desktop viewport | 1440 × 1024 CSS pixels |
| Mobile viewport | 390 × 844 CSS pixels |
| Browser scale | 100% |
| Theme | Light |
| Motion | Reduced |
| Locale | `en` |
| Time zone | `UTC` |
| Data | Tracked, deterministic synthetic fixture |

Capture only the application surface needed for orientation. Exclude browser
chrome, developer tools, notifications, account menus, and unrelated personal
applications. Prefer one clean frame over a sequence of nearly identical
screenshots. Crop annotations must remain outside meaningful UI content.

When a review trigger changes, regenerate the screenshot with the same fixture
and capture settings, inspect the visual diff, update `last_reviewed`, and
record any intentional capture change in `provenance`. If the UI is not stable
enough for a deterministic fixture, keep the procedure prose-only.
