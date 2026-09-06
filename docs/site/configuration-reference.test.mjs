import assert from 'node:assert/strict';
import {readFile, readdir} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import test from 'node:test';
import {fileURLToPath} from 'node:url';

const siteRoot = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(siteRoot, '../..');
const referenceRoot = resolve(siteRoot, '../public/reference/configuration');

async function configurationStructs() {
  const sources = await Promise.all([
    'server/config/config.go',
    'server/config/external_authentication.go',
  ].map((file) => readFile(resolve(repositoryRoot, file), 'utf8')));

  const structs = new Map();
  for (const source of sources) {
    for (const match of source.matchAll(/^type (\w+) struct \{\n([\s\S]*?)^\}/gm)) {
      const fields = [...match[2].matchAll(
        /^\s*(\w+)\s+([^\s`]+)\s+`json:"([^",]+)[^"]*"`/gm,
      )].map((field) => ({
        jsonName: field[3],
        type: field[2],
      }));
      structs.set(match[1], fields);
    }
  }
  return structs;
}

function leafPaths(structs, typeName, prefix = '') {
  const fields = structs.get(typeName);
  if (!fields?.length) return [prefix];

  return fields.flatMap((field) => {
    const isSlice = field.type.startsWith('[]');
    const childType = field.type.replace(/^\[\]/, '').replace(/^\*/, '');
    const path = `${prefix}${prefix ? '.' : ''}${field.jsonName}${isSlice ? '[]' : ''}`;
    return structs.get(childType)?.length
      ? leafPaths(structs, childType, path)
      : [path];
  });
}

async function documentedPaths() {
  const paths = [];
  for (const entry of await readdir(referenceRoot, {withFileTypes: true})) {
    if (!entry.isFile() || !entry.name.endsWith('.mdx')) continue;
    const source = await readFile(resolve(referenceRoot, entry.name), 'utf8');
    for (const match of source.matchAll(/^\| `([^`]+)`(?:<br \/>)?/gm)) {
      paths.push({path: match[1], file: entry.name});
    }
  }
  return paths;
}

test('the human configuration reference covers every JSON leaf exactly once', async () => {
  const structs = await configurationStructs();
  const expected = leafPaths(structs, 'Config').sort();
  const documented = await documentedPaths();
  const counts = new Map();
  for (const item of documented) {
    counts.set(item.path, [...(counts.get(item.path) ?? []), item.file]);
  }

  const duplicates = [...counts]
    .filter(([, files]) => files.length > 1)
    .map(([path, files]) => `${path}: ${files.join(', ')}`);
  assert.deepEqual(duplicates, [], `duplicate configuration paths:\n${duplicates.join('\n')}`);
  assert.deepEqual(
    [...counts.keys()].sort(),
    expected,
    'update the human-written configuration reference when the Go configuration schema changes',
  );
});
