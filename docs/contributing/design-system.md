# Documentation design system

Proctor documentation should feel like technical evidence prepared for an
operator, institution administrator, security reviewer, or developer who needs
to trust what they are reading. Its design is precise, calm, and inspectable.
It is neither a marketing skin nor a collection of page-specific visual ideas.

This document owns the durable visual language. The human-edited values live in
[`docs/site/design-system/tokens.mjs`](../site/design-system/tokens.mjs). The
generated [`tokens.css`](../site/src/css/tokens.css) is the Docusaurus adapter,
and the design-system module supplies the approved palettes to the visual-asset
validator. Edit the source module, not either adapter.

## Direction

The subject is self-hosted examination infrastructure. The audience needs
assurance about authority, sequence, state, and recovery. The design therefore
uses the visual language of an evidence sheet: aligned registration geometry,
fenced regions, explicit paths, and short verification rails. These devices
encode containment or proof; they are never background decoration.

The signature element is the **fenced evidence plate**. A plate is a bounded
surface with a restrained 8-pixel drafting grid and one clearly emphasized
relationship. Use it for an illustration, architecture map, or other evidence
that must be inspected separately from prose. Do not cover ordinary pages in a
grid or repeat the Proctor mark as a pattern.

The desktop reading structure is intentionally asymmetric:

```text
┌──────────────────── global navigation ────────────────────┐
│ section navigation │ 72–76ch reading column │ page outline │
│                    │ wider evidence when needed            │
└────────────────────────────────────────────────────────────┘
```

The API reference may use a denser specialist variant, but it inherits the
same typography, colors, spacing, and interaction states.

## Reading surfaces

Guide pages keep a `72–76ch` prose measure, contextual section navigation on
the left, and an on-page outline on the right when headings warrant one. A
compact metadata line before the page heading identifies the intended audience
and content maturity. That line is orientation, not a decorative badge row.

API overview pages use the same reader but may begin with a contract summary:
what is authoritative, how much of the contract is indexed, and whether the
reference can send requests. Endpoint pages use the full available article
width and split it `61/39` between the operation contract and code/request
panel. Below the desktop breakpoint those panels stack in document order.

An endpoint presents information in this order: operation name, exact method
and OpenAPI path, purpose, Proctor request requirements, request details, then
responses. HTTP methods identify protocol data but do not receive arbitrary
rainbow colors; the selected technical accent remains purple. Teal and coral
are reserved for successful and failed response states. Code-language tabs
are neutral until selected.

## Core palette

Six colors define the identity. Lighter surfaces and dark-mode values are
derived tokens, not new meanings.

| Color | Light value | Meaning |
| --- | --- | --- |
| Proctor purple | `#5C00AA` | Proctor-controlled, authoritative, selected, or primary path |
| Ink | `#161616` | Content and neutral structure |
| Paper | `#FFFFFF` | Reading canvas and print-stable evidence plate |
| Teal | `#0B766E` | Reconstructible/transient effects or a successfully completed outcome |
| Amber | `#95520D` | Human intervention, caution, paused, or incomplete state |
| Coral | `#B4233C` | Denied, invalid, destructive, unavailable, or fail-closed outcome |

Color is semantic, not decorative:

- Purple is the only general focal accent. A card does not receive teal,
  amber, or coral merely to make a row look varied.
- Teal never marks an ordinary section or neutral diagram object. It means the
  result can be reconstructed, is transient, or is explicitly complete.
- Amber indicates that a person must notice or intervene, or that progress is
  deliberately incomplete. It does not mean “secondary.”
- Coral is reserved for real failure, rejection, destructive action, or an
  unavailable authority. It does not decorate security content.
- Gray and ink carry passive context, inactive structure, and neutral
  relationships.

Every meaning must also survive without color. Pair color with a label, state
word, line style, shape, or position. Normal text maintains at least `4.5:1`
contrast against its actual background; the design-system check verifies the
standard foreground/surface pairs in both themes.

### Theme surfaces

| Role | Light | Dark |
| --- | --- | --- |
| Canvas | `#FFFFFF` | `#141016` |
| Sidebar | `#F7F5F9` | `#1B151E` |
| Surface | `#FFFFFF` | `#211A25` |
| Raised surface | `#FFFFFF` | `#2A2130` |
| Border | `#E1DCE5` | `#3B3142` |
| Strong border | `#C8C0CE` | `#584A60` |
| Primary text | `#161616` | `#F8F6FA` |
| Muted text | `#655F69` | `#AEA4B3` |

