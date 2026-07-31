-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE user_tokens
    DROP CONSTRAINT IF EXISTS user_tokens_target_not_empty,
    DROP COLUMN IF EXISTS target;
