-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

UPDATE roles
SET permissions = array_remove(array_remove(permissions, 'user.view'), 'user.manage'),
    update_at = floor(extract(epoch FROM clock_timestamp()) * 1000)::bigint
WHERE name = 'system_admin'
  AND built_in
  AND delete_at = 0;

ALTER TABLE audit_events
    DROP CONSTRAINT audit_events_resource_type_check;

-- Preserve existing user-target audit history during a rollback while
-- restoring the older write contract for all new rows.
ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_resource_type_check
    CHECK (resource_type IN ('institution', 'academic_unit', 'class'))
    NOT VALID;
