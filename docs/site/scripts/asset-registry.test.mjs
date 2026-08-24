import assert from 'node:assert/strict';
import {mkdtemp, mkdir, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {resolve} from 'node:path';
import test from 'node:test';

import {auditAssetRegistry} from './asset-registry.mjs';

const safeSVG =
  '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50" viewBox="0 0 100 50"><rect width="100" height="50"/></svg>';

async function fixture({
  source = '<GovernedFigure asset="test-diagram" />',
  svg = safeSVG,
  width = 100,
  includeUnregistered = false,
  includeUngovernedStatic = false,
  privacyStatus = 'approved',
} = {}) {
  const root = await mkdtemp(resolve(tmpdir(), 'proctor-assets-'));
  await mkdir(resolve(root, 'docs/public/static/assets/diagrams'), {recursive: true});
  await mkdir(resolve(root, 'docs/public/operator'), {recursive: true});
  await mkdir(resolve(root, 'docs/api'), {recursive: true});
  await mkdir(resolve(root, 'docs/architecture'), {recursive: true});
  await writeFile(resolve(root, 'docs/public/static/assets/diagrams/test.svg'), svg);
  if (includeUnregistered) {
    await writeFile(resolve(root, 'docs/public/static/assets/diagrams/orphan.svg'), safeSVG);
  }
  if (includeUngovernedStatic) {
    await writeFile(resolve(root, 'docs/public/static/bypass.svg'), safeSVG);
  }
  await writeFile(resolve(root, 'docs/public/operator/index.mdx'), source);
  await writeFile(resolve(root, 'docs/architecture/runtime.md'), '# Runtime\n');
  await writeFile(
    resolve(root, 'docs/public/assets.json'),
    JSON.stringify({
      schema_version: 1,
      assets: [
        {
          id: 'test-diagram',
          path: 'static/assets/diagrams/test.svg',
          public_path: '/assets/diagrams/test.svg',
          kind: 'diagram',
          owner: 'Documentation maintainers',
          license: {
            status: 'pending',
            expression: null,
            note: 'Awaiting the documentation license decision.',
          },
          provenance: 'Original test fixture.',
          privacy_review: {status: privacyStatus, basis: 'Synthetic labels only.'},
          alt: 'A synthetic test diagram.',
          caption: 'A deterministic fixture.',
          width,
          height: 50,
          max_bytes: 2000,
          theme: 'light-high-contrast',
          last_reviewed: '2026-08-24',
          review_triggers: ['docs/architecture/runtime.md'],
        },
      ],
    }),
  );
  return root;
}

test('accepts a complete governed asset fixture', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture(),
    today: '2026-08-24',
  });
  assert.deepEqual(result.failures, []);
  assert.deepEqual(result.counts, {assets: 1, files: 1, references: 1});
});

test('rejects unregistered asset files', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({includeUnregistered: true}),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('unregistered asset file')));
});

test('rejects public static files outside the governed asset root', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({includeUngovernedStatic: true}),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('ungoverned public static file')));
});

test('rejects registered assets without an authored reference', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({source: '# No figure'}),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('not referenced')));
});

test('rejects active SVG content', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({
      svg: '<svg width="100" height="50" viewBox="0 0 100 50"><script>alert(1)</script></svg>',
    }),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('forbidden active')));
});

test('rejects external SVG resources', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({
      svg: '<svg width="100" height="50" viewBox="0 0 100 50"><use href="https://assets.example/shape.svg#mark"/></svg>',
    }),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('external resource')));
});

test('rejects dimension drift', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({width: 99}),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('dimensions do not match')));
});

test('requires an approved privacy review', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({privacyStatus: 'pending'}),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('privacy review must be approved')));
});

test('rejects unknown governed IDs and direct asset paths', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({
      source: '<GovernedFigure asset="missing-diagram" />\n[raw](/assets/diagrams/test.svg)',
    }),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('unknown governed asset')));
  assert(result.failures.some((failure) => failure.includes('direct asset paths are forbidden')));
});
