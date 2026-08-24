import {auditSearchIndex} from '../search/index.mjs';

const failures = await auditSearchIndex();
if (failures.length > 0) {
  console.error(failures.join('\n'));
  process.exitCode = 1;
} else {
  console.log('Local documentation search data is current');
}
