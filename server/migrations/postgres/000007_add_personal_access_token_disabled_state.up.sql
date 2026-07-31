-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE personal_access_tokens
    ADD COLUMN disabled_at bigint NOT NULL DEFAULT 0;

DROP INDEX personal_access_tokens_active_user_idx;
CREATE INDEX personal_access_tokens_active_user_idx
    ON personal_access_tokens (user_id)
    WHERE delete_at = 0 AND revoked_at = 0 AND disabled_at = 0;
