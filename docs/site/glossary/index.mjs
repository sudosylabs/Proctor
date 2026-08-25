import {readFile, readdir} from 'node:fs/promises';
import {dirname, extname, relative, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const moduleRoot = dirname(fileURLToPath(import.meta.url));
const defaultRepoRoot = resolve(moduleRoot, '../../..');
const glossarySkill = '.agents/skills/glossary/SKILL.md';

function slugify(term) {
  return term
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '');
}

function compact(lines) {
  return lines
    .map((line) => line.trim())
    .filter(Boolean)
    .join(' ');
}

export function parseGlossary(source) {
  const lines = source.replace(/\r\n/g, '\n').split('\n');
  const terms = [];
  const seen = new Set();
  let section = '';

  for (let index = 0; index < lines.length; index += 1) {
    const sectionMatch = lines[index].match(/^##\s+(.+?)\s*$/);
    if (sectionMatch) {
      section = sectionMatch[1];
      continue;
    }

    const termMatch = lines[index].match(/^\*\*([^*]+)\*\*:\s*$/);
    if (!termMatch) {
      continue;
    }
    if (!section) {
      throw new Error(`${termMatch[1]} appears before a level-two section`);
    }

    const term = termMatch[1].trim();
    const id = slugify(term);
    if (!id) {
      throw new Error(`cannot derive an identifier for ${term}`);
    }
    if (seen.has(id)) {
      throw new Error(`duplicate glossary identifier ${id}`);
    }
    seen.add(id);

    const definitionLines = [];
    let avoid = '';
    let cursor = index + 1;
    for (; cursor < lines.length; cursor += 1) {
      if (/^##\s+/.test(lines[cursor]) || /^\*\*[^*]+\*\*:\s*$/.test(lines[cursor])) {
        break;
      }
      const avoidMatch = lines[cursor].match(/^_Avoid_:\s*(.+?)\s*$/);
      if (avoidMatch) {
        avoid = avoidMatch[1].trim();
        continue;
      }
      if (!avoid) {
        definitionLines.push(lines[cursor]);
      }
    }
    index = cursor - 1;

    const definition = compact(definitionLines);
    if (!definition) {
      throw new Error(`${term} has no definition`);
    }
    if (!avoid) {
      throw new Error(`${term} has no Avoid guidance`);
    }

    terms.push({id, term, section, definition, avoid});
  }

  if (terms.length === 0) {
    throw new Error('glossary skill contains no glossary terms');
  }
  return terms;
}

export function renderGlossaryData(terms) {
  return `// Generated from ${glossarySkill}. Do not edit by hand.\n\nexport type GlossaryTerm = {\n  id: string;\n  term: string;\n  section: string;\n  definition: string;\n  avoid: string;\n};\n\nexport const glossaryTerms = ${JSON.stringify(terms, null, 2)} satisfies readonly GlossaryTerm[];\n\nexport const glossaryById = Object.fromEntries(\n  glossaryTerms.map((term) => [term.id, term]),\n) as Readonly<Record<string, GlossaryTerm>>;\n`;
}

export function renderGlossaryPage(termCount) {
  return `---\ntitle: Proctor glossary\ndescription: Canonical public definitions for Proctor domain terminology.\naudience: everyone\nmaturity: available\nslug: /glossary/\nsidebar_position: 1\npagination_next: null\npagination_prev: null\n---\n\n# Proctor Glossary\n\nThese ${termCount} definitions are generated from Proctor's canonical domain language.\nThey explain what each term means and the nearby vocabulary it must not be confused\nwith.\n\n<GlossaryIndex />\n`;
}

async function documentationFiles(directory, skip) {
  const files = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      if (path !== skip) {
        files.push(...(await documentationFiles(path, skip)));
      }
    } else if (entry.isFile() && ['.md', '.mdx'].includes(extname(entry.name))) {
      files.push(path);
    }
  }
  return files.sort();
}

function auditTermMarkup(source, name, knownIds) {
  const failures = [];
  const used = new Set();
  let fenced = false;
  for (const [offset, line] of source.split(/\r?\n/).entries()) {
    if (/^\s*```/.test(line)) {
      fenced = !fenced;
    }
    if (!line.includes('<Term')) {
      continue;
    }
    const location = `${name}:${offset + 1}`;
    if (fenced) {
      failures.push(`${location}: Term markup is forbidden inside code fences`);
    }
    if (/^\s*#{1,6}\s/.test(line)) {
      failures.push(`${location}: Term markup is forbidden in headings`);
    }
    if (/`[^`]*<Term[^`]*`/.test(line)) {
      failures.push(`${location}: Term markup is forbidden in inline code`);
    }
    if (/<GovernedFigure\b[^>]*<Term/.test(line)) {
      failures.push(`${location}: Term markup is forbidden in illustrations`);
    }
    for (const match of line.matchAll(/<Term\s+id=["']([^"']+)["'][^>]*>/g)) {
      const id = match[1];
      if (!knownIds.has(id)) {
        failures.push(`${location}: unknown glossary term ${id}`);
      }
      if (used.has(id)) {
        failures.push(`${location}: ${id} is already marked in this page; annotate only the curated first occurrence`);
      }
      used.add(id);
    }
    if (/<Term\b/.test(line) && !/<Term\s+id=["'][^"']+["']/.test(line)) {
      failures.push(`${location}: Term requires an explicit glossary id`);
    }
  }
  return failures;
}

export async function auditGlossary({repoRoot = defaultRepoRoot} = {}) {
  const failures = [];
  const contextPath = resolve(repoRoot, glossarySkill);
  let terms = [];
  try {
    terms = parseGlossary(await readFile(contextPath, 'utf8'));
  } catch (error) {
    return [`${glossarySkill}: ${error.message}`];
  }

  const expected = new Map([
    [
      resolve(repoRoot, 'docs/site/src/generated/glossary.ts'),
      renderGlossaryData(terms),
    ],
    [
      resolve(repoRoot, 'docs/public/reference/glossary.mdx'),
      renderGlossaryPage(terms.length),
    ],
  ]);
  for (const [path, wanted] of expected) {
    try {
      if ((await readFile(path, 'utf8')) !== wanted) {
        failures.push(`${relative(repoRoot, path)} is stale; run npm run generate:glossary`);
      }
    } catch (error) {
      failures.push(`${relative(repoRoot, path)}: ${error.message}`);
    }
  }

  const knownIds = new Set(terms.map((term) => term.id));
  const roots = [resolve(repoRoot, 'docs/public'), resolve(repoRoot, 'docs/api')];
  const generatedReference = resolve(repoRoot, 'docs/api/reference');
  for (const root of roots) {
    for (const file of await documentationFiles(root, generatedReference)) {
      failures.push(
        ...auditTermMarkup(
          await readFile(file, 'utf8'),
          relative(repoRoot, file),
          knownIds,
        ),
      );
    }
  }
  return failures;
}
