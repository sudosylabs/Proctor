// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import {createHash} from "node:crypto";
import {fileURLToPath} from "node:url";
import {readdir, readFile, writeFile} from "node:fs/promises";
import path from "node:path";
import process from "node:process";

import mjml2html from "mjml";

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url));
const defaultRoot = path.resolve(scriptDirectory, "..");

async function filesWithExtension(directory, extension) {
  return (await readdir(directory, {withFileTypes: true}))
    .filter((entry) => entry.isFile() && entry.name.endsWith(extension))
    .map((entry) => entry.name)
    .sort();
}

async function sourceDigest(root, sourceName) {
  const partialDirectory = path.join(root, "partials");
  const partialNames = (await readdir(partialDirectory, {withFileTypes: true}))
    .filter((entry) => entry.isFile() && entry.name.endsWith(".mjml"))
    .map((entry) => entry.name)
    .sort();
  const hash = createHash("sha256");
  const inputs = [
    sourceName,
    ...partialNames.map((name) => path.posix.join("partials", name)),
    "package.json",
    "package-lock.json",
  ];
  for (const relativeName of inputs) {
    hash.update(relativeName);
    hash.update("\0");
    hash.update(await readFile(path.join(root, relativeName)));
    hash.update("\0");
  }
  return hash.digest("hex");
}

async function compilerVersion(root) {
  const packageJSON = JSON.parse(await readFile(path.join(root, "package.json"), "utf8"));
  return packageJSON.devDependencies.mjml;
}

async function compileTemplate(root, sourceName) {
  const sourcePath = path.join(root, sourceName);
  const source = await readFile(sourcePath, "utf8");
  const result = await mjml2html(source, {
    filePath: sourcePath,
    ignoreIncludes: false,
    includePath: root,
    validationLevel: "strict",
    minify: false,
  });
  const errors = result.errors ?? [];
  if (errors.length > 0) {
    const details = errors.map((error) => error.formattedMessage ?? error.message).join("\n");
    throw new Error(`${sourceName} failed MJML validation:\n${details}`);
  }
  const version = await compilerVersion(root);
  const sourceHash = await sourceDigest(root, sourceName);
  const body = `${result.html
    .split("\n")
    .map((line) => line.trimEnd())
    .join("\n")
    .trim()}\n`;
  const outputHash = createHash("sha256").update(body).digest("hex");
  const header = `<!-- Code generated from ${sourceName} by mjml ${version}; DO NOT EDIT. Source digest: sha256:${sourceHash}. Output digest: sha256:${outputHash}. -->\n`;
  return `${header}${body}`;
}

export async function buildTemplates(root = defaultRoot) {
  for (const sourceName of await filesWithExtension(root, ".mjml")) {
    const outputName = `${sourceName.slice(0, -".mjml".length)}.html`;
    await writeFile(path.join(root, outputName), await compileTemplate(root, sourceName), "utf8");
  }
}

export async function checkTemplates(root = defaultRoot) {
  const stale = [];
  for (const sourceName of await filesWithExtension(root, ".mjml")) {
    const outputName = `${sourceName.slice(0, -".mjml".length)}.html`;
    let actual;
    try {
      actual = await readFile(path.join(root, outputName), "utf8");
    } catch (error) {
      if (error.code === "ENOENT") {
        stale.push(outputName);
        continue;
      }
      throw error;
    }
    const expected = await compileTemplate(root, sourceName);
    if (actual !== expected) {
      stale.push(outputName);
    }
  }
  if (stale.length > 0) {
    throw new Error(`generated templates are stale: ${stale.join(", ")}; run npm run generate`);
  }
}

async function main(args) {
  const command = args[0];
  if (command === "build") {
    await buildTemplates();
    return;
  }
  if (command === "check") {
    await checkTemplates();
    return;
  }
  throw new Error("usage: node scripts/templates.mjs <build|check>");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  });
}
