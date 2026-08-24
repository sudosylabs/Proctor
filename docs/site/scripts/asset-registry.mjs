import {lstat, readFile, readdir} from 'node:fs/promises';
import {dirname, extname, isAbsolute, relative, resolve, sep} from 'node:path';
import {fileURLToPath} from 'node:url';

const scriptRoot = dirname(fileURLToPath(import.meta.url));
const defaultRepoRoot = resolve(scriptRoot, '../../..');
const requiredEntryFields = [
  'id',
  'path',
  'public_path',
  'kind',
  'owner',
  'license',
  'provenance',
  'privacy_review',
  'alt',
  'caption',
  'width',
  'height',
  'max_bytes',
  'theme',
  'last_reviewed',
  'review_triggers',
];
const allowedExtensions = new Set(['.png', '.svg']);
const allowedKinds = new Set(['diagram', 'illustration', 'screenshot']);
const maximumBytesByExtension = new Map([
  ['.png', 2_000_000],
  ['.svg', 250_000],
]);
const documentationExtensions = new Set(['.md', '.mdx']);

function isInside(parent, child) {
  const path = relative(parent, child);
  return (
    path === '' ||
    (!path.startsWith(`..${sep}`) && path !== '..' && !isAbsolute(path))
  );
}

async function walk(directory, {extensions, skip = new Set()} = {}) {
  let entries;
  try {
    entries = await readdir(directory, {withFileTypes: true});
  } catch (error) {
    if (error.code === 'ENOENT') {
      return [];
    }
    throw error;
  }

  const files = [];
  for (const entry of entries.sort((left, right) =>
    left.name.localeCompare(right.name),
  )) {
    const path = resolve(directory, entry.name);
    if (skip.has(path)) {
      continue;
    }
    if (entry.isDirectory()) {
      files.push(...(await walk(path, {extensions, skip})));
    } else if (!extensions || extensions.has(extname(entry.name).toLowerCase())) {
      files.push(path);
    }
  }
  return files;
}

function parseSVGDimensions(source, name, failures) {
  if (/<!DOCTYPE|<!ENTITY/i.test(source)) {
    failures.push(`${name}: SVG doctypes and entities are forbidden`);
  }
  const forbiddenElement =
    /<(?:a|animate|animateMotion|animateTransform|feImage|foreignObject|image|script|set|style)\b/i;
  if (forbiddenElement.test(source)) {
    failures.push(`${name}: SVG contains a forbidden active or linking element`);
  }
  if (/\son[a-z]+\s*=|\bxml:base\s*=/i.test(source)) {
    failures.push(`${name}: SVG event-handler and xml:base attributes are forbidden`);
  }

  for (const match of source.matchAll(
    /(?:href|xlink:href)\s*=\s*["']([^"']+)["']/gi,
  )) {
    if (!match[1].startsWith('#')) {
      failures.push(`${name}: SVG references an external resource through ${match[1]}`);
    }
  }
  for (const match of source.matchAll(/url\(\s*["']?([^)'"\s]+)["']?\s*\)/gi)) {
    if (!match[1].startsWith('#')) {
      failures.push(`${name}: SVG references an external resource through ${match[1]}`);
    }
  }

  const root = source.match(/<svg\b([^>]*)>/i)?.[1] ?? '';
  const width = root.match(
    /\bwidth\s*=\s*["']([0-9]+(?:\.[0-9]+)?)["']/i,
  )?.[1];
  const height = root.match(
    /\bheight\s*=\s*["']([0-9]+(?:\.[0-9]+)?)["']/i,
  )?.[1];
  const viewBox = root.match(/\bviewBox\s*=\s*["']([^"']+)["']/i)?.[1];
  if (!width || !height || !viewBox) {
    failures.push(`${name}: SVG requires numeric width, height, and viewBox attributes`);
    return null;
  }

  const viewBoxParts = viewBox.trim().split(/[\s,]+/).map(Number);
  if (
    viewBoxParts.length !== 4 ||
    viewBoxParts.some((part) => !Number.isFinite(part)) ||
    viewBoxParts[0] !== 0 ||
    viewBoxParts[1] !== 0 ||
    viewBoxParts[2] !== Number(width) ||
    viewBoxParts[3] !== Number(height)
  ) {
    failures.push(`${name}: SVG viewBox must be 0 0 width height`);
  }
  return {width: Number(width), height: Number(height)};
}

function parsePNGDimensions(buffer, name, failures) {
  const signature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  if (buffer.length < 24 || !buffer.subarray(0, 8).equals(signature)) {
    failures.push(`${name}: invalid PNG signature or truncated IHDR`);
    return null;
  }
  if (buffer.toString('ascii', 12, 16) !== 'IHDR') {
    failures.push(`${name}: PNG does not begin with an IHDR chunk`);
    return null;
  }
  return {width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20)};
}

function nonemptyString(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function isCalendarDate(value) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value ?? '')) {
    return false;
  }
  const [year, month, day] = value.split('-').map(Number);
  const date = new Date(Date.UTC(year, month - 1, day));
  return (
    date.getUTCFullYear() === year &&
    date.getUTCMonth() === month - 1 &&
    date.getUTCDate() === day
  );
}

function validateEntryMetadata(entry, index, failures, today) {
  const label = nonemptyString(entry?.id)
    ? `asset ${entry.id}`
    : `asset at index ${index}`;
  if (!entry || typeof entry !== 'object' || Array.isArray(entry)) {
    failures.push(`${label}: registry entry must be an object`);
    return;
  }
  for (const field of requiredEntryFields) {
    if (!(field in entry)) {
      failures.push(`${label}: ${field} is required`);
    }
  }
  if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(entry.id ?? '')) {
    failures.push(`${label}: id must be lowercase kebab-case`);
  }
  if (!allowedKinds.has(entry.kind)) {
    failures.push(`${label}: kind must be diagram, illustration, or screenshot`);
  }
  for (const field of ['owner', 'provenance', 'alt', 'caption', 'theme']) {
    if (!nonemptyString(entry[field])) {
      failures.push(`${label}: ${field} must be a nonempty string`);
    }
  }
  for (const field of ['width', 'height', 'max_bytes']) {
    if (!Number.isSafeInteger(entry[field]) || entry[field] <= 0) {
      failures.push(`${label}: ${field} must be a positive integer`);
    }
  }

  if (!entry.license || !['pending', 'resolved'].includes(entry.license.status)) {
    failures.push(`${label}: license status must be pending or resolved`);
  } else if (
    entry.license.status === 'resolved' &&
    !nonemptyString(entry.license.expression)
  ) {
    failures.push(`${label}: resolved license requires an expression`);
  } else if (entry.license.status === 'pending' && !nonemptyString(entry.license.note)) {
    failures.push(`${label}: pending license requires an explanatory note`);
  }

  if (
    entry.privacy_review?.status !== 'approved' ||
    !nonemptyString(entry.privacy_review?.basis)
  ) {
    failures.push(`${label}: privacy review must be approved with a nonempty basis`);
  }

  if (!isCalendarDate(entry.last_reviewed)) {
    failures.push(`${label}: last_reviewed must be a real date using YYYY-MM-DD`);
  } else if (entry.last_reviewed > today) {
    failures.push(`${label}: last_reviewed cannot be in the future`);
  }
  if (
    !Array.isArray(entry.review_triggers) ||
    entry.review_triggers.length === 0 ||
    entry.review_triggers.some((trigger) => !nonemptyString(trigger))
  ) {
    failures.push(
      `${label}: review_triggers must contain at least one repository-relative path`,
    );
  }
}

