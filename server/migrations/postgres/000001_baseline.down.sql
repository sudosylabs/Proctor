-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Rolls back the pre-release schema baseline. Development databases should
-- normally be dropped and recreated rather than rolled back.

DROP TABLE IF EXISTS cluster_discovery_nodes;
DROP TABLE IF EXISTS job_attempts;
DROP TABLE IF EXISTS job_permanent_occurrences;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS installation_states;
DROP TABLE IF EXISTS command_outcomes;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS external_login_states;
DROP TABLE IF EXISTS mfa_recovery_codes;
DROP TABLE IF EXISTS mfa_credentials;
DROP TABLE IF EXISTS user_tokens;
DROP TABLE IF EXISTS personal_access_tokens;
DROP TABLE IF EXISTS session_credentials;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS role_bindings;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS class_members;
ALTER TABLE IF EXISTS exams DROP CONSTRAINT IF EXISTS exams_owner_manager_fkey;
DROP TABLE IF EXISTS exam_starter_workspace_entries;
DROP TABLE IF EXISTS exam_starter_workspace_objects;
DROP TABLE IF EXISTS exam_resources;
DROP TABLE IF EXISTS exam_managers;
DROP TABLE IF EXISTS exam_drafts;
DROP TABLE IF EXISTS exams;
DROP TABLE IF EXISTS academic_unit_members;
DROP TABLE IF EXISTS affiliations;
DROP TABLE IF EXISTS password_credentials;
DROP TABLE IF EXISTS external_identities;
DROP TABLE IF EXISTS file_legal_holds;
DROP TABLE IF EXISTS upload_leases;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS file_renditions;
ALTER TABLE IF EXISTS file_entries DROP CONSTRAINT IF EXISTS file_entries_current_revision_fkey;
DROP TABLE IF EXISTS file_revisions;
DROP TABLE IF EXISTS file_entries;
DROP TABLE IF EXISTS classes;
DROP TABLE IF EXISTS academic_periods;
DROP TABLE IF EXISTS programme_levels;
DROP TABLE IF EXISTS programmes;
DROP TABLE IF EXISTS academic_units;
DROP TABLE IF EXISTS institutions;
