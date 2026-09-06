// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import {copyFile, lstat, mkdir, readFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const repository = resolve(dirname(fileURLToPath(import.meta.url)), '..');

// Only repository tooling reads the artwork masters. Packages consume copies.
export const brandCopies = [
  ['lockups/horizontal/svg/proctor-lockup-horizontal-flat-light.svg', 'webapp/src/assets/brand/proctor-lockup.svg'],
  ['lockups/horizontal/svg/proctor-lockup-horizontal-flat-purple-white.svg', 'webapp/src/assets/brand/proctor-lockup-purple-white.svg'],
  ['favicons/light/proctor-favicon-light.svg', 'webapp/src/assets/brand/proctor-favicon-light.svg'],
  ['favicons/dark/proctor-favicon-dark.svg', 'webapp/src/assets/brand/proctor-favicon-dark.svg'],
  ['favicons/light/proctor-favicon-light-32.png', 'webapp/src/assets/brand/proctor-favicon-light-32.png'],
  ['favicons/dark/proctor-favicon-dark-32.png', 'webapp/src/assets/brand/proctor-favicon-dark-32.png'],
  ['app-icons/apple/png/proctor-apple-touch-icon-180.png', 'webapp/src/assets/brand/proctor-apple-touch-icon-180.png'],
  ['lockups/horizontal/svg/proctor-docs-lockup-horizontal-flat-white.svg', 'docs/site/static/img/brand/proctor-docs-lockup-white.svg'],
  ['favicons/light/proctor-favicon-light.svg', 'docs/site/static/img/brand/proctor-favicon-light.svg'],
  ['favicons/dark/proctor-favicon-dark.svg', 'docs/site/static/img/brand/proctor-favicon-dark.svg'],
  ['favicons/light/proctor-favicon-light-32.png', 'docs/site/static/img/brand/proctor-favicon-light-32.png'],
  ['lockups/horizontal/svg/proctor-lockup-horizontal-25d-light.svg', 'server/templates/proctor-lockup.svg'],
  ['lockups/horizontal/png/proctor-lockup-horizontal-25d-light-mail-600.png', 'server/templates/proctor-lockup-25d-v1.png', true],
];

async function regularFile(path) {
  const stat = await lstat(path);
  if (!stat.isFile()) throw new Error('expected a regular file');
  return readFile(path);
}

export async function checkBrandCopies(root = repository, copies = brandCopies) {
  const failures = [];
  for (const [source, target] of copies) {
    try {
      const [master, local] = await Promise.all([
        regularFile(resolve(root, 'brand', source)),
        regularFile(resolve(root, target)),
      ]);
      if (!master.equals(local)) failures.push(`${target} differs from approved artwork ${source}`);
    } catch (error) {
      failures.push(`${target}: ${error.message}`);
    }
  }
  return failures;
}

export async function copyBrandAssets(root = repository, copies = brandCopies) {
  // Validate all inputs before replacing any copy. Versioned mail images stay
  // immutable because queued messages refer to their original content IDs.
  for (const [source, target, immutable] of copies) {
    const master = await regularFile(resolve(root, 'brand', source));
    try {
      const local = await regularFile(resolve(root, target));
      if (immutable && !local.equals(master)) {
        throw new Error(`${target} is immutable; add a new version and update its mapping`);
      }
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
  }
  for (const [source, target] of copies) {
    const destination = resolve(root, target);
    await mkdir(dirname(destination), {recursive: true});
    await copyFile(resolve(root, 'brand', source), destination);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const command = process.argv[2] ?? 'check';
  try {
    if (command === 'copy') {
      await copyBrandAssets();
      process.stdout.write('Copied approved artwork. Review and update package-local asset digests before running package checks.\n');
    } else if (command !== 'check') {
      throw new Error('usage: node build/brand-assets.mjs [check|copy]');
    }
    const failures = await checkBrandCopies();
    if (failures.length) throw new Error(failures.join('\n'));
    process.stdout.write('Package brand copies match the approved artwork.\n');
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
