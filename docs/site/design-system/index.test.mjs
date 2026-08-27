import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import test from 'node:test';
import {fileURLToPath} from 'node:url';

import {
  CURRENT_ILLUSTRATION_SYSTEM,
  auditDesignSystem,
  illustrationPalette,
  renderDesignTokenCSS,
} from './index.mjs';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');

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

test('retired illustration systems are no longer accepted', () => {
  assert.throws(
    () => illustrationPalette('legacy-cobalt-v0', 'installation-authority-topology'),
    /unknown illustration system/,
  );
});

test('documentation brand copies match their canonical masters', async () => {
  const [canonicalWordmark, docsWordmark, canonicalDarkMark, docsDarkMark] =
    await Promise.all([
      readFile(
        resolve(repoRoot, 'assets/brand/lockup/proctor-docs-lockup-white.svg'),
        'utf8',
      ),
      readFile(
        resolve(
          repoRoot,
          'docs/site/static/img/brand/proctor-docs-lockup-white.svg',
        ),
        'utf8',
      ),
      readFile(resolve(repoRoot, 'assets/brand/mark/proctor-mark-black.svg'), 'utf8'),
      readFile(
        resolve(repoRoot, 'docs/site/static/img/brand/proctor-mark-dark.svg'),
        'utf8',
      ),
    ]);

  assert.equal(docsWordmark, canonicalWordmark);
  assert.equal(docsDarkMark, canonicalDarkMark);
});
