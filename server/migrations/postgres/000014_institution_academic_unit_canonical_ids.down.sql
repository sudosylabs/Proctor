-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE academic_units
    DROP CONSTRAINT IF EXISTS academic_units_parent_id_canonical_check,
    DROP CONSTRAINT IF EXISTS academic_units_institution_id_canonical_check,
    DROP CONSTRAINT IF EXISTS academic_units_id_canonical_check;

ALTER TABLE institutions
    DROP CONSTRAINT IF EXISTS institutions_id_canonical_check;
