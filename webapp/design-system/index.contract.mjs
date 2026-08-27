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
  assert.match(source, /"themeColor": "#111111"/);
  assert.match(source, /export type ThemePreference/);
  assert.match(source, /value === "system" \|\| isThemeID\(value\)/);
});

test('dark theme preserves the governed neutral, accent, and state palette', () => {
  const color = designTokens.themes.dark.color;
  assert.deepEqual(
    {
      canvas: color['background-canvas'],
      subtle: color['background-subtle'],
      surface: color['background-surface'],
      raised: color['background-raised'],
      border: color['border-default'],
      primary: color['action-primary'],
      hover: color['action-primary-hover'],
      onPrimary: color['action-on-primary'],
      focus: color['focus-ring'],
      link: color['foreground-link'],
      success: color['state-success'],
      successSurface: color['state-success-surface'],
      warning: color['state-warning'],
      warningSurface: color['state-warning-surface'],
      danger: color['state-danger'],
      dangerSurface: color['state-danger-surface'],
    },
    {
      canvas: '#111111',
      subtle: '#171717',
      surface: '#1c1c1c',
      raised: '#242424',
      border: '#2e2e2e',
      primary: '#a855f7',
      hover: '#c084fc',
      onPrimary: '#0a0a0a',
      focus: '#f5f3ff',
      link: '#e9d5ff',
      success: '#3ecf8e',
      successSurface: '#0f241c',
      warning: '#f5b942',
      warningSurface: '#2a2110',
      danger: '#f87171',
      dangerSurface: '#2a1218',
    },
  );
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