Illustrations remain light evidence plates in both site themes so their print,
contrast, and review result does not change with the surrounding page.

## Typography

Use locally bundled IBM Plex. The accepted Proctor wordmark was derived from
IBM Plex Sans Medium, so the documentation extends an existing brand choice.
No page or asset depends on a visitor having a commercial system font.

| Role | Family | Allowed weights |
| --- | --- | --- |
| Headings, interface, prose, illustration objects | IBM Plex Sans | `400`, `500`, `600`, `700` |
| Code, protocol values, state keys, utility annotations | IBM Plex Mono | `400`, `600` |

Do not request synthetic intermediate weights such as `650`, `720`, or `780`.
Do not set a literal font family in a page stylesheet. Use the generated
`--ifm-font-family-base` and `--ifm-font-family-monospace` variables.

The site type scale is:

| Role | Size | Line height | Default weight |
| --- | --- | --- | --- |
| Caption | `0.75rem` | `1.5` | `400` |
| Utility label | `0.75rem` | `1.25` | `600` |
| Small body | `0.875rem` | `1.6` | `400` |
| Body | `1rem` | `1.75` | `400` |
| Small heading | `1.25rem` | `1.35` | `600` |
| Section heading | `1.75rem` | `1.2` | `600` |
| Page heading | `3rem` | `1.05` | `700` |

Responsive page headings may use `clamp()` around the page-heading token. Body
copy stays within `72–76ch`; a large viewport is not permission to lengthen a
line.

Illustrations use only three intrinsic text sizes: `18px` object names, `14px`
short supporting labels, and `12px` IBM Plex Mono utility annotations. Do not
place smaller text in a 1200×720 figure. If the content does not fit, delete
detail or split the illustration.

## Spacing and geometry

The base grid is 8 pixels. The `4px` step exists only for optical corrections
inside a control or icon. Structural layout uses the tracked scale:

| Token | Pixels | Typical use |
| --- | ---: | --- |
| `space-1` | 4 | Optical adjustment |
| `space-2` | 8 | Tight internal gap |
| `space-3` | 12 | Label-to-value gap |
| `space-4` | 16 | Control padding |
| `space-5` | 24 | Card padding or related-object gap |
| `space-6` | 32 | Component separation |
| `space-7` | 48 | Section separation |
| `space-8` | 64 | Major composition gap |
| `space-9` | 96 | Page-level interval |

Use three radii consistently:

- controls and small tags: `6px`;
- cards: `12px`;
- evidence plates and large diagram objects: `16px`;
- pills: fully rounded only for a real status, token, or compact action.

A card is not a pill, and adjacent objects do not receive different radii for
visual variety. Shadows are limited to genuinely elevated surfaces such as a
search dialog. Ordinary cards and illustrations use borders and spacing.

## Icons

The Proctor icon vocabulary represents recurring domain or infrastructure
nouns. Icons inherit the mark's square construction, visible openings, and
45-degree transitions where the subject permits them.

- Optical sizes are `24px`, `32px`, and `48px`.
- The standard icon stroke is `2px`, with round caps and round joins unless a
  fenced edge requires a square end.
- Keep at least `2px` clear space at the 24px size.
- Do not mix emoji, a general-purpose icon library, and one-off SVG symbols.
- Do not add an icon for a concept used only once; use a label instead.
- Product or vendor marks remain their own artwork and never inherit the
  Proctor icon geometry.

The bounded icon set will be implemented in its own slice. Until then, do not
invent additional documentation icons.

## Illustration grammar

Every current-system diagram declares `visual_system` as
`proctor-assurance-v1` in the governed asset registry.

### Canvas and density

- Canvas: `1200×720` with a matching view box.
- Safe inset: `56px` on every side.
- Drafting grid: `8px`, using the plate-grid token at one-pixel width.
- Primary objects: normally four to seven.
- Focal elements: one, or at most two when a comparison is the point.
- One illustration communicates one claim. Split state, ownership, and
  transport behavior when combining them would require explanatory prose.

