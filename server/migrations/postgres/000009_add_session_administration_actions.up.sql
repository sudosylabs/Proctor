-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

UPDATE roles
SET permissions = (
        SELECT ARRAY(
            SELECT DISTINCT permission
            FROM unnest(
                roles.permissions ||
                ARRAY['session.view', 'session.manage']
            ) AS permission
            ORDER BY permission
        )
    ),
    update_at = floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint
WHERE name = 'system_admin'
  AND built_in
  AND delete_at = 0;
