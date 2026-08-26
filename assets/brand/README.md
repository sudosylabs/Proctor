# Brand assets

Accepted Proctor mark and the derived uses. Vectors are the source; PNGs are
convenience rasters of those vectors.

Brand color is `#5C00AA`. Dark ink is `#161616`.

These files are the canonical masters. Never import, embed, or path-reference
a vector or image from this tree. If a package needs a mark, icon, avatar, or
lockup, copy the needed files into that package and use the copy.

## Layout

| Folder | Use |
| --- | --- |
| [`mark/`](mark/) | Standalone container mark, transparent |
| [`app-icon/`](app-icon/) | Desktop / dock tile: purple squircle, white knockout |
| [`avatar/`](avatar/) | GitHub and circular avatars: purple circle, white knockout |
| [`lockup/`](lockup/) | Mark + “Proctor” wordmark |

## Mark

The mark is a rounded-square opening in front of a solid L-shaped wall,
offset up-left, with a visible gap and 45° miters on the L’s free ends. It
is a fenced workspace, not an eye.

- [`mark/proctor-mark.svg`](mark/proctor-mark.svg) — purple on transparent
- [`mark/proctor-mark-white.svg`](mark/proctor-mark-white.svg) — white on transparent
- [`mark/proctor-mark-black.svg`](mark/proctor-mark-black.svg) — ink on transparent; reserved for documentation-site presentation
- [`mark/proctor-mark-512.png`](mark/proctor-mark-512.png) — purple raster
- [`mark/proctor-mark-drawing.png`](mark/proctor-mark-drawing.png) — accepted original drawing

Do not fill the wall. Do not invent a second 16px mark; at that size the gap
and miters collapse. Keep this drawing.

## App icon

White knockout on a purple squircle. Same geometry as the mark; 18% padding.

## Avatar

White knockout on a purple circle. Same geometry, 21% padding, nudged a few
percent down-right so the L’s offset does not look top-heavy.

## Lockup

Horizontal mark + outlined “Proctor”. Align the type with the square, not the
full L. [`proctor-lockup.svg`](lockup/proctor-lockup.svg) uses the purple mark
and ink type for light surfaces;
[`proctor-lockup-purple-white.svg`](lockup/proctor-lockup-purple-white.svg)
keeps the purple mark with white type for dark product surfaces; and
[`proctor-lockup-white.svg`](lockup/proctor-lockup-white.svg) is the all-white
monochrome lockup.

Word outlines come from IBM Plex Sans Medium (SIL Open Font License). The
font file is not vendored here.

## Raster sizes

App icon PNGs: 1024, 512, 256, 128, 64, 32. Avatar: 512. Mark: 512. Lockup
previews sit on light (`#F4F4F6`) and dark (`#121214`) boards.
