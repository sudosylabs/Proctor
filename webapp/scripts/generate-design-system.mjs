// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import {mkdir, writeFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

import {
  generatedDesignTokenPath,
  generatedThemeCatalogPath,
  renderDesignTokenCSS,
  renderThemeCatalog,
} from '../design-system/index.mjs';

const webappRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const target = resolve(webappRoot, generatedDesignTokenPath);
const themeTarget = resolve(webappRoot, generatedThemeCatalogPath);

await mkdir(dirname(target), {recursive: true});
await writeFile(target, renderDesignTokenCSS(), 'utf8');
await mkdir(dirname(themeTarget), {recursive: true});
await writeFile(themeTarget, renderThemeCatalog(), 'utf8');
