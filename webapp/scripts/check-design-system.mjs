// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

import {auditDesignSystem} from '../design-system/index.mjs';

const failures = await auditDesignSystem();
if (failures.length > 0) {
  for (const failure of failures) {
    process.stderr.write(`${failure}\n`);
  }
  process.exitCode = 1;
}
