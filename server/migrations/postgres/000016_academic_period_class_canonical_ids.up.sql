-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Validate existing Academic Period and Class lineage before making the
-- model's canonical 26-character z-base-32 representation authoritative.

ALTER TABLE academic_periods
    ADD CONSTRAINT academic_periods_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_periods_institution_id_canonical_check
    CHECK (institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE classes
    ADD CONSTRAINT classes_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT classes_programme_level_id_canonical_check
    CHECK (programme_level_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT classes_academic_period_id_canonical_check
    CHECK (academic_period_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
