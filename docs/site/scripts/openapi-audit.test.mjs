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
  assert.equal(report.version, 2);
  assert.equal(report.coverage.descriptions.percent, 100);
  assert.equal(report.coverage.parameterDescriptions.percent, 100);
  assert.equal(report.coverage.requestBodyDescriptions.percent, 100);
  assert.equal(report.coverage.mutationExamples.percent, 100);
  assert.equal(report.coverage.tagProblemExamples.percent, 100);
  assert.equal(report.coverage.tagSuccessExamples.percent, 100);
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

test('short behavior and tag descriptions fail the full-content gate', () => {
  expectError((document) => {
    document.paths['/api/v1/discovery'].get.description = 'Too short.';
  }, 'behavior description must contain at least 80 characters');
  expectError((document) => {
    document.tags.find((tag) => tag.name === 'System').description = 'Too short.';
  }, 'description must contain at least 40 characters');
});

test('missing parameter and request-body descriptions fail across all operations', () => {
  expectError((document) => {
    delete document.paths['/api/v1/exams'].get.parameters[0].description;
  }, 'academic_unit_id: parameter description is required');
  expectError((document) => {
    delete document.components.requestBodies.Login.description;
  }, 'request body description is required');
});

test('mutations require a valid media example or executable-style code sample', () => {
  expectError((document) => {
    delete document.components.requestBodies.ConfigureExamDraftFocusLoss.content[
      'application/json'
    ].example;
  }, 'mutation request example is required');
  expectError((document) => {
    delete document.paths['/api/v1/auth/logout'].post['x-codeSamples'];
  }, 'mutation request example is required');
});

test('each product area requires representative success and Problem Details examples', () => {
  expectError((document) => {
    for (const pathItem of Object.values(document.paths)) {
      for (const operation of Object.values(pathItem)) {
        if (!operation?.tags?.includes('System')) {
          continue;
        }
        for (const [status, responseReference] of Object.entries(operation.responses ?? {})) {
          if (!/^2\d\d$/.test(status)) {
            continue;
          }
          const response = responseReference.$ref?.startsWith('#/components/responses/')
            ? document.components.responses[responseReference.$ref.split('/').at(-1)]
            : responseReference;
          for (const mediaType of Object.values(response?.content ?? {})) {
            delete mediaType.example;
            delete mediaType.examples;
          }
        }
      }
    }
  }, 'tag "System": at least one representative success response example is required');
  expectError((document) => {
    for (const response of Object.values(document.components.responses)) {
      const problem = response.content?.['application/problem+json'];
      if (problem) {
        delete problem.example;
        delete problem.examples;
      }
    }
  }, 'representative Problem Details example is required');
});
