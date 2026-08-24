import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

const componentUrl = new URL('../src/components/DocIcon/index.tsx', import.meta.url);
const stylesUrl = new URL('../src/components/DocIcon/styles.module.css', import.meta.url);

const expectedNames = [
  'deployment',
  'institution',
  'assurance',
  'integration',
  'api',
  'glossary',
  'architecture',
  'search',
  'close',
  'arrowRight',
  'external',
  'check',
];

test('the owned icon vocabulary is explicit and bounded', async () => {
  const source = await readFile(componentUrl, 'utf8');
  const registry = source.match(/export const DOC_ICON_NAMES = \[([\s\S]*?)\] as const;/);
  assert(registry, 'could not find the icon-name registry');
  const names = [...registry[1].matchAll(/'([^']+)'/g)].map((match) => match[1]);
  assert.deepEqual(names, expectedNames);
  assert.equal(new Set(names).size, names.length);
  assert.match(source, /export type DocIconSize = 24 \| 32 \| 48;/);
});

test('icons share one geometry and accessibility contract', async () => {
  const [source, styles] = await Promise.all([
    readFile(componentUrl, 'utf8'),
    readFile(stylesUrl, 'utf8'),
  ]);
  assert.match(source, /viewBox="0 0 24 24"/);
  assert.match(source, /strokeWidth="2"/);
  assert.match(source, /strokeLinecap="round"/);
  assert.match(source, /strokeLinejoin="round"/);
  assert.match(source, /aria-hidden=\{accessible \? undefined : true\}/);
  assert.match(source, /aria-label=\{accessible \? title : undefined\}/);
  assert.match(source, /focusable="false"/);
  assert.match(styles, /vector-effect: non-scaling-stroke/);
});
