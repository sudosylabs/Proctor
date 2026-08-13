-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Validate the existing Institution and Academic Unit identity graph before
-- making the model's canonical z-base-32 representation authoritative in SQL.

ALTER TABLE institutions
    ADD CONSTRAINT institutions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE academic_units
    ADD CONSTRAINT academic_units_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_units_institution_id_canonical_check
    CHECK (institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_units_parent_id_canonical_check
    CHECK (parent_id IS NULL OR parent_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
