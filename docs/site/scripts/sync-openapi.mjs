import {mkdir, readFile, rename, writeFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const source = resolve(siteRoot, '../../server/openapi.json');
const destination = resolve(siteRoot, 'static/openapi/openapi.json');
const temporary = `${destination}.tmp`;

const input = await readFile(source, 'utf8');
const document = JSON.parse(input);

if (
  typeof document !== 'object' ||
  document === null ||
  typeof document.openapi !== 'string' ||
  typeof document.paths !== 'object' ||
  document.paths === null
) {
  throw new Error('server/openapi.json is not a usable OpenAPI document');
}

await mkdir(dirname(destination), {recursive: true});
await writeFile(temporary, input, 'utf8');
await rename(temporary, destination);

console.log(`Published OpenAPI ${document.openapi} from server/openapi.json`);
