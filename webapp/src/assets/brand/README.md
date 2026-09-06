# Webapp brand assets

This directory owns the reviewed artwork used by the server-hosted browser
application. Assets remain beneath `src/` so Vite emits referenced files with
content fingerprints under the server's immutable `/assets/` namespace.
Product code, builds, tests, and package checks use only these local files.

The in-page identity uses flat horizontal lockups: purple with ink lettering
on light product surfaces, and purple with white lettering on dark product
surfaces. Browser favicons use the transparent 2.5D mark, selected by the
system color preference: purple for light browser chrome and ivory with lilac
for dark browser chrome. The Apple touch icon is the reviewed 180px static
Apple rendering with an opaque background for a saved home-screen shortcut.

The wordmark outlines come from IBM Plex Sans Medium under the SIL Open Font
License 1.1. The SVGs retain that attribution; no font file is embedded in the
artwork. The container mark and its 2.5D treatment are Proctor artwork.

The local `manifest.json` records each accepted asset's SHA-256 and dimensions.
After an intentional asset refresh, review the artwork and update this
manifest with the copies. From `webapp/`, run `npm run brand-assets:check` to
verify local content, dimensions, and the exact file set. Repository-level
maintenance owns copying approved artwork into this package; the package does
not read an external asset tree.
