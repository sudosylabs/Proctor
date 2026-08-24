import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import test from 'node:test';

const require = createRequire(import.meta.url);
const codegen = require('postman-code-generators');
const {Request} = require('postman-collection');

function convert(language, variant, request) {
  return new Promise((resolve, reject) => {
    codegen.convert(language, variant, request, {}, (error, snippet) => {
      if (error) {
        reject(new Error(String(error)));
      } else {
        resolve(snippet);
      }
    });
  });
}

test('the renderer can generate client snippets without dependency install scripts', async () => {
  const request = new Request({
    method: 'GET',
    url: 'https://proctor.example.edu/api/v1/health/live',
  });
  const cases = [
    ['curl', 'cURL'],
    ['javascript', 'Fetch'],
    ['python', 'Requests'],
  ];

  for (const [language, variant] of cases) {
    const snippet = await convert(language, variant, request);
    assert.match(snippet, /proctor\.example\.edu\/api\/v1\/health\/live/);
  }
});
