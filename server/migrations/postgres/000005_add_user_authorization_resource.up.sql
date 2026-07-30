-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE audit_events
    DROP CONSTRAINT audit_events_resource_type_check;

ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_resource_type_check
    CHECK (resource_type IN ('institution', 'academic_unit', 'class', 'user'));

UPDATE roles
SET permissions = (
        SELECT ARRAY(
            SELECT DISTINCT permission
            FROM unnest(roles.permissions || ARRAY['user.view', 'user.manage']) AS permission
            ORDER BY permission
        )
    ),
    update_at = floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint
WHERE name = 'system_admin'
  AND built_in
  AND delete_at = 0;
