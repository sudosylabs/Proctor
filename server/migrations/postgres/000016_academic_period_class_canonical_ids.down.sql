-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE classes
    DROP CONSTRAINT IF EXISTS classes_academic_period_id_canonical_check,
    DROP CONSTRAINT IF EXISTS classes_programme_level_id_canonical_check,
    DROP CONSTRAINT IF EXISTS classes_id_canonical_check;

ALTER TABLE academic_periods
    DROP CONSTRAINT IF EXISTS academic_periods_institution_id_canonical_check,
    DROP CONSTRAINT IF EXISTS academic_periods_id_canonical_check;
