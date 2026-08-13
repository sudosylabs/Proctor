-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Validate the existing membership graph before making the model's canonical
-- 26-character z-base-32 representation authoritative in SQL.

ALTER TABLE academic_unit_members
    ADD CONSTRAINT academic_unit_members_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_unit_members_academic_unit_id_canonical_check
    CHECK (academic_unit_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_unit_members_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE class_members
    ADD CONSTRAINT class_members_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT class_members_class_id_canonical_check
    CHECK (class_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT class_members_academic_period_id_canonical_check
    CHECK (academic_period_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT class_members_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
