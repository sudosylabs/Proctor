import assert from 'node:assert/strict';
import test from 'node:test';
import {insertApiContractPanel} from './openapi-reference-generation.mjs';
import {verifyOpenapiReference} from './openapi-reference.mjs';

const operation = {
  operationId: 'getLiveness',
  summary: 'Check liveness',
  tags: ['System'],
  security: [],
  'x-proctor-auth': 'public',
  'x-proctor-error-codes': ['system.internal'],
  'x-proctor-idempotency': 'none',
};

const specification = {
  tags: [{name: 'System'}],
  paths: {'/health/live': {get: operation}},
};

function generatedPage(overrides = {}) {
  const api = {
    ...operation,
    method: 'get',
    path: '/health/live',
    extensions: [
      {key: 'x-proctor-auth', value: operation['x-proctor-auth']},
      {key: 'x-proctor-error-codes', value: operation['x-proctor-error-codes']},
      {key: 'x-proctor-idempotency', value: operation['x-proctor-idempotency']},
    ],
    ...overrides,
  };
  return {
    name: 'get-liveness.api.mdx',
    source:
      `---\nid: get-liveness\napi: ${JSON.stringify(api)}\n` +
      'hide_send_button: true\n---\n\n<ApiContractPanel />\n',
  };
}

const tagPages = [
  {name: 'system.tag.mdx', source: '---\nid: system\ntitle: "System"\n---\n'},
];
const sidebar = `
  id: "reference/get-liveness",
  className: "api-method get",
`;

test('accepts a complete generated reference', () => {
  assert.deepEqual(
    verifyOpenapiReference({
      specification,
      pages: [generatedPage()],
      tagPages,
      sidebar,
    }),
    {operationCount: 1, tagCount: 1},
  );
});

test('rejects a missing endpoint page', () => {
  assert.throws(
    () => verifyOpenapiReference({specification, pages: [], tagPages, sidebar: ''}),
    /getLiveness: generated endpoint page is missing/,
  );
});

test('rejects changed Proctor metadata', () => {
  const page = generatedPage({
    'x-proctor-auth': 'principal_required',
    extensions: [
      {key: 'x-proctor-auth', value: 'principal_required'},
      {key: 'x-proctor-error-codes', value: operation['x-proctor-error-codes']},
      {key: 'x-proctor-idempotency', value: operation['x-proctor-idempotency']},
    ],
  });

  assert.throws(
    () =>
      verifyOpenapiReference({specification, pages: [page], tagPages, sidebar}),
    /generated x-proctor-auth differs from the canonical operation/,
  );
});

test('places the contract panel after the operation introduction', () => {
  const source =
    '---\nid: example\n---\n\nimport One from "one";\n\n# Title\n\nDescription.\n\n<ParamsDetails>\n</ParamsDetails>\n';
  const result = insertApiContractPanel(source);

  assert.match(
    result,
    /Description\.\n\n<ApiContractPanel \/>\n\n<ParamsDetails>/,
  );
  assert.equal(insertApiContractPanel(result), result);
});

test('rejects generated output without a request-details seam', () => {
  assert.throws(
    () => insertApiContractPanel('---\nid: broken\n---\n\n# Title\n', 'broken.api.mdx'),
    /broken\.api\.mdx: could not locate the generated request-details seam/,
  );
});
