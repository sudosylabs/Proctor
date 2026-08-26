// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import {readFile, readdir} from 'node:fs/promises';
import {dirname, relative, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

import {designTokens} from './tokens.mjs';

const moduleRoot = dirname(fileURLToPath(import.meta.url));
const defaultWebappRoot = resolve(moduleRoot, '..');
const generatedTokenPath = 'src/styles/tokens.css';
const generatedThemePath = 'src/generated/design-system/themes.ts';

const requiredColorTokens = [
  'background-canvas',
  'background-subtle',
  'background-surface',
  'background-raised',
  'background-inverse',
  'foreground-default',
  'foreground-subtle',
  'foreground-muted',
  'foreground-disabled',
  'foreground-inverse',
  'foreground-link',
  'border-default',
  'border-strong',
  'border-interactive',
  'focus-ring',
  'action-primary',
  'action-primary-hover',
  'action-primary-active',
  'action-primary-disabled',
  'action-on-primary',
  'action-secondary',
  'action-secondary-hover',
  'action-secondary-active',
  'action-on-secondary',
  'state-info',
  'state-info-surface',
  'state-success',
  'state-success-surface',
  'state-warning',
  'state-warning-surface',
  'state-danger',
  'state-danger-surface',
  'selection-background',
  'selection-foreground',
  'scrim',
  'skeleton',
];

const requiredShadowTokens = ['raised', 'overlay', 'dialog'];

function kebabCase(value) {
  return value.replace(/[A-Z]/g, (match) => `-${match.toLowerCase()}`);
}

function cssVariables(values, prefix) {
  return Object.entries(values)
    .map(([name, value]) => `    --proctor-${prefix}-${kebabCase(name)}: ${value};`)
    .join('\n');
}

function systemVariables(tokens) {
  const declarations = [
    `    --proctor-font-family-display: ${tokens.typography.display.family};`,
    `    --proctor-font-family-text: ${tokens.typography.text.family};`,
    `    --proctor-font-family-mono: ${tokens.typography.mono.family};`,
  ];
  for (const [group, values] of Object.entries(tokens.system)) {
    declarations.push(cssVariables(values, kebabCase(group)));
  }
  return declarations.join('\n');
}

function themeVariables(theme) {
  return [
    `    color-scheme: ${theme.colorScheme};`,
    cssVariables(theme.color, 'color'),
    cssVariables(theme.shadow, 'shadow'),
  ].join('\n');
}

function fontFaces(tokens) {
  return Object.values(tokens.typography)
    .flatMap((font) =>
      Object.entries(font.files).map(
        ([weight, source]) => `@font-face {
  font-family: "${font.name}";
  font-style: normal;
  font-display: swap;
  font-weight: ${weight};
  src: url("${source}") format("woff2");
}`,
      ),
    )
    .join('\n\n');
}

export function renderDesignTokenCSS(tokens = designTokens) {
  const explicitThemes = Object.entries(tokens.themes)
    .map(
      ([id, theme]) => `  :root[data-theme="${id}"] {\n${themeVariables(theme)}\n  }`,
    )
    .join('\n\n');

  return `${fontFaces(tokens)}

/* Copyright 2026 SudoSylabs. SPDX-License-Identifier: AGPL-3.0-only. */
/* Generated from design-system/tokens.mjs. Do not edit by hand. */
@layer reset, tokens, base, components, utilities, overrides;

@layer tokens {
  :root {
${systemVariables(tokens)}
${themeVariables(tokens.themes.light)}
  }

  @media (prefers-color-scheme: dark) {
    :root:not([data-theme]) {
${themeVariables(tokens.themes.dark)}
    }
  }

${explicitThemes}

  @media (prefers-reduced-motion: reduce) {
    :root {
      --proctor-duration-immediate: 0.01ms;
      --proctor-duration-fast: 0.01ms;
      --proctor-duration-moderate: 0.01ms;
      --proctor-duration-slow: 0.01ms;
    }
  }
}
`;
}

export function renderThemeCatalog(tokens = designTokens) {
  const themes = Object.entries(tokens.themes).map(([id, theme]) => ({
    id,
    colorScheme: theme.colorScheme,
    themeColor: theme.color['background-canvas'],
  }));
  return `// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Generated from design-system/tokens.mjs. Do not edit.
export const themeCatalog = ${JSON.stringify(themes, null, 2)} as const;

export type ThemeID = (typeof themeCatalog)[number]["id"];

export const themePreferenceValues = [
  "system",
  ...themeCatalog.map((theme) => theme.id),
] as const;

export type ThemePreference = (typeof themePreferenceValues)[number];

export function isThemeID(value: string): value is ThemeID {
  return themeCatalog.some((theme) => theme.id === value);
}

export function isThemePreference(value: string): value is ThemePreference {
  return value === "system" || isThemeID(value);
}
`;
}

function parseHexColor(value) {
  if (!/^#[0-9a-f]{6}$/i.test(value)) {
    throw new Error(`expected six-digit hex color, got ${value}`);
  }
  return value
    .slice(1)
    .match(/../g)
    .map((channel) => Number.parseInt(channel, 16) / 255);
}

function relativeLuminance(value) {
  const [red, green, blue] = parseHexColor(value).map((channel) =>
    channel <= 0.04045
      ? channel / 12.92
      : ((channel + 0.055) / 1.055) ** 2.4,
  );
  return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
}

function contrastRatio(foreground, background) {
  const foregroundLuminance = relativeLuminance(foreground);
  const backgroundLuminance = relativeLuminance(background);
  return (
    (Math.max(foregroundLuminance, backgroundLuminance) + 0.05) /
    (Math.min(foregroundLuminance, backgroundLuminance) + 0.05)
  );
}

function checkContrast(failures, name, foreground, background, minimum) {
  const ratio = contrastRatio(foreground, background);
  if (ratio < minimum) {
    failures.push(`${name} contrast ${ratio.toFixed(2)} is below ${minimum}:1`);
  }
}

function keysEqual(actual, expected) {
  return JSON.stringify([...actual].sort()) === JSON.stringify([...expected].sort());
}

export function auditTokenContract(tokens = designTokens) {
  const failures = [];
  if (tokens.id !== 'proctor-web-v1') {
    failures.push('design-system id must remain proctor-web-v1 until a reviewed contract revision');
  }
  if (tokens.themes?.light?.colorScheme !== 'light') {
    failures.push('the light theme must declare colorScheme light');
  }
  if (tokens.themes?.dark?.colorScheme !== 'dark') {
    failures.push('the dark theme must declare colorScheme dark');
  }
  if (tokens.themes?.light?.color?.['action-primary'] !== '#5c00aa') {
    failures.push('the light primary action must use canonical Proctor purple #5c00aa');
  }

  for (const [id, theme] of Object.entries(tokens.themes ?? {})) {
    if (!/^[a-z][a-z0-9-]{0,31}$/.test(id)) {
      failures.push(`theme id ${id} is not a stable lowercase identifier`);
    }
    if (!theme.label?.trim()) {
      failures.push(`theme ${id} requires a maintainer-facing label`);
    }
    if (!['light', 'dark'].includes(theme.colorScheme)) {
      failures.push(`theme ${id} has unsupported colorScheme ${theme.colorScheme}`);
    }
    if (!keysEqual(Object.keys(theme.color ?? {}), requiredColorTokens)) {
      failures.push(`theme ${id} must expose the complete semantic color contract`);
    }
    if (!keysEqual(Object.keys(theme.shadow ?? {}), requiredShadowTokens)) {
      failures.push(`theme ${id} must expose the complete semantic shadow contract`);
    }
  }

  const fontWeights = new Set([400, 500, 600, 700]);
  for (const [role, font] of Object.entries(tokens.typography ?? {})) {
    if (!font.name?.trim() || !font.family?.includes(`"${font.name}"`)) {
      failures.push(`${role} typography requires a matching face name and family stack`);
    }
    const fileWeights = Object.keys(font.files ?? {}).map(Number);
    if (!keysEqual(fileWeights, font.weights ?? [])) {
      failures.push(`${role} typography must provide one file for every allowed weight`);
    }
    if (
      Object.values(font.files ?? {}).some(
        (source) =>
          !source.startsWith('@fontsource/') || !source.endsWith('-normal.woff2'),
      )
    ) {
      failures.push(`${role} typography must use local normal-style Fontsource WOFF2 files`);
    }
    for (const weight of font.weights ?? []) {
      if (!fontWeights.has(weight)) {
        failures.push(`${role} typography uses unsupported weight ${weight}`);
      }
    }
  }

  for (const [id, theme] of Object.entries(tokens.themes ?? {})) {
    const color = theme.color ?? {};
    if (!keysEqual(Object.keys(color), requiredColorTokens)) {
      continue;
    }
    for (const backgroundName of [
      'background-canvas',
      'background-subtle',
      'background-surface',
      'background-raised',
    ]) {
      for (const foregroundName of [
        'foreground-default',
        'foreground-subtle',
        'foreground-muted',
      ]) {
        checkContrast(
          failures,
          `${id} ${foregroundName} on ${backgroundName}`,
          color[foregroundName],
          color[backgroundName],
          4.5,
        );
      }
    }
    checkContrast(
      failures,
      `${id} link on canvas`,
      color['foreground-link'],
      color['background-canvas'],
      4.5,
    );
    checkContrast(
      failures,
      `${id} inverse foreground`,
      color['foreground-inverse'],
      color['background-inverse'],
      4.5,
    );
    for (const action of ['action-primary', 'action-primary-hover', 'action-primary-active']) {
      checkContrast(
        failures,
        `${id} action label on ${action}`,
        color['action-on-primary'],
        color[action],
        4.5,
      );
    }
    for (const action of [
      'action-secondary',
      'action-secondary-hover',
      'action-secondary-active',
    ]) {
      checkContrast(
        failures,
        `${id} secondary action label on ${action}`,
        color['action-on-secondary'],
        color[action],
        4.5,
      );
    }
    for (const state of ['info', 'success', 'warning', 'danger']) {
      checkContrast(
        failures,
        `${id} ${state} state`,
        color[`state-${state}`],
        color[`state-${state}-surface`],
        4.5,
      );
    }
    for (const backgroundName of [
      'background-canvas',
      'background-subtle',
      'background-surface',
      'background-raised',
    ]) {
      checkContrast(
        failures,
        `${id} focus ring on ${backgroundName}`,
        color['focus-ring'],
        color[backgroundName],
        3,
      );
      checkContrast(
        failures,
        `${id} interactive border on ${backgroundName}`,
        color['border-interactive'],
        color[backgroundName],
        3,
      );
    }
    checkContrast(
      failures,
      `${id} selection`,
      color['selection-foreground'],
      color['selection-background'],
      4.5,
    );
  }

  const spacing = Object.values(tokens.system?.space ?? {}).map((value) =>
    value === '0' ? 0 : Number.parseFloat(value),
  );
  if (spacing.some((value, index) => index > 0 && value <= spacing[index - 1])) {
    failures.push('spacing scale must be strictly increasing');
  }
  if (tokens.system?.controlSize?.default !== '2.75rem') {
    failures.push('default control size must retain the 44px pointer target floor');
  }
  return failures;
}

async function walk(directory) {
  const files = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await walk(path)));
    } else if (entry.isFile()) {
      files.push(path);
    }
  }
  return files.sort();
}

