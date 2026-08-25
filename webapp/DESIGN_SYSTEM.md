# Proctor webapp design system

This document is the durable visual and interaction contract for the browser
application shipped with Proctor Server. It establishes the system beneath
future pages without implementing a page or component library.

The human-owned token source is
[`design-system/tokens.mjs`](./design-system/tokens.mjs). Generated
[`src/styles/tokens.css`](./src/styles/tokens.css) and
[`src/generated/design-system/themes.ts`](./src/generated/design-system/themes.ts)
are its browser adapters and must not be edited directly. Product components
consume those adapters; they do not import the documentation site's Docusaurus
tokens or create a competing palette.

## Scope

The subject is self-hosted examination infrastructure. Its users include
students working under time pressure, educators preparing and reviewing
Exams, and Institution administrators configuring access and academic
structure. The interface's primary job is to make current authority, state,
progress, and the next safe action unmistakable.

This foundation owns:

- visual direction and interface principles;
- reference, system, and semantic token layers;
- light and dark theme contracts;
- typography, spacing, geometry, elevation, motion, and responsive rules;
- accessibility, localization, content, and state requirements;
- CSS ownership and the path for adding themes, components, and pages; and
- automated token generation, contrast checks, and authored-style guards.

It does not yet own page composition, component APIs, an icon library or
product icon vocabulary, illustration assets, navigation behavior, or a
persisted theme chooser. Those decisions require a real hosted-page flow and
its states rather than speculative abstractions.

## Direction

The product should feel like a **proof-first examination workspace**: calm,
precise, and explicit about what is authoritative. It is not a generic
analytics dashboard, a marketing surface, or a simulation of paper forms.
Purple identifies the primary Proctor-controlled path. Teal, amber, and coral
appear only when their state meaning is true. Neutral surfaces carry the rest
of the interface.

The signature structural idea is the **verification rail**. A future page may
use a quiet, persistent region to keep the smallest useful chain of Exam,
Sitting, Attempt, save, connectivity, or integrity state visible beside the
current task. It is evidence, not decoration: it appears only when losing that
context would make an action ambiguous. Its exact component and responsive
placement remain page-design work.

The system follows five principles:

1. **State before decoration.** Color, position, and motion explain state or
   hierarchy; they do not create arbitrary variety.
2. **One obvious primary path.** A view has one visually dominant action or
   decision. Destructive and irreversible actions remain distinct.
3. **Calm under pressure.** Timed or security-sensitive flows avoid surprise,
   layout movement, ambient animation, and clever copy.
4. **Evidence survives presentation.** Meaning remains available without
   color, motion, hover, a wide viewport, or a particular theme.
5. **The system grows from real use.** A repeated, stable need earns a token or
   component. File size, symmetry, and hypothetical reuse do not.

## Token architecture

Tokens form a one-way dependency chain:

~~~text
reference palette
    ↓
system scales (type, spacing, geometry, motion, layers)
    ↓
semantic theme roles (background, foreground, action, state, elevation)
    ↓
component tokens, introduced only with a real component
    ↓
page composition
~~~

The reference palette is private to the human-owned source. Generated CSS does
not expose `purple-500` or similar palette variables because a component must
state why it needs a color. Components use semantic properties such as
`--proctor-color-background-surface`, `--proctor-color-action-primary`, or
`--proctor-color-state-danger`.

System scales do not change with a color theme. A theme supplies the complete
semantic color and shadow maps plus its native `color-scheme`. Component
tokens may alias system and semantic tokens when a component has a stable,
reviewed contract. They must not contain a second theme selector.

### Naming

CSS custom properties use a stable prefix and role-first names:

~~~text
--proctor-space-4
--proctor-font-size-body
--proctor-radius-control
--proctor-color-background-canvas
--proctor-color-foreground-muted
--proctor-color-action-primary-hover
--proctor-shadow-dialog
~~~

Avoid appearance names such as `gray`, `bright-purple`, `left-sidebar`, or
`big-gap`. Avoid component names at the system or semantic layer. A new token
needs a recurring decision or a cross-cutting semantic role; a one-off value
stays local until repetition proves an abstraction.

## Themes

`light` and `dark` are the initial registered themes. `system` is a preference
mode, not a third theme: with no `data-theme` attribute, CSS selects light or
dark through `prefers-color-scheme`. An explicit theme is represented on the
document element:

~~~html
<html data-theme="dark">
~~~

