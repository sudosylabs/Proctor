-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Validate the existing Programme and Programme Level identity graph before
-- making the model's canonical 26-character z-base-32 representation
-- authoritative in SQL.

ALTER TABLE programmes
    ADD CONSTRAINT programmes_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT programmes_academic_unit_id_canonical_check
    CHECK (academic_unit_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE programme_levels
    ADD CONSTRAINT programme_levels_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT programme_levels_programme_id_canonical_check
    CHECK (programme_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
