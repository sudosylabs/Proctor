import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {dirname, resolve} from 'node:path';
import test from 'node:test';
import {fileURLToPath} from 'node:url';

import {auditOpenAPI} from './openapi-audit.mjs';

const siteRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const canonical = JSON.parse(
  await readFile(resolve(siteRoot, '../../server/openapi.json'), 'utf8'),
);

function cloneCanonical() {
  return structuredClone(canonical);
}

function expectError(mutator, expected) {
  const candidate = cloneCanonical();
  mutator(candidate);
  const report = auditOpenAPI(candidate);
  assert.equal(report.ok, false);
  assert.ok(report.errors.some((error) => error.includes(expected)), report.errors.join('\n'));
}

test('the canonical OpenAPI documentation data passes the audit', () => {
  const report = auditOpenAPI(cloneCanonical());
  assert.equal(report.ok, true, report.errors.join('\n'));
  assert.equal(report.pilot.complete, report.pilot.expected);
  assert.equal(report.coverage.tagged.percent, 100);
  assert.equal(report.coverage.explicitIdempotency.percent, 100);
});

test('duplicate operation IDs fail the audit', () => {
  expectError((document) => {
    document.paths['/health/ready'].get.operationId =
      document.paths['/health/live'].get.operationId;
  }, 'is already used by');
});

test('unknown tags fail the audit', () => {
  expectError((document) => {
    document.paths['/health/live'].get.tags = ['Implementation package'];
  }, 'unknown tag');
});

test('missing summaries and Proctor extensions fail the audit', () => {
  expectError((document) => {
    document.paths['/health/live'].get.summary = '';
  }, 'summary is required');
  expectError((document) => {
    delete document.paths['/health/live'].get['x-proctor-auth'];
  }, 'x-proctor-auth is required');
  expectError((document) => {
    delete document.paths['/health/live'].get['x-proctor-error-codes'];
  }, 'x-proctor-error-codes must be an array');
  expectError((document) => {
    delete document.paths['/health/live'].get['x-proctor-idempotency'];
  }, 'x-proctor-idempotency must be none, optional, or required');
});

test('incomplete pilot descriptions, parameters, request bodies, and examples fail', () => {
  expectError((document) => {
    document.paths['/api/v1/discovery'].get.description = 'Too short.';
  }, 'description must contain at least 80 characters');
  expectError((document) => {
    delete document.paths['/api/v1/discovery'].get['x-codeSamples'];
  }, 'at least one x-codeSamples example is required');
  expectError((document) => {
    delete document.paths['/api/v1/exams'].get.parameters[0].description;
  }, 'academic_unit_id: parameter description is required');
  expectError((document) => {
    delete document.components.requestBodies.Login.description;
  }, 'request body description is required');
});