Every registered theme exposes exactly the same semantic color and elevation
keys. The generator rejects incomplete themes, so a component never asks which
theme is active. Each theme also sets native `color-scheme`, allowing browser
controls and scrollbars to match. A future theme controller must keep
`<meta name="theme-color">` aligned with the effective canvas.

The label in the token source is for maintainers and design tooling. A visible
theme chooser resolves its user-facing label through the webapp localization
catalog; it does not render the source label directly.

The foundation deliberately does not choose persistence yet. Unauthenticated
hosted pages follow the system preference. A future authenticated appearance
slice may register a bounded User Settings value and a CSP-compatible
pre-paint bootstrap, but it must not invent a second server authority or place
sensitive state in browser storage.

To add a theme:

1. Add one stable lowercase theme ID to `designTokens.themes`.
2. Supply a human label, `light` or `dark` native color scheme, and the complete
   semantic color and shadow maps.
3. Document any new meaning here. A different value is not automatically a
   different meaning.
4. Run `npm run design-system:generate` and `npm run design-system:check`.
5. Exercise every implemented page in the new theme before exposing it in a
   chooser, and add its localized chooser label with that UI slice.

No component or page change should be required merely to register a complete
theme.

## Color

Six reference colors anchor the product and the documentation site. Theme
surfaces and interaction variants derive from them.

| Reference | Value | Meaning |
| --- | --- | --- |
| Ink | `#161616` | Neutral content and structure |
| Paper | `#FFFFFF` | Light reading and working surface |
| Proctor purple | `#5C00AA` | Authoritative, selected, or primary path |
| Teal | `#0B766E` | Successful completion or healthy state |
| Amber | `#95520D` | Attention, pause, or required intervention |
| Coral | `#B4233C` | Invalid, denied, destructive, or unavailable state |

Purple is the only general focal accent. Teal, amber, and coral are state
colors, not card decoration or product-area identifiers. Every state also has
a word, icon, shape, or position cue. Links remain distinguishable outside
color alone when surrounded by prose.

The initial theme surfaces are intentionally close to the established Proctor
documentation identity while serving denser product interaction:

| Role | Light | Dark |
| --- | --- | --- |
| Canvas | `#FFFFFF` | `#141016` |
| Subtle background | `#F7F5F9` | `#1B151E` |
| Surface | `#FFFFFF` | `#211A25` |
| Raised surface | `#FFFFFF` | `#2A2130` |
| Primary text | `#161616` | `#F8F6FA` |
| Muted text | `#655F69` | `#AEA4B3` |
| Default border | `#E1DCE5` | `#3B3142` |
| Focus | `#5C00AA` | `#C38BF5` |

The audit verifies standard foreground/surface pairs at `4.5:1`, state text on
its state surface at `4.5:1`, and focus rings at `3:1` against their adjacent
surfaces. Disabled content is not relied upon to convey required information.

## Typography

All fonts are locally bundled through Fontsource. A production page makes no
third-party font request and does not depend on a visitor owning a font.

| Role | Family | Weights | Use |
| --- | --- | --- | --- |
| Display | IBM Plex Sans Condensed | `500`, `600` | Restrained page landmarks and high-level identity |
| Text and interface | IBM Plex Sans | `400`, `500`, `600`, `700` | Body copy, headings, controls, and labels |
| Data and protocol | IBM Plex Mono | `400`, `600` | IDs, codes, times, tabular technical values, and editor content |

The condensed face is the one deliberate typographic risk. It connects to the
square, engineered Proctor mark and leaves more width for exact page titles,
but it is not used for paragraphs, form labels, or dense control chrome. Do
not request synthetic weights or set literal font families in authored CSS.
The initial files are normal-style Latin WOFF2 subsets. Adding a locale with
characters outside that coverage requires the matching tracked subset and
fallback review in the same slice. Do not preload every face; a visible page
may preload only the face and weight proven critical to its first paint.

The tracked scale is:

| Role | Size | Line height |
| --- | --- | --- |
| Caption | `0.75rem` | `1.4` |
| Label | `0.8125rem` | `1.35` |
| Small body | `0.875rem` | `1.55` |
| Body | `1rem` | `1.6` |
| Large body | `1.125rem` | `1.55` |
| Small heading | `1.25rem` | `1.35` |
| Medium heading | `1.5rem` | `1.25` |
| Large heading | `2rem` | `1.15` |
| Display | `clamp(2.25rem, 1.8rem + 1.5vw, 3.5rem)` | `1.02` |

