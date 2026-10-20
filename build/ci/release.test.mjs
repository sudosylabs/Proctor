// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readdir, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parseReleaseTag, releaseMetadata, verifyChecksums, verifyReleaseCommit, writeChecksums, writeReleaseProvenance } from "./release.mjs";

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

test("release executables always compile with the production environment default", async () => {
  const commands = execFileSync("make", ["--dry-run", "package", "PACKAGE_DIR=/tmp/proctor-release-test-output"], {
    cwd: new URL("../../server/", import.meta.url), encoding: "utf8",
  });
  const builds = commands.split("\n").filter((line) => line.startsWith("go build "));
  assert.equal(builds.length, 2);
  // Production is the untagged default; only an explicit dev tag opts out.
  for (const command of builds) assert.doesNotMatch(command, /-tags/);
});

test("the local and release images share one package recipe and Dockerfile", async () => {
  const container = await readFile(new URL("../make/container.mk", import.meta.url), "utf8");
  const workflow = await readFile(new URL("../../.github/workflows/release.yml", import.meta.url), "utf8");
  const dockerfile = await readFile(new URL("../docker/Dockerfile.release", import.meta.url), "utf8");
  // The image copies packaged binaries; it never compiles source.
  assert.doesNotMatch(dockerfile, /go build|^FROM .* AS /m);
  assert.match(dockerfile, /COPY --chown=65532:65532 \$\{TARGETARCH\}\/proctor\/ \.\//);
  assert.match(workflow, /file: build\/docker\/Dockerfile\.release/);
  assert.match(container, /--file "\$\(ROOT_DIR\)\/build\/docker\/Dockerfile\.release"/);
  assert.match(container, /\$\(call package-linux,/);
  const dockerfiles = await readdir(new URL("../docker/", import.meta.url));
  assert.deepEqual(dockerfiles.filter((name) => name !== "Dockerfile.buildenv").sort(), ["Dockerfile.release"]);
});

test("checksum manifests are written and strictly verified without platform tools", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "proctor-checksums-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await writeFile(join(directory, "b.tar.gz"), "second");
  await writeFile(join(directory, "a.tar.gz"), "first");
  await writeChecksums(directory, ["b.tar.gz", "a.tar.gz"]);
  const sha = (value) => createHash("sha256").update(value).digest("hex");
  assert.equal(await readFile(join(directory, "SHA256SUMS"), "utf8"), `${sha("first")}  a.tar.gz\n${sha("second")}  b.tar.gz\n`);
  await verifyChecksums(directory);

  await writeFile(join(directory, "a.tar.gz"), "tampered");
  await assert.rejects(verifyChecksums(directory), /mismatch for a.tar.gz/);
  await writeFile(join(directory, "a.tar.gz"), "first");
  for (const manifest of [`${sha("first")} a.tar.gz\n`, `${sha("first")}  ../a.tar.gz\n`, `${sha("first")}  a.tar.gz`,
    `${sha("first")}  a.tar.gz\n${sha("first")}  a.tar.gz\n`, `${sha("first")}  missing.tar.gz\n`]) {
    await writeFile(join(directory, "SHA256SUMS"), manifest);
    await assert.rejects(verifyChecksums(directory));
  }
  await writeFile(join(directory, "empty.tar.gz"), "");
  await assert.rejects(writeChecksums(directory, ["empty.tar.gz"]), /must not be empty/);
  await assert.rejects(writeChecksums(directory, ["../a.tar.gz"]), /Invalid artifact name/);
});
