// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  auditDesignSystem,
  auditTokenContract,
  renderDesignTokenCSS,
  renderThemeCatalog,
} from './index.mjs';
import {designTokens} from './tokens.mjs';

test('the tracked design-system adapter is current and valid', async () => {
  assert.deepEqual(await auditDesignSystem(), []);
});

test('generated runtime catalog derives theme and preference identifiers', () => {
  const source = renderThemeCatalog();
  assert.match(source, /"id": "light"/);
  assert.match(source, /"id": "dark"/);
  assert.match(source, /"themeColor": "#ffffff"/);
  assert.match(source, /"themeColor": "#141016"/);
  assert.match(source, /export type ThemePreference/);
  assert.match(source, /value === "system" \|\| isThemeID\(value\)/);
});

test('generated CSS supports system, light, dark, and reduced-motion modes', () => {
  const source = renderDesignTokenCSS();
  assert.match(source, /:root:not\(\[data-theme\]\)/);
  assert.match(source, /:root\[data-theme="light"\]/);
  assert.match(source, /:root\[data-theme="dark"\]/);
  assert.match(source, /prefers-reduced-motion: reduce/);
  assert.match(
    source,
    /@fontsource\/ibm-plex-sans-condensed\/files\/ibm-plex-sans-condensed-latin-600-normal\.woff2/,
  );
  assert.match(source, /font-display: swap/);
  assert.match(
    source,
    /@layer reset, tokens, base, components, utilities, overrides;/,
  );
});

test('a theme cannot omit a semantic role', () => {
  const tokens = structuredClone(designTokens);
  delete tokens.themes.dark.color['state-danger'];
  assert(
    auditTokenContract(tokens).some((failure) =>
      failure.includes('dark must expose the complete semantic color contract'),
    ),
  );
});

test('contrast regressions fail the token contract', () => {
  const tokens = structuredClone(designTokens);
  tokens.themes.light.color['foreground-muted'] = '#ffffff';
  assert(
    auditTokenContract(tokens).some((failure) =>
      failure.includes('light foreground-muted on background-canvas contrast'),
    ),
  );
});
