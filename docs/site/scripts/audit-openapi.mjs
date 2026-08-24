import {readFile} from 'node:fs/promises';
import {dirname, relative, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

import {auditOpenAPI} from './openapi-audit.mjs';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const contractPath = resolve(siteRoot, '../../server/openapi.json');
const document = JSON.parse(await readFile(contractPath, 'utf8'));
const report = auditOpenAPI(document);

console.log(
  JSON.stringify(
    {
      source: relative(siteRoot, contractPath),
      ...report,
    },
    null,
    2,
  ),
);

if (!report.ok) {
  process.exitCode = 1;
}
