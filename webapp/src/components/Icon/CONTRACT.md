# Icon contract

`Icon` is the sole product-icon adapter for the server-hosted webapp. It maps
stable, semantic Proctor names to selected Lucide React glyphs so feature code
does not depend on library names or APIs. The current vocabulary is
`showPassword`, `hidePassword`, and `loading`; a new name is added only after
a concrete product use proves its meaning. Inline notices rely on text and a
semantic rule instead of an icon.

The component owns inline SVG rendering, the design-system `16px`, `20px`, and
`24px` size tokens, a consistent `2px` stroke, `currentColor`, and a stable
diagnostic `data-proctor-icon` value. Consumers may supply a class to establish
semantic color or alignment. They may not alter the icon's SVG structure,
stroke grammar, or view box. `lucide-react` is imported only by this adapter.

Every rendered `Icon` is decorative: it is not focusable and is hidden from
assistive technology. The owning component must communicate meaning in visible
text or an accessible loading label. An icon-only control owns its accessible
name and pointer target; the icon does not. Directional icons require a separate
review for right-to-left behavior before entering the vocabulary.

Brand marks, provider marks, illustrations, avatars, and CSS structural state
indicators are not product icons. They retain their own asset or component
contracts and must not be approximated with Lucide glyphs.

Unit tests cover every governed name and size. Browser tests cover icons in
their real password-disclosure and loading contexts, including theme and narrow
layout behavior.
