import {readFile, readdir} from 'node:fs/promises';
import {dirname, relative, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

import {designTokens} from './tokens.mjs';

const moduleRoot = dirname(fileURLToPath(import.meta.url));
const defaultRepoRoot = resolve(moduleRoot, '../../..');

export const CURRENT_ILLUSTRATION_SYSTEM = designTokens.illustration.system;

function cssVariables(values, prefix = '--proctor-') {
  return Object.entries(values)
    .map(([name, value]) => `  ${prefix}${name}: ${value};`)
    .join('\n');
}

function infimaVariables(values) {
  return Object.entries(values)
    .map(([name, value]) => `  --ifm-color-${name}: ${value};`)
    .join('\n');
}

export function renderDesignTokenCSS() {
  const fontImports = [
    ...designTokens.typography.sans.imports,
    ...designTokens.typography.mono.imports,
  ]
    .map((path) => `@import "${path}";`)
    .join('\n');
  const scaleVariables = [
    cssVariables(designTokens.spacing, '--proctor-space-'),
    cssVariables(designTokens.type, '--proctor-type-'),
    cssVariables(designTokens.radius, '--proctor-radius-'),
    cssVariables(designTokens.stroke, '--proctor-stroke-'),
  ].join('\n');

  return `${fontImports}

/* Generated from docs/site/design-system/tokens.mjs. Do not edit by hand. */
:root {
  color-scheme: light;

${cssVariables(designTokens.light)}
${scaleVariables}

${infimaVariables(designTokens.infima.light)}
  --ifm-background-color: var(--proctor-canvas);
  --ifm-background-surface-color: var(--proctor-surface);
  --ifm-font-family-base: ${designTokens.typography.sans.family};
  --ifm-font-family-monospace: ${designTokens.typography.mono.family};
  --ifm-heading-font-family: var(--ifm-font-family-base);
  --ifm-heading-font-weight: 600;
  --ifm-heading-color: var(--proctor-ink);
  --ifm-font-color-base: var(--proctor-ink-soft);
  --ifm-navbar-height: 4rem;
  --ifm-navbar-shadow: none;
  --ifm-global-radius: var(--proctor-radius-control);
  --ifm-code-font-size: 0.875rem;
  --ifm-container-width-xl: 1440px;
  --doc-sidebar-width: 17.5rem;
}

[data-theme="dark"] {
  color-scheme: dark;

${cssVariables(designTokens.dark)}

${infimaVariables(designTokens.infima.dark)}
  --ifm-background-color: var(--proctor-canvas);
  --ifm-background-surface-color: var(--proctor-surface);
  --ifm-heading-color: var(--proctor-ink);
  --ifm-font-color-base: var(--proctor-ink-soft);
}
`;
}

function colorChannels(hex) {
  return hex
    .slice(1)
    .match(/../g)
    .map((channel) => Number.parseInt(channel, 16) / 255);
}

function relativeLuminance(hex) {
  const [red, green, blue] = colorChannels(hex).map((channel) =>
    channel <= 0.03928
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

function validateTokenContract() {
  const failures = [];
  const lightKeys = Object.keys(designTokens.light).sort();
  const darkKeys = Object.keys(designTokens.dark).sort();
  if (JSON.stringify(lightKeys) !== JSON.stringify(darkKeys)) {
    failures.push('light and dark themes must expose the same semantic tokens');
  }
  if (designTokens.light.primary !== '#5c00aa') {
    failures.push('the light primary token must remain the canonical brand purple #5c00aa');
  }

  const contrastPairs = [
    ['light ink', designTokens.light.ink, designTokens.light.canvas],
    ['light soft ink', designTokens.light['ink-soft'], designTokens.light.canvas],
    ['light muted ink', designTokens.light['ink-muted'], designTokens.light.canvas],
    ['light primary', designTokens.light.primary, designTokens.light.canvas],
    ['light action', designTokens.light['on-action'], designTokens.light.action],
    ['light teal', designTokens.light.transient, designTokens.light['transient-soft']],
    ['light attention', designTokens.light.attention, designTokens.light['attention-soft']],
    ['light danger', designTokens.light.danger, designTokens.light['danger-soft']],
    ['light plate primary', designTokens.light['print-primary'], designTokens.light.plate],
    ['light plate complete', designTokens.light['print-complete'], designTokens.light.plate],
    ['light plate complete surface', designTokens.light['print-complete'], designTokens.light['print-complete-soft']],
    ['dark ink', designTokens.dark.ink, designTokens.dark.canvas],
    ['dark soft ink', designTokens.dark['ink-soft'], designTokens.dark.canvas],
    ['dark muted ink', designTokens.dark['ink-muted'], designTokens.dark.canvas],
    ['dark primary', designTokens.dark.primary, designTokens.dark.canvas],
    ['dark action', designTokens.dark['on-action'], designTokens.dark.action],
    ['dark teal', designTokens.dark.transient, designTokens.dark['transient-soft']],
    ['dark attention', designTokens.dark.attention, designTokens.dark['attention-soft']],
    ['dark danger', designTokens.dark.danger, designTokens.dark['danger-soft']],
    ['dark plate primary', designTokens.dark['print-primary'], designTokens.dark.plate],
    ['dark plate complete', designTokens.dark['print-complete'], designTokens.dark.plate],
    ['dark plate complete surface', designTokens.dark['print-complete'], designTokens.dark['print-complete-soft']],
  ];
  for (const [name, foreground, background] of contrastPairs) {
    const ratio = contrastRatio(foreground, background);
    if (ratio < 4.5) {
      failures.push(`${name} contrast ${ratio.toFixed(2)} is below 4.5:1`);
    }
  }

  const standardWeights = new Set([400, 500, 600, 700]);
  for (const weight of designTokens.typography.sans.weights) {
    if (!standardWeights.has(weight)) {
      failures.push(`unsupported sans font weight ${weight}`);
    }
  }
  for (const weight of designTokens.typography.mono.weights) {
    if (!standardWeights.has(weight)) {
      failures.push(`unsupported mono font weight ${weight}`);
    }
  }
  return failures;
}

async function walkCSS(directory) {
  const files = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await walkCSS(path)));
    } else if (entry.isFile() && entry.name.endsWith('.css')) {
      files.push(path);
    }
  }
  return files.sort();
}

