import {readFile, readdir} from 'node:fs/promises';
import {dirname, extname, relative, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {audienceLabels} from '../navigation.mjs';

const moduleRoot = dirname(fileURLToPath(import.meta.url));
const defaultRepoRoot = resolve(moduleRoot, '../../..');
const methods = new Set(['get', 'post', 'put', 'patch', 'delete', 'options', 'head']);

function compact(value) {
  return value.replace(/\s+/g, ' ').trim();
}

function kebab(value) {
  return value
    .replace(/([a-z0-9])([A-Z])/g, '$1-$2')
    .replace(/[^a-zA-Z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
    .toLowerCase();
}

function parseFrontmatter(source) {
  const match = source.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
  if (!match) {
    return {fields: {}, body: source};
  }
  const fields = {};
  for (const line of match[1].split(/\r?\n/)) {
    const field = line.match(/^([a-z_]+):\s*(.*?)\s*$/);
    if (field) {
      fields[field[1]] = field[2].replace(/^(['"])(.*)\1$/, '$2');
    }
  }
  return {fields, body: source.slice(match[0].length)};
}

export function searchableText(body) {
  return compact(
    body
      .replace(/^\s*import\s+.*$/gm, ' ')
      .replace(/```[\s\S]*?```/g, ' ')
      .replace(/\{\/\*[\s\S]*?\*\/\}/g, ' ')
      .replace(/<[^>]+>/g, ' ')
      .replace(/!?(\[([^\]]+)\])\([^)]*\)/g, '$2')
      .replace(/[`*_>#|{}]/g, ' '),
  );
}

function routeFor(file, root, routeBase, slug) {
  if (slug) {
    if (slug === '/') {
      return `${routeBase}/` || '/';
    }
    const cleanSlug = slug.replace(/^\/+|\/+$/g, '');
    return `${routeBase}/${cleanSlug}${slug.endsWith('/') ? '/' : ''}`;
  }
  const path = relative(root, file).replace(/\\/g, '/').replace(/\.(?:md|mdx)$/i, '');
  const clean = path.endsWith('/index') ? path.slice(0, -'/index'.length) : path;
  return `${routeBase}/${clean}`.replace(/\/{2,}/g, '/');
}

async function walk(directory, skip) {
  const files = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      if (path !== skip) {
        files.push(...(await walk(path, skip)));
      }
    } else if (entry.isFile() && ['.md', '.mdx'].includes(extname(entry.name))) {
      files.push(path);
    }
  }
  return files.sort();
}

async function authoredEntries(repoRoot) {
  const sources = [
    {root: resolve(repoRoot, 'docs/public'), routeBase: ''},
    {root: resolve(repoRoot, 'docs/api'), routeBase: '/api'},
  ];
  const generatedReference = resolve(repoRoot, 'docs/api/reference');
  const entries = [];
  for (const source of sources) {
    for (const file of await walk(source.root, generatedReference)) {
      const raw = await readFile(file, 'utf8');
      const {fields, body} = parseFrontmatter(raw);
      if (!fields.title || !fields.description) {
        continue;
      }
      const href = routeFor(file, source.root, source.routeBase, fields.slug);
      const text = searchableText(body);
      entries.push({
        id: `page:${href}`,
        kind: file.endsWith('glossary.mdx') ? 'glossary' : 'guide',
        group: audienceLabels[fields.audience] ?? 'Documentation',
        title: fields.title,
        description: fields.description,
        href,
        searchText: compact(`${fields.title} ${fields.description} ${text}`).toLowerCase(),
      });
    }
  }
  return entries;
}

async function openAPIEntries(repoRoot) {
  const specification = JSON.parse(
    await readFile(resolve(repoRoot, 'server/openapi.json'), 'utf8'),
  );
  const entries = [];
  for (const tag of specification.tags ?? []) {
    const slug = kebab(tag.name);
    entries.push({
      id: `area:${slug}`,
      kind: 'product-area',
      group: 'API product area',
      title: tag.name,
      description: compact(tag.description ?? ''),
      href: `/api/reference/${slug}`,
      searchText: compact(`${tag.name} ${tag.description ?? ''}`).toLowerCase(),
    });
  }
  for (const [path, pathItem] of Object.entries(specification.paths ?? {})) {
    for (const [method, operation] of Object.entries(pathItem)) {
      if (!methods.has(method) || !operation?.operationId) {
        continue;
      }
      const slug = kebab(operation.operationId);
      const tag = operation.tags?.[0] ?? 'API reference';
      const description = compact(operation.description ?? operation.summary ?? '');
      const extensions = [
        operation['x-proctor-auth'],
        operation['x-proctor-idempotency'],
        ...(operation['x-proctor-error-codes'] ?? []),
      ].filter(Boolean);
      entries.push({
        id: `operation:${operation.operationId}`,
        kind: 'endpoint',
        group: tag,
        title: operation.summary ?? operation.operationId,
        description,
        href: `/api/reference/${slug}`,
        method: method.toUpperCase(),
        path,
        searchText: compact(
          `${operation.operationId} ${operation.summary ?? ''} ${description} ${tag} ${method} ${path} ${extensions.join(' ')}`,
        ).toLowerCase(),
      });
    }
  }
  return entries;
}

export async function buildSearchIndex({repoRoot = defaultRepoRoot} = {}) {
  const entries = [
    ...(await authoredEntries(repoRoot)),
    ...(await openAPIEntries(repoRoot)),
  ];
  const ids = new Set();
  const hrefs = new Set();
  for (const entry of entries) {
    if (ids.has(entry.id)) {
      throw new Error(`duplicate search entry ${entry.id}`);
    }
    ids.add(entry.id);
    if (hrefs.has(entry.href) && entry.kind !== 'endpoint') {
      throw new Error(`duplicate search route ${entry.href}`);
    }
    hrefs.add(entry.href);
  }
  return entries;
}

export function renderSearchData(entries) {
  return `// Generated from public MDX and server/openapi.json. Do not edit by hand.\n\nexport type SearchEntry = {\n  id: string;\n  kind: 'guide' | 'glossary' | 'product-area' | 'endpoint';\n  group: string;\n  title: string;\n  description: string;\n  href: string;\n  searchText: string;\n  method?: string;\n  path?: string;\n};\n\nexport const searchEntries = ${JSON.stringify(entries, null, 2)} satisfies readonly SearchEntry[];\n`;
}

export async function auditSearchIndex({repoRoot = defaultRepoRoot} = {}) {
  try {
    const entries = await buildSearchIndex({repoRoot});
    const path = resolve(repoRoot, 'docs/site/src/generated/search-index.ts');
    const actual = await readFile(path, 'utf8');
    return actual === renderSearchData(entries)
      ? []
      : ['docs/site/src/generated/search-index.ts is stale; run npm run generate:search'];
  } catch (error) {
    return [`search index: ${error.message}`];
  }
}
