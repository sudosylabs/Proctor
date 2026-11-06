// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { evaluateReport } from "./container-scan.mjs";

const finding = (Severity, FixedVersion) => ({
  VulnerabilityID: `CVE-${Severity}-${FixedVersion ?? "none"}`,
  PkgName: "example", InstalledVersion: "1.0.0", Severity, ...(FixedVersion ? { FixedVersion } : {}),
});

test("critical findings and fixable high findings block the image", () => {
  const { blocking, reported } = evaluateReport({
    SchemaVersion: 2,
    Results: [
      { Target: "os", Vulnerabilities: [finding("CRITICAL"), finding("CRITICAL", "1.0.1"), finding("HIGH", "1.0.2")] },
      { Target: "proctor", Vulnerabilities: [finding("HIGH"), finding("MEDIUM", "1.0.3"), finding("LOW")] },
      { Target: "empty" },
    ],
  });
  assert.deepEqual(blocking.map(({ id }) => id), ["CVE-CRITICAL-none", "CVE-CRITICAL-1.0.1", "CVE-HIGH-1.0.2"]);
  assert.deepEqual(reported.map(({ id }) => id), ["CVE-HIGH-none"]);
});

test("a clean report passes and a non-Trivy document is rejected", () => {
  assert.deepEqual(evaluateReport({ SchemaVersion: 2 }), { blocking: [], reported: [] });
  for (const value of [null, [], {}, { Results: [] }]) assert.throws(() => evaluateReport(value), /Trivy JSON report/);
});

test("the release and scheduled scans share the pinned make recipe", async () => {
  const config = await readFile(new URL("../make/config.mk", import.meta.url), "utf8");
  const container = await readFile(new URL("../make/container.mk", import.meta.url), "utf8");
  const release = await readFile(new URL("../../.github/workflows/release.yml", import.meta.url), "utf8");
  const scan = await readFile(new URL("../../.github/workflows/container-scan.yml", import.meta.url), "utf8");
  assert.match(config, /^PROCTOR_TRIVY_IMAGE \?= aquasec\/trivy:\d+\.\d+\.\d+@sha256:[a-f0-9]{64}$/m);
  assert.match(container, /build\/ci\/container-scan\.mjs/);
  assert.match(scan, /make container\b/);
  assert.match(scan, /make container-scan\b/);
  // The release scans each platform of the locally built image before any
  // registry login, push, or signature exists.
  const scanAt = release.indexOf("PROCTOR_RUNTIME_IMAGE=\"$image\" container-scan");
  assert.ok(scanAt > release.indexOf("outputs: type=oci,dest="));
  assert.ok(scanAt < release.indexOf("docker/login-action@"));
  assert.ok(scanAt < release.indexOf("docker://$RELEASE_IMAGE"));
  assert.ok(scanAt < release.indexOf("cosign sign-blob"));
  assert.doesNotMatch(release, /push: true/);
  assert.match(release, /for arch in amd64 arm64; do\n +image="proctor-release-scan:\$arch"/);
});
