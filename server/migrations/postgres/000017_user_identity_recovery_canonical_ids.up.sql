-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Enforce canonical identifiers for durable identity and recovery entities.
-- Credential hashes, provider subjects, and protocol values are intentionally
-- excluded because they are not entity identifiers.

ALTER TABLE users
    ADD CONSTRAINT users_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT users_default_profile_picture_file_id_canonical_check
    CHECK (default_profile_picture_file_id IS NULL OR default_profile_picture_file_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT users_custom_profile_picture_file_id_canonical_check
    CHECK (custom_profile_picture_file_id IS NULL OR custom_profile_picture_file_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE external_identities
    ADD CONSTRAINT external_identities_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT external_identities_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE password_credentials
    ADD CONSTRAINT password_credentials_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT password_credentials_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE external_login_states
    ADD CONSTRAINT external_login_states_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE user_tokens
    ADD CONSTRAINT user_tokens_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT user_tokens_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
