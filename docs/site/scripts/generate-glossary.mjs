import {mkdir, readFile, writeFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

import {
  parseGlossary,
  renderGlossaryData,
  renderGlossaryPage,
} from '../glossary/index.mjs';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = resolve(siteRoot, '../..');
const glossarySkill = '.agents/skills/glossary/SKILL.md';
const terms = parseGlossary(
  await readFile(resolve(repoRoot, glossarySkill), 'utf8'),
);
const outputs = new Map([
  [resolve(siteRoot, 'src/generated/glossary.ts'), renderGlossaryData(terms)],
  [resolve(repoRoot, 'docs/public/reference/glossary.mdx'), renderGlossaryPage(terms.length)],
]);

for (const [path, source] of outputs) {
  await mkdir(dirname(path), {recursive: true});
  await writeFile(path, source);
}
console.log(`Generated ${terms.length} public glossary definitions from ${glossarySkill}`);
