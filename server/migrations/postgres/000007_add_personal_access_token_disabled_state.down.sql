-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

DROP INDEX personal_access_tokens_active_user_idx;

ALTER TABLE personal_access_tokens
    DROP COLUMN disabled_at;

CREATE INDEX personal_access_tokens_active_user_idx
    ON personal_access_tokens (user_id)
    WHERE delete_at = 0 AND revoked_at = 0;
