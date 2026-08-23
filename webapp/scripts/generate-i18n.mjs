// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import { readdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const webappDirectory = join(dirname(fileURLToPath(import.meta.url)), "..");
const catalogDirectory = join(webappDirectory, "..", "server", "i18n");
const output = join(webappDirectory, "src", "generated", "i18n", "catalogs.ts");

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

const source = `// Generated from server/i18n by scripts/generate-i18n.mjs. Do not edit.\n` +
  `export const catalogs = ${JSON.stringify(catalogs, null, 2)} as const;\n\n` +
  `export type WebappLocale = keyof typeof catalogs;\n`;

await writeFile(output, source, "utf8");
