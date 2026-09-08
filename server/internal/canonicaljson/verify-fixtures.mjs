// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';

// A reference for valid canonical values. Raw-input rejection vectors are checked
// by the Go tests and must also be adopted by Desktop's strict decoder. JSON.parse
// alone cannot validate duplicate fields, lexical integer forms, or lost surrogates.
function canonical(value) {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value !== null && typeof value === 'object') {
    return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonical(value[key])}`).join(',')}}`;
  }
  if (typeof value === 'number') assert(Number.isSafeInteger(value));
  return JSON.stringify(value);
}

const fixtures = JSON.parse(readFileSync(new URL('./testdata/agreement.json', import.meta.url), 'utf8'));
let checked = 0;
for (const fixture of fixtures) {
  if (fixture.reject) continue;
  const value = JSON.parse(Buffer.from(fixture.input_base64, 'base64').toString('utf8'));
  const bytes = Buffer.from(canonical(value), 'utf8');
  assert.equal(bytes.toString('base64'), fixture.canonical_base64, fixture.name);
  assert.equal(`sha256:${createHash('sha256').update(bytes).digest('hex')}`, fixture.sha256, fixture.name);
  checked++;
}
console.log(`Verified ${checked} exact canonical byte/hash vectors using JSON.stringify scalar serialization.`);
