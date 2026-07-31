-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

UPDATE roles
SET permissions = array_remove(
        array_remove(permissions, 'session.view'),
        'session.manage'
    ),
    update_at = floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint
WHERE name = 'system_admin'
  AND built_in
  AND delete_at = 0;
