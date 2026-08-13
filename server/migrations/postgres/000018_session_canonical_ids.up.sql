-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Validate the existing Session identity graph before making the model's
-- canonical 26-character z-base-32 representation authoritative in SQL.

ALTER TABLE sessions
    ADD CONSTRAINT sessions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT sessions_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE session_credentials
    ADD CONSTRAINT session_credentials_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT session_credentials_session_id_canonical_check
    CHECK (session_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT session_credentials_family_id_canonical_check
    CHECK (family_id IS NULL OR family_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT session_credentials_parent_id_canonical_check
    CHECK (parent_id IS NULL OR parent_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT session_credentials_replaced_by_id_canonical_check
    CHECK (replaced_by_id IS NULL OR replaced_by_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
