import assert from 'node:assert/strict';
import test from 'node:test';

import {auditDocumentationCoverage} from './documentation-coverage.mjs';

const openapi = {
  tags: [{name: 'System'}, {name: 'Authentication'}],
  paths: {
    '/health/live': {get: {operationId: 'getLiveness'}},
    '/api/v1/auth/login': {post: {operationId: 'login'}},
  },
};

const completeCoverage = {
  version: 1,
  productAreas: [
    {tag: 'System', page: 'docs/system.mdx'},
    {tag: 'Authentication', page: 'docs/auth.mdx'},
  ],
  workflows: [{
    id: 'start-session',
    phase: 2,
    state: 'planned',
    page: 'docs/auth.mdx',
    operations: ['getLiveness', 'login'],
  }],
};

test('accepts complete product-area and workflow coverage', () => {
  assert.deepEqual(auditDocumentationCoverage(
    openapi,
    completeCoverage,
    new Set(['docs/system.mdx', 'docs/auth.mdx']),
    ['start-session'],
  ), []);
});

test('rejects missing pages, tags, and operation identifiers', () => {
  const coverage = structuredClone(completeCoverage);
  coverage.productAreas.pop();
  coverage.workflows[0].operations[1] = 'removedLogin';
  const errors = auditDocumentationCoverage(
    openapi,
    coverage,
    new Set(['docs/system.mdx']),
    ['start-session'],
  );
  assert.ok(errors.includes('OpenAPI product area "Authentication" has no authored entry page'));
  assert.ok(errors.includes('workflow "start-session" targets missing page "docs/auth.mdx"'));
  assert.ok(errors.includes('workflow "start-session" names unknown operation "removedLogin"'));
});

test('rejects duplicate map entries and invalid workflow metadata', () => {
  const coverage = structuredClone(completeCoverage);
  coverage.productAreas.push({tag: 'System', page: 'docs/system.mdx'});
  coverage.workflows.push(structuredClone(coverage.workflows[0]));
  coverage.workflows[1].phase = 0;
  coverage.workflows[1].state = 'unknown';
  const errors = auditDocumentationCoverage(
    openapi,
    coverage,
    new Set(['docs/system.mdx', 'docs/auth.mdx']),
    ['start-session'],
  );
  assert.ok(errors.includes('product area "System" is mapped more than once'));
  assert.ok(errors.includes('workflow id "start-session" is empty or duplicated'));
  assert.ok(errors.includes('workflow "start-session" has an invalid phase'));
  assert.ok(errors.includes('workflow "start-session" has invalid state "unknown"'));
});

test('rejects removal from the required multi-operation workflow inventory', () => {
  const coverage = structuredClone(completeCoverage);
  coverage.workflows = [];
  const errors = auditDocumentationCoverage(
    openapi,
    coverage,
    new Set(['docs/system.mdx', 'docs/auth.mdx']),
    ['start-session'],
  );
  assert.ok(errors.includes('required multi-operation workflow "start-session" is missing'));
});

test('rejects workflows absent from the expected inventory', () => {
  const errors = auditDocumentationCoverage(
    openapi,
    completeCoverage,
    new Set(['docs/system.mdx', 'docs/auth.mdx']),
    [],
  );
  assert.ok(errors.includes('workflow "start-session" is absent from the expected workflow inventory'));
});
