import {auditDesignSystem} from '../design-system/index.mjs';

const failures = await auditDesignSystem();
if (failures.length > 0) {
  console.error(failures.join('\n'));
  process.exitCode = 1;
} else {
  console.log('Documentation design system is internally consistent');
}
