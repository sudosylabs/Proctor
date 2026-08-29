import assert from 'node:assert/strict';
import test from 'node:test';

import {
  auditSearchIndex,
  buildSearchIndex,
  renderSearchData,
  searchableText,
} from './index.mjs';

test('source-only MDX comments never enter reader search data', () => {
  assert.equal(
    searchableText('Visible before. {/* TODO(owner): internal decision. */} Visible after.'),
    'Visible before. Visible after.',
  );
});

test('the tracked search index is current', async () => {
  assert.deepEqual(await auditSearchIndex(), []);
});

test('search covers authored pages, product areas, and every operation', async () => {
  const entries = await buildSearchIndex();
  assert.equal(entries.filter((entry) => entry.kind === 'endpoint').length, 236);
  assert.equal(entries.filter((entry) => entry.kind === 'product-area').length, 16);
  assert(entries.some((entry) => entry.href === '/operator/'));
  assert(entries.some((entry) => entry.href === '/glossary/'));
  assert(entries.some((entry) => entry.href === '/api/guides/authentication'));
  assert(
    entries.some(
      (entry) =>
        entry.id === 'operation:getLiveness' &&
        entry.href === '/api/reference/get-liveness' &&
        entry.method === 'GET',
    ),
  );
});

test('the generated adapter records its human-owned inputs', async () => {
  assert.match(renderSearchData(await buildSearchIndex()), /public MDX and server\/openapi\.json/);
});
