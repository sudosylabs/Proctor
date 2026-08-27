# Product theme contract

`ProductTheme` is the single in-process owner of effective product-theme
resolution. It combines a typed theme preference with the current system color
scheme and returns the registered effective theme, root `data-theme` value,
and browser theme-color metadata as one immutable resolution.

The document synchronizer and `AccessPageShell` consume that same resolution.
The document owns root attributes and metadata; the shell selects a governed
lockup from the effective theme's `colorScheme`. Feature pages do not import
theme-specific assets or infer presentation from `prefers-color-scheme`.

`system` keeps `data-theme` absent, tracks system color-scheme changes, and
retains one media-qualified browser color per system scheme. An explicit theme
sets its registered ID on the root and uses one unqualified browser color.
Adding a complete theme to the generated catalog requires no page-specific
brand change: every theme declares whether it needs the light- or dark-surface
lockup.

The browser favicon remains a separate browser-chrome policy selected by the
system preference in `index.html`. This module does not add a theme chooser,
persistence, or feature-owned theme state.
