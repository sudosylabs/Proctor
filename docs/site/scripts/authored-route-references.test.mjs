import assert from 'node:assert/strict';
import test from 'node:test';

import {auditAuthoredRouteReferences} from './authored-route-references.mjs';

const openapi = {
  paths: {
    '/api/v1/discovery': {get: {}},
    '/api/v1/exams': {post: {}},
    '/api/v1/exams/{exam_id}': {get: {}, patch: {}},
  },
};

test('accepts current literal, placeholder, and curl operation references', () => {
  const errors = auditAuthoredRouteReferences(openapi, [{
    name: 'guide.mdx',
    source: `
GET /api/v1/discovery

\`PATCH /api/v1/exams/{exam_id}\`

\`\`\`sh
curl --request PATCH \\
  --url https://proctor.example.edu/api/v1/exams/<exam-id>
\`\`\`
`,
  }]);
  assert.deepEqual(errors, []);
});

test('rejects stale paths and wrong methods with source locations', () => {
  const errors = auditAuthoredRouteReferences(openapi, [{
    name: 'guide.mdx',
    source: 'GET /api/v1/access-policy/discovery\nPOST /api/v1/discovery\n',
  }]);
  assert.deepEqual(errors, [
    'guide.mdx:1: /api/v1/access-policy/discovery does not match a current OpenAPI path',
    'guide.mdx:2: POST /api/v1/discovery does not match a current OpenAPI operation',
  ]);
});

test('ignores API-shaped paths on explicitly external origins', () => {
  const errors = auditAuthoredRouteReferences(openapi, [{
    name: 'observability.mdx',
    source: 'http://127.0.0.1:19090/api/v1/query',
  }]);
  assert.deepEqual(errors, []);
});

test('infers curl GET and body-driven POST per command', () => {
  const errors = auditAuthoredRouteReferences(openapi, [{
    name: 'curl-guide.mdx',
    source: [
      '```sh',
      'curl https://proctor.example.edu/api/v1/discovery',
      '',
      'curl --data \'{"name":"Exam"}\' \\',
      '  https://proctor.example.edu/api/v1/exams',
      '',
      'curl --request PATCH \\',
      '  --url https://proctor.example.edu/api/v1/exams/<exam-id>',
      '```',
    ].join('\n'),
  }]);
  assert.deepEqual(errors, []);
});

test('rejects wrong curl-implied methods without sharing another command method', () => {
  const errors = auditAuthoredRouteReferences(openapi, [{
    name: 'curl-guide.mdx',
    source: [
      '```sh',
      'curl --data \'{}\' https://proctor.example.edu/api/v1/discovery',
      'curl https://proctor.example.edu/api/v1/exams',
      'curl --request GET https://proctor.example.edu/api/v1/discovery',
      '```',
    ].join('\n'),
  }]);
  assert.deepEqual(errors, [
    'curl-guide.mdx:2: POST /api/v1/discovery does not match a current OpenAPI operation',
    'curl-guide.mdx:3: GET /api/v1/exams does not match a current OpenAPI operation',
  ]);
});
