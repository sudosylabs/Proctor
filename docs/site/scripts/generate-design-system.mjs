import {writeFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

import {renderDesignTokenCSS} from '../design-system/index.mjs';

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const output = resolve(scriptRoot, '../src/css/tokens.css');

await writeFile(output, renderDesignTokenCSS());
console.log('Generated docs/site/src/css/tokens.css');
