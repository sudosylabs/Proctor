import {auditGlossary} from '../glossary/index.mjs';

const failures = await auditGlossary();
if (failures.length > 0) {
  console.error(failures.join('\n'));
  process.exitCode = 1;
} else {
  console.log('Public glossary data and curated term markup are valid');
}