Body copy uses a maximum `74ch` measure. Headings use balanced wrapping when
supported. Data columns use tabular numerals. A viewport may change layout and
display size, but it does not shrink ordinary body text below the tracked
scale.

## Spacing and geometry

Structural layout follows an 8-pixel grid. A 4-pixel step exists for optical
correction and tight internal relationships, not page composition.

| Token | Pixels | Typical use |
| --- | ---: | --- |
| `space-0` | 0 | Explicit reset |
| `space-1` | 4 | Optical or icon correction |
| `space-2` | 8 | Tight internal gap |
| `space-3` | 12 | Label-to-value or compact padding |
| `space-4` | 16 | Default control or group padding |
| `space-5` | 24 | Card padding or related-object gap |
| `space-6` | 32 | Component separation |
| `space-7` | 48 | Section separation |
| `space-8` | 64 | Major composition gap |
| `space-9` | 96 | Page interval |

Controls use `40px`, `44px`, and `48px` size tokens. The default interactive
target is `44px`; a visually compact control still needs an equivalent pointer
target unless it is an inline text link. Use `6px` radii for controls, `12px`
for cards, and `16px` for large panels. Full rounding is reserved for a true
status, token, avatar, or compact circular action.

Borders and spacing establish ordinary hierarchy. Shadows are reserved for
real elevation: a raised floating control, overlay, or dialog. A card does not
receive a shadow merely to look complete.

## Icons and imagery

The optical icon sizes are `16px`, `20px`, and `24px`. Product icons inherit
`currentColor`, use a consistent stroke and view box, and never replace a text
label merely to save space. Decorative icons are hidden from assistive
technology; an icon-only action has an explicit accessible name and the
default pointer target.

Do not mix emoji, ad hoc SVGs, and several icon libraries. Select or create an
icon source only when the first visible slice establishes a recurring product
vocabulary, then wrap it behind one owned primitive. Product and provider
marks remain original artwork and do not inherit the interface icon grammar.

Meaningful images have intrinsic dimensions, alternative text, and a governed
same-origin source. Decorative imagery is exceptional in the operational
product and must not compete with state or task content.

## Layout and responsiveness

Pages are mobile-first and adapt where their content stops working, not at
named device classes. The shared thresholds are `30rem`, `48rem`, `72rem`, and
`90rem`; component-level rearrangement prefers container queries. CSS custom
properties are not used in media-query conditions, so authored queries use
these documented values exactly.

Use CSS Grid or Flexbox and logical properties. Do not measure layout in
rendering code when CSS can express it. Text, translated labels, IDs, and
user-controlled names must survive narrow and unusually wide content.
Full-bleed regions account for safe-area insets. At `200%` zoom, the primary
task remains available without two-dimensional scrolling except where the
content itself is inherently two-dimensional.

The initial measures are `36rem` for focused forms, `74ch` for prose, `72rem`
for content, and `90rem` for the complete application frame. They are bounds,
not instructions to fill every viewport.

Layer tokens progress from base (`0`) through sticky (`10`), popover (`20`),
overlay (`30`), dialog (`40`), and notification (`50`). Components do not
invent larger numbers. Prefer native dialog and popover top-layer behavior
where it satisfies the interaction contract; otherwise one application-owned
portal preserves stacking and focus ownership.

## Interaction and motion

Every interactive primitive defines rest, hover where available, active,
focus-visible, disabled, pending, and relevant invalid or selected states.
Hover never reveals required information. Actions use `button`; navigation
uses a real link. Compound controls use `focus-within` when the group needs a
single visible boundary.

The focus indicator is a three-pixel semantic focus ring with enough offset to
remain visible against adjacent surfaces. Sticky regions, dialogs, and
overlays must not obscure the focused element.

Motion explains a state change or preserves orientation. The scale is `80ms`,
`140ms`, `200ms`, and `320ms`; transitions list properties explicitly and
prefer `transform` and `opacity`. Generated tokens collapse those durations
under `prefers-reduced-motion`, but a component must also keep its complete
meaning without animation and stop non-essential repeated motion.

## Accessibility and content

WCAG 2.2 AA is the minimum release floor, not an end-state claim. Each visible
slice is reviewed with keyboard, pointer, touch, screen reader semantics,
browser zoom, high contrast or forced colors, reduced motion, and both color
themes.

Durable rules include:

- use native semantic elements before ARIA;
- provide one page heading, a logical heading hierarchy, and a skip link to
  the main task;
