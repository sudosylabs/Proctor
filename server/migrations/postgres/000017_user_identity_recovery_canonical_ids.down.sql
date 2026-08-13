-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE user_tokens
    DROP CONSTRAINT IF EXISTS user_tokens_user_id_canonical_check,
    DROP CONSTRAINT IF EXISTS user_tokens_id_canonical_check;

ALTER TABLE external_login_states
    DROP CONSTRAINT IF EXISTS external_login_states_id_canonical_check;

ALTER TABLE password_credentials
    DROP CONSTRAINT IF EXISTS password_credentials_user_id_canonical_check,
    DROP CONSTRAINT IF EXISTS password_credentials_id_canonical_check;

ALTER TABLE external_identities
    DROP CONSTRAINT IF EXISTS external_identities_user_id_canonical_check,
    DROP CONSTRAINT IF EXISTS external_identities_id_canonical_check;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_custom_profile_picture_file_id_canonical_check,
    DROP CONSTRAINT IF EXISTS users_default_profile_picture_file_id_canonical_check,
    DROP CONSTRAINT IF EXISTS users_id_canonical_check;
