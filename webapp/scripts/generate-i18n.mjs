// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import { readdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const webappDirectory = join(dirname(fileURLToPath(import.meta.url)), "..");
const catalogDirectory = join(webappDirectory, "..", "server", "i18n");
const output = join(webappDirectory, "src", "generated", "i18n", "catalogs.ts");
const sourceDirectory = join(webappDirectory, "src");

async function collectSourceMessageIDs(directory) {
  const ids = new Set();

  async function visitDirectory(current) {
    for (const entry of await readdir(current, { withFileTypes: true })) {
      if (entry.isDirectory()) {
        if (entry.name !== "generated") {
          await visitDirectory(join(current, entry.name));
        }
        continue;
      }
      if (!entry.isFile() || !/\.tsx?$/.test(entry.name)) {
        continue;
      }
      const file = join(current, entry.name);
      const source = ts.createSourceFile(
        file,
        await readFile(file, "utf8"),
        ts.ScriptTarget.Latest,
        false,
        entry.name.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
      );
      function visit(node) {
        if (ts.isStringLiteralLike(node) && node.text.startsWith("webapp.")) {
          ids.add(node.text);
        }
        ts.forEachChild(node, visit);
      }
      visit(source);
    }
  }

  await visitDirectory(directory);
  return ids;
}

const catalogs = {};
for (const file of (await readdir(catalogDirectory)).filter((name) => name.endsWith(".json")).sort()) {
  const locale = file.slice(0, -".json".length);
  const definitions = JSON.parse(await readFile(join(catalogDirectory, file), "utf8"));
  if (!Array.isArray(definitions)) {
    throw new Error(`${file} must contain a localization definition array`);
  }
  const messages = {};
  for (const definition of definitions) {
    if (
      typeof definition === "object" &&
      definition !== null &&
      typeof definition.id === "string" &&
      definition.id.startsWith("webapp.") &&
      typeof definition.translation === "string"
    ) {
      messages[definition.id] = definition.translation;
    }
  }
  catalogs[locale] = messages;
}

const sourceMessageIDs = await collectSourceMessageIDs(sourceDirectory);
const englishMessageIDs = new Set(Object.keys(catalogs.en ?? {}));
const missing = [...sourceMessageIDs].filter((id) => !englishMessageIDs.has(id)).sort();
const orphaned = [...englishMessageIDs].filter((id) => !sourceMessageIDs.has(id)).sort();
if (missing.length > 0 || orphaned.length > 0) {
  const details = [];
  if (missing.length > 0) {
    details.push(`missing from server/i18n/en.json: ${missing.join(", ")}`);
  }
  if (orphaned.length > 0) {
    details.push(`not referenced by webapp source: ${orphaned.join(", ")}`);
  }
  throw new Error(`webapp localization ownership drift (${details.join("; ")})`);
}

const source = `// Generated from server/i18n by scripts/generate-i18n.mjs. Do not edit.\n` +
  `export const catalogs = ${JSON.stringify(catalogs, null, 2)} as const;\n\n` +
  `export type WebappLocale = keyof typeof catalogs;\n`;

await writeFile(output, source, "utf8");
