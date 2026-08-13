-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE affiliations
    DROP CONSTRAINT IF EXISTS affiliations_user_id_canonical_check,
    DROP CONSTRAINT IF EXISTS affiliations_id_canonical_check;
