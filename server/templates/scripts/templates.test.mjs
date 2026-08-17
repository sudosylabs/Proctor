// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, writeFile} from "node:fs/promises";
import {tmpdir} from "node:os";
import path from "node:path";
import test from "node:test";

import {buildTemplates, checkTemplates} from "./templates.mjs";

test("generation is deterministic and freshness detects source drift", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "proctor-mail-templates-"));
  await mkdir(path.join(root, "partials"));
  await writeFile(
    path.join(root, "package.json"),
    JSON.stringify({devDependencies: {mjml: "5.4.0"}}),
  );
  await writeFile(path.join(root, "package-lock.json"), "{}\n");
  await writeFile(
    path.join(root, "example.mjml"),
    "<mjml><mj-body><mj-section><mj-column><mj-text>{{.Copy.Body}}</mj-text></mj-column></mj-section></mj-body></mjml>\n",
  );

  await buildTemplates(root);
  const first = await readFile(path.join(root, "example.html"), "utf8");
  await buildTemplates(root);
  const second = await readFile(path.join(root, "example.html"), "utf8");
  assert.equal(second, first);
  await checkTemplates(root);

  await writeFile(
    path.join(root, "example.mjml"),
    "<mjml><mj-body><mj-section><mj-column><mj-text>changed {{.Copy.Body}}</mj-text></mj-column></mj-section></mj-body></mjml>\n",
  );
  await assert.rejects(checkTemplates(root), /stale/);
});
