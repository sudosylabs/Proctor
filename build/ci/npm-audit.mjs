// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Applies the npm dependency advisory policy to an `npm audit --json` report:
// every high or critical advisory fails unless its GitHub advisory ID is
// explicitly accepted. Packages that are vulnerable only through an accepted
// advisory therefore pass, while any other high advisory still blocks.

import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";

const blockingSeverities = new Set(["high", "critical"]);
const advisoryID = /^GHSA(?:-[23456789cfghjmpqrvwx]{4}){3}$/;

export function evaluateReport(report, accepted = []) {
  if (!report || typeof report !== "object" || report.error || typeof report.vulnerabilities !== "object" ||
    report.vulnerabilities === null) {
    throw new Error("Expected an npm audit JSON report");
  }
  for (const id of accepted) if (!advisoryID.test(id)) throw new Error(`Invalid accepted advisory ID: ${id}`);
  const acceptedIDs = new Set(accepted);
  const advisories = new Map();
  for (const vulnerability of Object.values(report.vulnerabilities)) {
    for (const via of vulnerability?.via ?? []) {
      if (typeof via !== "object" || via === null) continue;
      const id = String(via.url ?? "").split("/").pop();
      if (!advisoryID.test(id)) throw new Error(`Advisory without a GitHub advisory URL: ${via.title ?? via.name}`);
      advisories.set(id, { id, severity: String(via.severity ?? "unknown"), package: via.name, range: via.range });
    }
  }
  const blocking = [];
  const allowed = [];
  for (const advisory of advisories.values()) {
    if (!blockingSeverities.has(advisory.severity)) continue;
    (acceptedIDs.has(advisory.id) ? allowed : blocking).push(advisory);
  }
  return { blocking, allowed };
}

function describe({ id, severity, package: name, range }) {
  return `${severity} ${id} ${name} ${range}`;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    if (process.argv.length < 3) throw new Error("usage: node build/ci/npm-audit.mjs REPORT.json [ACCEPTED_GHSA_ID...]");
    const report = JSON.parse(await readFile(resolve(process.argv[2]), "utf8"));
    const { blocking, allowed } = evaluateReport(report, process.argv.slice(3));
    for (const entry of allowed) console.log(`accepted: ${describe(entry)}`);
    for (const entry of blocking) console.error(`blocking: ${describe(entry)}`);
    if (blocking.length > 0) throw new Error(`${blocking.length} high or critical npm advisories are not accepted`);
    console.log(`No unaccepted high or critical npm advisories (${allowed.length} accepted)`);
  } catch (error) { console.error(error.message); process.exitCode = 1; }
}
