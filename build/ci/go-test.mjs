// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import { spawn } from "node:child_process";
import { createWriteStream } from "node:fs";
import { appendFile, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { basename, join, resolve } from "node:path";
import { createInterface } from "node:readline";
import { finished } from "node:stream/promises";
import { pathToFileURL } from "node:url";

const escapeXML = (value) => String(value).replace(/[<>&"']/g, (char) => ({
  "<": "&lt;", ">": "&gt;", "&": "&amp;", '"': "&quot;", "'": "&apos;",
})[char]);

// Keep outcomes, not unbounded test output, in memory. Raw JSON and stderr are
// streamed to separate files, including when a package fails to build.
export function testReport(events, exitCode) {
  const cases = [...events.values()];
  if (exitCode !== 0 && !cases.some((item) => item.action === "fail")) {
    cases.push({ name: "go test process", package: "runner", action: "fail", elapsed: 0 });
  }
  const count = (action) => cases.filter((item) => item.action === action).length;
  const failures = count("fail");
  const skipped = count("skip");
  const xml = cases.map((item) => {
    const status = item.action === "fail" ? '<failure message="See events.jsonl and stderr.log"/>'
      : item.action === "skip" ? "<skipped/>" : "";
    return `  <testcase name="${escapeXML(item.name)}" classname="${escapeXML(item.package)}" time="${item.elapsed}">${status}</testcase>`;
  }).join("\n");
  return {
    tests: cases.length, failures, skipped,
    junit: `<?xml version="1.0" encoding="UTF-8"?>\n<testsuite name="go test" tests="${cases.length}" failures="${failures}" skipped="${skipped}">\n${xml}\n</testsuite>\n`,
  };
}

export async function runGoTests(args, { env = process.env, cwd = process.cwd(), command = "go" } = {}) {
  if (!env.PROCTOR_TEST_REPORT_DIR) throw new Error("PROCTOR_TEST_REPORT_DIR is required");
  const root = resolve(env.PROCTOR_TEST_REPORT_DIR);
  await mkdir(root, { recursive: true });
  const directory = await mkdtemp(join(root, `${basename(cwd)}-`));
  const commandArgs = ["test", "-json", "-count=1", "-shuffle=on",
    "-covermode=atomic", `-coverprofile=${join(directory, "coverage.out")}`, ...args];
  await writeFile(join(directory, "command.json"), JSON.stringify({ cwd, args: commandArgs }, null, 2));

  const raw = createWriteStream(join(directory, "events.jsonl"));
  const errors = createWriteStream(join(directory, "stderr.log"));
  const child = spawn(command, commandArgs, { cwd, env, stdio: ["ignore", "pipe", "pipe"] });
  const results = new Map();
  let reportError;
  const onError = (error) => { reportError = error; child.kill("SIGTERM"); };
  raw.on("error", onError);
  errors.on("error", onError);
  child.stdout.pipe(raw);
  child.stderr.pipe(errors);
  child.stderr.on("data", (data) => process.stderr.write(data));
  const lines = createInterface({ input: child.stdout });
  lines.on("line", (line) => {
    let event;
    try { event = JSON.parse(line); } catch { return; }
    if (event.Output) process.stdout.write(event.Output);
    if (!["pass", "fail", "skip"].includes(event.Action)) return;
    // Successful package summaries aren't extra test cases. Failed packages
    // still need a result when TestMain, compilation, or setup failed.
    if (!event.Test && event.Action !== "fail") return;
    const packageName = event.Package ?? event.ImportPath ?? "unknown";
    const name = event.Test ?? "package";
    results.set(`${packageName}/${name}`, {
      name, package: packageName, action: event.Action, elapsed: event.Elapsed ?? 0,
    });
  });
  const terminate = () => child.kill("SIGTERM");
  process.on("SIGTERM", terminate);
  process.on("SIGINT", terminate);
  const exitCode = await new Promise((done) => {
    child.on("error", (error) => { reportError = error; });
    child.on("close", (code) => done(code ?? 1));
  });
  process.off("SIGTERM", terminate);
  process.off("SIGINT", terminate);
  await Promise.all([finished(raw), finished(errors)]);
  const report = testReport(results, reportError ? 1 : exitCode);
  await writeFile(join(directory, "junit.xml"), report.junit);
  const coverage = spawn("go", ["tool", "cover", `-func=${join(directory, "coverage.out")}`], { cwd, env });
  const coverageFile = createWriteStream(join(directory, "coverage.txt"));
  coverage.stdout.pipe(coverageFile);
  coverage.stderr.resume();
  coverage.on("error", () => coverageFile.end());
  await new Promise((done) => coverage.on("close", done));
  await finished(coverageFile);
  const coverageText = await readFile(join(directory, "coverage.txt"), "utf8");
  const total = /^total:.*?(\d+(?:\.\d+)?%)$/m.exec(coverageText)?.[1] ?? "unavailable";
  const summary = `### Go tests: ${basename(directory)}\n\nTests: ${report.tests}; failures: ${report.failures}; skipped: ${report.skipped}; exit: ${exitCode}; statement coverage: ${total}.\n\nSee this invocation's JUnit, raw events, stderr, and coverage artifacts.\n`;
  await writeFile(join(directory, "summary.md"), summary);
  if (env.GITHUB_STEP_SUMMARY) await appendFile(env.GITHUB_STEP_SUMMARY, `${summary}\n`);
  if (reportError) throw reportError;
  return exitCode;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try { process.exitCode = await runGoTests(process.argv.slice(2)); }
  catch (error) { console.error(error.message); process.exitCode = 1; }
}
