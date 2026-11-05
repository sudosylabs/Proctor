// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { evaluateReport } from "./npm-audit.mjs";

const advisory = (id, severity, name) => ({
  source: 1, name, title: `${name} advisory`, severity, range: "<1.0.1", url: `https://github.com/advisories/${id}`,
});

const report = {
  auditReportVersion: 2,
  vulnerabilities: {
    accepted: { name: "accepted", severity: "high", via: [advisory("GHSA-qxc2-j82w-r537", "high", "accepted")] },
    dependent: { name: "dependent", severity: "high", via: ["accepted"] },
    fixable: { name: "fixable", severity: "high", via: [advisory("GHSA-4528-x5fx-h6hg", "high", "fixable")] },
    critical: { name: "critical", severity: "critical", via: [advisory("GHSA-mrwp-f3q8-3g9p", "critical", "critical")] },
    moderate: { name: "moderate", severity: "moderate", via: [advisory("GHSA-gcqx-w48m-w2h4", "moderate", "moderate")] },
  },
};

test("high and critical advisories block unless accepted by ID", () => {
  const { blocking, allowed } = evaluateReport(report, ["GHSA-qxc2-j82w-r537"]);
  assert.deepEqual(blocking.map(({ id }) => id), ["GHSA-4528-x5fx-h6hg", "GHSA-mrwp-f3q8-3g9p"]);
  assert.deepEqual(allowed.map(({ id }) => id), ["GHSA-qxc2-j82w-r537"]);
  assert.deepEqual(evaluateReport(report, ["GHSA-qxc2-j82w-r537", "GHSA-4528-x5fx-h6hg", "GHSA-mrwp-f3q8-3g9p"]).blocking, []);
});

test("a clean report passes and malformed input is rejected", () => {
  assert.deepEqual(evaluateReport({ vulnerabilities: {} }), { blocking: [], allowed: [] });
  for (const value of [null, [], {}, { vulnerabilities: null }, { error: { code: "ENOAUDIT" }, vulnerabilities: {} }]) {
    assert.throws(() => evaluateReport(value), /npm audit JSON report/);
  }
  assert.throws(() => evaluateReport({ vulnerabilities: {} }, ["CVE-2026-1"]), /Invalid accepted advisory ID/);
  assert.throws(() => evaluateReport({ vulnerabilities: { x: { via: [{ name: "x", severity: "high" }] } } }),
    /without a GitHub advisory URL/);
});

test("both npm audits fail on high, and the docs audit accepts only the documented advisories", async () => {
  const workflow = await readFile(new URL("../../.github/workflows/security.yml", import.meta.url), "utf8");
  const readme = await readFile(new URL("../../docs/site/README.md", import.meta.url), "utf8");
  assert.match(workflow, /npm audit --omit=dev --audit-level=high/);
  assert.doesNotMatch(workflow, /--audit-level=(?:info|low|moderate|critical|none)/);
  const accepted = workflow.match(/node \.\.\/\.\.\/build\/ci\/npm-audit\.mjs [^\n]+/)?.[0].match(/GHSA(?:-[a-z0-9]{4}){3}/g);
  assert.deepEqual(accepted?.sort(), ["GHSA-qxc2-j82w-r537", "GHSA-vfj7-8cjw-p6xm"]);
  for (const id of accepted) assert.match(readme, new RegExp(id));
});