function validateAuthoredCSS(source, name) {
  const failures = [];
  if (/--proctor-(?:cobalt|aqua)(?:-|\b)/.test(source)) {
    failures.push(`${name}: legacy color-token names are forbidden`);
  }
  if (/(?:#[0-9a-f]{3,8}\b|\b(?:rgb|hsl)a?\()/i.test(source)) {
    failures.push(`${name}: literal colors belong in design-system/tokens.mjs`);
  }
  if (/(?<![-\w])(?:black|white|red|blue|green|purple|orange|teal|gray|grey)(?![-\w])/i.test(source)) {
    failures.push(`${name}: named colors belong in design-system/tokens.mjs`);
  }
  for (const match of source.matchAll(/\bfont-weight\s*:\s*(\d+)/g)) {
    if (![400, 500, 600, 700].includes(Number(match[1]))) {
      failures.push(`${name}: font weight ${match[1]} is outside the IBM Plex scale`);
    }
  }
  for (const match of source.matchAll(/\bfont\s*:\s*(\d+)\b/g)) {
    if (![400, 500, 600, 700].includes(Number(match[1]))) {
      failures.push(`${name}: font shorthand weight ${match[1]} is outside the IBM Plex scale`);
    }
  }
  for (const match of source.matchAll(/\{([^{}]*)\}/gs)) {
    const declaration = match[1];
    if (!declaration.includes('var(--ifm-font-family-monospace)')) {
      continue;
    }
    const weight = declaration.match(/\bfont-weight\s*:\s*(\d+)/)?.[1];
    if (weight && !designTokens.typography.mono.weights.includes(Number(weight))) {
      failures.push(
        `${name}: monospace font weight ${weight} is not bundled for IBM Plex Mono`,
      );
    }
  }
  return failures;
}

export async function auditDesignSystem({repoRoot = defaultRepoRoot} = {}) {
  const failures = validateTokenContract();
  const generatedPath = resolve(repoRoot, 'docs/site/src/css/tokens.css');
  const expectedCSS = renderDesignTokenCSS();
  let actualCSS = '';
  try {
    actualCSS = await readFile(generatedPath, 'utf8');
  } catch (error) {
    failures.push(`docs/site/src/css/tokens.css: ${error.message}`);
  }
  if (actualCSS && actualCSS !== expectedCSS) {
    failures.push(
      'docs/site/src/css/tokens.css: generated output is stale; run npm run generate:design-system',
    );
  }

  const styleRoots = [
    resolve(repoRoot, 'docs/site/src'),
    resolve(repoRoot, 'docs/public'),
    resolve(repoRoot, 'docs/api'),
  ];
  for (const styleRoot of styleRoots) {
    for (const file of await walkCSS(styleRoot)) {
      if (file === generatedPath) {
        continue;
      }
      const name = relative(repoRoot, file);
      failures.push(...validateAuthoredCSS(await readFile(file, 'utf8'), name));
    }
  }
  return failures;
}

export function illustrationPalette(system, assetId) {
  if (system === designTokens.illustration.system) {
    return new Set(designTokens.illustration.palette);
  }
  throw new Error(`unknown illustration system ${system}`);
}
