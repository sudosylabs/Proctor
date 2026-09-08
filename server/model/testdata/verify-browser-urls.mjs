// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Independent browser serialization reference. Rejected inputs belong to the
// stricter Proctor grammar and are covered by Go tests: URL alone permits some
// forms that the shared contract forbids. This does not certify a Desktop build.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';

const fixtures = JSON.parse(readFileSync(new URL('browser_urls.json', import.meta.url)));
let verified = 0;
for (const fixture of fixtures.cases.filter((item) => item.valid)) {
  const parsed = new URL(fixture.input, 'https://example.com');
  if (fixture.kind === 'navigation') {
    const location = { scheme: parsed.protocol.slice(0, -1), host: parsed.hostname, path: parsed.pathname };
    if (parsed.port !== '') location.port = parsed.port;
    assert.deepEqual(location, fixture.location, fixture.name);
  } else if (fixture.kind === 'origin') {
    assert.equal(parsed.origin, fixture.canonical, fixture.name);
  } else {
    const prefix = parsed.pathname === '/' ? '/' : parsed.pathname.replace(/\/$/, '');
    assert.equal(prefix, fixture.canonical, fixture.name);
  }
  verified++;
}
console.log(`Verified ${verified} browser origin/path fixtures against JavaScript URL.`);

const policyFixtures = JSON.parse(readFileSync(new URL('browser_policies.json', import.meta.url)));
function canonical(value) {
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  if (value !== null && typeof value === 'object') return '{' + Object.keys(value).sort().map(key => JSON.stringify(key) + ':' + canonical(value[key])).join(',') + '}';
  return JSON.stringify(value);
}
for (const fixture of policyFixtures.cases) {
  const bytes = canonical(fixture.policy);
  assert.equal(bytes, fixture.canonical, fixture.name);
  assert.equal('sha256:' + createHash('sha256').update(bytes).digest('hex'), fixture.digest, fixture.name);
}
console.log(`Verified ${policyFixtures.cases.length} Browser Policy canonical byte and digest fixtures.`);
