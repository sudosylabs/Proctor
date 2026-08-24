import {readdir, readFile, writeFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {insertApiContractPanel} from './openapi-reference-generation.mjs';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const referenceRoot = resolve(siteRoot, '../api/reference');
const names = (await readdir(referenceRoot))
  .filter((name) => name.endsWith('.api.mdx'))
  .sort();

for (const name of names) {
  const path = resolve(referenceRoot, name);
  const source = await readFile(path, 'utf8');
  await writeFile(path, insertApiContractPanel(source, name));
}

console.log(`Added Proctor contract panels to ${names.length} endpoint pages`);
