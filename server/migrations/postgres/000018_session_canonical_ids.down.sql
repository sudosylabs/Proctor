-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE session_credentials
    DROP CONSTRAINT IF EXISTS session_credentials_replaced_by_id_canonical_check,
    DROP CONSTRAINT IF EXISTS session_credentials_parent_id_canonical_check,
    DROP CONSTRAINT IF EXISTS session_credentials_family_id_canonical_check,
    DROP CONSTRAINT IF EXISTS session_credentials_session_id_canonical_check,
    DROP CONSTRAINT IF EXISTS session_credentials_id_canonical_check;

ALTER TABLE sessions
    DROP CONSTRAINT IF EXISTS sessions_user_id_canonical_check,
    DROP CONSTRAINT IF EXISTS sessions_id_canonical_check;
