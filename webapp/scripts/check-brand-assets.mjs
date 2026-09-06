// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import { createHash } from 'node:crypto';
import { readFile, readdir } from 'node:fs/promises';

const packageAssets = new URL('../src/assets/brand/', import.meta.url);
const { assets } = JSON.parse(
  await readFile(new URL('manifest.json', packageAssets), 'utf8'),
);

function dimensions(file, data) {
  if (file.endsWith('.png')) {
    if (
      data.length < 24 ||
      data.subarray(0, 8).toString('hex') !== '89504e470d0a1a0a'
    ) {
      throw new Error('not a PNG image');
    }
    return { width: data.readUInt32BE(16), height: data.readUInt32BE(20) };
  }
  if (file.endsWith('.svg')) {
    const viewBox = data.toString('utf8').match(/<svg\b[^>]*\bviewBox="0 0 (\d+) (\d+)"/);
    if (viewBox === null) {
      throw new Error('missing SVG dimensions');
    }
    return { width: Number(viewBox[1]), height: Number(viewBox[2]) };
  }
  throw new Error('unsupported brand asset format');
}

const failures = [];
const expectedFiles = new Set(['README.md', 'manifest.json']);
for (const asset of assets) {
  if (!/^[a-z0-9-]+\.(png|svg)$/.test(asset.file)) {
    failures.push(`invalid local brand asset name: ${asset.file}`);
    continue;
  }
  if (expectedFiles.has(asset.file)) {
    failures.push(`duplicate brand asset: ${asset.file}`);
    continue;
  }
  expectedFiles.add(asset.file);
  try {
    const data = await readFile(new URL(asset.file, packageAssets));
    if (createHash('sha256').update(data).digest('hex') !== asset.sha256) {
      failures.push(`${asset.file} differs from its reviewed local copy`);
    }
    const size = dimensions(asset.file, data);
    if (size.width !== asset.width || size.height !== asset.height) {
      failures.push(`${asset.file} must have dimensions ${asset.width}x${asset.height}`);
    }
  } catch (error) {
    failures.push(`${asset.file}: ${error.message}`);
  }
}

for (const filename of await readdir(packageAssets)) {
  if (!expectedFiles.has(filename)) {
    failures.push(`unreviewed webapp brand asset: ${filename}`);
  }
}

if (failures.length > 0) {
  for (const failure of failures) {
    process.stderr.write(`${failure}\n`);
  }
  process.exitCode = 1;
}
