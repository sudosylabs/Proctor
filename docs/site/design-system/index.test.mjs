import assert from 'node:assert/strict';
import test from 'node:test';

import {
  CURRENT_ILLUSTRATION_SYSTEM,
  LEGACY_ILLUSTRATION_SYSTEM,
  auditDesignSystem,
  illustrationPalette,
  renderDesignTokenCSS,
} from './index.mjs';

test('the tracked design-system adapter is current and valid', async () => {
  assert.deepEqual(await auditDesignSystem(), []);
});

test('generated CSS self-hosts IBM Plex and exposes canonical purple', () => {
  const source = renderDesignTokenCSS();
  assert.match(source, /@fontsource\/ibm-plex-sans\/latin-400\.css/);
  assert.match(source, /@fontsource\/ibm-plex-mono\/latin-600\.css/);
  assert.match(source, /--proctor-primary: #5c00aa/);
  assert.match(source, /--ifm-font-family-base: "IBM Plex Sans"/);
});

test('current illustrations use the purple semantic palette', () => {
  const palette = illustrationPalette(CURRENT_ILLUSTRATION_SYSTEM, 'new-diagram');
  assert(palette.has('#5c00aa'));
  assert(!palette.has('#3657d6'));
});

test('the cobalt palette is frozen to the four migration assets', () => {
  assert(
    illustrationPalette(
      LEGACY_ILLUSTRATION_SYSTEM,
      'installation-authority-topology',
    ).has('#3657d6'),
  );
  assert.throws(
    () => illustrationPalette(LEGACY_ILLUSTRATION_SYSTEM, 'new-diagram'),
    /frozen/,
  );
});
