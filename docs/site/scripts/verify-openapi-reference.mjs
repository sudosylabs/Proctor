import {readdir, readFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {verifyOpenapiReference} from './openapi-reference.mjs';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repositoryRoot = resolve(siteRoot, '../..');
const referenceRoot = resolve(repositoryRoot, 'docs/api/reference');

const specification = JSON.parse(
  await readFile(resolve(repositoryRoot, 'server/openapi.json'), 'utf8'),
);
const names = await readdir(referenceRoot);

async function sourceFiles(suffix) {
  return Promise.all(
    names
      .filter((name) => name.endsWith(suffix))
      .sort()
      .map(async (name) => ({
        name,
        source: await readFile(resolve(referenceRoot, name), 'utf8'),
      })),
  );
}

const report = verifyOpenapiReference({
  specification,
  pages: await sourceFiles('.api.mdx'),
  tagPages: await sourceFiles('.tag.mdx'),
  sidebar: await readFile(resolve(referenceRoot, 'sidebar.ts'), 'utf8'),
});

console.log(
  `OpenAPI reference matches ${report.operationCount} operations across ` +
    `${report.tagCount} product areas`,
);
