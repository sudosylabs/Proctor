import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {readFile, readdir} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import test from 'node:test';
import {fileURLToPath} from 'node:url';

import {
  CURRENT_ILLUSTRATION_SYSTEM,
  auditDesignSystem,
  illustrationPalette,
  renderDesignTokenCSS,
} from './index.mjs';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

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

test('documentation ships only the reviewed local brand assets', async () => {
  const brandDirectory = resolve(siteRoot, 'static/img/brand');
  const expectedDigests = {
    'proctor-docs-lockup-white.svg':
      'ddd47cdd882006acb919a7bdb80b2098d4409800ea76c9f1900d89405bfed604',
    'proctor-favicon-dark.svg':
      'dbef0c418cd7fcb8bfda37824f15513e2ae57c09bed8553f351ba68ab1387617',
    'proctor-favicon-light-32.png':
      '8ddc9e10b52121abff7b9c356023587f7fa9b10b4d46ffa853947fc5f23fe69d',
    'proctor-favicon-light.svg':
      'a7a43a34b58c0da5e3247cc611f19168a2d4dc7d7ce416c076d1169ef356e4fb',
  };
  assert.deepEqual(
    (await readdir(brandDirectory)).sort(),
    Object.keys(expectedDigests).sort(),
  );

  await Promise.all(
    Object.entries(expectedDigests).map(async ([name, expectedDigest]) => {
      const contents = await readFile(resolve(brandDirectory, name));
      assert.equal(
        createHash('sha256').update(contents).digest('hex'),
        expectedDigest,
        `reviewed documentation asset ${name} changed`,
      );
    }),
  );
});
