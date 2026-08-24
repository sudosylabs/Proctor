import {readdir, readFile} from 'node:fs/promises';
import {dirname, extname, relative, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const contentRoots = [
  {label: 'public', path: resolve(siteRoot, '../public')},
  {label: 'api', path: resolve(siteRoot, '../api')},
];
const generatedReferenceRoot = resolve(siteRoot, '../api/reference');
const requiredFields = ['title', 'description', 'audience', 'maturity'];
const knownAudiences = new Set([
  'everyone',
  'operator',
  'institution-administrator',
  'security-reviewer',
  'developer',
  'api-consumer',
]);
const knownMaturity = new Set(['available', 'preview', 'planned']);
const failures = [];

async function documentationFiles(directory) {
  const entries = await readdir(directory, {withFileTypes: true});
  const files = [];

  for (const entry of entries.sort((left, right) => left.name.localeCompare(right.name))) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      if (path === generatedReferenceRoot) {
        continue;
      }
      files.push(...(await documentationFiles(path)));
    } else if (['.md', '.mdx'].includes(extname(entry.name).toLowerCase())) {
      files.push(path);
    }
  }
  return files;
}

function parseFrontmatter(source, name) {
  const match = source.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
  if (!match) {
    failures.push(`${name}: missing YAML frontmatter`);
    return {};
  }

  const fields = {};
  for (const line of match[1].split(/\r?\n/)) {
    const field = line.match(/^([a-z_]+):\s*(.*?)\s*$/);
    if (field) {
      fields[field[1]] = field[2].replace(/^(['"])(.*)\1$/, '$2');
    }
  }
  return fields;
}

for (const contentRoot of contentRoots) {
  for (const file of await documentationFiles(contentRoot.path)) {
    const name = `${contentRoot.label}/${relative(contentRoot.path, file)}`;
    const source = await readFile(file, 'utf8');
    const frontmatter = parseFrontmatter(source, name);

    for (const field of requiredFields) {
      if (!frontmatter[field]) {
        failures.push(`${name}: frontmatter field ${field} is required`);
      }
    }
    if (frontmatter.audience && !knownAudiences.has(frontmatter.audience)) {
      failures.push(`${name}: unknown audience ${frontmatter.audience}`);
    }
    if (frontmatter.maturity && !knownMaturity.has(frontmatter.maturity)) {
      failures.push(`${name}: unknown maturity ${frontmatter.maturity}`);
    }
    if (/!\[[^\]]*\]\(/.test(source)) {
      failures.push(`${name}: Markdown images are forbidden; use GovernedFigure with a registered asset ID`);
    }
    if (/<img\b/i.test(source) || /^\s*import\s+.*\.(?:avif|gif|heic|heif|icns|jpe?g|jxl|png|svg|webp)['"];?\s*$/im.test(source)) {
      failures.push(`${name}: direct image elements and imports are forbidden; use GovernedFigure with a registered asset ID`);
    }
  }
}

if (failures.length > 0) {
  console.error(failures.join('\n'));
  process.exitCode = 1;
} else {
  console.log('Public and API documentation metadata is valid');
}
