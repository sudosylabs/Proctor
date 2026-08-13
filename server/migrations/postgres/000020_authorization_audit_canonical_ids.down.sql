-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE audit_events
    DROP CONSTRAINT IF EXISTS audit_events_scope_id_canonical_check,
    DROP CONSTRAINT IF EXISTS audit_events_resource_id_canonical_check,
    DROP CONSTRAINT IF EXISTS audit_events_session_id_canonical_check,
    DROP CONSTRAINT IF EXISTS audit_events_actor_id_canonical_check,
    DROP CONSTRAINT IF EXISTS audit_events_id_canonical_check;

ALTER TABLE role_bindings
    DROP CONSTRAINT IF EXISTS role_bindings_scope_id_canonical_check,
    DROP CONSTRAINT IF EXISTS role_bindings_role_id_canonical_check,
    DROP CONSTRAINT IF EXISTS role_bindings_user_id_canonical_check,
    DROP CONSTRAINT IF EXISTS role_bindings_id_canonical_check;

ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_id_canonical_check;
