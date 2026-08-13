-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE class_members
    DROP CONSTRAINT IF EXISTS class_members_user_id_canonical_check,
    DROP CONSTRAINT IF EXISTS class_members_academic_period_id_canonical_check,
    DROP CONSTRAINT IF EXISTS class_members_class_id_canonical_check,
    DROP CONSTRAINT IF EXISTS class_members_id_canonical_check;

ALTER TABLE academic_unit_members
    DROP CONSTRAINT IF EXISTS academic_unit_members_user_id_canonical_check,
    DROP CONSTRAINT IF EXISTS academic_unit_members_academic_unit_id_canonical_check,
    DROP CONSTRAINT IF EXISTS academic_unit_members_id_canonical_check;
