# Proctor brand

Final artwork for the Proctor container mark, preserving its open centre,
separation gap, and rounded front rim. The 2.5D treatment gives the existing
shape depth through shaded surfaces and a continuous bevel.

This directory is the sole source of approved artwork. Modules must copy the
files they need into their own package; imports, filesystem reads, symlinks,
and build or test dependencies on this directory are prohibited. Keep only
finished artwork, editable design sources, and usage information here.

## Choose the surface

`light` and `dark` describe the surface behind the artwork. Use the purple
2.5D mark on light surfaces and the ivory-front version on dark surfaces.
Matching lockups pair these with ink or white lettering. Flat marks are
available in purple (`#5C00AA`), ink (`#161616`), and white for monochrome use.
Standalone marks and lockups have transparent backgrounds.

| Use | Artwork |
| --- | --- |
| General identity | [Marks](marks/), [wordmarks](wordmarks/), and [horizontal](lockups/horizontal/) or [vertical](lockups/vertical/) lockups |
| Mail | Transparent horizontal PNG lockups named `mail-600` |
| Browser favicons | [2.5D mark](favicons/) with transparency, without a background tile |
| Windows | [2.5D mark](app-icons/windows/) with transparency; PNG, SVG, and multiresolution ICO |
| Linux | [2.5D mark](app-icons/linux/) with transparency; PNG and SVG |
| Apple | [Native icon](app-icons/apple/Proctor.icon/) with a charcoal-to-black background and system appearance variants |
| Apple home-screen bookmarks | Opaque 180px PNG named `proctor-apple-touch-icon-180` |
| Profile images | [Circle and square avatars](avatars/) with white or charcoal backgrounds |

The Apple `.icon` uses the same ivory-and-lilac geometry, palette, and bevel
artwork as the standalone `dark` mark and Windows/Linux `dark` icons.
Its charcoal-to-black background uses Apple's native material and appearance
handling. Additional foreground highlights, blur, translucency,
and shadows are disabled to keep the narrow rim crisp.
[PNG exports](app-icons/apple/png/) and
[ICNS](app-icons/apple/Proctor.icns) provide static fallbacks. Other platforms
use the standalone mark without an enclosing tile.

Preserve the supplied geometry and proportions. The original mark is also
used at favicon sizes; its narrow gap naturally becomes less distinct at
16 pixels. SVGs provide scalable artwork; PNGs are ready-sized exports.

The webapp retains flat page lockups, including the purple-mark/white-lettering
variant. The documentation site retains its flat “Proctor Docs” lockup.
Browser favicons and mail headers use the 2.5D artwork.

Repository-level copy and drift checks live outside this directory. Run
`make brand-copy` to refresh declared package copies, review their local
digests, then run `make brand-check` and the affected package checks. Versioned
mail images cannot be overwritten because queued messages retain their image
identity; add a new version when the mail artwork changes.

See [attribution](ATTRIBUTION.md) and the
[repository licensing policy](../LICENSING.md).
