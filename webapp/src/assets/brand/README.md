# Webapp brand assets

This directory contains the smallest reviewed Proctor brand set needed by the
server-hosted browser application. Files remain beneath `src/` so Vite emits
referenced assets with content fingerprints under `/assets/`, the immutable
asset namespace owned by the server web UI.

The canonical masters remain in the repository
[`assets/brand`](../../../../assets/brand/README.md) directory. Do not edit
exact copies here or import a canonical master across the package boundary.

| Local asset | Canonical source | Use |
| --- | --- | --- |
| `proctor-mark.svg` | `mark/proctor-mark.svg` | Purple transparent browser favicon for light presentation |
| `proctor-mark-32.png` | Derived from `mark/proctor-mark-512.png` | Purple transparent 32px browser fallback for light presentation |
| `proctor-mark-white.svg` | `mark/proctor-mark-white.svg` | White transparent browser favicon for dark presentation |
| `proctor-mark-white-32.png` | Derived from `mark/proctor-mark-white.svg` | White transparent 32px browser fallback for dark presentation |
| `proctor-apple-touch-icon-180.png` | Derived from `mark/proctor-mark-512.png` | Transparent saved iOS and iPadOS home-screen icon |
| `proctor-lockup.svg` | `lockup/proctor-lockup.svg` | Brand lockup on light surfaces |
| `proctor-lockup-purple-white.svg` | `lockup/proctor-lockup-purple-white.svg` | Purple-mark, white-wordmark lockup on dark product surfaces |
| `proctor-lockup-white.svg` | `lockup/proctor-lockup-white.svg` | All-white lockup retained for approved monochrome uses |

The raster icons preserve each mark's transparent background and accepted
geometry. On macOS, regenerate them from the repository root with:

```sh
sips -z 32 32 assets/brand/mark/proctor-mark-512.png \
  --out webapp/src/assets/brand/proctor-mark-32.png
sips -z 180 180 assets/brand/mark/proctor-mark-512.png \
  --out webapp/src/assets/brand/proctor-apple-touch-icon-180.png
rsvg-convert -w 32 -h 32 assets/brand/mark/proctor-mark-white.svg \
  -o webapp/src/assets/brand/proctor-mark-white-32.png
```

After refreshing a copy or derivative, run `npm run brand-assets:check` from
`webapp/`. If a raster changed intentionally, update its reviewed source and
output digests in `scripts/check-brand-assets.mjs` in the same change.