function auditAuthoredCSS(source, name) {
  const failures = [];
  if (/(?:#[0-9a-f]{3,8}\b|\b(?:rgb|hsl)a?\()/i.test(source)) {
    failures.push(`${name}: literal colors belong in design-system/tokens.mjs`);
  }
  if (/\btransition\s*:\s*all\b/i.test(source)) {
    failures.push(`${name}: transition: all is forbidden; list changed properties`);
  }
  if (/\boutline\s*:\s*(?:none|0)\b/i.test(source)) {
    failures.push(`${name}: removing the outline is forbidden without an audited replacement`);
  }
  for (const match of source.matchAll(/\bfont-family\s*:\s*([^;}]*)/gi)) {
    if (!match[1].includes('var(--proctor-font-family-')) {
      failures.push(`${name}: font families must use Proctor typography tokens`);
    }
  }
  for (const match of source.matchAll(/\bbox-shadow\s*:\s*([^;}]*)/gi)) {
    const value = match[1].trim();
    if (value !== 'none' && !value.startsWith('var(--proctor-shadow-')) {
      failures.push(`${name}: box shadows must use semantic elevation tokens`);
    }
  }
  return failures;
}

export async function auditDesignSystem({webappRoot = defaultWebappRoot} = {}) {
  const failures = auditTokenContract();
  const generatedPath = resolve(webappRoot, generatedTokenPath);
  let actualCSS = '';
  try {
    actualCSS = await readFile(generatedPath, 'utf8');
  } catch (error) {
    failures.push(`${generatedTokenPath}: ${error.message}`);
  }
  if (actualCSS && actualCSS !== renderDesignTokenCSS()) {
    failures.push(`${generatedTokenPath}: generated output is stale`);
  }

  let actualThemes = '';
  try {
    actualThemes = await readFile(resolve(webappRoot, generatedThemePath), 'utf8');
  } catch (error) {
    failures.push(`${generatedThemePath}: ${error.message}`);
  }
  if (actualThemes && actualThemes !== renderThemeCatalog()) {
    failures.push(`${generatedThemePath}: generated output is stale`);
  }

  try {
    const index = await readFile(resolve(webappRoot, 'index.html'), 'utf8');
    if (!/<html\s+lang="en"\s+dir="ltr">/.test(index)) {
      failures.push('index.html: safe language and direction fallbacks must be explicit');
    }
    if (!/<meta\s+name="color-scheme"\s+content="light dark"\s*\/>/.test(index)) {
      failures.push('index.html: color-scheme metadata must advertise light and dark');
    }
    for (const id of ['light', 'dark']) {
      const canvas = designTokens.themes[id].color['background-canvas'];
      const pattern = new RegExp(
        `<meta\\s+name="theme-color"\\s+content="${canvas}"\\s+media="\\(prefers-color-scheme: ${id}\\)"\\s*\\/>`,
      );
      if (!pattern.test(index)) {
        failures.push(`index.html: ${id} theme-color metadata is missing or stale`);
      }
    }
  } catch (error) {
    failures.push(`index.html: ${error.message}`);
  }

  try {
    const entry = await readFile(resolve(webappRoot, 'src/main.tsx'), 'utf8');
    const globalStyles = Array.from(
      entry.matchAll(/import "\.\/styles\/(reset|tokens|base)\.css";/g),
      (match) => match[1],
    );
    if (JSON.stringify(globalStyles) !== JSON.stringify(['reset', 'tokens', 'base'])) {
      failures.push('src/main.tsx: global styles must load in reset, tokens, base order');
    }
  } catch (error) {
    failures.push(`src/main.tsx: ${error.message}`);
  }

  try {
    const viteConfig = await readFile(resolve(webappRoot, 'vite.config.ts'), 'utf8');
    if (!/target:\s*"baseline-widely-available"/.test(viteConfig)) {
      failures.push('vite.config.ts: production browser target must be explicit');
    }
  } catch (error) {
    failures.push(`vite.config.ts: ${error.message}`);
  }

  const sourceRoot = resolve(webappRoot, 'src');
  for (const file of await walk(sourceRoot)) {
    if (!file.endsWith('.css') || file === generatedPath) {
      continue;
    }
    failures.push(
      ...auditAuthoredCSS(await readFile(file, 'utf8'), relative(webappRoot, file)),
    );
  }
  return failures;
}

export const generatedDesignTokenPath = generatedTokenPath;
export const generatedThemeCatalogPath = generatedThemePath;