- associate every form control with a visible label, stable `name`, correct
  input type, input mode, and autocomplete value;
- never block paste into passwords, one-time codes, or any other input;
- place validation guidance beside its field, associate it programmatically,
  and focus the first invalid field after a failed submission;
- announce asynchronous validation and completion through an appropriate live
  region without moving focus unnecessarily;
- give icon-only actions an accessible name and hide decorative icons;
- provide dimensions and useful alternative text for meaningful images;
- preserve browser zoom and provide keyboard alternatives for gestures; and
- confirm destructive actions or provide a genuine undo path.

Interface copy uses active voice, stable action names, sentence case, and exact
domain terms from the repository [`glossary` skill](../.agents/skills/glossary/SKILL.md).
Errors say what happened
and the next safe step without exposing sensitive state. Empty states orient
the user toward an available action. Loading copy uses the ellipsis character
(`…`), not three periods.

## Localization and bidirectionality

Layouts allow text expansion and use logical direction properties. The
document language and direction follow the resolved locale. Dates, times,
numbers, relative durations, and lists use `Intl` rather than manual formats.
Identifiers, protocol names, and the Proctor brand opt out of automatic
translation where necessary.

No component concatenates translated fragments into a sentence. Labels remain
visible when copy grows, and controls do not depend on English word length.
Icon direction is reviewed: some arrows mirror in right-to-left layouts while
media controls, charts, and protocol symbols may not.

## CSS and component ownership

Global CSS is intentionally small and ordered through cascade layers:

~~~css
@layer reset, tokens, base, components, utilities, overrides;
~~~

- `tokens` is generated from the human-owned token module.
- `reset` and `base` will own document defaults once the first visible slice
  requires them.
- component styles remain colocated and locally scoped, consuming semantic
  custom properties.
- `utilities` contains only a small reviewed set of structural helpers.
- `overrides` is reserved for explicit integration seams, never specificity
  repair.

The product uses build-time CSS and CSS Modules rather than runtime styling.
Authored styles do not contain literal colors, literal font families,
unreviewed shadows, `transition: all`, or removed outlines. The automated audit
enforces those rules for every authored CSS file under `src`.

A component enters the shared system only when at least two real consumers
need the same semantics and behavior. Its contract then documents:

- purpose and non-purpose;
- semantic element and keyboard behavior;
- required and optional states;
- accessible name, description, error, and announcement behavior;
- content limits and localization behavior;
- responsive and container behavior;
- allowed tokens and any justified component aliases; and
- tests and visual review cases.

Components accept domain-neutral presentation inputs. Pages and feature
modules retain Exam, Sitting, Attempt, authorization, and API orchestration.
The design system does not become a product-state service locator.

## Growth workflow

### Add a token

1. Confirm the decision is recurring or cross-cutting.
2. Put a theme-independent value in the appropriate system scale, or add a
   semantic role to every theme.
3. Name the role by intent rather than appearance or location.
4. Update this contract and the audit when the meaning or invariant changes.
5. Regenerate and verify; migrate consumers in the same change.

Tokens are changed in place before a stable public release. After stable
release, a renamed or removed token receives an explicit migration window
rather than a silent semantic repurpose.

### Add a component

Start inside the first real feature. Promote it only after a second consumer
proves shared behavior. Add the narrow component contract, interaction tests,
accessibility checks, representative content extremes, and both-theme visual
evidence. Do not add an index of empty component categories.

### Add a page

Register the hosted route through the existing generated route catalog. Keep
credential capture in the nonvisual bootstrap and pass only purpose-specific,
sanitized state into the page. Compose feature-owned behavior from shared
primitives, preserve URL-addressable view state, and test reload, back/forward,
deep-link, failure, empty, pending, offline, and permission-denied outcomes as
applicable.

## Commands and acceptance

From `webapp`:

~~~sh
npm run design-system:generate
npm run design-system:check
npm run check
~~~

Generation is one-way. The check rejects stale generated CSS or runtime theme
catalogs, incomplete themes, standard contrast regressions, unbundled
typography, an unsafe default target size, and forbidden authored-style forms.

Before a visible slice is complete, exercise it at minimum at `390×844`,
`768×1024`, and `1440×1024`; in light, dark, and system modes; with keyboard
only; at `200%` zoom; with reduced motion; with short and long localized copy;
and with empty, pending, success, recoverable failure, and terminal failure
states that the flow can actually enter. Screenshots support review but do not
replace interaction.
