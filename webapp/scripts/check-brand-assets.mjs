// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import {createHash} from 'node:crypto';
import {readFile, readdir} from 'node:fs/promises';

const packageAssets = new URL('../src/assets/brand/', import.meta.url);
const canonicalAssets = new URL('../../assets/brand/', import.meta.url);

const exactCopies = [
  {
    local: 'proctor-mark.svg',
    canonical: 'mark/proctor-mark.svg',
  },
  {
    local: 'proctor-lockup.svg',
    canonical: 'lockup/proctor-lockup.svg',
  },
  {
    local: 'proctor-lockup-white.svg',
    canonical: 'lockup/proctor-lockup-white.svg',
  },
];

const derivedRasterSource = {
  canonical: 'mark/proctor-mark-512.png',
  canonicalSHA256:
    '563655416240e68642d78aba57f363be03ce492172cb1215086ab5d5ea4944f5',
};

const derivedRasters = [
  {
    local: 'proctor-mark-32.png',
    localSHA256:
      'ccdf760968020f6655e7b669782833b189d509d83d0c210d4a43e1e07664f800',
    width: 32,
    height: 32,
  },
  {
    local: 'proctor-apple-touch-icon-180.png',
    localSHA256:
      '3e265c9705f47811d40af8effe8c176043574faeee692c70bdec33d7b0443faa',
    width: 180,
    height: 180,
  },
];

function sha256(data) {
  return createHash('sha256').update(data).digest('hex');
}

function pngDimensions(data) {
  const signature = '89504e470d0a1a0a';
  if (data.length < 24 || data.subarray(0, 8).toString('hex') !== signature) {
    throw new Error('not a PNG image');
  }
  return {width: data.readUInt32BE(16), height: data.readUInt32BE(20)};
}

const failures = [];

for (const asset of exactCopies) {
  const [local, canonical] = await Promise.all([
    readFile(new URL(asset.local, packageAssets)),
    readFile(new URL(asset.canonical, canonicalAssets)),
  ]);
  if (!local.equals(canonical)) {
    failures.push(
      `${asset.local} differs from canonical asset ${asset.canonical}`,
    );
  }
}

const rasterSource = await readFile(
  new URL(derivedRasterSource.canonical, canonicalAssets),
);
if (sha256(rasterSource) !== derivedRasterSource.canonicalSHA256) {
  failures.push(
    `${derivedRasterSource.canonical} changed; regenerate and review the webapp raster assets`,
  );
}

for (const asset of derivedRasters) {
  const local = await readFile(new URL(asset.local, packageAssets));
  if (sha256(local) !== asset.localSHA256) {
    failures.push(`${asset.local} differs from its reviewed derivative`);
  }
  try {
    const dimensions = pngDimensions(local);
    if (dimensions.width !== asset.width || dimensions.height !== asset.height) {
      failures.push(`${asset.local} must be ${asset.width}x${asset.height}px`);
    }
  } catch (error) {
    failures.push(`${asset.local}: ${error.message}`);
  }
}

const expectedFiles = new Set([
  'README.md',
  ...exactCopies.map((asset) => asset.local),
  ...derivedRasters.map((asset) => asset.local),
]);
const actualFiles = await readdir(packageAssets);
for (const filename of actualFiles) {
  if (!expectedFiles.has(filename)) {
    failures.push(`unreviewed webapp brand asset: ${filename}`);
  }
}
for (const filename of expectedFiles) {
  if (!actualFiles.includes(filename)) {
    failures.push(`missing webapp brand asset: ${filename}`);
  }
}

if (failures.length > 0) {
  for (const failure of failures) {
    process.stderr.write(`${failure}\n`);
  }
  process.exitCode = 1;
}