The page heading names the subject. The figure contains only object names,
short state labels, and necessary relationship labels. The visible caption
states the invariant. Do not repeat a poster title, subtitle, paragraph,
footnote, or prose-heavy legend inside the SVG.

### Objects and strokes

- Large objects use a `16px` radius and `2px` border.
- Small controls use a `6px` radius.
- Passive context uses ink or gray borders on paper.
- The authoritative or selected object uses purple fill or border, not both on
  every object in the path.
- Semantic surfaces use their corresponding soft token and retain a textual or
  geometric cue.
- All shapes align to the 8-pixel grid; use a 4-pixel optical offset only when
  stroke centering otherwise appears uneven.

### Connectors and arrows

- Primary connectors are `3px`; secondary relationships are `2px`; the
  drafting grid and annotation brackets are `1px`.
- Arrowheads are `12×12px`. Use one marker geometry across the complete asset
  set.
- Solid purple: authoritative ownership, commit, or primary transition.
- Dashed purple: explicit reference that is not ownership.
- Dashed teal: reconstructible or best-effort effect after a durable outcome.
- Amber: a human intervention or deliberately incomplete transition.
- Coral: rejection or fail-closed termination.
- Gray: passive context or a neutral alternative.

Every connector begins at a visible source boundary and ends with its arrow tip
on a meaningful target boundary. Orthogonal branches share an actual path
segment or junction dot. A branch may not stop near a spine, and an arrow may
not terminate in whitespace. Labels sit at least `8px` away from a connector
and never cover a bend or junction.

### Annotation rail

An evidence plate may end with one short bracket or rail stating the invariant
made visible by the drawing. The rail uses a one-pixel neutral stroke and no
more than two lines of 14px text. It must not become a second caption or a place
to rescue an unclear illustration.

## Vocabulary in visual design

Use canonical Proctor terms from [`CONTEXT.md`](../../CONTEXT.md). An
illustration label is not a place to create a shorter synonym such as “session”
for Exam Sitting or “group” for Class. If a canonical term makes the drawing
too dense, simplify the drawing rather than rename the concept.

The glossary and tooltip slice will make selected first occurrences
explainable. Tooltips do not appear inside illustrations, headings, code, or
API paths.

## Interaction and motion

Movement must explain a state change or preserve orientation. Hover movement is
limited to a small translation on a real interactive target. Do not animate
illustration paths, add ambient particles, or scatter unrelated transitions.
All motion honors `prefers-reduced-motion` and leaves the complete meaning
available without animation.

Focus indicators use a three-pixel primary outline with three-pixel offset.
Hover is never the only way to reveal information. Tooltips, dialogs, menus,
and search results must work with keyboard, pointer, and touch input.

## Human authoring workflow

To change a durable token:

1. Edit [`tokens.mjs`](../site/design-system/tokens.mjs).
2. Update this document when meaning, allowed use, or geometry changes.
3. From `docs/site`, run `npm run generate:design-system`.
4. Run `npm run check:design-system` and `npm run test:design-system`.
5. Review the affected pages in both themes at desktop and mobile widths.

The generator is deliberately one-way. It hides CSS serialization, font
imports, Infima compatibility variables, contrast checks, and palette lookup
behind a small interface. A maintainer does not synchronize separate color
lists by hand.

Authored stylesheets may use semantic custom properties and `color-mix()`, but
they may not introduce literal colors, the retired `cobalt`/`aqua` token names,
or unbundled font weights. The design-system audit rejects those forms.

## Legacy illustration migration

The four diagrams created before this contract declare
`legacy-cobalt-v0`. That palette is frozen to their exact registry IDs so no
new asset can use it. Each diagram will be simplified or split, redrawn under
`proctor-assurance-v1`, reviewed in its documentation page, and then removed
from the legacy allowlist. The legacy declaration is a migration fact, not an
alternative theme.

## Acceptance checklist

Before a visible slice is complete, inspect it in a browser:

- desktop `1440×1024` and mobile `390×844`;
- light and dark themes;
- keyboard order, focus visibility, and reduced motion;
- text contrast, line length, zoom, and narrow-width wrapping;
- no unintended horizontal overflow;
- figures at page width and full size;
- connector continuity, text containment, and print contrast;
- locally served IBM Plex with no third-party font request; and
- no semantic color used merely for visual variety.

Screenshots are evidence for the review, not a substitute for exercising the
page.
