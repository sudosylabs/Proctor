import assert from 'node:assert/strict';
import {readFile, readdir} from 'node:fs/promises';
import {dirname, relative, resolve} from 'node:path';
import test from 'node:test';
import {fileURLToPath} from 'node:url';
import ts from 'typescript';

import {audienceLabels, guides, referenceLinks} from './navigation.mjs';

const siteRoot = dirname(fileURLToPath(import.meta.url));
const publicRoot = resolve(siteRoot, '../public');
// The sidebar is a human-owned TypeScript object. Erase its type-only import
// with our pinned compiler so these tests use the same source as Docusaurus.
const {outputText} = ts.transpileModule(
  await readFile(resolve(siteRoot, 'sidebars.ts'), 'utf8'),
  {compilerOptions: {module: ts.ModuleKind.ESNext}},
);
const {default: sidebars} = await import(
  `data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`
);

function documentIDs(items) {
  return items.flatMap((item) => {
    if (typeof item === 'string') return [item];
    if (item.type === 'doc') return [item.id];
    if (item.type !== 'category') return [];
    return [
      ...(item.link?.type === 'doc' ? [item.link.id] : []),
      ...documentIDs(item.items),
    ];
  });
}

async function authoredPages(directory = publicRoot) {
  const pages = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const file = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== 'static') pages.push(...await authoredPages(file));
    } else if (/\.mdx?$/.test(entry.name)) {
      const source = await readFile(file, 'utf8');
      const frontmatter = source.match(/^---\n([\s\S]*?)\n---/)?.[1] ?? '';
      pages.push({
        id: relative(publicRoot, file).replace(/\.mdx?$/, ''),
        slug: frontmatter.match(/^slug: (.+)$/m)?.[1]?.replace(/\/$/, '') || '/',
        audience: frontmatter.match(/^audience: (.+)$/m)?.[1],
      });
    }
  }
  return pages;
}

test('every public page belongs to exactly one contextual sidebar', async () => {
  const ids = Object.values(sidebars).flatMap(documentIDs);
  assert.equal(new Set(ids).size, ids.length, 'a page must not have two sidebar owners');
  assert.deepEqual(ids.sort(), (await authoredPages()).map((page) => page.id).sort());
});

test('the homepage opens global navigation, not an empty mobile sidebar', async () => {
  const source = await readFile(resolve(publicRoot, 'index.mdx'), 'utf8');
  assert.match(source, /^displayed_sidebar: null$/m);
});

test('each guide opens its own visible, non-collapsing section', () => {
  assert.equal(guides.length, 7);
  assert.equal(new Set(guides.map((guide) => guide.sidebarId)).size, guides.length);
  for (const guide of guides) {
    const prefix = guide.to.slice(1);
    const [section] = sidebars[guide.sidebarId];
    assert.equal(section.link.id, `${prefix}index`);
    assert.equal(section.collapsed, false);
    assert.equal(section.collapsible, false);
    assert(documentIDs([section]).every((id) => id.startsWith(prefix)), guide.label);
  }
});

test('guide and reference destinations resolve to authored pages', async () => {
  const routes = new Set((await authoredPages()).map((page) => page.slug));
  for (const destination of [...guides, ...referenceLinks]) {
    assert(routes.has(destination.to.replace(/\/$/, '')), destination.to);
  }
});

test('every public audience has a readable metadata and search label', async () => {
  for (const {id, audience} of await authoredPages()) {
    assert(Object.hasOwn(audienceLabels, audience), `${id}: ${audience}`);
  }
});
