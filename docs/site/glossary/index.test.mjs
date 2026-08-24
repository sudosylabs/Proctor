import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import test from 'node:test';

import {
  auditGlossary,
  parseGlossary,
  renderGlossaryData,
  renderGlossaryPage,
} from './index.mjs';

test('the tracked glossary is current and valid', async () => {
  assert.deepEqual(await auditGlossary(), []);
});

test('CONTEXT.md produces the complete canonical vocabulary', async () => {
  const terms = parseGlossary(
    await readFile(new URL('../../../CONTEXT.md', import.meta.url), 'utf8'),
  );
  assert.equal(terms.length, 61);
  assert.deepEqual(terms[0], {
    id: 'installation',
    term: 'Installation',
    section: 'Installation',
    definition:
      'One logical deployment of Proctor representing exactly one institution, regardless of how many application processes serve it.',
    avoid: 'Tenant, university instance',
  });
  assert(terms.some((term) => term.id === 'participation-lease'));
  assert(terms.some((term) => term.id === 'personal-access-token'));
});

test('renderers name the authority and remain deterministic', () => {
  const terms = parseGlossary(
    '## Identity\n\n**Principal**:\nThe identity acting now.\n_Avoid_: User\n',
  );
  assert.match(renderGlossaryData(terms), /Generated from CONTEXT\.md/);
  assert.match(renderGlossaryPage(1), /These 1 definitions/);
});

test('duplicate normalized term identifiers are rejected', () => {
  assert.throws(
    () =>
      parseGlossary(
        '## Identity\n\n**User Token**:\nFirst.\n_Avoid_: Session\n\n**User-token**:\nSecond.\n_Avoid_: Session\n',
      ),
    /duplicate glossary identifier user-token/,
  );
});
