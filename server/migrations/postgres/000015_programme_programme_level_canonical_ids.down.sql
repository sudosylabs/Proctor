-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE programme_levels
    DROP CONSTRAINT IF EXISTS programme_levels_programme_id_canonical_check,
    DROP CONSTRAINT IF EXISTS programme_levels_id_canonical_check;

ALTER TABLE programmes
    DROP CONSTRAINT IF EXISTS programmes_academic_unit_id_canonical_check,
    DROP CONSTRAINT IF EXISTS programmes_id_canonical_check;
