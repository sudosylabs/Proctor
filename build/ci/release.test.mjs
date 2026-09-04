// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parseReleaseTag, releaseMetadata, verifyReleaseCommit, writeReleaseProvenance } from "./release.mjs";

test("release tags select precisely one module and preserve prereleases", () => {
  for (const module of ["server", "packages/cache", "packages/mail", "packages/vfs"]) {
    const metadata = parseReleaseTag(`${module}/v0.12.3-rc.1`);
    assert.equal(metadata.module, module);
    assert.equal(metadata.version, "0.12.3-rc.1");
    assert.equal(metadata.prerelease, true);
    assert.equal(metadata.server, module === "server");
  }
  assert.equal(parseReleaseTag("server/v1.2.3").prerelease, false);
});

test("reject ambiguous, injectable, or incompatible release tags", () => {
  for (const tag of ["v1.2.3", "server/v2.0.0", "packages/other/v1.2.3", "server/v01.2.3", "server/v1.2.3-01", "server/v1.2.3+build", "server/v1.2.3\nimage=evil", "server/v1.2.3;echo bad", "server/v1.2", "server/v1.2.3-rc.01"]) {
    assert.throws(() => parseReleaseTag(tag), undefined, tag);
  }
});

test("release source must be the exact tag commit reachable from the default branch", async (t) => {
  const cwd = await mkdtemp(join(tmpdir(), "proctor-release-git-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const git = (...args) => execFileSync("git", args, {
    cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, GIT_CONFIG_GLOBAL: "/dev/null", GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_COUNT: "0" },
  }).trim();
  git("init", "--initial-branch=main");
  git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "baseline");
  const baseline = git("rev-parse", "HEAD");
  git("update-ref", "refs/remotes/origin/main", baseline);
  git("tag", "server/v0.1.0");
  const options = { tag: "server/v0.1.0", sha: baseline, defaultBranch: "main", cwd };
  assert.doesNotThrow(() => verifyReleaseCommit(options));
  assert.throws(() => verifyReleaseCommit({ ...options, sha: "0".repeat(40) }));
  git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "unmerged");
  git("tag", "server/v0.2.0");
  assert.throws(() => verifyReleaseCommit({ ...options, tag: "server/v0.2.0", sha: git("rev-parse", "HEAD") }));
});

test("release metadata rejects non-tag events and mismatched module declarations", async (t) => {
  const cwd = await mkdtemp(join(tmpdir(), "proctor-release-metadata-"));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  const git = (...args) => execFileSync("git", args, {
    cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, GIT_CONFIG_GLOBAL: "/dev/null", GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_COUNT: "0" },
  }).trim();
  git("init", "--initial-branch=main");
  await mkdir(join(cwd, "packages/mail"), { recursive: true });
  const moduleFile = join(cwd, "packages/mail/go.mod");
  await writeFile(moduleFile, "module github.com/sudosylabs/proctor/packages/mail\n\ngo 1.25.13\n");
  git("add", "packages/mail/go.mod");
  git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "module");
  git("tag", "packages/mail/v0.1.0");
  git("update-ref", "refs/remotes/origin/main", "HEAD");
  const env = {
    GITHUB_EVENT_NAME: "push", GITHUB_REF_TYPE: "tag", GITHUB_REF_NAME: "packages/mail/v0.1.0",
    GITHUB_SHA: git("rev-parse", "HEAD"), GITHUB_REPOSITORY: "sudosylabs/Proctor", RELEASE_DEFAULT_BRANCH: "main",
  };
  const result = await releaseMetadata(env, cwd);
  assert.equal(result.image, "ghcr.io/sudosylabs/proctor");
  assert.equal(result["source-tree"], "HEAD:packages/mail");
  assert.equal(result["source-archive"], "proctor-mail-0.1.0-source.tar.gz");
  for (const override of [{ GITHUB_EVENT_NAME: "pull_request" }, { GITHUB_REF_TYPE: "branch" }, { GITHUB_REPOSITORY: "bad\nvalue" }]) {
    await assert.rejects(releaseMetadata({ ...env, ...override }, cwd));
  }
  await writeFile(moduleFile, "module example.invalid/other\n");
  await assert.rejects(releaseMetadata(env, cwd), /declared Go module path/);
});

test("manifest covers exact source bytes and provenance; rejects missing and linked artifacts", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "proctor-release-artifacts-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const env = { GITHUB_REF_NAME: "packages/mail/v0.1.0", GITHUB_SHA: "a".repeat(40), GITHUB_REPOSITORY: "sudosylabs/Proctor" };
  await assert.rejects(writeReleaseProvenance(directory, env), /source archive is missing/);
  const archive = "proctor-mail-0.1.0-source.tar.gz";
  await symlink("/nonexistent-fixture", join(directory, archive));
  await assert.rejects(writeReleaseProvenance(directory, env), /regular files/);
  await rm(join(directory, archive));
  await writeFile(join(directory, archive), "source bytes");
  await writeReleaseProvenance(directory, env);
  const sums = await readFile(join(directory, "SHA256SUMS"), "utf8");
  assert.match(sums, new RegExp(createHash("sha256").update("source bytes").digest("hex")));
  assert.match(sums, /provenance.json/);
  const provenance = JSON.parse(await readFile(join(directory, "provenance.json"), "utf8"));
  assert.equal(provenance.source.commit, env.GITHUB_SHA);
  await assert.rejects(writeReleaseProvenance(directory, env), /Unexpected or incomplete/);
});

test("server manifest requires both architectures, source, and an immutable image identity", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "proctor-server-release-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const env = { GITHUB_REF_NAME: "server/v0.1.0", GITHUB_SHA: "b".repeat(40), GITHUB_REPOSITORY: "sudosylabs/Proctor" };
  for (const name of ["proctor-0.1.0-source.tar.gz", "proctor-0.1.0-linux-amd64.tar.gz", "proctor-0.1.0-linux-arm64.tar.gz"]) {
    await writeFile(join(directory, name), "fixture bytes");
  }
  await assert.rejects(writeReleaseProvenance(directory, env), /Unexpected or incomplete/);
  await writeFile(join(directory, "image-digest.txt"), "ghcr.io/sudosylabs/proctor:latest\n");
  await assert.rejects(writeReleaseProvenance(directory, env), /immutable SHA-256/);
  await writeFile(join(directory, "image-digest.txt"), `ghcr.io/sudosylabs/proctor@sha256:${"c".repeat(64)}\n`);
  await writeFile(join(directory, "unapproved.txt"), "must not publish");
  await assert.rejects(writeReleaseProvenance(directory, env), /Unexpected or incomplete/);
  await rm(join(directory, "unapproved.txt"));
  await writeReleaseProvenance(directory, env);
  const provenance = JSON.parse(await readFile(join(directory, "provenance.json"), "utf8"));
  assert.equal(provenance.artifacts.length, 4);
  assert.match(await readFile(join(directory, "SHA256SUMS"), "utf8"), /image-digest.txt/);
});
