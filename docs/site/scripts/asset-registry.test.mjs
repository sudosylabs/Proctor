import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
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
  visualSystem = 'proctor-assurance-v1',
  visualReviewHash,
  visualReviewChecks = [
    'text_containment',
    'connector_continuity',
    'rendered_legibility',
    'narrow_viewport',
    'print_contrast',
  ],
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
      schema_version: 3,
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
          visual_system: visualSystem,
          last_reviewed: '2026-08-24',
          review_triggers: ['docs/architecture/runtime.md'],
          visual_review: {
            status: 'approved',
            source_sha256:
              visualReviewHash ?? createHash('sha256').update(svg).digest('hex'),
            reviewed: '2026-08-24',
            method: 'Rendered browser fixture review.',
            checks: visualReviewChecks,
            viewports: [
              {name: 'desktop', width: 1440, height: 1024},
              {name: 'mobile', width: 390, height: 844},
            ],
          },
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

test('rejects SVG paint outside the approved visual system', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({
      svg: '<svg width="100" height="50" viewBox="0 0 100 50"><rect width="100" height="50" fill="#123456"/></svg>',
    }),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('approved palette')));
});

test('rejects the retired cobalt paint from a current-system illustration', async () => {
  const svg =
    '<svg width="100" height="50" viewBox="0 0 100 50"><rect width="100" height="50" fill="#3657d6"/></svg>';
  const result = await auditAssetRegistry({
    repoRoot: await fixture({svg}),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('approved palette')));
});

test('rejects an unknown illustration system', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({visualSystem: 'invented-v1'}),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('unknown illustration system')));
});

test('rejects dimension drift', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({width: 99}),
    today: '2026-08-24',
  });
  assert(result.failures.some((failure) => failure.includes('dimensions do not match')));
});

test('rejects asset bytes that drift from the approved visual review', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({visualReviewHash: '0'.repeat(64)}),
    today: '2026-08-24',
  });
  assert(
    result.failures.some((failure) =>
      failure.includes('does not match its approved visual review'),
    ),
  );
});

test('requires the complete visual acceptance checklist', async () => {
  const result = await auditAssetRegistry({
    repoRoot: await fixture({
      visualReviewChecks: [
        'text_containment',
        'rendered_legibility',
        'narrow_viewport',
        'print_contrast',
      ],
    }),
    today: '2026-08-24',
  });
  assert(
    result.failures.some((failure) =>
      failure.includes('missing required check connector_continuity'),
    ),
  );
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
