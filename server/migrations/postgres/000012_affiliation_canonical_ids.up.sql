-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Validate the existing Affiliation identity graph before making the model's
-- canonical 26-character z-base-32 representation authoritative in SQL.

ALTER TABLE affiliations
    ADD CONSTRAINT affiliations_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT affiliations_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
