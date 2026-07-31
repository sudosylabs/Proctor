-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

-- Purpose-specific credentials must remain bound to the email address for
-- which they were issued. Existing tokens predate this invariant and are
-- invalidated rather than guessed or migrated unsafely.
ALTER TABLE user_tokens
    ADD COLUMN target varchar(254) NOT NULL DEFAULT 'invalidated@migration.invalid';

UPDATE user_tokens
   SET update_at = GREATEST(update_at, create_at),
       consumed_at = CASE
           WHEN consumed_at = 0 THEN GREATEST(update_at, create_at)
           ELSE consumed_at
       END;

ALTER TABLE user_tokens
    ALTER COLUMN target DROP DEFAULT,
    ADD CONSTRAINT user_tokens_target_not_empty CHECK (target <> '');
