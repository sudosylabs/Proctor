import assert from 'node:assert/strict';
import {execFile} from 'node:child_process';
import {readFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import test from 'node:test';
import {promisify} from 'node:util';
import {fileURLToPath} from 'node:url';

const execFileAsync = promisify(execFile);
const siteRoot = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(siteRoot, '../..');
const referencePath = resolve(siteRoot, '../public/developers/make-commands.mdx');

test('the public Make reference covers every supported root target', async () => {
  const [{stdout}, source] = await Promise.all([
    execFileAsync('make', ['help'], {cwd: repositoryRoot}),
    readFile(referencePath, 'utf8'),
  ]);

  const supported = new Set(
    [...stdout.matchAll(/^  ([a-z][a-z0-9_.-]*)\s+/gm)]
      .map((match) => match[1]),
  );
  const documented = new Set(
    [...source.matchAll(/`make ([a-z][a-z0-9_.-]*)(?:`|\s)/g)]
      .map((match) => match[1]),
  );

  assert.deepEqual(
    [...documented].sort(),
    [...supported].sort(),
    'update the human-written Make command reference when the supported root command surface changes',
  );
});
