// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import assert from 'node:assert/strict';
import {mkdir, mkdtemp, readFile, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';

import {checkBrandCopies, copyBrandAssets} from './brand-assets.mjs';

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'proctor-brand-copies-'));
  t.after(() => rm(root, {recursive: true, force: true}));
  await mkdir(join(root, 'brand'));
  await mkdir(join(root, 'consumer'));
  await writeFile(join(root, 'brand', 'mark.svg'), 'approved artwork');
  return root;
}

test('copying and checking requires no package import of the masters', async (t) => {
  const root = await fixture(t);
  const copies = [['mark.svg', 'consumer/local.svg']];
  assert.equal((await checkBrandCopies(root, copies)).length, 1);
  await copyBrandAssets(root, copies);
  assert.deepEqual(await checkBrandCopies(root, copies), []);
  await writeFile(join(root, 'consumer', 'local.svg'), 'drifted artwork');
  assert.match((await checkBrandCopies(root, copies))[0], /differs from approved artwork/);
  await copyBrandAssets(root, copies);
  await rm(join(root, 'brand'), {recursive: true});
  assert.equal(await readFile(join(root, 'consumer', 'local.svg'), 'utf8'), 'approved artwork');
});

test('immutable mail artwork blocks every copy before overwriting a queued message asset', async (t) => {
  const root = await fixture(t);
  await writeFile(join(root, 'consumer', 'ui.svg'), 'old UI');
  await writeFile(join(root, 'consumer', 'mail-v1.svg'), 'frozen mail artwork');
  const copies = [['mark.svg', 'consumer/ui.svg'], ['mark.svg', 'consumer/mail-v1.svg', true]];
  await assert.rejects(copyBrandAssets(root, copies), /is immutable/);
  assert.equal(await readFile(join(root, 'consumer', 'ui.svg'), 'utf8'), 'old UI');
  assert.equal(await readFile(join(root, 'consumer', 'mail-v1.svg'), 'utf8'), 'frozen mail artwork');
});

test('missing masters cannot partially overwrite package copies', async (t) => {
  const root = await fixture(t);
  await writeFile(join(root, 'consumer', 'local.svg'), 'existing artwork');
  const copies = [['mark.svg', 'consumer/local.svg'], ['missing.svg', 'consumer/second.svg']];
  await assert.rejects(copyBrandAssets(root, copies), /ENOENT/);
  assert.equal(await readFile(join(root, 'consumer', 'local.svg'), 'utf8'), 'existing artwork');
});
