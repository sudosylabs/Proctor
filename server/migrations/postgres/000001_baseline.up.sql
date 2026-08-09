-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Pre-release schema baseline. Existing development databases must
-- be recreated; there is no upgrade path from earlier bigint-millisecond
-- migrations. Temporal columns use timestamptz. Soft archive uses nullable
-- archived_at. Open-ended intervals and optional lifecycle instants use NULL
-- rather than integer zero sentinels. Revision columns match domain optimistic
-- concurrency where applicable.

-- ---------------------------------------------------------------------------
-- Structural academic domain
-- ---------------------------------------------------------------------------

CREATE TABLE institutions (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    singleton boolean NOT NULL DEFAULT true CHECK (singleton),
    CONSTRAINT institutions_name_key UNIQUE (name),
    CONSTRAINT institutions_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX institutions_singleton_key
    ON institutions (singleton) WHERE archived_at IS NULL;

CREATE TABLE academic_units (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    institution_id varchar(26) NOT NULL REFERENCES institutions(id),
    parent_id varchar(26) REFERENCES academic_units(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    CONSTRAINT academic_units_parent_id_not_self_check CHECK (parent_id IS NULL OR parent_id <> id),
    CONSTRAINT academic_units_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX academic_units_institution_id_parent_id_name_key
    ON academic_units (institution_id, name) WHERE archived_at IS NULL;
CREATE INDEX academic_units_institution_id_parent_id_idx
    ON academic_units (parent_id) WHERE archived_at IS NULL;

CREATE TABLE programmes (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    academic_unit_id varchar(26) NOT NULL REFERENCES academic_units(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    CONSTRAINT programmes_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX programmes_academic_unit_id_name_key
    ON programmes (academic_unit_id, name) WHERE archived_at IS NULL;

CREATE TABLE programme_levels (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    programme_id varchar(26) NOT NULL REFERENCES programmes(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    CONSTRAINT programme_levels_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX programme_levels_programme_id_name_key
    ON programme_levels (programme_id, name) WHERE archived_at IS NULL;

CREATE TABLE academic_periods (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    institution_id varchar(26) NOT NULL REFERENCES institutions(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    start_at timestamptz NOT NULL,
    end_at timestamptz NOT NULL,
    CONSTRAINT academic_periods_start_at_end_at_check CHECK (end_at > start_at),
    CONSTRAINT academic_periods_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX academic_periods_institution_id_name_key
    ON academic_periods (institution_id, name) WHERE archived_at IS NULL;

CREATE TABLE classes (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    programme_level_id varchar(26) NOT NULL REFERENCES programme_levels(id),
    academic_period_id varchar(26) NOT NULL REFERENCES academic_periods(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    CONSTRAINT classes_id_academic_period_id_key UNIQUE (id, academic_period_id),
    CONSTRAINT classes_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX classes_programme_level_id_academic_period_id_name_key
    ON classes (programme_level_id, academic_period_id, name) WHERE archived_at IS NULL;
CREATE INDEX classes_academic_period_id_idx
    ON classes (academic_period_id) WHERE archived_at IS NULL;

-- ---------------------------------------------------------------------------
-- Identity domain
-- ---------------------------------------------------------------------------

CREATE TABLE file_entries (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    current_revision_id varchar(26),
    indexing_policy varchar(16) NOT NULL CHECK (indexing_policy IN ('none', 'metadata', 'content')),
    CONSTRAINT file_entries_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE TABLE file_revisions (
    id varchar(26) PRIMARY KEY,
    file_entry_id varchar(26) NOT NULL REFERENCES file_entries(id),
    created_at timestamptz NOT NULL,
    availability varchar(16) NOT NULL CHECK (availability IN ('pending', 'available', 'quarantined', 'rejected')),
    indexing_state varchar(16) NOT NULL CHECK (indexing_state IN ('not_required', 'pending', 'ready', 'failed')),
    UNIQUE (id, file_entry_id)
);

ALTER TABLE file_entries ADD CONSTRAINT file_entries_current_revision_fkey
    FOREIGN KEY (current_revision_id, id) REFERENCES file_revisions(id, file_entry_id);

CREATE TABLE file_renditions (
    id varchar(26) PRIMARY KEY,
    file_revision_id varchar(26) NOT NULL REFERENCES file_revisions(id),
    created_at timestamptz NOT NULL,
    name varchar(64) NOT NULL,
    media_type varchar(255) NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0),
    sha256 char(64) NOT NULL,
    UNIQUE (file_revision_id, name)
);

CREATE TABLE users (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    username varchar(64) NOT NULL,
    email varchar(254) NOT NULL,
    email_verified boolean NOT NULL DEFAULT false,
    display_name varchar(512) NOT NULL DEFAULT '',
    first_name varchar(256) NOT NULL DEFAULT '',
    last_name varchar(256) NOT NULL DEFAULT '',
    locale varchar(35) NOT NULL,
    timezone varchar(64) NOT NULL,
    last_login_at timestamptz,
    last_activity_at timestamptz,
    disabled_at timestamptz,
    default_profile_picture_seed char(64) NOT NULL,
    default_profile_picture_file_id varchar(26) REFERENCES file_entries(id),
    custom_profile_picture_file_id varchar(26) REFERENCES file_entries(id),
    profile_picture_changed_at timestamptz,
    CONSTRAINT users_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX users_username_key ON users (username) WHERE archived_at IS NULL;
CREATE UNIQUE INDEX users_email_key ON users (email) WHERE archived_at IS NULL;

CREATE TABLE upload_leases (
    id varchar(26) PRIMARY KEY,
    file_revision_id varchar(26) NOT NULL UNIQUE REFERENCES file_revisions(id),
    created_by_user_id varchar(26) NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    CONSTRAINT upload_leases_lifecycle_check CHECK (updated_at >= created_at AND expires_at > created_at)
);

CREATE TABLE external_identities (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    provider varchar(64) NOT NULL,
    subject varchar(2048) NOT NULL,
    last_seen_at timestamptz,
    CONSTRAINT external_identities_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX external_identities_provider_subject_key
    ON external_identities (provider, subject) WHERE archived_at IS NULL;

CREATE TABLE password_credentials (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    password_hash varchar(1024) NOT NULL,
    password_changed_at timestamptz NOT NULL,
    CONSTRAINT password_credentials_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX password_credentials_user_id_key
    ON password_credentials (user_id) WHERE archived_at IS NULL;

CREATE TABLE affiliations (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    user_id varchar(26) NOT NULL REFERENCES users(id),
    kind varchar(32) NOT NULL CHECK (kind IN ('student', 'teacher', 'staff', 'external')),
    start_at timestamptz NOT NULL,
    end_at timestamptz,
    CONSTRAINT affiliations_start_at_end_at_check CHECK (end_at IS NULL OR end_at > start_at),
    CONSTRAINT affiliations_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX affiliations_user_id_kind_key
    ON affiliations (user_id, kind) WHERE archived_at IS NULL AND end_at IS NULL;

CREATE TABLE academic_unit_members (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    academic_unit_id varchar(26) NOT NULL REFERENCES academic_units(id),
    user_id varchar(26) NOT NULL REFERENCES users(id),
    start_at timestamptz NOT NULL,
    end_at timestamptz,
    CONSTRAINT academic_unit_members_start_at_end_at_check CHECK (end_at IS NULL OR end_at > start_at),
    CONSTRAINT academic_unit_members_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX academic_unit_members_academic_unit_id_user_id_key
    ON academic_unit_members (academic_unit_id, user_id)
    WHERE archived_at IS NULL AND end_at IS NULL;

CREATE TABLE class_members (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    class_id varchar(26) NOT NULL,
    academic_period_id varchar(26) NOT NULL REFERENCES academic_periods(id),
    user_id varchar(26) NOT NULL REFERENCES users(id),
    start_at timestamptz NOT NULL,
    end_at timestamptz,
    CONSTRAINT class_members_start_at_end_at_check CHECK (end_at IS NULL OR end_at > start_at),
    CONSTRAINT class_members_class_id_academic_period_id_fkey
        FOREIGN KEY (class_id, academic_period_id)
        REFERENCES classes(id, academic_period_id),
    CONSTRAINT class_members_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX class_members_one_active_class_per_period_key
    ON class_members (user_id, academic_period_id)
    WHERE archived_at IS NULL AND end_at IS NULL;
CREATE INDEX class_members_class_id_idx
    ON class_members (class_id) WHERE archived_at IS NULL;

CREATE TABLE roles (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    permissions text[] NOT NULL DEFAULT '{}',
    built_in boolean NOT NULL DEFAULT false,
    CONSTRAINT roles_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX roles_name_key ON roles (name) WHERE archived_at IS NULL;

CREATE TABLE role_bindings (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    role_id varchar(26) NOT NULL REFERENCES roles(id),
    scope_type varchar(32) NOT NULL CHECK (scope_type IN ('institution', 'academic_unit', 'class')),
    scope_id varchar(26) NOT NULL,
    start_at timestamptz NOT NULL,
    end_at timestamptz,
    CONSTRAINT role_bindings_start_at_end_at_check CHECK (end_at IS NULL OR end_at > start_at),
    CONSTRAINT role_bindings_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX role_bindings_user_id_role_id_scope_type_scope_id_key
    ON role_bindings (user_id, role_id, scope_type, scope_id)
    WHERE archived_at IS NULL AND end_at IS NULL;

CREATE INDEX role_bindings_user_id_scope_type_scope_id_idx
    ON role_bindings (user_id, scope_type, scope_id)
    WHERE archived_at IS NULL AND end_at IS NULL;

CREATE INDEX role_bindings_user_id_start_at_end_at_idx
    ON role_bindings (user_id, start_at, end_at)
    WHERE archived_at IS NULL;

CREATE TABLE sessions (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    client_type varchar(16) NOT NULL CHECK (client_type IN ('desktop', 'cli', 'web')),
    device_id varchar(128) NOT NULL DEFAULT '',
    device_name varchar(512) NOT NULL DEFAULT '',
    authentication_method varchar(64) NOT NULL,
    authentication_strength varchar(32) NOT NULL
        CHECK (authentication_strength IN ('single_factor', 'multi_factor')),
    authenticated_at timestamptz NOT NULL,
    mfa_completed_at timestamptz,
    last_activity_at timestamptz NOT NULL,
    idle_expires_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revocation_reason varchar(1024) NOT NULL DEFAULT '',
    CONSTRAINT sessions_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE INDEX sessions_user_id_last_activity_at_idx
    ON sessions (user_id) WHERE archived_at IS NULL AND revoked_at IS NULL;
CREATE INDEX sessions_expires_at_idx
    ON sessions (expires_at) WHERE archived_at IS NULL AND revoked_at IS NULL;

CREATE TABLE session_credentials (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    session_id varchar(26) NOT NULL REFERENCES sessions(id),
    kind varchar(16) NOT NULL CHECK (kind IN ('access', 'refresh')),
    token_hash char(64) NOT NULL,
    family_id varchar(26),
    parent_id varchar(26) REFERENCES session_credentials(id),
    replaced_by_id varchar(26) REFERENCES session_credentials(id),
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT session_credentials_parent_id_not_self_check CHECK (parent_id IS NULL OR parent_id <> id),
    CONSTRAINT session_credentials_replaced_by_id_not_self_check
        CHECK (replaced_by_id IS NULL OR replaced_by_id <> id),
    CONSTRAINT session_credentials_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX session_credentials_token_hash_key ON session_credentials (token_hash);
CREATE INDEX session_credentials_family_id_kind_idx
    ON session_credentials (family_id)
    WHERE family_id IS NOT NULL AND archived_at IS NULL;

CREATE TABLE personal_access_tokens (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    description varchar(1024) NOT NULL,
    token_hash char(64) NOT NULL,
    scopes text[] NOT NULL,
    academic_unit_id varchar(26) REFERENCES academic_units(id),
    expires_at timestamptz NOT NULL,
    last_used_at timestamptz,
    disabled_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT personal_access_tokens_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX personal_access_tokens_token_hash_key
    ON personal_access_tokens (token_hash);
CREATE INDEX personal_access_tokens_user_id_created_at_idx
    ON personal_access_tokens (user_id)
    WHERE archived_at IS NULL AND revoked_at IS NULL AND disabled_at IS NULL;

CREATE TABLE user_tokens (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    purpose varchar(32) NOT NULL CHECK (purpose IN ('password_reset', 'email_verification')),
    token_hash char(64) NOT NULL,
    target varchar(254) NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT user_tokens_target_not_empty_check CHECK (target <> ''),
    CONSTRAINT user_tokens_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX user_tokens_token_hash_key ON user_tokens (token_hash);
CREATE INDEX user_tokens_user_id_purpose_created_at_idx
    ON user_tokens (user_id, purpose)
    WHERE archived_at IS NULL AND consumed_at IS NULL;

CREATE TABLE mfa_credentials (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    state varchar(16) NOT NULL CHECK (state IN ('pending', 'active')),
    encrypted_secret varchar(4096) NOT NULL,
    encryption_key_id char(16) NOT NULL,
    pending_expires_at timestamptz,
    activated_at timestamptz,
    last_used_time_step bigint NOT NULL DEFAULT 0,
    CONSTRAINT mfa_credentials_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX mfa_credentials_user_id_key
    ON mfa_credentials (user_id) WHERE archived_at IS NULL;

CREATE TABLE mfa_recovery_codes (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    code_hash char(64) NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT mfa_recovery_codes_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX mfa_recovery_codes_code_hash_key ON mfa_recovery_codes (code_hash);
CREATE INDEX mfa_recovery_codes_user_id_idx
    ON mfa_recovery_codes (user_id)
    WHERE archived_at IS NULL AND consumed_at IS NULL;

CREATE TABLE external_login_states (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    provider varchar(64) NOT NULL,
    state_hash char(64) NOT NULL,
    binding_hash char(64) NOT NULL,
    return_to varchar(2048) NOT NULL,
    client_type varchar(32) NOT NULL CHECK (client_type IN ('desktop', 'web')),
    device_id varchar(128) NOT NULL DEFAULT '',
    device_name varchar(512) NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CONSTRAINT external_login_states_created_at_expires_at_check
        CHECK (
            expires_at > created_at AND
            (consumed_at IS NULL OR (consumed_at >= created_at AND consumed_at < expires_at))
        )
);

CREATE UNIQUE INDEX external_login_states_state_hash_key
    ON external_login_states (state_hash);

CREATE INDEX external_login_states_expires_at_idx
    ON external_login_states (expires_at);

-- ---------------------------------------------------------------------------
-- Authorization audit and installation marker
-- ---------------------------------------------------------------------------

CREATE TABLE audit_events (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    actor_id varchar(26) REFERENCES users(id),
    session_id varchar(26) REFERENCES sessions(id),
    action varchar(128) NOT NULL,
    resource_type varchar(32) NOT NULL
        CHECK (resource_type IN ('institution', 'academic_unit', 'class', 'user')),
    resource_id varchar(26) NOT NULL,
    scope_type varchar(32) NOT NULL
        CHECK (scope_type IN ('institution', 'academic_unit', 'class')),
    scope_id varchar(26) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('attempt', 'success', 'fail')),
    request_id varchar(128) NOT NULL DEFAULT '',
    node_id varchar(128) NOT NULL,
    client_type varchar(32) NOT NULL DEFAULT '',
    authentication_method varchar(64) NOT NULL DEFAULT '',
    ip_address varchar(64) NOT NULL DEFAULT '',
    user_agent varchar(512) NOT NULL DEFAULT '',
    error_code varchar(128) NOT NULL DEFAULT '',
    parameters jsonb,
    prior_state jsonb,
    result jsonb,
    CONSTRAINT audit_events_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE INDEX audit_events_created_at_id_idx ON audit_events (created_at DESC, id DESC);
CREATE INDEX audit_events_actor_id_created_at_id_idx
    ON audit_events (actor_id, created_at DESC, id DESC)
    WHERE actor_id IS NOT NULL;
CREATE INDEX audit_events_action_created_at_id_idx
    ON audit_events (action, created_at DESC, id DESC);
CREATE INDEX audit_events_resource_type_resource_id_created_at_id_idx
    ON audit_events (resource_type, resource_id, created_at DESC, id DESC);
CREATE INDEX audit_events_status_created_at_id_idx
    ON audit_events (created_at, id) WHERE status = 'attempt';

CREATE TABLE installation_states (
    singleton smallint PRIMARY KEY CHECK (singleton = 1),
    initialized_at timestamptz NOT NULL,
    institution_id varchar(26) NOT NULL REFERENCES institutions(id),
    administrator_user_id varchar(26) NOT NULL REFERENCES users(id)
);

-- ---------------------------------------------------------------------------
-- Cluster bootstrap discovery (disposable leases; not a message bus)
-- ---------------------------------------------------------------------------

CREATE TABLE cluster_discovery_nodes (
    node_id varchar(128) PRIMARY KEY,
    advertise_address varchar(512) NOT NULL,
    server_version varchar(128) NOT NULL,
    protocol_min integer NOT NULL,
    protocol_max integer NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT cluster_discovery_nodes_protocol_range_check
        CHECK (protocol_min > 0 AND protocol_max >= protocol_min),
    CONSTRAINT cluster_discovery_nodes_lifetime_check
        CHECK (expires_at > updated_at),
    CONSTRAINT cluster_discovery_nodes_advertise_address_check
        CHECK (char_length(btrim(advertise_address)) > 0)
);

CREATE INDEX cluster_discovery_nodes_expires_at_idx
    ON cluster_discovery_nodes (expires_at);

CREATE INDEX cluster_discovery_nodes_expires_at_node_id_idx
    ON cluster_discovery_nodes (expires_at, node_id);
