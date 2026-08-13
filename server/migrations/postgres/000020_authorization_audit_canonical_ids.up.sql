-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Validate authorization and audit identity graphs before making canonical
-- 26-character z-base-32 identifiers authoritative in SQL.

ALTER TABLE roles
    ADD CONSTRAINT roles_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE role_bindings
    ADD CONSTRAINT role_bindings_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT role_bindings_user_id_canonical_check CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT role_bindings_role_id_canonical_check CHECK (role_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT role_bindings_scope_id_canonical_check CHECK (scope_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT audit_events_actor_id_canonical_check CHECK (actor_id IS NULL OR actor_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT audit_events_session_id_canonical_check CHECK (session_id IS NULL OR session_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT audit_events_resource_id_canonical_check CHECK (resource_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT audit_events_scope_id_canonical_check CHECK (scope_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
