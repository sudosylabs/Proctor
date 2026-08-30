import {readdir, readFile} from 'node:fs/promises';
import {dirname, extname, relative, resolve} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

const methods = new Set(['delete', 'get', 'head', 'options', 'patch', 'post', 'put', 'trace']);
const routePattern = /\/api\/v1(?:\/[A-Za-z0-9._~{}<>$-]+)*/g;
const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const generatedReferenceRoot = resolve(siteRoot, '../api/reference');
const contentRoots = [
  {label: 'public', path: resolve(siteRoot, '../public')},
  {label: 'api', path: resolve(siteRoot, '../api')},
];

function routeTemplate(path) {
  return path
    .split(/[?#]/, 1)[0]
    .split('/')
    .map((segment) => (/^(?:\{[^}]+\}|<[^>]+>|\$[A-Za-z_][A-Za-z0-9_]*)$/.test(segment) ? '*' : segment))
    .join('/');
}

function sameTemplate(left, right) {
  const leftSegments = routeTemplate(left).split('/');
  const rightSegments = routeTemplate(right).split('/');
  return leftSegments.length === rightSegments.length && leftSegments.every(
    (segment, index) => segment === '*' || rightSegments[index] === '*' || segment === rightSegments[index],
  );
}

function lineNumber(source, offset) {
  return source.slice(0, offset).split('\n').length;
}

function absoluteOriginBefore(source, offset) {
  const prefix = source.slice(Math.max(0, offset - 160), offset);
  const match = prefix.match(/https?:\/\/[^\s'"`()<>]*$/i);
  if (!match) {
    return null;
  }
  try {
    return new URL(match[0]).origin;
  } catch {
    return null;
  }
}

function inlineMethod(source, offset) {
  const lineStart = source.lastIndexOf('\n', offset - 1) + 1;
  const prefix = source.slice(lineStart, offset);
  return prefix.match(/\b(DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT|TRACE)\s+(?:https?:\/\/[^\s]+)?$/i)?.[1]?.toLowerCase() ?? null;
}

function shellCommands(source) {
  const commands = [];
  let command = '';
  let commandOffset = 0;

  for (const line of source.matchAll(/[^\n]*(?:\n|$)/g)) {
    if (!line[0]) {
      continue;
    }
    if (!command) {
      commandOffset = line.index ?? 0;
    }
    command += line[0];
    if (!line[0].replace(/\n$/, '').trimEnd().endsWith('\\')) {
      commands.push({offset: commandOffset, source: command});
      command = '';
    }
  }
  if (command) {
    commands.push({offset: commandOffset, source: command});
  }
  return commands;
}

function curlMethod(command) {
  if (!/(?:^|\s)curl(?:\s|$)/.test(command)) {
    return null;
  }
  const explicit = command.match(
    /(?:--request(?:=|\s+)|-X\s*)(DELETE|GET|HEAD|OPTIONS|PATCH|POST|PUT|TRACE)\b/i,
  )?.[1]?.toLowerCase();
  if (explicit) {
    return explicit;
  }
  if (/(?:^|\s)(?:--head|-I)(?:\s|$)/.test(command)) {
    return 'head';
  }
  if (/(?:^|\s)(?:--get|-G)(?:\s|$)/.test(command)) {
    return 'get';
  }
  if (/(?:^|\s)(?:--data(?:-[a-z-]+)?|-d|--form(?:-string)?|-F)(?:=|\s)/i.test(command)) {
    return 'post';
  }
  return 'get';
}

function curlMethods(source) {
  const found = new Map();
  for (const block of source.matchAll(/```(?:bash|sh|shell)\s*\n([\s\S]*?)```/gi)) {
    const blockOffset = (block.index ?? 0) + block[0].indexOf(block[1]);
    for (const command of shellCommands(block[1])) {
      const method = curlMethod(command.source);
      if (!method) {
        continue;
      }
      for (const reference of command.source.matchAll(routePattern)) {
        const absoluteOffset = blockOffset + command.offset + (reference.index ?? 0);
        const origin = absoluteOriginBefore(source, absoluteOffset);
        if (origin && origin !== 'https://proctor.example.edu') {
          continue;
        }
        found.set(`${reference[0]}:${lineNumber(source, absoluteOffset)}`, method);
      }
    }
  }
  return found;
}

export function auditAuthoredRouteReferences(openapi, sources) {
  const operations = Object.entries(openapi.paths ?? {}).map(([path, pathItem]) => ({
    methods: new Set(Object.keys(pathItem).filter((method) => methods.has(method))),
    path,
  }));
  const errors = [];

  for (const {name, source} of sources) {
    const blockMethods = curlMethods(source);
    for (const reference of source.matchAll(routePattern)) {
      const offset = reference.index ?? 0;
      const origin = absoluteOriginBefore(source, offset);
      if (origin && origin !== 'https://proctor.example.edu') {
        continue;
      }

      const path = reference[0];
      const line = lineNumber(source, offset);
      if (path === '/api/v1') {
        continue;
      }
      const candidates = operations.filter((operation) => sameTemplate(operation.path, path));
      if (candidates.length === 0) {
        errors.push(`${name}:${line}: ${path} does not match a current OpenAPI path`);
        continue;
      }

      const method = inlineMethod(source, offset) ?? blockMethods.get(`${path}:${line}`) ?? null;
      if (method && !candidates.some((candidate) => candidate.methods.has(method))) {
        errors.push(`${name}:${line}: ${method.toUpperCase()} ${path} does not match a current OpenAPI operation`);
      }
    }
  }

  return errors;
}

async function documentationFiles(directory) {
  const entries = await readdir(directory, {withFileTypes: true});
  const files = [];
  for (const entry of entries.sort((left, right) => left.name.localeCompare(right.name))) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      if (path !== generatedReferenceRoot) {
        files.push(...(await documentationFiles(path)));
      }
    } else if (['.md', '.mdx'].includes(extname(entry.name).toLowerCase())) {
      files.push(path);
    }
  }
  return files;
}

async function main() {
  const openapi = JSON.parse(await readFile(resolve(siteRoot, '../../server/openapi.json'), 'utf8'));
  const sources = [];
  for (const root of contentRoots) {
    for (const path of await documentationFiles(root.path)) {
      sources.push({
        name: `${root.label}/${relative(root.path, path)}`,
        source: await readFile(path, 'utf8'),
      });
    }
  }

  const errors = auditAuthoredRouteReferences(openapi, sources);
  if (errors.length > 0) {
    console.error(errors.join('\n'));
    process.exitCode = 1;
    return;
  }
  console.log(`Authored Proctor API references match ${Object.keys(openapi.paths ?? {}).length} OpenAPI paths`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  await main();
}
