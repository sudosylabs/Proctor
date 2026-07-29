CREATE TABLE users (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    username varchar(64) NOT NULL,
    email varchar(254) NOT NULL,
    email_verified boolean NOT NULL DEFAULT false,
    display_name varchar(512) NOT NULL DEFAULT '',
    first_name varchar(256) NOT NULL DEFAULT '',
    last_name varchar(256) NOT NULL DEFAULT '',
    locale varchar(35) NOT NULL,
    timezone varchar(64) NOT NULL,
    last_login_at bigint NOT NULL DEFAULT 0,
    last_activity_at bigint NOT NULL DEFAULT 0,
    disabled_at bigint NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX users_active_username_key ON users (username) WHERE delete_at = 0;
CREATE UNIQUE INDEX users_active_email_key ON users (email) WHERE delete_at = 0;

CREATE TABLE external_identities (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    provider varchar(64) NOT NULL,
    subject varchar(2048) NOT NULL,
    last_seen_at bigint NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX external_identities_active_subject_key
    ON external_identities (provider, subject) WHERE delete_at = 0;

CREATE TABLE password_credentials (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    password_hash varchar(1024) NOT NULL,
    password_changed_at bigint NOT NULL
);

CREATE UNIQUE INDEX password_credentials_active_user_key
    ON password_credentials (user_id) WHERE delete_at = 0;

CREATE TABLE affiliations (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    kind varchar(32) NOT NULL CHECK (kind IN ('student', 'teacher', 'staff', 'external')),
    start_at bigint NOT NULL,
    end_at bigint NOT NULL DEFAULT 0,
    CONSTRAINT affiliations_valid_range CHECK (start_at > 0 AND (end_at = 0 OR end_at > start_at))
);

CREATE UNIQUE INDEX affiliations_active_kind_key
    ON affiliations (user_id, kind) WHERE delete_at = 0 AND end_at = 0;

CREATE TABLE academic_unit_members (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    academic_unit_id varchar(26) NOT NULL REFERENCES academic_units(id),
    user_id varchar(26) NOT NULL REFERENCES users(id),
    start_at bigint NOT NULL,
    end_at bigint NOT NULL DEFAULT 0,
    CONSTRAINT academic_unit_members_valid_range CHECK (start_at > 0 AND (end_at = 0 OR end_at > start_at))
);

CREATE UNIQUE INDEX academic_unit_members_active_key
    ON academic_unit_members (academic_unit_id, user_id) WHERE delete_at = 0 AND end_at = 0;

CREATE TABLE class_members (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    class_id varchar(26) NOT NULL,
    academic_period_id varchar(26) NOT NULL REFERENCES academic_periods(id),
    user_id varchar(26) NOT NULL REFERENCES users(id),
    start_at bigint NOT NULL,
    end_at bigint NOT NULL DEFAULT 0,
    CONSTRAINT class_members_valid_range CHECK (start_at > 0 AND (end_at = 0 OR end_at > start_at)),
    CONSTRAINT class_members_class_period_fkey
        FOREIGN KEY (class_id, academic_period_id)
        REFERENCES classes(id, academic_period_id)
);

CREATE UNIQUE INDEX class_members_one_active_class_per_period_key
    ON class_members (user_id, academic_period_id) WHERE delete_at = 0 AND end_at = 0;
CREATE INDEX class_members_class_id_idx ON class_members (class_id) WHERE delete_at = 0;

CREATE TABLE roles (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    permissions text[] NOT NULL DEFAULT '{}',
    built_in boolean NOT NULL DEFAULT false
);

CREATE UNIQUE INDEX roles_active_name_key ON roles (name) WHERE delete_at = 0;

CREATE TABLE role_bindings (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    role_id varchar(26) NOT NULL REFERENCES roles(id),
    scope_type varchar(32) NOT NULL CHECK (scope_type IN ('institution', 'academic_unit', 'class')),
    scope_id varchar(26) NOT NULL,
    start_at bigint NOT NULL,
    end_at bigint NOT NULL DEFAULT 0,
    CONSTRAINT role_bindings_valid_range CHECK (start_at > 0 AND (end_at = 0 OR end_at > start_at))
);

CREATE INDEX role_bindings_user_scope_idx
    ON role_bindings (user_id, scope_type, scope_id) WHERE delete_at = 0 AND end_at = 0;

CREATE TABLE sessions (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    client_type varchar(16) NOT NULL CHECK (client_type IN ('desktop', 'cli', 'web')),
    device_id varchar(128) NOT NULL DEFAULT '',
    device_name varchar(512) NOT NULL DEFAULT '',
    authentication_method varchar(64) NOT NULL,
    authentication_strength varchar(32) NOT NULL CHECK (authentication_strength IN ('single_factor', 'multi_factor')),
    authenticated_at bigint NOT NULL,
    mfa_completed_at bigint NOT NULL DEFAULT 0,
    last_activity_at bigint NOT NULL,
    idle_expires_at bigint NOT NULL,
    expires_at bigint NOT NULL,
    revoked_at bigint NOT NULL DEFAULT 0,
    revocation_reason varchar(1024) NOT NULL DEFAULT ''
);

CREATE INDEX sessions_active_user_idx ON sessions (user_id) WHERE delete_at = 0 AND revoked_at = 0;
CREATE INDEX sessions_expiry_idx ON sessions (expires_at) WHERE delete_at = 0 AND revoked_at = 0;

CREATE TABLE session_credentials (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    session_id varchar(26) NOT NULL REFERENCES sessions(id),
    kind varchar(16) NOT NULL CHECK (kind IN ('access', 'refresh')),
    token_hash char(64) NOT NULL,
    family_id varchar(26),
    parent_id varchar(26) REFERENCES session_credentials(id),
    replaced_by_id varchar(26) REFERENCES session_credentials(id),
    expires_at bigint NOT NULL,
    used_at bigint NOT NULL DEFAULT 0,
    revoked_at bigint NOT NULL DEFAULT 0,
    CONSTRAINT session_credentials_not_self_parent CHECK (parent_id IS NULL OR parent_id <> id),
    CONSTRAINT session_credentials_not_self_replacement CHECK (replaced_by_id IS NULL OR replaced_by_id <> id)
);

CREATE UNIQUE INDEX session_credentials_token_hash_key ON session_credentials (token_hash);
CREATE INDEX session_credentials_family_idx
    ON session_credentials (family_id) WHERE family_id IS NOT NULL AND delete_at = 0;

CREATE TABLE personal_access_tokens (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    description varchar(1024) NOT NULL,
    token_hash char(64) NOT NULL,
    scopes text[] NOT NULL,
    academic_unit_id varchar(26) REFERENCES academic_units(id),
    expires_at bigint NOT NULL,
    last_used_at bigint NOT NULL DEFAULT 0,
    revoked_at bigint NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX personal_access_tokens_token_hash_key ON personal_access_tokens (token_hash);
CREATE INDEX personal_access_tokens_active_user_idx
    ON personal_access_tokens (user_id) WHERE delete_at = 0 AND revoked_at = 0;

CREATE TABLE user_tokens (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    purpose varchar(32) NOT NULL CHECK (purpose IN ('password_reset', 'email_verification')),
    token_hash char(64) NOT NULL,
    expires_at bigint NOT NULL,
    consumed_at bigint NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX user_tokens_token_hash_key ON user_tokens (token_hash);
CREATE INDEX user_tokens_active_user_purpose_idx
    ON user_tokens (user_id, purpose) WHERE delete_at = 0 AND consumed_at = 0;
