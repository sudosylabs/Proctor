// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Applies the runtime image vulnerability policy to a Trivy JSON report:
// every critical finding fails, and a high finding fails once a fixed
// version exists. Unfixed high findings are reported but cannot block a
// release that no upgrade could repair.

import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

export function evaluateReport(report) {
  if (!report || typeof report !== "object" || !("SchemaVersion" in report)) {
    throw new Error("Expected a Trivy JSON report");
  }
  const blocking = [];
  const reported = [];
  for (const result of report.Results ?? []) {
    for (const finding of result.Vulnerabilities ?? []) {
      const severity = String(finding.Severity ?? "UNKNOWN").toUpperCase();
      if (severity !== "CRITICAL" && severity !== "HIGH") continue;
      const fixed = Boolean(finding.FixedVersion);
      const entry = {
        id: finding.VulnerabilityID,
        severity,
        target: result.Target,
        package: finding.PkgName,
        installed: finding.InstalledVersion,
        fixed: finding.FixedVersion ?? "",
      };
      (severity === "CRITICAL" || fixed ? blocking : reported).push(entry);
    }
  }
  return { blocking, reported };
}

function describe({ id, severity, target, package: name, installed, fixed }) {
  return `${severity} ${id} ${name} ${installed} -> ${fixed || "no fix"} (${target})`;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    if (process.argv.length !== 3) throw new Error("usage: node build/ci/container-scan.mjs REPORT.json");
    const { blocking, reported } = evaluateReport(JSON.parse(await readFile(resolve(process.argv[2]), "utf8")));
    for (const entry of reported) console.log(`allowed: ${describe(entry)}`);
    for (const entry of blocking) console.error(`blocking: ${describe(entry)}`);
    if (blocking.length > 0) {
      throw new Error(`${blocking.length} critical or fixable high vulnerabilities in the runtime image`);
    }
    console.log(`No critical or fixable high vulnerabilities (${reported.length} unfixed high reported)`);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