export async function auditAssetRegistry({
  repoRoot = defaultRepoRoot,
  today = new Date().toISOString().slice(0, 10),
} = {}) {
  const publicRoot = resolve(repoRoot, 'docs/public');
  const apiRoot = resolve(repoRoot, 'docs/api');
  const publicStaticRoot = resolve(publicRoot, 'static');
  const assetRoot = resolve(publicStaticRoot, 'assets');
  const registryPath = resolve(publicRoot, 'assets.json');
  const generatedReferenceRoot = resolve(apiRoot, 'reference');
  const failures = [];

  let registry;
  try {
    registry = JSON.parse(await readFile(registryPath, 'utf8'));
  } catch (error) {
    return {
      failures: [`docs/public/assets.json: ${error.message}`],
      counts: {assets: 0, files: 0, references: 0},
    };
  }

  if (registry.schema_version !== 1) {
    failures.push('docs/public/assets.json: schema_version must be 1');
  }
  if (!Array.isArray(registry.assets)) {
    failures.push('docs/public/assets.json: assets must be an array');
    return {failures, counts: {assets: 0, files: 0, references: 0}};
  }

  const ids = new Set();
  const registeredPaths = new Map();
  for (const [index, entry] of registry.assets.entries()) {
    validateEntryMetadata(entry, index, failures, today);
    const label = nonemptyString(entry?.id)
      ? `asset ${entry.id}`
      : `asset at index ${index}`;
    if (ids.has(entry?.id)) {
      failures.push(`${label}: duplicate id`);
    }
    ids.add(entry?.id);

    if (
      !nonemptyString(entry?.path) ||
      entry.path.includes('\\') ||
      isAbsolute(entry.path)
    ) {
      failures.push(`${label}: path must be a portable repository-relative path`);
      continue;
    }
    const filePath = resolve(publicRoot, entry.path);
    if (!entry.path.startsWith('static/assets/') || !isInside(assetRoot, filePath)) {
      failures.push(`${label}: path must remain under docs/public/static/assets`);
      continue;
    }
    const extension = extname(filePath).toLowerCase();
    if (!allowedExtensions.has(extension)) {
      failures.push(`${label}: only SVG and PNG assets are allowed`);
    }
    if (entry.kind === 'screenshot' && extension !== '.png') {
      failures.push(`${label}: screenshots must use PNG`);
    }
    const absoluteMaximum = maximumBytesByExtension.get(extension);
    if (absoluteMaximum && entry.max_bytes > absoluteMaximum) {
      failures.push(
        `${label}: max_bytes exceeds the ${absoluteMaximum}-byte ${extension} ceiling`,
      );
    }
    const expectedPublicPath = `/${entry.path.slice('static/'.length)}`;
    if (entry.public_path !== expectedPublicPath) {
      failures.push(`${label}: public_path must be ${expectedPublicPath}`);
    }
    if (registeredPaths.has(filePath)) {
      failures.push(`${label}: path is already owned by ${registeredPaths.get(filePath)}`);
    }
    registeredPaths.set(filePath, entry.id);

    let fileInfo;
    let data;
    try {
      fileInfo = await lstat(filePath);
      data = await readFile(filePath);
    } catch (error) {
      failures.push(`${label}: registered file cannot be read (${error.message})`);
      continue;
    }
    if (!fileInfo.isFile() || fileInfo.isSymbolicLink()) {
      failures.push(`${label}: registered asset must be a regular file, not a symlink`);
      continue;
    }
    if (data.byteLength > entry.max_bytes) {
      failures.push(`${label}: ${data.byteLength} bytes exceeds max_bytes ${entry.max_bytes}`);
    }
    const dimensions = extension === '.svg'
      ? parseSVGDimensions(data.toString('utf8'), label, failures)
      : parsePNGDimensions(data, label, failures);
    if (
      dimensions &&
      (dimensions.width !== entry.width || dimensions.height !== entry.height)
    ) {
      failures.push(
        `${label}: registered ${entry.width}x${entry.height} dimensions do not match file ${dimensions.width}x${dimensions.height}`,
      );
    }

    for (const trigger of Array.isArray(entry.review_triggers) ? entry.review_triggers : []) {
      if (isAbsolute(trigger) || trigger.includes('\\')) {
        failures.push(`${label}: review trigger ${trigger} must be repository-relative`);
        continue;
      }
      const triggerPath = resolve(repoRoot, trigger);
      if (!isInside(repoRoot, triggerPath)) {
        failures.push(`${label}: review trigger ${trigger} escapes the repository`);
        continue;
      }
      try {
        const triggerInfo = await lstat(triggerPath);
        if (!triggerInfo.isFile() || triggerInfo.isSymbolicLink()) {
          failures.push(`${label}: review trigger ${trigger} must be a regular file`);
        }
      } catch {
        failures.push(`${label}: review trigger ${trigger} does not exist`);
      }
    }
  }

  const publicStaticFiles = await walk(publicStaticRoot);
  const assetFiles = publicStaticFiles.filter((file) => isInside(assetRoot, file));
  for (const file of publicStaticFiles) {
    if (!isInside(assetRoot, file)) {
      failures.push(`ungoverned public static file: ${relative(repoRoot, file)}`);
    }
  }
  for (const file of assetFiles) {
    if (!registeredPaths.has(file)) {
      failures.push(`unregistered asset file: ${relative(repoRoot, file)}`);
    }
  }

  const references = new Map();
  const contentFiles = [
    ...(await walk(publicRoot, {extensions: documentationExtensions})),
    ...(await walk(apiRoot, {
      extensions: documentationExtensions,
      skip: new Set([generatedReferenceRoot]),
    })),
  ];
  let referenceCount = 0;
  for (const file of contentFiles) {
    const source = await readFile(file, 'utf8');
    const name = relative(repoRoot, file);
    for (const match of source.matchAll(
      /<GovernedFigure\b[^>]*\basset\s*=\s*["']([^"']+)["'][^>]*\/?\s*>/g,
    )) {
      referenceCount += 1;
      const id = match[1];
      references.set(id, (references.get(id) ?? 0) + 1);
      if (!ids.has(id)) {
        failures.push(`${name}: references unknown governed asset ${id}`);
      }
    }
    if (/\/assets\/[a-z0-9_./-]+\.(?:png|svg)/i.test(source)) {
      failures.push(
        `${name}: direct asset paths are forbidden; use GovernedFigure with a registry ID`,
      );
    }
  }
  for (const id of ids) {
    if (nonemptyString(id) && !references.has(id)) {
      failures.push(`asset ${id}: registered asset is not referenced by authored documentation`);
    }
  }

  return {
    failures,
    counts: {
      assets: registry.assets.length,
      files: assetFiles.length,
      references: referenceCount,
    },
  };
}
