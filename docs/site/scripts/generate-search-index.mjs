import {mkdir, writeFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

import {buildSearchIndex, renderSearchData} from '../search/index.mjs';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const output = resolve(siteRoot, 'src/generated/search-index.ts');
const entries = await buildSearchIndex();
await mkdir(dirname(output), {recursive: true});
await writeFile(output, renderSearchData(entries));
console.log(`Generated local search data for ${entries.length} documentation targets`);
