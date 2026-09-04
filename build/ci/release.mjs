// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { createReadStream } from "node:fs";
import { appendFile, lstat, readFile, readdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

export function parseReleaseTag(tag) {
  const match = /^(server|packages\/(cache|mail|vfs))\/v(0|1)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/.exec(tag);
  if (!match || tag.length > 120) throw new Error("Expected server/vX.Y.Z or packages/{cache,mail,vfs}/vX.Y.Z (major 0 or 1)");
  if (match[6]?.split(".").some((part) => /^\d+$/.test(part) && /^0\d/.test(part))) {
    throw new Error("Numeric prerelease identifiers cannot have leading zeros");
  }
  const module = match[1];
  const version = tag.slice(module.length + 2);
  const name = match[2] ? `proctor-${match[2]}` : "proctor";
  return {
    tag, module, version, server: module === "server",
    prerelease: Boolean(match[6]),
    "source-tree": module === "server" ? "HEAD" : `HEAD:${module}`,
    "source-prefix": `${name}-${version}/`,
    "source-archive": `${name}-${version}-source.tar.gz`,
  };
}

export function verifyReleaseCommit({ tag, sha, defaultBranch, cwd }) {
  if (!/^[a-f0-9]{40}$/.test(sha)) throw new Error("Expected a full GitHub commit SHA");
  const git = (...args) => execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  git("check-ref-format", `refs/heads/${defaultBranch}`);
  if (git("rev-parse", "HEAD") !== sha || git("rev-parse", `refs/tags/${tag}^{commit}`) !== sha) {
    throw new Error("Release tag and checked-out commit do not match the triggering SHA");
  }
  git("merge-base", "--is-ancestor", sha, `refs/remotes/origin/${defaultBranch}`);
}

export async function releaseMetadata(env = process.env, cwd = process.cwd()) {
  if (env.GITHUB_EVENT_NAME !== "push" || env.GITHUB_REF_TYPE !== "tag") throw new Error("Releases require an explicit tag push");
  if (!/^[\w.-]+\/[\w.-]+$/.test(env.GITHUB_REPOSITORY ?? "")) throw new Error("Invalid repository identity");
  const metadata = parseReleaseTag(env.GITHUB_REF_NAME ?? "");
  verifyReleaseCommit({ tag: metadata.tag, sha: env.GITHUB_SHA, defaultBranch: env.RELEASE_DEFAULT_BRANCH, cwd });
  const moduleFile = await readFile(join(cwd, metadata.module, "go.mod"), "utf8");
  if (!new RegExp(`^module github\\.com/sudosylabs/proctor/${metadata.module}\\s*$`, "m").test(moduleFile)) {
    throw new Error("The tag must match the declared Go module path");
  }
  return { ...metadata, image: `ghcr.io/${env.GITHUB_REPOSITORY.toLowerCase()}` };
}

// This is signed release metadata, not a claim of a particular SLSA level.
// It binds delivered bytes to their source and the exact CI execution.
export async function writeReleaseProvenance(directory, env = process.env) {
  const metadata = parseReleaseTag(env.GITHUB_REF_NAME ?? "");
  const names = (await readdir(directory)).filter((name) => name !== "SHA256SUMS").sort();
  if (!names.includes(metadata["source-archive"])) throw new Error("Release source archive is missing");
  const expected = [metadata["source-archive"]];
  if (metadata.server) expected.push(
    `proctor-${metadata.version}-linux-amd64.tar.gz`,
    `proctor-${metadata.version}-linux-arm64.tar.gz`, "image-digest.txt",
  );
  if (JSON.stringify(names) !== JSON.stringify(expected.sort())) throw new Error("Unexpected or incomplete release payload");
  const artifacts = [];
  for (const name of names) {
    const path = join(directory, name);
    const stat = await lstat(path);
    if (!stat.isFile()) throw new Error("Release artifacts must be regular files");
    if (stat.size === 0) throw new Error("Release artifacts must not be empty");
    if (name === "image-digest.txt") {
      const digest = await readFile(path, "utf8");
      const prefix = `ghcr.io/${env.GITHUB_REPOSITORY.toLowerCase()}@sha256:`;
      if (!digest.startsWith(prefix) || !/^[a-f0-9]{64}\n$/.test(digest.slice(prefix.length))) {
        throw new Error("Image identity must be this repository's immutable SHA-256 digest");
      }
    }
    const hash = createHash("sha256");
    for await (const bytes of createReadStream(path)) hash.update(bytes);
    artifacts.push({ name, sha256: hash.digest("hex"), size: stat.size });
  }
  const provenance = JSON.stringify({
    schema_version: 1,
    source: { repository: env.GITHUB_REPOSITORY, commit: env.GITHUB_SHA, tag: metadata.tag, module: metadata.module },
    build: {
      workflow: env.GITHUB_WORKFLOW_REF, workflow_commit: env.GITHUB_WORKFLOW_SHA,
      run: `${env.GITHUB_SERVER_URL}/${env.GITHUB_REPOSITORY}/actions/runs/${env.GITHUB_RUN_ID}`,
      attempt: env.GITHUB_RUN_ATTEMPT,
    },
    artifacts,
  }, null, 2) + "\n";
  await writeFile(join(directory, "provenance.json"), provenance, { flag: "wx" });
  artifacts.push({ name: "provenance.json", sha256: createHash("sha256").update(provenance).digest("hex") });
  await writeFile(join(directory, "SHA256SUMS"), artifacts.map(({ name, sha256 }) => `${sha256}  ${name}\n`).join(""));
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    if (process.argv[2] === "metadata") {
      const metadata = await releaseMetadata();
      if (!process.env.GITHUB_OUTPUT) throw new Error("GITHUB_OUTPUT is required");
      await appendFile(process.env.GITHUB_OUTPUT, Object.entries(metadata).map(([key, value]) => `${key}=${value}\n`).join(""));
    } else if (process.argv[2] === "provenance" && process.argv[3]) {
      await writeReleaseProvenance(resolve(process.argv[3]));
    } else throw new Error("usage: node build/ci/release.mjs metadata|provenance DIRECTORY");
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
