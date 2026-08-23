-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Pre-release schema baseline. Existing development databases must be
-- recreated; there is no upgrade path from earlier development migration
-- sets. Temporal columns use timestamptz. Soft archive uses nullable
-- archived_at. Open-ended intervals and optional lifecycle instants use NULL
-- rather than integer zero sentinels. Revision columns match domain optimistic
-- concurrency where applicable.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

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
CREATE INDEX academic_units_directory_search_idx
    ON academic_units USING gin (name gin_trgm_ops, display_name gin_trgm_ops)
    WHERE archived_at IS NULL;

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
CREATE INDEX programmes_directory_search_idx
    ON programmes USING gin (name gin_trgm_ops, display_name gin_trgm_ops)
    WHERE archived_at IS NULL;

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
CREATE INDEX programme_levels_directory_search_idx
    ON programme_levels USING gin (name gin_trgm_ops, display_name gin_trgm_ops)
    WHERE archived_at IS NULL;

CREATE TABLE academic_periods (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    owner_type varchar(32) NOT NULL CHECK (owner_type IN ('institution', 'academic_unit')),
    institution_id varchar(26) REFERENCES institutions(id),
    academic_unit_id varchar(26) REFERENCES academic_units(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    start_at timestamptz NOT NULL,
    end_at timestamptz NOT NULL,
    CONSTRAINT academic_periods_owner_check CHECK (
        (owner_type = 'institution' AND institution_id IS NOT NULL AND academic_unit_id IS NULL)
        OR (owner_type = 'academic_unit' AND institution_id IS NULL AND academic_unit_id IS NOT NULL)
    ),
    CONSTRAINT academic_periods_start_at_end_at_check CHECK (end_at > start_at),
    CONSTRAINT academic_periods_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX academic_periods_institution_id_name_key
    ON academic_periods (institution_id, name)
    WHERE owner_type = 'institution' AND archived_at IS NULL;
CREATE UNIQUE INDEX academic_periods_academic_unit_id_name_key
    ON academic_periods (academic_unit_id, name)
    WHERE owner_type = 'academic_unit' AND archived_at IS NULL;
CREATE INDEX academic_periods_directory_search_idx
    ON academic_periods USING gin (name gin_trgm_ops, display_name gin_trgm_ops)
    WHERE archived_at IS NULL;

CREATE TABLE classes (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    mail_audience_revision bigint NOT NULL DEFAULT 0 CHECK (mail_audience_revision >= 0),
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
CREATE INDEX classes_directory_search_idx
    ON classes USING gin (name gin_trgm_ops, display_name gin_trgm_ops)
    WHERE archived_at IS NULL;

-- ---------------------------------------------------------------------------
-- Durable finite background work
-- ---------------------------------------------------------------------------

CREATE TABLE jobs (
    id varchar(26) PRIMARY KEY,
    type varchar(64) NOT NULL,
    status varchar(24) NOT NULL CHECK (status IN ('queued', 'running', 'cancel_requested', 'succeeded', 'failed', 'canceled')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    command_version integer NOT NULL CHECK (command_version > 0),
    command jsonb NOT NULL,
    checkpoint_version integer,
    checkpoint jsonb,
    result_version integer,
    result jsonb,
    public_error_code varchar(128) NOT NULL DEFAULT '',
    dedupe_key varchar(255) NOT NULL,
    dedupe_policy varchar(16) NOT NULL DEFAULT 'active' CHECK (dedupe_policy IN ('active', 'permanent')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    maximum_attempts integer NOT NULL CHECK (maximum_attempts > 0),
    work_reserved integer NOT NULL DEFAULT 0 CHECK (work_reserved >= 0),
    progress_current bigint,
    progress_total bigint,
    progress_stage varchar(64),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    CONSTRAINT jobs_lifecycle_check CHECK (
        updated_at >= created_at AND attempt_count <= maximum_attempts AND
        ((status IN ('succeeded', 'failed', 'canceled')) = (completed_at IS NOT NULL)) AND
        ((checkpoint IS NULL AND checkpoint_version IS NULL) OR (checkpoint IS NOT NULL AND checkpoint_version > 0)) AND
        ((result IS NULL AND result_version IS NULL) OR (result IS NOT NULL AND result_version > 0)) AND
        ((progress_current IS NULL AND progress_total IS NULL AND progress_stage IS NULL) OR
         (progress_current >= 0 AND progress_total > 0 AND progress_current <= progress_total AND progress_stage <> '')) AND
        octet_length(command::text) <= 65536 AND
        (checkpoint IS NULL OR octet_length(checkpoint::text) <= 65536) AND
        (result IS NULL OR octet_length(result::text) <= 65536)
    )
);

CREATE INDEX jobs_claim_idx ON jobs (available_at, created_at, id) WHERE status = 'queued';
CREATE UNIQUE INDEX jobs_active_type_dedupe_key_idx ON jobs (type, dedupe_key)
    WHERE dedupe_policy = 'active' AND status IN ('queued', 'running', 'cancel_requested');
CREATE UNIQUE INDEX jobs_permanent_type_dedupe_key_idx ON jobs (type, dedupe_key)
    WHERE dedupe_policy = 'permanent';

-- Permanent occurrence keys deliberately outlive retained Job history. The
-- referenced Job ID is informational rather than a foreign key so cleanup can
-- delete terminal execution detail without making an old date runnable again.
CREATE TABLE job_permanent_occurrences (
    type varchar(128) NOT NULL,
    dedupe_key varchar(255) NOT NULL,
    job_id varchar(26) NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (type, dedupe_key)
);

CREATE TABLE job_attempts (
    id varchar(26) PRIMARY KEY,
    job_id varchar(26) NOT NULL REFERENCES jobs(id),
    number integer NOT NULL CHECK (number > 0),
    status varchar(24) NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'canceled', 'lease_expired', 'incompatible')),
    node_id varchar(255) NOT NULL,
    claim_token char(64) NOT NULL UNIQUE,
    started_at timestamptz NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    public_error_code varchar(128) NOT NULL DEFAULT '',
    UNIQUE (job_id, number),
    CONSTRAINT job_attempts_lifecycle_check CHECK (
        heartbeat_at >= started_at AND lease_expires_at > heartbeat_at AND
        ((status = 'running') <> (completed_at IS NOT NULL))
    )
);

CREATE INDEX job_attempts_expired_idx ON job_attempts (lease_expires_at, id) WHERE status = 'running';
CREATE INDEX job_attempts_incompatible_node_idx ON job_attempts (job_id, node_id) WHERE status = 'incompatible';

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
    purpose varchar(32) NOT NULL CHECK (purpose IN ('profile_picture_custom', 'profile_picture_default', 'submission', 'exam_resource')),
    purge_claimed boolean NOT NULL DEFAULT false,
    UNIQUE (id, purge_claimed),
    CONSTRAINT file_entries_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE TABLE file_revisions (
    id varchar(26) PRIMARY KEY,
    file_entry_id varchar(26) NOT NULL REFERENCES file_entries(id),
    created_at timestamptz NOT NULL,
    availability varchar(16) NOT NULL CHECK (availability IN ('pending', 'available', 'quarantined', 'rejected')),
    indexing_state varchar(16) NOT NULL CHECK (indexing_state IN ('not_required', 'pending', 'ready', 'failed')),
    purge_claim_id varchar(26),
    purge_claimed_at timestamptz,
    CONSTRAINT file_revisions_purge_claim_check CHECK ((purge_claim_id IS NULL) = (purge_claimed_at IS NULL)),
    UNIQUE (id, file_entry_id)
);

CREATE UNIQUE INDEX file_revisions_purge_claim_id_key
    ON file_revisions (purge_claim_id) WHERE purge_claim_id IS NOT NULL;

ALTER TABLE file_entries ADD CONSTRAINT file_entries_current_revision_fkey
    FOREIGN KEY (current_revision_id, id) REFERENCES file_revisions(id, file_entry_id);

CREATE TABLE file_renditions (
    id varchar(26) PRIMARY KEY,
    file_revision_id varchar(26) NOT NULL REFERENCES file_revisions(id),
    created_at timestamptz NOT NULL,
    name varchar(64) NOT NULL,
    media_type varchar(255) NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    width integer NOT NULL,
    height integer NOT NULL,
    sha256 char(64) NOT NULL,
    UNIQUE (file_revision_id, name),
    CONSTRAINT file_renditions_dimensions_check CHECK (
        (width = 0 AND height = 0) OR (width > 0 AND height > 0)
    )
);

-- User mail eligibility changes and disabled Sitting fan-outs share this one
-- transactional chronology. The singleton row makes commit ordering exact:
-- a disabled watermark and a concurrent eligibility mutation cannot pass one
-- another without observing the same row lock.
CREATE TABLE mail_audience_states (
    singleton smallint PRIMARY KEY CHECK (singleton = 1),
    user_eligibility_revision bigint NOT NULL DEFAULT 0 CHECK (user_eligibility_revision >= 0)
);

INSERT INTO mail_audience_states (singleton) VALUES (1);

CREATE TABLE users (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    username varchar(64) NOT NULL,
    email varchar(254) NOT NULL,
    email_verified boolean NOT NULL DEFAULT false,
    mail_eligibility_revision bigint NOT NULL DEFAULT 0 CHECK (mail_eligibility_revision >= 0),
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
CREATE INDEX users_directory_search_idx
    ON users USING gin (
        username gin_trgm_ops,
        email gin_trgm_ops,
        display_name gin_trgm_ops,
        first_name gin_trgm_ops,
        last_name gin_trgm_ops
    ) WHERE archived_at IS NULL;

CREATE TABLE user_settings_documents (
    user_id varchar(26) PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    source text NOT NULL,
    format_version integer NOT NULL CHECK (format_version > 0),
    revision varchar(26) NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT user_settings_documents_source_size_check CHECK (octet_length(source) <= 262144),
    CONSTRAINT user_settings_documents_lifecycle_check CHECK (updated_at >= created_at)
);

-- An Invitation exists before its recipient has a User row. It freezes one
-- exact relationship package and stores only a domain-separated
-- digest of the 256-bit claim delivered by mail.
CREATE TABLE invitations (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    purpose varchar(32) NOT NULL CHECK (purpose IN ('student_class', 'teacher_academic_unit', 'academic_unit_role', 'institution_role')),
    state varchar(24) NOT NULL CHECK (state IN ('pending', 'accepted', 'revoked', 'expired', 'superseded')),
    target_email varchar(254) NOT NULL CHECK (target_email = lower(btrim(target_email))),
    class_id varchar(26) REFERENCES classes(id),
    academic_period_id varchar(26) REFERENCES academic_periods(id),
    academic_unit_id varchar(26) REFERENCES academic_units(id),
    role_id varchar(26),
    role_actions text[] NOT NULL DEFAULT '{}',
    intended_start_at timestamptz NOT NULL,
    intended_end_at timestamptz,
    suggested_username varchar(64) NOT NULL DEFAULT '',
    suggested_display_name varchar(512) NOT NULL DEFAULT '',
    suggested_first_name varchar(256) NOT NULL DEFAULT '',
    suggested_last_name varchar(256) NOT NULL DEFAULT '',
    suggested_locale varchar(35) NOT NULL DEFAULT '',
    suggested_timezone varchar(64) NOT NULL DEFAULT '',
    inviter_user_id varchar(26) NOT NULL REFERENCES users(id),
    scope_type varchar(32) NOT NULL CHECK (scope_type IN ('class', 'academic_unit', 'institution')),
    scope_id varchar(26) NOT NULL,
    claim_hash char(64) NOT NULL UNIQUE CHECK (claim_hash ~ '^[0-9a-f]{64}$'),
    expires_at timestamptz NOT NULL,
    accepted_at timestamptz,
    accepted_user_id varchar(26) REFERENCES users(id),
    accepted_affiliation_id varchar(26),
    accepted_class_member_id varchar(26),
    accepted_academic_unit_member_id varchar(26),
    accepted_role_binding_id varchar(26),
    CONSTRAINT invitations_package_check CHECK (
        (purpose = 'student_class' AND class_id IS NOT NULL AND academic_period_id IS NOT NULL
            AND academic_unit_id IS NULL AND role_id IS NULL AND role_actions = '{}' AND scope_type = 'class' AND scope_id = class_id) OR
        (purpose = 'teacher_academic_unit' AND class_id IS NULL AND academic_period_id IS NULL
            AND academic_unit_id IS NOT NULL AND role_id IS NOT NULL AND cardinality(role_actions) > 0
            AND scope_type = 'academic_unit' AND scope_id = academic_unit_id) OR
        (purpose = 'academic_unit_role' AND class_id IS NULL AND academic_period_id IS NULL
            AND academic_unit_id IS NOT NULL AND role_id IS NOT NULL AND cardinality(role_actions) > 0
            AND scope_type = 'academic_unit' AND scope_id = academic_unit_id) OR
        (purpose = 'institution_role' AND class_id IS NULL AND academic_period_id IS NULL
            AND academic_unit_id IS NULL AND role_id IS NOT NULL AND cardinality(role_actions) > 0
            AND scope_type = 'institution')
    ),
    CONSTRAINT invitations_effective_bounds_check CHECK (intended_end_at IS NULL OR intended_end_at > intended_start_at),
    CONSTRAINT invitations_lifetime_check CHECK (expires_at = created_at + interval '7 days'),
    CONSTRAINT invitations_lifecycle_check CHECK (
        updated_at >= created_at AND
        ((state = 'accepted') = (accepted_at IS NOT NULL AND accepted_user_id IS NOT NULL AND
            ((purpose = 'student_class' AND accepted_affiliation_id IS NOT NULL AND accepted_class_member_id IS NOT NULL
                AND accepted_academic_unit_member_id IS NULL AND accepted_role_binding_id IS NULL) OR
             (purpose = 'teacher_academic_unit' AND accepted_affiliation_id IS NOT NULL AND accepted_class_member_id IS NULL
                AND accepted_academic_unit_member_id IS NOT NULL AND accepted_role_binding_id IS NOT NULL) OR
             (purpose IN ('academic_unit_role', 'institution_role') AND accepted_affiliation_id IS NULL
                AND accepted_class_member_id IS NULL AND accepted_academic_unit_member_id IS NULL AND accepted_role_binding_id IS NOT NULL)))) AND
        (accepted_at IS NULL OR (accepted_at >= created_at AND accepted_at < expires_at))
    )
);

CREATE UNIQUE INDEX invitations_pending_student_period_key
    ON invitations (target_email, academic_period_id)
    WHERE state = 'pending' AND purpose = 'student_class';

CREATE UNIQUE INDEX invitations_pending_teacher_package_key
    ON invitations (target_email, academic_unit_id, role_id)
    WHERE state = 'pending' AND purpose = 'teacher_academic_unit';

CREATE UNIQUE INDEX invitations_pending_academic_unit_role_package_key
    ON invitations (target_email, academic_unit_id, role_id)
    WHERE state = 'pending' AND purpose = 'academic_unit_role';

CREATE UNIQUE INDEX invitations_pending_institution_role_package_key
    ON invitations (target_email, scope_id, role_id)
    WHERE state = 'pending' AND purpose = 'institution_role';

-- Administrative imports and student progression retain only a seven-day
-- private preview/report. Job commands contain the opaque aggregate ID;
-- recipient data remains in these domain-owned tables and is removed with it.
CREATE TABLE onboarding_imports (
    id varchar(26) PRIMARY KEY,
    mode varchar(32) NOT NULL CHECK (mode IN ('student_class', 'teacher_academic_unit', 'institution', 'academic_administration', 'student_progression')),
    state varchar(32) NOT NULL CHECK (state IN ('uploading', 'parsing', 'preview_ready', 'executing', 'completed', 'completed_with_errors', 'canceled', 'failed')),
    scope_type varchar(32) NOT NULL CHECK (scope_type IN ('class', 'academic_unit', 'institution')),
    scope_id varchar(26) NOT NULL,
    role_id varchar(26),
    source_period_id varchar(26) REFERENCES academic_periods(id),
    source_class_id varchar(26) REFERENCES classes(id),
    destination_period_id varchar(26) REFERENCES academic_periods(id),
    destination_class_id varchar(26) REFERENCES classes(id),
    source_period_revision bigint NOT NULL DEFAULT 0 CHECK (source_period_revision >= 0),
    source_class_revision bigint NOT NULL DEFAULT 0 CHECK (source_class_revision >= 0),
    destination_period_revision bigint NOT NULL DEFAULT 0 CHECK (destination_period_revision >= 0),
    destination_class_revision bigint NOT NULL DEFAULT 0 CHECK (destination_class_revision >= 0),
    effective_at timestamptz,
    actor_user_id varchar(26) NOT NULL REFERENCES users(id),
    principal jsonb NOT NULL,
    preview_digest char(64) NOT NULL DEFAULT '' CHECK (preview_digest = '' OR preview_digest ~ '^[0-9a-f]{64}$'),
    ignored_headers text[] NOT NULL DEFAULT '{}',
    total_rows integer NOT NULL DEFAULT 0 CHECK (total_rows BETWEEN 0 AND 50000),
    valid_rows integer NOT NULL DEFAULT 0 CHECK (valid_rows >= 0),
    invalid_rows integer NOT NULL DEFAULT 0 CHECK (invalid_rows >= 0),
    succeeded_rows integer NOT NULL DEFAULT 0 CHECK (succeeded_rows >= 0),
    no_op_rows integer NOT NULL DEFAULT 0 CHECK (no_op_rows >= 0),
    failed_rows integer NOT NULL DEFAULT 0 CHECK (failed_rows >= 0),
    skipped_rows integer NOT NULL DEFAULT 0 CHECK (skipped_rows >= 0),
    commit_policy varchar(32) CHECK (commit_policy IN ('require_all_valid', 'valid_rows_only')),
    commit_expected_revision bigint CHECK (commit_expected_revision > 0),
    commit_at timestamptz,
    commit_key_digest bytea,
    parse_job_id varchar(26) NOT NULL UNIQUE REFERENCES jobs(id),
    execution_job_id varchar(26) UNIQUE REFERENCES jobs(id),
    failure_code varchar(128) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    CONSTRAINT onboarding_imports_scope_check CHECK (
        (mode = 'student_class' AND scope_type = 'class' AND role_id IS NULL) OR
        (mode = 'teacher_academic_unit' AND scope_type = 'academic_unit' AND role_id IS NOT NULL) OR
        (mode = 'institution' AND scope_type = 'institution' AND role_id IS NULL) OR
        (mode = 'academic_administration' AND scope_type IN ('institution', 'academic_unit', 'class') AND role_id IS NULL) OR
        (mode = 'student_progression' AND scope_type = 'class' AND role_id IS NULL AND
         source_period_id IS NOT NULL AND source_class_id IS NOT NULL AND destination_period_id IS NOT NULL AND
         destination_class_id = scope_id AND effective_at IS NOT NULL AND source_period_revision > 0 AND
         source_class_revision > 0 AND destination_period_revision > 0 AND destination_class_revision > 0)
    ),
    CONSTRAINT onboarding_imports_progression_package_check CHECK (
        (mode = 'student_progression' AND source_class_id <> destination_class_id) OR
        (mode <> 'student_progression' AND source_period_id IS NULL AND source_class_id IS NULL AND
         destination_period_id IS NULL AND destination_class_id IS NULL AND effective_at IS NULL AND
         source_period_revision = 0 AND source_class_revision = 0 AND destination_period_revision = 0 AND destination_class_revision = 0)
    ),
    CONSTRAINT onboarding_imports_counts_check CHECK (
        valid_rows + invalid_rows = total_rows AND
        succeeded_rows + no_op_rows + failed_rows + skipped_rows <= total_rows
    ),
    CONSTRAINT onboarding_imports_lifecycle_check CHECK (
        updated_at >= created_at AND expires_at = created_at + interval '7 days' AND
        (state NOT IN ('uploading', 'parsing') OR preview_digest = '') AND
        (state NOT IN ('preview_ready', 'executing', 'completed', 'completed_with_errors') OR preview_digest <> '') AND
        ((state IN ('executing', 'completed', 'completed_with_errors') AND execution_job_id IS NOT NULL) OR
         state NOT IN ('executing', 'completed', 'completed_with_errors'))
    )
);

CREATE UNIQUE INDEX onboarding_imports_active_scope_idx
    ON onboarding_imports (mode, scope_type, scope_id)
    WHERE state = 'executing';
CREATE UNIQUE INDEX onboarding_imports_active_progression_source_idx
    ON onboarding_imports (source_class_id)
    WHERE mode = 'student_progression' AND state = 'executing';
CREATE INDEX onboarding_imports_expiry_idx ON onboarding_imports (expires_at, id);

CREATE TABLE onboarding_import_rows (
    import_id varchar(26) NOT NULL REFERENCES onboarding_imports(id) ON DELETE CASCADE,
    row_number integer NOT NULL CHECK (row_number BETWEEN 1 AND 50000),
    reference varchar(128) NOT NULL,
    operation varchar(64) NOT NULL,
    scope_type varchar(32) NOT NULL CHECK (scope_type IN ('class', 'academic_unit', 'institution')),
    scope_id varchar(26) NOT NULL,
    target_revision bigint NOT NULL CHECK (target_revision >= 0),
    role_id varchar(26),
    role_revision bigint NOT NULL DEFAULT 0 CHECK (role_revision >= 0),
    target_email varchar(254) NOT NULL CHECK (target_email = lower(btrim(target_email))),
    user_id varchar(26) REFERENCES users(id),
    relationship_ref varchar(26),
    relationship_revision bigint NOT NULL DEFAULT 0 CHECK (relationship_revision >= 0),
    destination_relationship_ref varchar(26),
    destination_relationship_revision bigint NOT NULL DEFAULT 0 CHECK (destination_relationship_revision >= 0),
    affiliation_kind varchar(32) NOT NULL DEFAULT '',
    suggested_username varchar(64) NOT NULL DEFAULT '',
    suggested_display_name varchar(512) NOT NULL DEFAULT '',
    suggested_first_name varchar(256) NOT NULL DEFAULT '',
    suggested_last_name varchar(256) NOT NULL DEFAULT '',
    suggested_locale varchar(35) NOT NULL DEFAULT '',
    suggested_timezone varchar(64) NOT NULL DEFAULT '',
    intended_start_at timestamptz,
    intended_end_at timestamptz,
    preview_status varchar(16) NOT NULL CHECK (preview_status IN ('valid', 'invalid', 'duplicate')),
    preview_code varchar(128) NOT NULL DEFAULT '',
    status varchar(16) NOT NULL CHECK (status IN ('valid', 'invalid', 'duplicate', 'pending', 'succeeded', 'no_op', 'failed', 'skipped', 'canceled')),
    public_code varchar(128) NOT NULL DEFAULT '',
    invitation_id varchar(26) REFERENCES invitations(id),
    result_ref varchar(26),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (import_id, row_number),
    CONSTRAINT onboarding_import_rows_result_check CHECK (
        (status <> 'succeeded' OR invitation_id IS NOT NULL OR result_ref IS NOT NULL) AND
        NOT (invitation_id IS NOT NULL AND result_ref IS NOT NULL) AND
        (invitation_id IS NULL OR status IN ('succeeded', 'no_op')) AND
        (result_ref IS NULL OR status IN ('succeeded', 'no_op')) AND
        (status = 'failed') = (public_code <> '')
    )
);

CREATE INDEX onboarding_import_rows_execution_idx
    ON onboarding_import_rows (import_id, row_number) WHERE status = 'pending';

-- One immutable logical occurrence owns one or more frozen recipient
-- deliveries. Occurrence actors and recipient targets are deliberately
-- independent because an administrator may notify a pre-User Invitation.
CREATE TABLE mail_occurrences (
    id varchar(26) PRIMARY KEY,
    kind varchar(32) NOT NULL CHECK (kind IN ('operator_test', 'account_token', 'security_notice', 'invitation', 'academic_administration', 'sitting_schedule', 'exam_management', 'submission_receipt', 'result_release')),
    template_key varchar(128) NOT NULL CHECK (template_key IN (
        'system.mail_test', 'identity.verify_email', 'identity.password_reset',
        'identity.password_changed', 'identity.email_change_warning_old',
        'identity.email_change_verify_new', 'identity.email_verified_by_admin',
		'identity.account_disabled', 'identity.account_enabled',
		'identity.sessions_revoked_by_admin',
		'identity.mfa_enabled', 'identity.mfa_disabled',
		'identity.mfa_recovery_codes_regenerated',
		'identity.personal_access_token_created', 'identity.personal_access_token_enabled',
		'identity.personal_access_token_disabled', 'identity.personal_access_token_revoked',
        'access.student_class_invitation', 'access.teacher_academic_unit_invitation',
        'access.academic_unit_role_invitation', 'access.institution_role_invitation',
        'access.invitation_accepted', 'access.invitation_revoked',
        'academic.class_enrolled', 'academic.class_enrollment_ended', 'academic.class_transferred',
        'academic.academic_unit_assigned', 'academic.academic_unit_assignment_ended',
        'authorization.scoped_role_assigned', 'authorization.scoped_role_ended',
        'authorization.institution_role_assigned', 'authorization.institution_role_ended',
        'exam.sitting_scheduled', 'exam.sitting_rescheduled',
        'exam.sitting_cancelled', 'exam.sitting_assignment_removed',
        'exam.manager_added', 'exam.manager_removed',
        'exam.ownership_transferred_to_you', 'exam.ownership_transferred_from_you',
        'exam.submission_received', 'exam.submission_automatically_sealed',
        'exam.result_released'
    )),
    actor_user_id varchar(26) NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL,
    CONSTRAINT mail_occurrences_identity_key UNIQUE (id, template_key)
);

CREATE FUNCTION reject_mail_occurrence_update() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'mail occurrences are immutable' USING ERRCODE = '23514';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER mail_occurrences_reject_update
    BEFORE UPDATE ON mail_occurrences
    FOR EACH ROW EXECUTE FUNCTION reject_mail_occurrence_update();

CREATE TABLE mail_deliveries (
    id varchar(26) PRIMARY KEY,
    occurrence_id varchar(26) NOT NULL,
    job_id varchar(26) NOT NULL UNIQUE REFERENCES jobs(id),
    target_user_id varchar(26) REFERENCES users(id),
    -- Invitation rows are retention-bounded; the opaque ID remains in mail history after purge.
    target_invitation_id varchar(26),
    template_key varchar(128) NOT NULL CHECK (template_key IN (
        'system.mail_test', 'identity.verify_email', 'identity.password_reset',
        'identity.password_changed', 'identity.email_change_warning_old',
        'identity.email_change_verify_new', 'identity.email_verified_by_admin',
		'identity.account_disabled', 'identity.account_enabled',
		'identity.sessions_revoked_by_admin',
		'identity.mfa_enabled', 'identity.mfa_disabled',
		'identity.mfa_recovery_codes_regenerated',
		'identity.personal_access_token_created', 'identity.personal_access_token_enabled',
		'identity.personal_access_token_disabled', 'identity.personal_access_token_revoked',
        'access.student_class_invitation', 'access.teacher_academic_unit_invitation',
        'access.academic_unit_role_invitation', 'access.institution_role_invitation',
        'access.invitation_accepted', 'access.invitation_revoked',
        'academic.class_enrolled', 'academic.class_enrollment_ended', 'academic.class_transferred',
        'academic.academic_unit_assigned', 'academic.academic_unit_assignment_ended',
        'authorization.scoped_role_assigned', 'authorization.scoped_role_ended',
        'authorization.institution_role_assigned', 'authorization.institution_role_ended',
        'exam.sitting_scheduled', 'exam.sitting_rescheduled',
        'exam.sitting_cancelled', 'exam.sitting_assignment_removed',
        'exam.manager_added', 'exam.manager_removed',
        'exam.ownership_transferred_to_you', 'exam.ownership_transferred_from_you',
        'exam.submission_received', 'exam.submission_automatically_sealed',
        'exam.result_released'
    )),
    template_digest char(64) NOT NULL CHECK (template_digest ~ '^[0-9a-f]{64}$'),
    masked_recipient varchar(254) NOT NULL
        CHECK (masked_recipient ~ '^(\*{3}|[^*@[:space:]]\*{1,3})@[^*@[:space:]]+$'),
    state varchar(24) NOT NULL CHECK (state IN ('queued', 'sending', 'accepted', 'failed', 'suppressed', 'canceled')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    message_date timestamptz NOT NULL,
    deadline timestamptz NOT NULL,
    message_id varchar(900) NOT NULL UNIQUE,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 8),
    accepted_at timestamptz,
    failed_at timestamptz,
    public_failure_code varchar(128) NOT NULL DEFAULT ''
        CHECK (public_failure_code = '' OR public_failure_code ~ '^[a-z][a-z0-9_.-]{0,127}$'),
    payload_key_id char(32)
        CHECK (payload_key_id ~ '^[0-9a-f]{32}$'),
    encrypted_payload jsonb,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    CONSTRAINT mail_deliveries_occurrence_identity_fkey
        FOREIGN KEY (occurrence_id) REFERENCES mail_occurrences(id),
    CONSTRAINT mail_deliveries_exact_target_check CHECK (
        (target_user_id IS NULL) <> (target_invitation_id IS NULL)
    ),
    CONSTRAINT mail_deliveries_lifecycle_check CHECK (
        updated_at >= created_at AND message_date = created_at AND deadline > created_at AND
        ((state = 'accepted') = (accepted_at IS NOT NULL)) AND
        ((state = 'failed') = (failed_at IS NOT NULL)) AND
        ((state IN ('accepted', 'suppressed', 'canceled')) = (encrypted_payload IS NULL)) AND
        ((payload_key_id IS NULL) = (encrypted_payload IS NULL)) AND
        (state <> 'queued' OR ((attempt_count = 0) = (public_failure_code = ''))) AND
        (state <> 'sending' OR (attempt_count > 0 AND public_failure_code = '')) AND
        (state <> 'accepted' OR (attempt_count > 0 AND public_failure_code = '' AND accepted_at = updated_at)) AND
        (state <> 'failed' OR (attempt_count > 0 AND public_failure_code <> '' AND failed_at = updated_at)) AND
        (encrypted_payload IS NULL OR octet_length(encrypted_payload::text) <= 2097152)
    )
);

CREATE UNIQUE INDEX mail_deliveries_occurrence_user_recipient_key
    ON mail_deliveries (occurrence_id, target_user_id)
    WHERE target_user_id IS NOT NULL;

CREATE UNIQUE INDEX mail_deliveries_occurrence_invitation_recipient_key
    ON mail_deliveries (occurrence_id, target_invitation_id)
    WHERE target_invitation_id IS NOT NULL;

CREATE INDEX mail_deliveries_state_deadline_idx
    ON mail_deliveries (state, deadline, created_at, id);

CREATE INDEX mail_deliveries_operator_list_idx
    ON mail_deliveries (created_at DESC, id DESC)
    INCLUDE (state, template_key);

CREATE INDEX mail_deliveries_terminal_retention_idx
    ON mail_deliveries (state, updated_at, id);

-- This bounded aggregate makes startup key-ring validation independent of
-- delivery backlog size. Mail lifecycle transactions increment on enqueue and
-- decrement when ciphertext is destroyed or retained history is deleted.
CREATE TABLE mail_payload_keys (
    key_id char(32) PRIMARY KEY CHECK (key_id ~ '^[0-9a-f]{32}$'),
    active_references bigint NOT NULL CHECK (active_references >= 0)
);

-- Fan-out expansion is delivered in later mail slices, but its frozen bundle
-- is already an active encrypted value and therefore participates in the same
-- bounded rekey/refcount contract from the first rotation implementation.
CREATE TABLE mail_fanout_bundles (
    id varchar(26) PRIMARY KEY,
    payload_key_id char(32) NOT NULL CHECK (payload_key_id ~ '^[0-9a-f]{32}$'),
    encrypted_payload jsonb NOT NULL
        CHECK (octet_length(encrypted_payload::text) <= 4194304),
    created_at timestamptz NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0)
);

-- One durable fence prevents an old-primary node from introducing work after
-- a rotation begins. The active Job identity also rejects stale attempts even
-- when a later operation promotes the same primary again.
CREATE TABLE mail_key_state (
    singleton boolean PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    required_primary_key_id char(32)
        CHECK (required_primary_key_id ~ '^[0-9a-f]{32}$'),
    active_rekey_job_id varchar(26) REFERENCES jobs(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT mail_key_state_operation_check CHECK (
        active_rekey_job_id IS NULL OR required_primary_key_id IS NOT NULL
    )
);

INSERT INTO mail_key_state(singleton, required_primary_key_id, active_rekey_job_id, updated_at)
VALUES (TRUE, NULL, NULL, clock_timestamp());

-- One PostgreSQL-owned token bucket coordinates outbound sends across every
-- application node in this installation. Ordinary work leaves four burst
-- tokens available for credential delivery.
CREATE TABLE mail_send_rate_limit (
    singleton boolean PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    tokens double precision NOT NULL CHECK (tokens >= 0 AND tokens <= 20),
    updated_at timestamptz NOT NULL
);

CREATE TABLE upload_leases (
    id varchar(26) PRIMARY KEY,
    file_revision_id varchar(26) NOT NULL UNIQUE REFERENCES file_revisions(id),
    created_by_user_id varchar(26) NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
	bytes_received bigint NOT NULL DEFAULT 0 CHECK (bytes_received >= 0),
    CONSTRAINT upload_leases_lifecycle_check CHECK (updated_at >= created_at AND expires_at > created_at)
);

CREATE TABLE file_legal_holds (
    file_entry_id varchar(26) PRIMARY KEY,
    purge_claimed boolean NOT NULL DEFAULT false CHECK (NOT purge_claimed),
    created_at timestamptz NOT NULL,
    reason_code varchar(128) NOT NULL,
    FOREIGN KEY (file_entry_id, purge_claimed) REFERENCES file_entries(id, purge_claimed)
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
CREATE INDEX academic_unit_members_current_exam_visibility_idx
    ON academic_unit_members (academic_unit_id, user_id, start_at, end_at)
    WHERE archived_at IS NULL;

-- ---------------------------------------------------------------------------
-- Examination authoring
-- ---------------------------------------------------------------------------

CREATE TABLE exams (
    id varchar(26) PRIMARY KEY,
    academic_unit_id varchar(26) NOT NULL REFERENCES academic_units(id),
    creator_user_id varchar(26) NOT NULL REFERENCES users(id),
    owner_user_id varchar(26) NOT NULL REFERENCES users(id),
    default_revision_id varchar(26),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    CONSTRAINT exams_lifecycle_check CHECK (
        updated_at >= created_at AND
        (archived_at IS NULL OR archived_at >= created_at)
    )
);

CREATE INDEX exams_academic_unit_id_updated_at_id_idx
    ON exams (academic_unit_id, updated_at DESC, id DESC);
CREATE INDEX exams_updated_at_id_idx ON exams (updated_at DESC, id DESC);
CREATE INDEX exams_active_academic_unit_updated_at_id_idx
    ON exams (academic_unit_id, updated_at DESC, id DESC) WHERE archived_at IS NULL;
CREATE INDEX exams_archived_academic_unit_updated_at_id_idx
    ON exams (academic_unit_id, updated_at DESC, id DESC) WHERE archived_at IS NOT NULL;
CREATE INDEX exams_active_updated_at_id_idx
    ON exams (updated_at DESC, id DESC) WHERE archived_at IS NULL;
CREATE INDEX exams_archived_updated_at_id_idx
    ON exams (updated_at DESC, id DESC) WHERE archived_at IS NOT NULL;

CREATE TABLE exam_drafts (
    exam_id varchar(26) PRIMARY KEY REFERENCES exams(id) ON DELETE CASCADE,
    title text NOT NULL,
    instructions_markdown text NOT NULL DEFAULT '',
    policy jsonb NOT NULL,
    execution_profile jsonb NOT NULL DEFAULT '{"schema_version":1,"enabled":false,"image":"","network":"none"}'::jsonb,
    base_revision_id varchar(26),
    updated_at timestamptz NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    CONSTRAINT exam_drafts_title_check CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT exam_drafts_instructions_markdown_check
        CHECK (octet_length(instructions_markdown) <= 65536),
    CONSTRAINT exam_drafts_policy_size_check CHECK (octet_length(policy::text) <= 65536),
    CONSTRAINT exam_drafts_execution_profile_size_check CHECK (octet_length(execution_profile::text) <= 1024)
);

CREATE INDEX exam_drafts_title_search_idx
    ON exam_drafts USING gin (title gin_trgm_ops);

CREATE TABLE exam_managers (
    exam_id varchar(26) NOT NULL REFERENCES exams(id) ON DELETE CASCADE,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    granted_by_user_id varchar(26) NOT NULL REFERENCES users(id),
    granted_at timestamptz NOT NULL,
    PRIMARY KEY (exam_id, user_id)
);

CREATE INDEX exam_managers_user_id_exam_id_idx ON exam_managers (user_id, exam_id);
CREATE INDEX exam_managers_exam_id_granted_at_user_id_idx
    ON exam_managers (exam_id, granted_at DESC, user_id DESC);

ALTER TABLE exams
    ADD CONSTRAINT exams_owner_manager_fkey
    FOREIGN KEY (id, owner_user_id)
    REFERENCES exam_managers (exam_id, user_id)
    DEFERRABLE INITIALLY DEFERRED;

-- Stable Exam Resource identity is independent of mutable Draft attachment.
-- Published Revisions and purpose-bound live-correction stages pin this
-- catalog so old immutable bytes remain reachable after Draft replacement or
-- removal.
CREATE TABLE exam_resource_identities (
    id varchar(26) PRIMARY KEY,
    exam_id varchar(26) NOT NULL REFERENCES exams(id) ON DELETE CASCADE,
    file_entry_id varchar(26) NOT NULL UNIQUE REFERENCES file_entries(id),
    UNIQUE (exam_id, id, file_entry_id)
);

CREATE TABLE exam_resources (
    id varchar(26) PRIMARY KEY,
    exam_id varchar(26) NOT NULL REFERENCES exams(id) ON DELETE CASCADE,
    file_entry_id varchar(26) NOT NULL REFERENCES file_entries(id),
    selected_file_revision_id varchar(26) NOT NULL,
    display_name text NOT NULL,
    description_markdown text NOT NULL DEFAULT '',
    position smallint NOT NULL CHECK (position >= 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    UNIQUE (file_entry_id),
    CONSTRAINT exam_resources_selected_revision_fkey
        FOREIGN KEY (selected_file_revision_id, file_entry_id)
        REFERENCES file_revisions(id, file_entry_id),
    CONSTRAINT exam_resources_identity_fkey
        FOREIGN KEY (exam_id, id, file_entry_id)
        REFERENCES exam_resource_identities(exam_id, id, file_entry_id),
    CONSTRAINT exam_resources_display_name_check
        CHECK (char_length(display_name) BETWEEN 1 AND 255),
    CONSTRAINT exam_resources_description_markdown_check
        CHECK (octet_length(description_markdown) <= 16384),
    CONSTRAINT exam_resources_lifecycle_check CHECK (
        updated_at >= created_at AND
        (archived_at IS NULL OR archived_at >= created_at)
    )
);

CREATE UNIQUE INDEX exam_resources_active_position_key
    ON exam_resources (exam_id, position) WHERE archived_at IS NULL;

CREATE TABLE exam_starter_workspace_objects (
    id varchar(26) PRIMARY KEY,
    exam_id varchar(26) NOT NULL REFERENCES exams(id) ON DELETE CASCADE,
    created_by_user_id varchar(26) NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    state varchar(16) NOT NULL CHECK (state IN ('staged', 'current', 'reclaimable', 'claimed')),
    content_version text,
    media_type varchar(255),
    size_bytes bigint,
    sha256 char(64),
    reclaim_after timestamptz,
    claim_token varchar(128),
    claimed_at timestamptz,
    UNIQUE (exam_id, id),
    CONSTRAINT exam_starter_workspace_objects_lifecycle_check CHECK (
        updated_at >= created_at AND expires_at > created_at AND
        (reclaim_after IS NULL OR reclaim_after >= created_at) AND
        (claimed_at IS NULL OR claimed_at >= created_at)
    ),
    CONSTRAINT exam_starter_workspace_objects_content_check CHECK (
        (state = 'staged' AND
            content_version IS NULL AND media_type IS NULL AND
            size_bytes IS NULL AND sha256 IS NULL
        ) OR (state = 'current' AND
            content_version IS NOT NULL AND
            content_version ~ '^[A-Za-z0-9_-]{26}$' AND
            media_type IS NOT NULL AND char_length(btrim(media_type)) > 0 AND
            size_bytes IS NOT NULL AND
            size_bytes BETWEEN 0 AND 10485760 AND
            sha256 IS NOT NULL AND
            sha256 ~ '^[0-9a-f]{64}$'
        ) OR (state IN ('reclaimable', 'claimed') AND (
            (content_version IS NULL AND media_type IS NULL AND
                size_bytes IS NULL AND sha256 IS NULL) OR
            (content_version IS NOT NULL AND
                content_version ~ '^[A-Za-z0-9_-]{26}$' AND
                media_type IS NOT NULL AND char_length(btrim(media_type)) > 0 AND
                size_bytes IS NOT NULL AND
                size_bytes BETWEEN 0 AND 10485760 AND
                sha256 IS NOT NULL AND
                sha256 ~ '^[0-9a-f]{64}$')
        ))
    ),
    CONSTRAINT exam_starter_workspace_objects_reclaim_check CHECK (
        (state IN ('reclaimable', 'claimed')) = (reclaim_after IS NOT NULL)
    ),
    CONSTRAINT exam_starter_workspace_objects_claim_check CHECK (
        (claim_token IS NULL) = (claimed_at IS NULL) AND
        (state = 'claimed') = (claim_token IS NOT NULL) AND
        (claim_token IS NULL OR char_length(btrim(claim_token)) > 0)
    )
);

CREATE INDEX exam_starter_workspace_objects_cleanup_idx
    ON exam_starter_workspace_objects (state, reclaim_after, id)
    WHERE state IN ('reclaimable', 'claimed');
CREATE INDEX exam_starter_workspace_objects_expiry_idx
    ON exam_starter_workspace_objects (expires_at, id) WHERE state = 'staged';

CREATE TABLE exam_starter_workspace_entries (
    id varchar(26) PRIMARY KEY,
    exam_id varchar(26) NOT NULL REFERENCES exams(id) ON DELETE CASCADE,
    kind varchar(16) NOT NULL CHECK (kind IN ('file', 'directory')),
    path text NOT NULL,
    current_object_id varchar(26),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    CONSTRAINT exam_starter_workspace_entries_current_object_fkey
        FOREIGN KEY (exam_id, current_object_id)
        REFERENCES exam_starter_workspace_objects(exam_id, id),
    CONSTRAINT exam_starter_workspace_entries_content_check CHECK (
        (kind = 'file' AND (archived_at IS NOT NULL OR current_object_id IS NOT NULL)) OR
        (kind = 'directory' AND current_object_id IS NULL)
    ),
    CONSTRAINT exam_starter_workspace_entries_path_check CHECK (
        octet_length(path) BETWEEN 1 AND 1024
    ),
    CONSTRAINT exam_starter_workspace_entries_lifecycle_check CHECK (
        updated_at >= created_at AND
        (archived_at IS NULL OR archived_at >= created_at)
    )
);

CREATE UNIQUE INDEX exam_starter_workspace_entries_active_path_key
    ON exam_starter_workspace_entries (exam_id, path) WHERE archived_at IS NULL;
CREATE UNIQUE INDEX exam_starter_workspace_entries_current_object_key
    ON exam_starter_workspace_entries (current_object_id)
    WHERE current_object_id IS NOT NULL;

-- Published Exam Revisions are immutable aggregate snapshots. Canonical policy
-- bytes remain bytea because JSONB textual order is not a digest contract.
ALTER TABLE exam_resources
    ADD CONSTRAINT exam_resources_exam_id_id_file_entry_id_key UNIQUE (exam_id, id, file_entry_id);
ALTER TABLE exam_starter_workspace_entries
    ADD CONSTRAINT exam_starter_workspace_entries_exam_id_id_key UNIQUE (exam_id, id);
ALTER TABLE file_renditions
    ADD CONSTRAINT file_renditions_id_file_revision_id_key UNIQUE (id, file_revision_id);

CREATE TABLE exam_revisions (
    id varchar(26) PRIMARY KEY,
    exam_id varchar(26) NOT NULL REFERENCES exams(id),
    number bigint NOT NULL CHECK (number > 0),
    snapshot_schema_version integer NOT NULL CHECK (snapshot_schema_version = 1),
    source_draft_revision bigint NOT NULL CHECK (source_draft_revision > 0),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    instructions_markdown text NOT NULL DEFAULT '' CHECK (octet_length(instructions_markdown) <= 65536),
    policy_schema_version integer NOT NULL CHECK (policy_schema_version > 0),
    policy_document jsonb NOT NULL,
    policy_canonical bytea NOT NULL CHECK (octet_length(policy_canonical) BETWEEN 1 AND 65536),
    policy_digest char(64) NOT NULL CHECK (policy_digest ~ '^[0-9a-f]{64}$'),
    execution_profile_document jsonb NOT NULL,
    execution_profile_canonical bytea NOT NULL CHECK (octet_length(execution_profile_canonical) BETWEEN 1 AND 1024),
    execution_profile_digest char(64) NOT NULL CHECK (execution_profile_digest ~ '^[0-9a-f]{64}$'),
    starter_workspace_digest char(64) NOT NULL CHECK (starter_workspace_digest ~ '^[0-9a-f]{64}$'),
    content_digest char(64) NOT NULL CHECK (content_digest ~ '^[0-9a-f]{64}$'),
    resource_count smallint NOT NULL CHECK (resource_count BETWEEN 0 AND 10),
    starter_entry_count integer NOT NULL CHECK (starter_entry_count BETWEEN 0 AND 500),
    starter_total_bytes bigint NOT NULL CHECK (starter_total_bytes BETWEEN 0 AND 52428800),
    published_by_user_id varchar(26) NOT NULL REFERENCES users(id),
    published_at timestamptz NOT NULL,
    base_revision_id varchar(26),
    publication_kind varchar(24) NOT NULL CHECK (publication_kind IN ('standard', 'live_correction')),
    sealed boolean NOT NULL DEFAULT false,
    UNIQUE (exam_id, id),
    UNIQUE (exam_id, id, sealed),
    UNIQUE (exam_id, number),
    CONSTRAINT exam_revisions_base_revision_fkey
        FOREIGN KEY (exam_id, base_revision_id) REFERENCES exam_revisions(exam_id, id),
    CONSTRAINT exam_revisions_base_not_self_check CHECK (base_revision_id IS NULL OR base_revision_id <> id)
);

CREATE INDEX exam_revisions_exam_id_number_idx ON exam_revisions (exam_id, number DESC);

CREATE TABLE exam_revision_resources (
    exam_revision_id varchar(26) NOT NULL,
    exam_id varchar(26) NOT NULL,
    resource_id varchar(26) NOT NULL,
    file_entry_id varchar(26) NOT NULL,
    file_revision_id varchar(26) NOT NULL,
    rendition_id varchar(26) NOT NULL,
    display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 255),
    description_markdown text NOT NULL DEFAULT '' CHECK (octet_length(description_markdown) <= 16384),
    position smallint NOT NULL CHECK (position BETWEEN 0 AND 9),
    media_type varchar(255) NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 0 AND 10485760),
    sha256 char(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (exam_revision_id, resource_id),
    UNIQUE (exam_revision_id, position),
    CONSTRAINT exam_revision_resources_revision_fkey
        FOREIGN KEY (exam_id, exam_revision_id) REFERENCES exam_revisions(exam_id, id),
    CONSTRAINT exam_revision_resources_resource_fkey
        FOREIGN KEY (exam_id, resource_id, file_entry_id) REFERENCES exam_resource_identities(exam_id, id, file_entry_id),
    CONSTRAINT exam_revision_resources_file_revision_fkey
        FOREIGN KEY (file_revision_id, file_entry_id) REFERENCES file_revisions(id, file_entry_id),
    CONSTRAINT exam_revision_resources_rendition_fkey
        FOREIGN KEY (rendition_id, file_revision_id) REFERENCES file_renditions(id, file_revision_id)
);

CREATE TABLE exam_revision_starter_workspace_entries (
    exam_revision_id varchar(26) NOT NULL,
    exam_id varchar(26) NOT NULL,
    entry_id varchar(26) NOT NULL,
    kind varchar(16) NOT NULL CHECK (kind IN ('file', 'directory')),
    path text NOT NULL CHECK (octet_length(path) BETWEEN 1 AND 1024),
    object_id varchar(26),
    content_version text,
    media_type varchar(255),
    size_bytes bigint,
    sha256 char(64),
    PRIMARY KEY (exam_revision_id, entry_id),
    UNIQUE (exam_revision_id, path),
    CONSTRAINT exam_revision_workspace_revision_fkey
        FOREIGN KEY (exam_id, exam_revision_id) REFERENCES exam_revisions(exam_id, id),
    CONSTRAINT exam_revision_workspace_entry_fkey
        FOREIGN KEY (exam_id, entry_id) REFERENCES exam_starter_workspace_entries(exam_id, id),
    CONSTRAINT exam_revision_workspace_object_fkey
        FOREIGN KEY (exam_id, object_id) REFERENCES exam_starter_workspace_objects(exam_id, id),
    CONSTRAINT exam_revision_workspace_content_check CHECK (
        (kind = 'directory' AND object_id IS NULL AND content_version IS NULL AND media_type IS NULL AND size_bytes IS NULL AND sha256 IS NULL) OR
        (kind = 'file' AND object_id IS NOT NULL AND content_version ~ '^[A-Za-z0-9_-]{26}$' AND
            media_type IS NOT NULL AND char_length(btrim(media_type)) > 0 AND
            size_bytes BETWEEN 0 AND 10485760 AND sha256 ~ '^[0-9a-f]{64}$')
    )
);

ALTER TABLE exams
    ADD CONSTRAINT exams_default_revision_fkey
    FOREIGN KEY (id, default_revision_id) REFERENCES exam_revisions(exam_id, id);
ALTER TABLE exam_drafts
    ADD CONSTRAINT exam_drafts_base_revision_fkey
    FOREIGN KEY (exam_id, base_revision_id) REFERENCES exam_revisions(exam_id, id);

CREATE FUNCTION guard_exam_revision_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NOT OLD.sealed AND NEW.sealed AND
       (to_jsonb(NEW) - 'sealed') = (to_jsonb(OLD) - 'sealed') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'published Exam Revisions are immutable' USING ERRCODE = '55000';
END;
$$;

CREATE FUNCTION reject_exam_revision_child_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'published Exam Revision children are immutable' USING ERRCODE = '55000';
END;
$$;

CREATE FUNCTION reject_sealed_exam_revision_child_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    revision_sealed boolean;
BEGIN
    -- Publication already owns this Exam row. A concurrent insertion waits
    -- until that transaction seals the Revision before it can inspect it.
    PERFORM 1 FROM exams WHERE id = NEW.exam_id FOR KEY SHARE;
    SELECT sealed INTO revision_sealed FROM exam_revisions
        WHERE exam_id = NEW.exam_id AND id = NEW.exam_revision_id;
    IF revision_sealed THEN
        RAISE EXCEPTION 'published Exam Revision children are immutable' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION enforce_exam_revision_sealed() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    current_revision exam_revisions%ROWTYPE;
    actual_resources integer;
    actual_starter_entries integer;
    actual_starter_bytes bigint;
BEGIN
    SELECT * INTO current_revision FROM exam_revisions WHERE id = NEW.id;
    SELECT count(*) INTO actual_resources FROM exam_revision_resources
        WHERE exam_revision_id = NEW.id;
    SELECT count(*), COALESCE(sum(size_bytes), 0)
        INTO actual_starter_entries, actual_starter_bytes
        FROM exam_revision_starter_workspace_entries
        WHERE exam_revision_id = NEW.id;
    IF NOT current_revision.sealed OR current_revision.resource_count <> actual_resources OR
       current_revision.starter_entry_count <> actual_starter_entries OR
       current_revision.starter_total_bytes <> actual_starter_bytes THEN
        RAISE EXCEPTION 'Exam Revision snapshot was not sealed completely' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER exam_revisions_immutable
    BEFORE UPDATE OR DELETE ON exam_revisions
    FOR EACH ROW EXECUTE FUNCTION guard_exam_revision_mutation();
CREATE TRIGGER exam_revision_resources_immutable
    BEFORE UPDATE OR DELETE ON exam_revision_resources
    FOR EACH ROW EXECUTE FUNCTION reject_exam_revision_child_mutation();
CREATE TRIGGER exam_revision_workspace_immutable
    BEFORE UPDATE OR DELETE ON exam_revision_starter_workspace_entries
    FOR EACH ROW EXECUTE FUNCTION reject_exam_revision_child_mutation();
CREATE TRIGGER exam_revision_resources_insert_guard
    BEFORE INSERT ON exam_revision_resources
    FOR EACH ROW EXECUTE FUNCTION reject_sealed_exam_revision_child_insert();
CREATE TRIGGER exam_revision_workspace_insert_guard
    BEFORE INSERT ON exam_revision_starter_workspace_entries
    FOR EACH ROW EXECUTE FUNCTION reject_sealed_exam_revision_child_insert();
CREATE CONSTRAINT TRIGGER exam_revision_sealed_check
    AFTER INSERT OR UPDATE ON exam_revisions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION enforce_exam_revision_sealed();

-- One Sitting delivers one sealed Exam Revision to one exact Class. The
-- constant boolean in the composite foreign key makes the sealed-only rule a
-- database invariant, rather than a convention of the scheduling adapter.
CREATE TABLE exam_sittings (
    id varchar(26) PRIMARY KEY,
    exam_id varchar(26) NOT NULL REFERENCES exams(id),
    exam_revision_id varchar(26) NOT NULL,
    exam_revision_sealed boolean NOT NULL DEFAULT true CHECK (exam_revision_sealed),
    class_id varchar(26) NOT NULL REFERENCES classes(id),
    scheduled_start_at timestamptz NOT NULL,
    scheduled_end_at timestamptz NOT NULL,
    state varchar(16) NOT NULL CHECK (state IN ('scheduled', 'open', 'paused', 'closing', 'closed', 'canceled')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    opened_at timestamptz,
    paused_at timestamptz,
    closing_at timestamptz,
    closed_at timestamptz,
    canceled_at timestamptz,
    reason_code varchar(32),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    mail_reconciliation_actor_user_id varchar(26) REFERENCES users(id),
    mail_disabled_suppressed_revision bigint,
    mail_disabled_suppressed_audience_revision bigint,
    mail_disabled_suppressed_eligibility_revision bigint,
    UNIQUE (exam_id, id),
    CONSTRAINT exam_sittings_revision_fkey
        FOREIGN KEY (exam_id, exam_revision_id, exam_revision_sealed)
        REFERENCES exam_revisions(exam_id, id, sealed),
    CONSTRAINT exam_sittings_schedule_check CHECK (scheduled_start_at < scheduled_end_at),
    CONSTRAINT exam_sittings_mail_disabled_suppressed_revision_check CHECK (
        (mail_disabled_suppressed_revision IS NULL AND mail_disabled_suppressed_audience_revision IS NULL AND
            mail_disabled_suppressed_eligibility_revision IS NULL) OR
        (mail_disabled_suppressed_revision BETWEEN 1 AND revision AND mail_disabled_suppressed_audience_revision >= 0 AND
            mail_disabled_suppressed_eligibility_revision >= 0)
    ),
    CONSTRAINT exam_sittings_timestamps_check CHECK (
        updated_at >= created_at AND
        (opened_at IS NULL OR opened_at BETWEEN created_at AND updated_at) AND
        (paused_at IS NULL OR paused_at BETWEEN created_at AND updated_at) AND
        (closing_at IS NULL OR closing_at BETWEEN created_at AND updated_at) AND
        (closed_at IS NULL OR closed_at BETWEEN created_at AND updated_at) AND
        (canceled_at IS NULL OR canceled_at BETWEEN created_at AND updated_at)
    ),
    CONSTRAINT exam_sittings_lifecycle_check CHECK (
        (state = 'scheduled' AND opened_at IS NULL AND paused_at IS NULL AND closing_at IS NULL AND
            closed_at IS NULL AND canceled_at IS NULL AND reason_code IS NULL) OR
        (state = 'open' AND opened_at IS NOT NULL AND paused_at IS NULL AND closing_at IS NULL AND
            closed_at IS NULL AND canceled_at IS NULL AND reason_code IS NULL) OR
        (state = 'paused' AND opened_at IS NOT NULL AND paused_at IS NOT NULL AND closing_at IS NULL AND
            closed_at IS NULL AND canceled_at IS NULL AND reason_code IS NULL) OR
        (state = 'closing' AND opened_at IS NOT NULL AND paused_at IS NULL AND closing_at IS NOT NULL AND
            closed_at IS NULL AND canceled_at IS NULL AND reason_code IN ('manager_closed', 'scheduled_end_reached')) OR
        (state = 'closed' AND opened_at IS NOT NULL AND paused_at IS NULL AND closing_at IS NOT NULL AND
            closed_at IS NOT NULL AND canceled_at IS NULL AND reason_code IN ('manager_closed', 'scheduled_end_reached')) OR
        (state = 'canceled' AND opened_at IS NULL AND paused_at IS NULL AND closing_at IS NULL AND
            closed_at IS NULL AND canceled_at IS NOT NULL AND
            reason_code IN ('manager_canceled', 'schedule_elapsed', 'academic_structure_invalid'))
    )
);

CREATE INDEX exam_sittings_exam_schedule_idx
    ON exam_sittings (exam_id, scheduled_start_at DESC, id DESC);
CREATE INDEX exam_sittings_exam_class_schedule_idx
    ON exam_sittings (exam_id, class_id, scheduled_start_at DESC, id DESC);
CREATE INDEX exam_sittings_exam_state_schedule_idx
    ON exam_sittings (exam_id, state, scheduled_start_at DESC, id DESC);
CREATE INDEX exam_sittings_lifecycle_open_due_idx
    ON exam_sittings (scheduled_start_at, id) WHERE state = 'scheduled';
CREATE INDEX exam_sittings_lifecycle_deadline_due_idx
    ON exam_sittings (scheduled_end_at, id) WHERE state IN ('open', 'paused');
CREATE INDEX exam_sittings_lifecycle_closing_due_idx
    ON exam_sittings (closing_at, id) WHERE state = 'closing';

-- Resource bytes staged for a live correction remain pending and invisible
-- until the named correction transaction consumes them. Exact command replay
-- returns the same preallocated identities and current stage state.
CREATE TABLE exam_correction_resource_stages (
    id varchar(26) PRIMARY KEY,
    exam_id varchar(26) NOT NULL,
    exam_sitting_id varchar(26) NOT NULL,
    base_revision_id varchar(26) NOT NULL,
    target varchar(16) NOT NULL CHECK (target IN ('addition', 'replacement')),
    resource_id varchar(26) NOT NULL,
    file_entry_id varchar(26) NOT NULL,
    file_revision_id varchar(26) NOT NULL UNIQUE,
    upload_lease_id varchar(26) NOT NULL UNIQUE,
    rendition_id varchar(26) NOT NULL UNIQUE,
    created_by_user_id varchar(26) NOT NULL REFERENCES users(id),
    state varchar(16) NOT NULL CHECK (state IN ('pending', 'ready', 'consumed')),
    created_at timestamptz NOT NULL,
    cleanup_protected_until timestamptz NOT NULL,
    ready_at timestamptz,
    consumed_at timestamptz,
    CONSTRAINT exam_correction_stages_sitting_fkey
        FOREIGN KEY (exam_id, exam_sitting_id) REFERENCES exam_sittings(exam_id, id),
    CONSTRAINT exam_correction_stages_base_fkey
        FOREIGN KEY (exam_id, base_revision_id) REFERENCES exam_revisions(exam_id, id),
    CONSTRAINT exam_correction_stages_identity_fkey
        FOREIGN KEY (exam_id, resource_id, file_entry_id)
        REFERENCES exam_resource_identities(exam_id, id, file_entry_id),
    CONSTRAINT exam_correction_stages_file_revision_fkey
        FOREIGN KEY (file_revision_id, file_entry_id) REFERENCES file_revisions(id, file_entry_id),
    CONSTRAINT exam_correction_stages_upload_lease_fkey
        FOREIGN KEY (upload_lease_id) REFERENCES upload_leases(id),
    CONSTRAINT exam_correction_stages_state_check CHECK (
        (state = 'pending' AND ready_at IS NULL AND consumed_at IS NULL) OR
        (state = 'ready' AND ready_at IS NOT NULL AND consumed_at IS NULL) OR
        (state = 'consumed' AND ready_at IS NOT NULL AND consumed_at IS NOT NULL)
    ),
    CONSTRAINT exam_correction_stages_time_check CHECK (
        cleanup_protected_until >= created_at AND
        (ready_at IS NULL OR ready_at >= created_at) AND
        (consumed_at IS NULL OR consumed_at >= ready_at)
    )
);

CREATE INDEX exam_correction_resource_stages_sitting_state_idx
    ON exam_correction_resource_stages (exam_sitting_id, state, id);

CREATE TABLE class_members (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    mail_audience_revision bigint NOT NULL CHECK (mail_audience_revision > 0),
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
CREATE INDEX class_members_current_class_user_idx
    ON class_members (class_id, user_id, start_at, end_at)
    WHERE archived_at IS NULL;
CREATE INDEX class_members_class_user_history_idx
    ON class_members (class_id, user_id, start_at, end_at, archived_at);

-- One bounded Sitting transition owns one encrypted, release-frozen render
-- bundle. Its expansion Job pages the current roster and the durable
-- last-communicated projection; deleting the bundle after terminal expansion
-- destroys the shared recoverable payload without deleting mail history.
CREATE TABLE exam_sitting_mail_fanouts (
    occurrence_id varchar(26) PRIMARY KEY REFERENCES mail_occurrences(id),
    bundle_id varchar(26) UNIQUE REFERENCES mail_fanout_bundles(id) ON DELETE SET NULL,
    exam_sitting_id varchar(26) NOT NULL REFERENCES exam_sittings(id),
    sitting_revision bigint NOT NULL CHECK (sitting_revision > 0),
    prior_class_id varchar(26) REFERENCES classes(id),
    change_kind varchar(16) NOT NULL CHECK (change_kind IN ('scheduled', 'rescheduled', 'cancelled', 'reconciled')),
    created_at timestamptz NOT NULL,
    deadline timestamptz NOT NULL CHECK (deadline > created_at),
    completed_at timestamptz,
	terminal_reason varchar(24) NOT NULL DEFAULT '' CHECK (terminal_reason IN
		('', 'completed', 'superseded', 'suppressed_disabled', 'expired', 'failed', 'orphaned')),
    CONSTRAINT exam_sitting_mail_fanouts_lifecycle_check CHECK (
		((completed_at IS NULL AND bundle_id IS NOT NULL AND terminal_reason = '') OR
		 (completed_at IS NOT NULL AND bundle_id IS NULL AND terminal_reason <> '')) AND
        (completed_at IS NULL OR completed_at >= created_at)
    )
);

CREATE INDEX exam_sitting_mail_fanouts_sitting_revision_idx
    ON exam_sitting_mail_fanouts (exam_sitting_id, sitting_revision DESC);
CREATE UNIQUE INDEX exam_sitting_mail_fanouts_one_active_per_sitting_key
    ON exam_sitting_mail_fanouts (exam_sitting_id) WHERE completed_at IS NULL;

-- Desired delivery identity fences Start against a newer schedule fact. The
-- communicated columns advance only when SMTP accepts that desired delivery.
CREATE TABLE exam_sitting_mail_recipients (
    exam_sitting_id varchar(26) NOT NULL REFERENCES exam_sittings(id),
    user_id varchar(26) NOT NULL REFERENCES users(id),
    desired_occurrence_id varchar(26) REFERENCES mail_occurrences(id),
    desired_delivery_id varchar(26) REFERENCES mail_deliveries(id),
    desired_sitting_revision bigint CHECK (desired_sitting_revision > 0),
    desired_template_key varchar(128),
    communicated_sitting_revision bigint CHECK (communicated_sitting_revision > 0),
    communicated_template_key varchar(128),
    communicated_class_id varchar(26) REFERENCES classes(id),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (exam_sitting_id, user_id),
    CONSTRAINT exam_sitting_mail_recipients_desired_check CHECK (
        (desired_occurrence_id IS NULL) = (desired_delivery_id IS NULL) AND
        (desired_delivery_id IS NULL) = (desired_sitting_revision IS NULL) AND
        (desired_sitting_revision IS NULL) = (desired_template_key IS NULL) AND
        (desired_template_key IS NULL OR desired_template_key IN
            ('exam.sitting_scheduled', 'exam.sitting_rescheduled', 'exam.sitting_cancelled', 'exam.sitting_assignment_removed'))
    ),
    CONSTRAINT exam_sitting_mail_recipients_communicated_check CHECK (
        (communicated_sitting_revision IS NULL) = (communicated_template_key IS NULL) AND
        (communicated_template_key IS NULL) = (communicated_class_id IS NULL) AND
        (communicated_template_key IS NULL OR communicated_template_key IN
            ('exam.sitting_scheduled', 'exam.sitting_rescheduled', 'exam.sitting_cancelled', 'exam.sitting_assignment_removed'))
    )
);

CREATE INDEX exam_sitting_mail_recipients_user_idx
    ON exam_sitting_mail_recipients (user_id, exam_sitting_id);

ALTER TABLE invitations
    ADD CONSTRAINT invitations_accepted_affiliation_id_fkey
    FOREIGN KEY (accepted_affiliation_id) REFERENCES affiliations(id),
    ADD CONSTRAINT invitations_accepted_class_member_id_fkey
    FOREIGN KEY (accepted_class_member_id) REFERENCES class_members(id),
    ADD CONSTRAINT invitations_accepted_academic_unit_member_id_fkey
    FOREIGN KEY (accepted_academic_unit_member_id) REFERENCES academic_unit_members(id);

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

ALTER TABLE invitations
    ADD CONSTRAINT invitations_role_id_fkey FOREIGN KEY (role_id) REFERENCES roles(id);

ALTER TABLE onboarding_imports
    ADD CONSTRAINT onboarding_imports_role_id_fkey FOREIGN KEY (role_id) REFERENCES roles(id);

ALTER TABLE onboarding_import_rows
    ADD CONSTRAINT onboarding_import_rows_role_id_fkey FOREIGN KEY (role_id) REFERENCES roles(id);
ALTER TABLE onboarding_import_rows
    ADD CONSTRAINT onboarding_import_rows_destination_relationship_ref_fkey
    FOREIGN KEY (destination_relationship_ref) REFERENCES class_members(id);

CREATE TABLE role_bindings (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    role_id varchar(26) NOT NULL REFERENCES roles(id),
    -- Kept as a canonical provenance identifier without a foreign key because
    -- terminal Invitation rows are retention-bounded while Role history is not.
    origin_invitation_id varchar(26),
    origin_academic_unit_member_id varchar(26) REFERENCES academic_unit_members(id),
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

CREATE INDEX role_bindings_origin_invitation_id_idx
    ON role_bindings (origin_invitation_id)
    WHERE origin_invitation_id IS NOT NULL;

ALTER TABLE invitations
    ADD CONSTRAINT invitations_accepted_role_binding_id_fkey
    FOREIGN KEY (accepted_role_binding_id) REFERENCES role_bindings(id);

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
	authentication_provider_id varchar(64) NOT NULL DEFAULT ''
		CHECK (authentication_provider_id = '' OR authentication_provider_id ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
	external_identity_id varchar(26) REFERENCES external_identities(id),
	authentication_strength varchar(32) NOT NULL
        CHECK (authentication_strength IN ('single_factor', 'multi_factor')),
    authenticated_at timestamptz NOT NULL,
    mfa_completed_at timestamptz,
    last_activity_at timestamptz NOT NULL,
    idle_expires_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revocation_reason varchar(1024) NOT NULL DEFAULT '',
    CONSTRAINT sessions_lifecycle_check CHECK (updated_at >= created_at),
	CONSTRAINT sessions_revocation_check CHECK (
		(revoked_at IS NULL AND revocation_reason = '') OR
		(revoked_at IS NOT NULL AND revocation_reason IN (
			'access_policy_changed', 'account_disabled', 'administrator_all_sessions',
			'administrator_session', 'authentication_audit_failed', 'external_identity_unlinked',
			'inactive_user', 'password_removed', 'password_reset', 'refresh_replay',
			'user_all_sessions', 'user_logout', 'user_session'
		))
	),
	CONSTRAINT sessions_authentication_identity_check CHECK (
		(authentication_provider_id = '' AND external_identity_id IS NULL) OR
		(authentication_provider_id <> '' AND external_identity_id IS NOT NULL)
	)
);

CREATE INDEX sessions_user_id_last_activity_at_idx
    ON sessions (user_id) WHERE archived_at IS NULL AND revoked_at IS NULL;
CREATE INDEX sessions_expires_at_idx
	ON sessions (expires_at) WHERE archived_at IS NULL AND revoked_at IS NULL;
CREATE INDEX sessions_authentication_provider_id_idx
	ON sessions (authentication_provider_id) WHERE archived_at IS NULL AND revoked_at IS NULL;
CREATE INDEX sessions_external_identity_id_idx
	ON sessions (external_identity_id) WHERE external_identity_id IS NOT NULL AND archived_at IS NULL AND revoked_at IS NULL;

-- An Attempt is the stable work identity for one candidate in one Sitting.
-- Admission copies only logical workspace metadata; immutable starter bytes
-- remain pinned through opaque starter-object identities until copy-on-write.
CREATE TABLE exam_attempts (
    id varchar(26) PRIMARY KEY,
    exam_id varchar(26) NOT NULL,
    exam_sitting_id varchar(26) NOT NULL,
    candidate_user_id varchar(26) NOT NULL REFERENCES users(id),
    admission_revision_id varchar(26) NOT NULL,
    state varchar(16) NOT NULL CHECK (state IN ('active', 'suspended', 'submitted')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    submitted_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    UNIQUE (exam_sitting_id, candidate_user_id),
    UNIQUE (exam_sitting_id, id),
    UNIQUE (id, admission_revision_id),
    UNIQUE (id, exam_id, admission_revision_id),
    CONSTRAINT exam_attempts_sitting_fkey
        FOREIGN KEY (exam_id, exam_sitting_id) REFERENCES exam_sittings(exam_id, id),
    CONSTRAINT exam_attempts_admission_revision_fkey
        FOREIGN KEY (exam_id, admission_revision_id) REFERENCES exam_revisions(exam_id, id),
    CONSTRAINT exam_attempts_lifecycle_check CHECK (
        updated_at >= created_at AND
        ((state IN ('active', 'suspended') AND submitted_at IS NULL) OR
         (state = 'submitted' AND submitted_at BETWEEN created_at AND updated_at))
    )
);

CREATE INDEX exam_attempts_sitting_state_created_id_idx
    ON exam_attempts (exam_sitting_id, state, created_at DESC, id DESC);
CREATE INDEX exam_attempts_sitting_unfinished_id_idx
    ON exam_attempts (exam_sitting_id, id)
    WHERE state IN ('active', 'suspended');
CREATE INDEX exam_attempts_sitting_created_id_idx
    ON exam_attempts (exam_sitting_id, created_at DESC, id DESC);
CREATE INDEX exam_attempts_candidate_created_id_idx
    ON exam_attempts (candidate_user_id, created_at DESC, id DESC);

-- An Execution Grant records only authoritative placement and cleanup state.
-- Live host readiness and capacity are deliberately not indexed in PostgreSQL.
CREATE TABLE execution_grants (
    id varchar(26) PRIMARY KEY,
    exam_attempt_id varchar(26) NOT NULL REFERENCES exam_attempts(id),
    host_id varchar(64) NOT NULL CHECK (host_id ~ '^[A-Za-z0-9._-]{1,64}$'),
    image varchar(255) NOT NULL CHECK (image <> ''),
    network varchar(16) NOT NULL CHECK (network IN ('none', 'allowlist')),
    state varchar(16) NOT NULL CHECK (state IN ('reserved', 'ready', 'released')),
    applied_sitting_state varchar(16) NOT NULL CHECK (applied_sitting_state IN ('open', 'paused')),
    applied_sitting_revision bigint NOT NULL CHECK (applied_sitting_revision > 0),
    lifecycle_pending boolean NOT NULL DEFAULT false,
    pending_sitting_state varchar(16) CHECK (pending_sitting_state IN ('open', 'paused')),
    pending_sitting_revision bigint CHECK (pending_sitting_revision > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    released_at timestamptz,
    revoked_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    CONSTRAINT execution_grants_lifecycle_check CHECK (
        updated_at >= created_at AND
        ((state IN ('reserved', 'ready') AND released_at IS NULL AND revoked_at IS NULL) OR
         (state = 'released' AND released_at IS NOT NULL AND released_at >= created_at AND
          (revoked_at IS NULL OR revoked_at >= released_at)))
    ),
    CONSTRAINT execution_grants_pending_lifecycle_check CHECK (
        (lifecycle_pending AND pending_sitting_state IS NOT NULL AND pending_sitting_revision IS NOT NULL) OR
        (NOT lifecycle_pending AND pending_sitting_state IS NULL AND pending_sitting_revision IS NULL)
    )
);

CREATE UNIQUE INDEX execution_grants_one_active_attempt_idx
    ON execution_grants (exam_attempt_id) WHERE state IN ('reserved', 'ready');
CREATE INDEX execution_grants_pending_revocation_idx
    ON execution_grants (released_at, id) WHERE state = 'released' AND revoked_at IS NULL;

CREATE TABLE exam_attempt_workspaces (
    id varchar(26) PRIMARY KEY,
    exam_attempt_id varchar(26) NOT NULL UNIQUE,
    admission_revision_id varchar(26) NOT NULL,
    cursor bigint NOT NULL DEFAULT 0 CHECK (cursor >= 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (id, admission_revision_id),
    CONSTRAINT exam_attempt_workspaces_attempt_fkey
        FOREIGN KEY (exam_attempt_id, admission_revision_id)
        REFERENCES exam_attempts(id, admission_revision_id),
    CONSTRAINT exam_attempt_workspaces_lifecycle_check CHECK (updated_at >= created_at)
);

ALTER TABLE exam_revision_starter_workspace_entries
    ADD CONSTRAINT exam_revision_workspace_entry_object_key
    UNIQUE (exam_revision_id, entry_id, object_id);

CREATE TABLE exam_attempt_workspace_objects (
    id varchar(26) PRIMARY KEY,
    workspace_id varchar(26) NOT NULL REFERENCES exam_attempt_workspaces(id),
    admission_revision_id varchar(26) NOT NULL,
    source_starter_entry_id varchar(26),
    storage_origin varchar(16) NOT NULL CHECK (storage_origin IN ('starter', 'attempt')),
    starter_object_id varchar(26) REFERENCES exam_starter_workspace_objects(id),
    state varchar(16) NOT NULL CHECK (state IN ('staged', 'current', 'reclaimable', 'claimed')),
    content_version varchar(26),
    media_type varchar(255),
    size_bytes bigint,
    sha256 char(64),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz,
    reclaim_after timestamptz,
    claim_token varchar(128),
    claimed_at timestamptz,
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, id, admission_revision_id, source_starter_entry_id),
    CONSTRAINT exam_attempt_workspace_objects_source_fkey
        FOREIGN KEY (admission_revision_id, source_starter_entry_id, starter_object_id)
        REFERENCES exam_revision_starter_workspace_entries(exam_revision_id, entry_id, object_id),
    CONSTRAINT exam_attempt_workspace_objects_origin_check CHECK (
        (storage_origin = 'starter' AND source_starter_entry_id IS NOT NULL AND starter_object_id IS NOT NULL AND state = 'current') OR
        (storage_origin = 'attempt' AND source_starter_entry_id IS NULL AND starter_object_id IS NULL)
    ),
    CONSTRAINT exam_attempt_workspace_objects_content_check CHECK (
        (content_version IS NULL AND media_type IS NULL AND size_bytes IS NULL AND sha256 IS NULL) OR
        (content_version ~ '^[A-Za-z0-9_-]{26}$' AND char_length(btrim(media_type)) > 0 AND
         size_bytes BETWEEN 0 AND 10485760 AND sha256 ~ '^[0-9a-f]{64}$')
    ),
    CONSTRAINT exam_attempt_workspace_objects_lifecycle_check CHECK (
        updated_at >= created_at AND (expires_at IS NULL OR expires_at > created_at) AND
        ((storage_origin = 'starter' AND content_version IS NOT NULL AND expires_at IS NULL AND
          reclaim_after IS NULL AND claim_token IS NULL AND claimed_at IS NULL) OR
         (storage_origin = 'attempt' AND state = 'staged' AND expires_at IS NOT NULL AND
          reclaim_after IS NULL AND claim_token IS NULL AND claimed_at IS NULL) OR
         (storage_origin = 'attempt' AND state = 'current' AND content_version IS NOT NULL AND expires_at IS NULL AND
          reclaim_after IS NULL AND claim_token IS NULL AND claimed_at IS NULL) OR
         (storage_origin = 'attempt' AND state = 'reclaimable' AND reclaim_after IS NOT NULL AND
          claim_token IS NULL AND claimed_at IS NULL) OR
         (storage_origin = 'attempt' AND state = 'claimed' AND reclaim_after IS NOT NULL AND
          claim_token IS NOT NULL AND char_length(btrim(claim_token)) > 0 AND claimed_at = updated_at))
    )
);

CREATE INDEX exam_attempt_workspace_objects_starter_idx
    ON exam_attempt_workspace_objects (starter_object_id)
    WHERE starter_object_id IS NOT NULL;

CREATE INDEX exam_attempt_workspace_objects_staged_cleanup_idx
    ON exam_attempt_workspace_objects (expires_at, id)
    WHERE storage_origin = 'attempt' AND state = 'staged';

CREATE INDEX exam_attempt_workspace_objects_reclaimable_cleanup_idx
    ON exam_attempt_workspace_objects (reclaim_after, id)
    WHERE storage_origin = 'attempt' AND state = 'reclaimable';

CREATE INDEX exam_attempt_workspace_objects_claimed_cleanup_idx
    ON exam_attempt_workspace_objects (claimed_at, id)
    WHERE storage_origin = 'attempt' AND state = 'claimed';

CREATE TABLE exam_attempt_workspace_entries (
    id varchar(26) PRIMARY KEY,
    workspace_id varchar(26) NOT NULL,
    admission_revision_id varchar(26) NOT NULL,
    source_starter_entry_id varchar(26),
    kind varchar(16) NOT NULL CHECK (kind IN ('file', 'directory')),
    path text NOT NULL CHECK (octet_length(path) BETWEEN 1 AND 1024),
    current_object_id varchar(26),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (workspace_id, source_starter_entry_id),
    UNIQUE (workspace_id, id),
    CONSTRAINT exam_attempt_workspace_entries_workspace_fkey
        FOREIGN KEY (workspace_id, admission_revision_id)
        REFERENCES exam_attempt_workspaces(id, admission_revision_id),
    CONSTRAINT exam_attempt_workspace_entries_source_fkey
        FOREIGN KEY (admission_revision_id, source_starter_entry_id)
        REFERENCES exam_revision_starter_workspace_entries(exam_revision_id, entry_id),
    CONSTRAINT exam_attempt_workspace_entries_workspace_object_fkey
        FOREIGN KEY (workspace_id, current_object_id)
        REFERENCES exam_attempt_workspace_objects(workspace_id, id),
    CONSTRAINT exam_attempt_workspace_entries_content_check CHECK (
        (kind = 'file' AND current_object_id IS NOT NULL) OR
        (kind = 'directory' AND current_object_id IS NULL)
    ),
    CONSTRAINT exam_attempt_workspace_entries_lifecycle_check CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX exam_attempt_workspace_entries_path_key
    ON exam_attempt_workspace_entries (workspace_id, path);

CREATE TABLE exam_attempt_workspace_journal (
    workspace_id varchar(26) NOT NULL REFERENCES exam_attempt_workspaces(id),
    cursor bigint NOT NULL CHECK (cursor > 0),
    entry_id varchar(26) NOT NULL,
    entry_kind varchar(16) NOT NULL CHECK (entry_kind IN ('file', 'directory')),
    operation varchar(24) NOT NULL CHECK (operation IN ('create_file', 'create_directory', 'replace_file', 'move_entry', 'delete_entry')),
    old_path text,
    new_path text,
    content_version varchar(26),
    mutation_key_digest bytea NOT NULL CHECK (octet_length(mutation_key_digest) = 32),
    changed_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, cursor),
    CONSTRAINT exam_attempt_workspace_journal_path_check CHECK (
        (old_path IS NULL OR octet_length(old_path) BETWEEN 1 AND 1024) AND
        (new_path IS NULL OR octet_length(new_path) BETWEEN 1 AND 1024)
    )
);

CREATE INDEX exam_attempt_workspace_journal_entry_cursor_idx
    ON exam_attempt_workspace_journal (workspace_id, entry_id, cursor DESC);

CREATE TABLE exam_attempt_participations (
    id varchar(26) PRIMARY KEY,
    exam_attempt_id varchar(26) NOT NULL REFERENCES exam_attempts(id),
    state varchar(16) NOT NULL CHECK (state IN ('active', 'ended')),
    generation bigint NOT NULL CHECK (generation > 0),
    renewal_sequence bigint NOT NULL DEFAULT 0 CHECK (renewal_sequence >= 0),
    continuity_credential_hash char(64) NOT NULL UNIQUE
        CHECK (continuity_credential_hash ~ '^[0-9a-f]{64}$'),
    started_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    ended_at timestamptz,
    end_reason varchar(24) CHECK (end_reason IN (
        'interrupted', 'lease_expired', 'policy_suspended', 'kicked', 'submitted', 'sitting_closed'
    )),
    UNIQUE (exam_attempt_id, generation),
    UNIQUE (id, exam_attempt_id),
    UNIQUE (id, exam_attempt_id, generation),
    CONSTRAINT exam_attempt_participations_time_check CHECK (
        updated_at >= started_at AND lease_expires_at > started_at AND
        (renewal_sequence <> 0 OR lease_expires_at = started_at + INTERVAL '20 seconds')
    ),
    CONSTRAINT exam_attempt_participations_lifecycle_check CHECK (
        (state = 'active' AND ended_at IS NULL AND end_reason IS NULL) OR
        (state = 'ended' AND ended_at BETWEEN started_at AND updated_at AND end_reason IS NOT NULL)
    )
);

CREATE UNIQUE INDEX exam_attempt_participations_one_active_key
    ON exam_attempt_participations (exam_attempt_id) WHERE state = 'active';
CREATE INDEX exam_attempt_participations_expiry_idx
    ON exam_attempt_participations (lease_expires_at, id)
    WHERE state = 'active';

CREATE TABLE exam_attempt_connections (
    id varchar(26) PRIMARY KEY,
    exam_attempt_id varchar(26) NOT NULL,
    participation_id varchar(26) NOT NULL,
    session_id varchar(26) NOT NULL REFERENCES sessions(id),
    state varchar(16) NOT NULL CHECK (state IN ('open', 'closed')),
    opened_at timestamptz NOT NULL,
    closed_at timestamptz,
    close_reason varchar(24) CHECK (close_reason IN (
        'transport_closed', 'interrupted', 'lease_expired', 'policy_suspended', 'kicked', 'submitted', 'sitting_closed'
    )),
    UNIQUE (id, exam_attempt_id, participation_id),
    CONSTRAINT exam_attempt_connections_participation_fkey
        FOREIGN KEY (participation_id, exam_attempt_id)
        REFERENCES exam_attempt_participations(id, exam_attempt_id),
    CONSTRAINT exam_attempt_connections_lifecycle_check CHECK (
        (state = 'open' AND closed_at IS NULL AND close_reason IS NULL) OR
        (state = 'closed' AND closed_at >= opened_at AND close_reason IS NOT NULL)
    )
);

CREATE UNIQUE INDEX exam_attempt_connections_one_open_attempt_key
    ON exam_attempt_connections (exam_attempt_id) WHERE state = 'open';
CREATE UNIQUE INDEX exam_attempt_connections_one_open_participation_key
    ON exam_attempt_connections (participation_id) WHERE state = 'open';
CREATE INDEX exam_attempt_connections_attempt_opened_id_idx
    ON exam_attempt_connections (exam_attempt_id, opened_at DESC, id DESC);

CREATE FUNCTION guard_exam_attempt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.exam_id IS DISTINCT FROM OLD.exam_id OR
       NEW.exam_sitting_id IS DISTINCT FROM OLD.exam_sitting_id OR
       NEW.candidate_user_id IS DISTINCT FROM OLD.candidate_user_id OR
       NEW.admission_revision_id IS DISTINCT FROM OLD.admission_revision_id OR
       NEW.created_at IS DISTINCT FROM OLD.created_at OR OLD.state = 'submitted' THEN
        RAISE EXCEPTION 'Exam Attempt immutable identity or terminal state changed' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER exam_attempts_guard
    BEFORE UPDATE ON exam_attempts FOR EACH ROW EXECUTE FUNCTION guard_exam_attempt_mutation();

CREATE FUNCTION guard_exam_attempt_workspace_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.exam_attempt_id IS DISTINCT FROM OLD.exam_attempt_id OR
       NEW.admission_revision_id IS DISTINCT FROM OLD.admission_revision_id OR
       NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.cursor < OLD.cursor THEN
        RAISE EXCEPTION 'Exam Attempt Workspace immutable identity or cursor regressed' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER exam_attempt_workspaces_guard
    BEFORE UPDATE ON exam_attempt_workspaces FOR EACH ROW EXECUTE FUNCTION guard_exam_attempt_workspace_mutation();

CREATE FUNCTION guard_exam_attempt_workspace_entry_object() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    object_origin varchar(16);
    object_revision_id varchar(26);
    object_source_entry_id varchar(26);
BEGIN
    IF NEW.current_object_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT storage_origin, admission_revision_id, source_starter_entry_id
      INTO object_origin, object_revision_id, object_source_entry_id
      FROM exam_attempt_workspace_objects
     WHERE workspace_id = NEW.workspace_id AND id = NEW.current_object_id;
    IF object_origin IS NULL OR
       (object_origin = 'starter' AND (object_revision_id IS DISTINCT FROM NEW.admission_revision_id OR
        object_source_entry_id IS DISTINCT FROM NEW.source_starter_entry_id)) THEN
        RAISE EXCEPTION 'Exam Attempt Workspace object provenance does not match Entry'
            USING ERRCODE = '23503', CONSTRAINT = 'exam_attempt_workspace_entries_object_provenance';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER exam_attempt_workspace_entries_object_provenance
    BEFORE INSERT OR UPDATE OF current_object_id, admission_revision_id, source_starter_entry_id
    ON exam_attempt_workspace_entries FOR EACH ROW
    EXECUTE FUNCTION guard_exam_attempt_workspace_entry_object();

CREATE FUNCTION reject_exam_attempt_workspace_object_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.workspace_id IS DISTINCT FROM OLD.workspace_id OR
       NEW.admission_revision_id IS DISTINCT FROM OLD.admission_revision_id OR
       NEW.source_starter_entry_id IS DISTINCT FROM OLD.source_starter_entry_id OR
       NEW.storage_origin IS DISTINCT FROM OLD.storage_origin OR
       NEW.starter_object_id IS DISTINCT FROM OLD.starter_object_id OR
       NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.updated_at < OLD.updated_at OR
       (OLD.content_version IS NOT NULL AND (NEW.content_version IS DISTINCT FROM OLD.content_version OR
        NEW.media_type IS DISTINCT FROM OLD.media_type OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes OR
        NEW.sha256 IS DISTINCT FROM OLD.sha256)) THEN
        RAISE EXCEPTION 'Exam Attempt Workspace object identity or content changed' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER exam_attempt_workspace_objects_immutable
    BEFORE UPDATE ON exam_attempt_workspace_objects FOR EACH ROW
    EXECUTE FUNCTION reject_exam_attempt_workspace_object_update();

CREATE FUNCTION guard_attempt_participation_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.exam_attempt_id IS DISTINCT FROM OLD.exam_attempt_id OR
       NEW.generation IS DISTINCT FROM OLD.generation OR
       NEW.continuity_credential_hash IS DISTINCT FROM OLD.continuity_credential_hash OR
       NEW.started_at IS DISTINCT FROM OLD.started_at OR OLD.state = 'ended' OR
       NEW.renewal_sequence < OLD.renewal_sequence OR NEW.updated_at < OLD.updated_at OR
       NEW.lease_expires_at < OLD.lease_expires_at THEN
        RAISE EXCEPTION 'Attempt Participation identity, terminal state, or fence regressed' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER exam_attempt_participations_guard
    BEFORE UPDATE ON exam_attempt_participations FOR EACH ROW
    EXECUTE FUNCTION guard_attempt_participation_mutation();

CREATE FUNCTION guard_attempt_connection_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.exam_attempt_id IS DISTINCT FROM OLD.exam_attempt_id OR
       NEW.participation_id IS DISTINCT FROM OLD.participation_id OR NEW.session_id IS DISTINCT FROM OLD.session_id OR
       NEW.opened_at IS DISTINCT FROM OLD.opened_at OR OLD.state = 'closed' THEN
        RAISE EXCEPTION 'Attempt Connection immutable identity or terminal state changed' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER exam_attempt_connections_guard
    BEFORE UPDATE ON exam_attempt_connections FOR EACH ROW
    EXECUTE FUNCTION guard_attempt_connection_mutation();

-- Integrity records are append-preserving facts. A Suspension is the one
-- mutable enforcement episode: only its active-to-closed re-allow transition
-- may add the manager actor and private reason.
CREATE TABLE integrity_flags (
    id varchar(26) PRIMARY KEY,
    exam_attempt_id varchar(26) NOT NULL REFERENCES exam_attempts(id),
    generation bigint NOT NULL CHECK (generation > 0),
    policy_kind varchar(32) NOT NULL CHECK (policy_kind IN ('connection_loss', 'focus_loss')),
    state varchar(16) NOT NULL CHECK (state = 'open'),
    created_at timestamptz NOT NULL,
    UNIQUE (exam_attempt_id, generation, policy_kind),
    UNIQUE (id, exam_attempt_id, generation)
);

CREATE TABLE integrity_evidence (
    id varchar(26) PRIMARY KEY,
    exam_attempt_id varchar(26) NOT NULL,
    participation_id varchar(26) NOT NULL,
    integrity_flag_id varchar(26) NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    policy_kind varchar(32) NOT NULL CHECK (policy_kind IN ('connection_loss', 'focus_loss')),
    focus_loss_signal_id varchar(26),
    sequence bigint,
    duration_milliseconds bigint,
    source varchar(32),
    missing_before bigint,
    observed_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL,
    CONSTRAINT integrity_evidence_participation_fkey
        FOREIGN KEY (participation_id, exam_attempt_id, generation)
        REFERENCES exam_attempt_participations(id, exam_attempt_id, generation),
    CONSTRAINT integrity_evidence_flag_fkey
        FOREIGN KEY (integrity_flag_id, exam_attempt_id, generation)
        REFERENCES integrity_flags(id, exam_attempt_id, generation),
    CONSTRAINT integrity_evidence_time_check CHECK (recorded_at >= observed_at),
    CONSTRAINT integrity_evidence_kind_check CHECK (
        (policy_kind = 'connection_loss' AND focus_loss_signal_id IS NULL AND sequence IS NULL AND
         duration_milliseconds IS NULL AND source IS NULL AND missing_before IS NULL) OR
        (policy_kind = 'focus_loss' AND focus_loss_signal_id IS NOT NULL AND sequence > 0 AND
         duration_milliseconds BETWEEN 1 AND 86400000 AND
         (source IS NULL OR source IN ('window_blur', 'document_hidden', 'application_backgrounded', 'fullscreen_exited')) AND
         missing_before >= 0)
    ),
    UNIQUE (exam_attempt_id, generation, policy_kind, sequence)
);

-- One bounded mutable evaluator exists per Participation generation. It keeps
-- only the latest accepted outcome needed for natural sequence replay, bounded
-- aggregate diagnostics/overflow, and the current consumed-window state.
CREATE TABLE exam_attempt_focus_loss_evaluations (
    exam_attempt_id varchar(26) NOT NULL,
    participation_id varchar(26) NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    accepted_sequence bigint NOT NULL DEFAULT 0 CHECK (accepted_sequence >= 0),
    last_signal_id varchar(26),
    last_connection_id varchar(26),
    last_duration_milliseconds bigint,
    last_source varchar(32),
    last_received_at timestamptz,
    last_collection_enabled boolean NOT NULL DEFAULT false,
    last_qualified boolean NOT NULL DEFAULT false,
    last_missing_before bigint NOT NULL DEFAULT 0 CHECK (last_missing_before >= 0),
    unresolved_missing_count bigint NOT NULL DEFAULT 0 CHECK (unresolved_missing_count >= 0),
    last_window_incident_count integer NOT NULL DEFAULT 0 CHECK (last_window_incident_count BETWEEN 0 AND 99),
    last_threshold_crossed boolean NOT NULL DEFAULT false,
    last_policy_outcome varchar(32),
    retained_evidence_count integer NOT NULL DEFAULT 0 CHECK (retained_evidence_count BETWEEN 0 AND 100),
    overflow_count bigint NOT NULL DEFAULT 0 CHECK (overflow_count >= 0),
    overflow_first_received_at timestamptz,
    overflow_last_received_at timestamptz,
    overflow_maximum_duration_milliseconds bigint,
    diagnostic_count bigint NOT NULL DEFAULT 0 CHECK (diagnostic_count >= 0),
    integrity_flag_id varchar(26),
    warning_created boolean NOT NULL DEFAULT false,
    last_flag_returned boolean NOT NULL DEFAULT false,
    last_flag_created boolean NOT NULL DEFAULT false,
    last_warning_created boolean NOT NULL DEFAULT false,
    last_manager_notification_required boolean NOT NULL DEFAULT false,
    last_connection_closed boolean NOT NULL DEFAULT false,
    last_suspension_id varchar(26),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (exam_attempt_id, generation),
    UNIQUE (participation_id, exam_attempt_id),
    UNIQUE (last_signal_id),
    CONSTRAINT exam_attempt_focus_loss_evaluations_participation_fkey
        FOREIGN KEY (participation_id, exam_attempt_id, generation)
        REFERENCES exam_attempt_participations(id, exam_attempt_id, generation),
    CONSTRAINT exam_attempt_focus_loss_evaluations_connection_fkey
        FOREIGN KEY (last_connection_id, exam_attempt_id, participation_id)
        REFERENCES exam_attempt_connections(id, exam_attempt_id, participation_id),
    CONSTRAINT exam_attempt_focus_loss_evaluations_flag_fkey
        FOREIGN KEY (integrity_flag_id, exam_attempt_id, generation)
        REFERENCES integrity_flags(id, exam_attempt_id, generation),
    CONSTRAINT exam_attempt_focus_loss_evaluations_last_claim_check CHECK (
        (accepted_sequence = 0 AND last_signal_id IS NULL AND last_connection_id IS NULL AND last_duration_milliseconds IS NULL AND
         last_source IS NULL AND last_received_at IS NULL AND last_policy_outcome IS NULL) OR
        (accepted_sequence > 0 AND last_signal_id IS NOT NULL AND last_connection_id IS NOT NULL AND last_duration_milliseconds BETWEEN 1 AND 86400000 AND
         (last_source IS NULL OR last_source IN ('window_blur', 'document_hidden', 'application_backgrounded', 'fullscreen_exited')) AND
         last_received_at IS NOT NULL AND
         ((last_collection_enabled AND last_threshold_crossed AND last_policy_outcome IN ('flag', 'flag_and_warn', 'flag_and_suspend')) OR
          (last_collection_enabled AND NOT last_threshold_crossed AND last_policy_outcome IS NULL) OR
          (NOT last_collection_enabled AND last_policy_outcome IS NULL)))
    ),
    CONSTRAINT exam_attempt_focus_loss_evaluations_overflow_check CHECK (
        (overflow_count = 0 AND overflow_first_received_at IS NULL AND overflow_last_received_at IS NULL AND
         overflow_maximum_duration_milliseconds IS NULL) OR
        (overflow_count > 0 AND retained_evidence_count = 100 AND overflow_first_received_at IS NOT NULL AND
         overflow_last_received_at >= overflow_first_received_at AND overflow_maximum_duration_milliseconds BETWEEN 1 AND 86400000)
    )
);

-- Pending qualifiers are internal evaluator state, capped by the policy's
-- maximum threshold minus one. They become evidence only when a threshold
-- consumes the bucket, so unconsumed observations are never presented as facts.
CREATE TABLE exam_attempt_focus_loss_pending (
    exam_attempt_id varchar(26) NOT NULL,
    participation_id varchar(26) NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    sequence bigint NOT NULL CHECK (sequence > 0),
    signal_id varchar(26) NOT NULL UNIQUE,
    evidence_id varchar(26) NOT NULL UNIQUE,
    duration_milliseconds bigint NOT NULL CHECK (duration_milliseconds BETWEEN 1 AND 86400000),
    source varchar(32) CHECK (source IS NULL OR source IN ('window_blur', 'document_hidden', 'application_backgrounded', 'fullscreen_exited')),
    missing_before bigint NOT NULL CHECK (missing_before >= 0),
    received_at timestamptz NOT NULL,
    PRIMARY KEY (exam_attempt_id, generation, sequence),
    CONSTRAINT exam_attempt_focus_loss_pending_evaluation_fkey
        FOREIGN KEY (exam_attempt_id, generation)
        REFERENCES exam_attempt_focus_loss_evaluations(exam_attempt_id, generation) ON DELETE CASCADE,
    CONSTRAINT exam_attempt_focus_loss_pending_participation_fkey
        FOREIGN KEY (participation_id, exam_attempt_id, generation)
        REFERENCES exam_attempt_participations(id, exam_attempt_id, generation)
);

CREATE TABLE exam_attempt_suspensions (
    id varchar(26) PRIMARY KEY,
    exam_attempt_id varchar(26) NOT NULL,
    participation_id varchar(26) NOT NULL,
    integrity_flag_id varchar(26) NOT NULL UNIQUE,
    generation bigint NOT NULL CHECK (generation > 0),
    suspension_attempt_revision bigint NOT NULL CHECK (suspension_attempt_revision > 1),
    state varchar(16) NOT NULL CHECK (state IN ('active', 'closed')),
    source varchar(16) NOT NULL CHECK (source = 'policy'),
    candidate_reason varchar(64) NOT NULL CHECK (candidate_reason IN ('secure_connectivity_lost', 'focus_policy_review_required')),
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    reallowed_by_user_id varchar(26) REFERENCES users(id),
    private_reason text,
    UNIQUE (id, exam_attempt_id, participation_id, generation),
    CONSTRAINT exam_attempt_suspensions_participation_fkey
        FOREIGN KEY (participation_id, exam_attempt_id, generation)
        REFERENCES exam_attempt_participations(id, exam_attempt_id, generation),
    CONSTRAINT exam_attempt_suspensions_flag_fkey
        FOREIGN KEY (integrity_flag_id, exam_attempt_id, generation)
        REFERENCES integrity_flags(id, exam_attempt_id, generation),
    CONSTRAINT exam_attempt_suspensions_lifecycle_check CHECK (
        (state = 'active' AND ended_at IS NULL AND reallowed_by_user_id IS NULL AND private_reason IS NULL) OR
        (state = 'closed' AND ended_at >= started_at AND reallowed_by_user_id IS NOT NULL AND
         private_reason = btrim(private_reason) AND char_length(private_reason) BETWEEN 1 AND 1000 AND
         octet_length(private_reason) <= 4000)
    )
);

CREATE UNIQUE INDEX exam_attempt_suspensions_one_active_key
    ON exam_attempt_suspensions (exam_attempt_id) WHERE state = 'active';

ALTER TABLE exam_attempt_focus_loss_evaluations
    ADD CONSTRAINT exam_attempt_focus_loss_evaluations_suspension_fkey
    FOREIGN KEY (last_suspension_id, exam_attempt_id, participation_id, generation)
    REFERENCES exam_attempt_suspensions(id, exam_attempt_id, participation_id, generation);

CREATE FUNCTION reject_integrity_record_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Integrity records are immutable' USING ERRCODE = '55000';
END;
$$;
CREATE TRIGGER integrity_flags_immutable BEFORE UPDATE ON integrity_flags
    FOR EACH ROW EXECUTE FUNCTION reject_integrity_record_update();
CREATE TRIGGER integrity_evidence_immutable BEFORE UPDATE ON integrity_evidence
    FOR EACH ROW EXECUTE FUNCTION reject_integrity_record_update();

CREATE FUNCTION guard_exam_attempt_suspension_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.exam_attempt_id IS DISTINCT FROM OLD.exam_attempt_id OR
       NEW.participation_id IS DISTINCT FROM OLD.participation_id OR
       NEW.integrity_flag_id IS DISTINCT FROM OLD.integrity_flag_id OR
       NEW.generation IS DISTINCT FROM OLD.generation OR
       NEW.suspension_attempt_revision IS DISTINCT FROM OLD.suspension_attempt_revision OR NEW.source IS DISTINCT FROM OLD.source OR
       NEW.candidate_reason IS DISTINCT FROM OLD.candidate_reason OR NEW.started_at IS DISTINCT FROM OLD.started_at OR
       OLD.state = 'closed' THEN
        RAISE EXCEPTION 'Attempt Suspension identity or terminal state changed' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER exam_attempt_suspensions_guard BEFORE UPDATE ON exam_attempt_suspensions
    FOR EACH ROW EXECUTE FUNCTION guard_exam_attempt_suspension_mutation();

-- A Submission seals one exact acknowledged Workspace state without copying
-- bytes. The header is created unsealed only inside the sealing transaction;
-- its children are inserted from locked authoritative Workspace rows and the
-- header is sealed before commit. Exact owner keys prevent cross-Attempt,
-- cross-Participation, and cross-Workspace references.
ALTER TABLE exam_attempt_workspaces
    ADD CONSTRAINT exam_attempt_workspaces_submission_owner_key
    UNIQUE (exam_attempt_id, id);
ALTER TABLE exam_attempt_workspace_entries
    ADD CONSTRAINT exam_attempt_workspace_entries_submission_owner_key
    UNIQUE (workspace_id, id, current_object_id);

CREATE TABLE exam_submissions (
    id varchar(26) PRIMARY KEY,
    exam_attempt_id varchar(26) NOT NULL UNIQUE,
    workspace_id varchar(26) NOT NULL UNIQUE,
    participation_id varchar(26) NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    connection_id varchar(26) NOT NULL,
    manifest_schema_version integer NOT NULL CHECK (manifest_schema_version = 1),
    workspace_cursor bigint NOT NULL CHECK (workspace_cursor >= 0),
    manifest_digest char(64) NOT NULL CHECK (manifest_digest ~ '^[0-9a-f]{64}$'),
    manifest_entry_count integer NOT NULL CHECK (manifest_entry_count BETWEEN 0 AND 500),
    manifest_total_file_bytes bigint NOT NULL CHECK (manifest_total_file_bytes BETWEEN 0 AND 52428800),
    final_focus_loss_sequence bigint NOT NULL CHECK (final_focus_loss_sequence >= 0),
    integrity_state varchar(16) NOT NULL CHECK (integrity_state IN ('settled', 'gapped')),
    unresolved_integrity_count bigint NOT NULL CHECK (unresolved_integrity_count >= 0),
    submitted_at timestamptz NOT NULL,
    sealed boolean NOT NULL DEFAULT false,
    UNIQUE (id, workspace_id),
    CONSTRAINT exam_submissions_workspace_fkey
        FOREIGN KEY (exam_attempt_id, workspace_id)
        REFERENCES exam_attempt_workspaces(exam_attempt_id, id),
    CONSTRAINT exam_submissions_participation_fkey
        FOREIGN KEY (participation_id, exam_attempt_id, generation)
        REFERENCES exam_attempt_participations(id, exam_attempt_id, generation),
    CONSTRAINT exam_submissions_connection_fkey
        FOREIGN KEY (connection_id, exam_attempt_id, participation_id)
        REFERENCES exam_attempt_connections(id, exam_attempt_id, participation_id),
    CONSTRAINT exam_submissions_integrity_check CHECK (
        (integrity_state = 'settled' AND unresolved_integrity_count = 0) OR
        (integrity_state = 'gapped' AND unresolved_integrity_count > 0)
    )
);

CREATE TABLE exam_submission_manifest_entries (
    submission_id varchar(26) NOT NULL,
    workspace_id varchar(26) NOT NULL,
    entry_id varchar(26) NOT NULL,
    kind varchar(16) NOT NULL CHECK (kind IN ('file', 'directory')),
    path text NOT NULL CHECK (octet_length(path) BETWEEN 1 AND 1024),
    content_version varchar(26),
    media_type varchar(255),
    size_bytes bigint,
    sha256 char(64),
    storage_origin varchar(16),
    starter_object_id varchar(26),
    attempt_object_id varchar(26),
    workspace_object_id varchar(26),
    PRIMARY KEY (submission_id, entry_id),
    UNIQUE (submission_id, path),
    CONSTRAINT exam_submission_manifest_entries_submission_fkey
        FOREIGN KEY (submission_id, workspace_id)
        REFERENCES exam_submissions(id, workspace_id),
    CONSTRAINT exam_submission_manifest_entries_workspace_entry_fkey
        FOREIGN KEY (workspace_id, entry_id, workspace_object_id)
        REFERENCES exam_attempt_workspace_entries(workspace_id, id, current_object_id),
    CONSTRAINT exam_submission_manifest_entries_workspace_object_fkey
        FOREIGN KEY (workspace_id, workspace_object_id)
        REFERENCES exam_attempt_workspace_objects(workspace_id, id),
    CONSTRAINT exam_submission_manifest_entries_starter_object_fkey
        FOREIGN KEY (starter_object_id) REFERENCES exam_starter_workspace_objects(id),
    CONSTRAINT exam_submission_manifest_entries_attempt_object_fkey
        FOREIGN KEY (workspace_id, attempt_object_id)
        REFERENCES exam_attempt_workspace_objects(workspace_id, id),
    CONSTRAINT exam_submission_manifest_entries_content_check CHECK (
        (kind = 'directory' AND content_version IS NULL AND media_type IS NULL AND size_bytes IS NULL AND
         sha256 IS NULL AND storage_origin IS NULL AND starter_object_id IS NULL AND attempt_object_id IS NULL AND
         workspace_object_id IS NULL) OR
        (kind = 'file' AND content_version ~ '^[A-Za-z0-9_-]{26}$' AND
         media_type IS NOT NULL AND media_type = btrim(media_type) AND char_length(media_type) BETWEEN 1 AND 255 AND
         size_bytes BETWEEN 0 AND 10485760 AND sha256 ~ '^[0-9a-f]{64}$' AND workspace_object_id IS NOT NULL AND
         ((storage_origin = 'starter' AND starter_object_id IS NOT NULL AND attempt_object_id IS NULL) OR
          (storage_origin = 'attempt' AND starter_object_id IS NULL AND attempt_object_id = workspace_object_id)))
    )
);

CREATE FUNCTION guard_exam_submission_manifest_object() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    current_kind varchar(16);
    current_path text;
    current_object_id varchar(26);
    current_content_version varchar(26);
    current_media_type varchar(255);
    current_size_bytes bigint;
    current_sha256 char(64);
    current_origin varchar(16);
    current_starter_id varchar(26);
    current_attempt_id varchar(26);
BEGIN
    SELECT e.kind, e.path, e.current_object_id, o.content_version, o.media_type,
           o.size_bytes, o.sha256, o.storage_origin, o.starter_object_id,
           CASE WHEN o.storage_origin = 'attempt' THEN o.id END
      INTO current_kind, current_path, current_object_id, current_content_version,
           current_media_type, current_size_bytes, current_sha256, current_origin,
           current_starter_id, current_attempt_id
      FROM exam_attempt_workspace_entries e
      LEFT JOIN exam_attempt_workspace_objects o
        ON o.workspace_id = e.workspace_id AND o.id = e.current_object_id
     WHERE e.workspace_id = NEW.workspace_id AND e.id = NEW.entry_id;
    IF NOT FOUND OR
       current_kind IS DISTINCT FROM NEW.kind OR
       current_path IS DISTINCT FROM NEW.path OR
       current_object_id IS DISTINCT FROM NEW.workspace_object_id OR
       current_content_version IS DISTINCT FROM NEW.content_version OR
       current_media_type IS DISTINCT FROM NEW.media_type OR
       current_size_bytes IS DISTINCT FROM NEW.size_bytes OR
       current_sha256 IS DISTINCT FROM NEW.sha256 OR
       current_origin IS DISTINCT FROM NEW.storage_origin OR
       current_starter_id IS DISTINCT FROM NEW.starter_object_id OR
       current_attempt_id IS DISTINCT FROM NEW.attempt_object_id THEN
        RAISE EXCEPTION 'Submission manifest row does not match authoritative Workspace state'
            USING ERRCODE = '23503', CONSTRAINT = 'exam_submission_manifest_entries_object_provenance';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION guard_exam_submission_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NOT OLD.sealed AND NEW.sealed AND
       (to_jsonb(NEW) - 'sealed') = (to_jsonb(OLD) - 'sealed') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'sealed Submission is immutable' USING ERRCODE = '55000';
END;
$$;

CREATE FUNCTION reject_exam_submission_manifest_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'sealed Submission manifest is immutable' USING ERRCODE = '55000';
END;
$$;

CREATE FUNCTION guard_exam_submission_manifest_insert() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    submission_sealed boolean;
BEGIN
    SELECT sealed INTO submission_sealed FROM exam_submissions WHERE id = NEW.submission_id FOR KEY SHARE;
    IF submission_sealed THEN
        RAISE EXCEPTION 'sealed Submission manifest is immutable' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION enforce_exam_submission_sealed() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    current_submission exam_submissions%ROWTYPE;
    actual_entries integer;
    actual_bytes bigint;
BEGIN
    SELECT * INTO current_submission FROM exam_submissions WHERE id = NEW.id;
    SELECT count(*), COALESCE(sum(size_bytes), 0) INTO actual_entries, actual_bytes
      FROM exam_submission_manifest_entries WHERE submission_id = NEW.id;
    IF NOT current_submission.sealed OR current_submission.manifest_entry_count <> actual_entries OR
       current_submission.manifest_total_file_bytes <> actual_bytes THEN
        RAISE EXCEPTION 'Submission manifest was not sealed completely' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER exam_submissions_immutable
    BEFORE UPDATE OR DELETE ON exam_submissions
    FOR EACH ROW EXECUTE FUNCTION guard_exam_submission_mutation();
CREATE TRIGGER exam_submission_manifest_entries_immutable
    BEFORE UPDATE OR DELETE ON exam_submission_manifest_entries
    FOR EACH ROW EXECUTE FUNCTION reject_exam_submission_manifest_mutation();
CREATE TRIGGER exam_submission_manifest_entries_insert_guard
    BEFORE INSERT ON exam_submission_manifest_entries
    FOR EACH ROW EXECUTE FUNCTION guard_exam_submission_manifest_insert();
CREATE TRIGGER exam_submission_manifest_entries_object_provenance
    BEFORE INSERT ON exam_submission_manifest_entries
    FOR EACH ROW EXECUTE FUNCTION guard_exam_submission_manifest_object();
CREATE CONSTRAINT TRIGGER exam_submissions_sealed_check
    AFTER INSERT OR UPDATE ON exam_submissions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION enforce_exam_submission_sealed();

-- One post-seal Integrity Review is mutable only while draft. Finalization
-- snapshots the exact Flag/evidence inventory and release is a later one-way
-- transition. Private rationale and manager notes never enter audit/events.
ALTER TABLE exam_submissions
    ADD CONSTRAINT exam_submissions_review_owner_key UNIQUE (id, exam_attempt_id);
ALTER TABLE integrity_flags
    ADD CONSTRAINT integrity_flags_review_owner_key UNIQUE (id, exam_attempt_id);
ALTER TABLE integrity_evidence
    ADD CONSTRAINT integrity_evidence_review_owner_key UNIQUE (id, integrity_flag_id, exam_attempt_id);

CREATE TABLE integrity_discrepancies (
    id varchar(26) PRIMARY KEY,
    submission_id varchar(26) NOT NULL,
    exam_attempt_id varchar(26) NOT NULL,
    participation_id varchar(26) NOT NULL,
    connection_id varchar(26) NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    kind varchar(32) NOT NULL CHECK (kind = 'late_focus_loss'),
    schema_version integer NOT NULL CHECK (schema_version = 1),
    focus_loss_signal_id varchar(26) NOT NULL UNIQUE,
    sequence bigint NOT NULL CHECK (sequence > 0),
    duration_milliseconds bigint NOT NULL CHECK (duration_milliseconds BETWEEN 1 AND 86400000),
    source varchar(32) CHECK (source IN ('window_blur', 'document_hidden', 'application_backgrounded', 'fullscreen_exited')),
    missing_before bigint NOT NULL CHECK (missing_before >= 0),
    received_at timestamptz NOT NULL,
    UNIQUE (id, submission_id, exam_attempt_id),
    UNIQUE (submission_id, participation_id, generation, sequence),
    CONSTRAINT integrity_discrepancies_submission_fkey
        FOREIGN KEY (submission_id, exam_attempt_id)
        REFERENCES exam_submissions(id, exam_attempt_id),
    CONSTRAINT integrity_discrepancies_participation_fkey
        FOREIGN KEY (participation_id, exam_attempt_id, generation)
        REFERENCES exam_attempt_participations(id, exam_attempt_id, generation),
    CONSTRAINT integrity_discrepancies_connection_fkey
        FOREIGN KEY (connection_id, exam_attempt_id, participation_id)
        REFERENCES exam_attempt_connections(id, exam_attempt_id, participation_id)
);
CREATE INDEX integrity_discrepancies_submission_page_idx
    ON integrity_discrepancies (submission_id, id);
CREATE TRIGGER integrity_discrepancies_immutable BEFORE UPDATE OR DELETE ON integrity_discrepancies
    FOR EACH ROW EXECUTE FUNCTION reject_integrity_record_update();

CREATE TABLE submission_reviews (
    id varchar(26) PRIMARY KEY,
    submission_id varchar(26) NOT NULL UNIQUE,
    exam_attempt_id varchar(26) NOT NULL,
    state varchar(16) NOT NULL CHECK (state IN ('draft', 'finalized')),
    release_state varchar(16) NOT NULL CHECK (release_state IN ('withheld', 'released')),
    revision bigint NOT NULL CHECK (revision > 0),
    created_by_user_id varchar(26) NOT NULL REFERENCES users(id),
    manager_notes text NOT NULL DEFAULT '',
    student_remarks_markdown text NOT NULL DEFAULT '',
    flag_count integer NOT NULL DEFAULT 0 CHECK (flag_count BETWEEN 0 AND 200),
    evidence_count integer NOT NULL DEFAULT 0 CHECK (evidence_count BETWEEN 0 AND 20000),
    discrepancy_count integer NOT NULL DEFAULT 0 CHECK (discrepancy_count BETWEEN 0 AND 200),
    evidence_inventory_digest char(64),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    finalized_at timestamptz,
    finalized_by_user_id varchar(26) REFERENCES users(id),
    released_at timestamptz,
    released_by_user_id varchar(26) REFERENCES users(id),
    UNIQUE (id, exam_attempt_id),
    UNIQUE (id, submission_id),
    UNIQUE (id, submission_id, exam_attempt_id),
    CONSTRAINT submission_reviews_submission_fkey
        FOREIGN KEY (submission_id, exam_attempt_id)
        REFERENCES exam_submissions(id, exam_attempt_id),
    CONSTRAINT submission_reviews_text_check CHECK (
        manager_notes = btrim(manager_notes) AND char_length(manager_notes) <= 3000 AND octet_length(manager_notes) <= 12000 AND
        student_remarks_markdown = btrim(student_remarks_markdown) AND char_length(student_remarks_markdown) <= 8192 AND
        octet_length(student_remarks_markdown) <= 32768
    ),
    CONSTRAINT submission_reviews_lifecycle_check CHECK (
        (state = 'draft' AND release_state = 'withheld' AND flag_count = 0 AND evidence_count = 0 AND discrepancy_count = 0 AND
         evidence_inventory_digest IS NULL AND finalized_at IS NULL AND finalized_by_user_id IS NULL AND
         released_at IS NULL AND released_by_user_id IS NULL) OR
        (state = 'finalized' AND evidence_inventory_digest ~ '^[0-9a-f]{64}$' AND finalized_at >= created_at AND
         finalized_by_user_id IS NOT NULL AND
         ((release_state = 'withheld' AND released_at IS NULL AND released_by_user_id IS NULL) OR
          (release_state = 'released' AND released_at >= finalized_at AND released_by_user_id IS NOT NULL)))
    )
);

-- A fresh Result release reserves one PostgreSQL timestamp before rendering
-- its candidate notice. The terminal aggregate consumes this bounded row so
-- callers cannot substitute a node-supplied release time.
CREATE TABLE submission_review_release_preparations (
    submission_review_id varchar(26) PRIMARY KEY REFERENCES submission_reviews(id) ON DELETE CASCADE,
    submission_id varchar(26) NOT NULL REFERENCES exam_submissions(id) ON DELETE CASCADE,
    expected_review_revision bigint NOT NULL CHECK (expected_review_revision > 0),
    release_at timestamptz NOT NULL,
    CONSTRAINT submission_review_release_preparations_review_submission_fkey
        FOREIGN KEY (submission_review_id, submission_id)
        REFERENCES submission_reviews(id, submission_id),
    CONSTRAINT submission_review_release_preparations_submission_review_id_canonical_check
        CHECK (submission_review_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    CONSTRAINT submission_review_release_preparations_submission_id_canonical_check
        CHECK (submission_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$')
);

CREATE TABLE integrity_review_decisions (
    id varchar(26) PRIMARY KEY,
    submission_review_id varchar(26) NOT NULL,
    exam_attempt_id varchar(26) NOT NULL,
    integrity_flag_id varchar(26) NOT NULL,
    outcome varchar(16) NOT NULL CHECK (outcome IN ('confirmed', 'dismissed', 'inconclusive')),
    revision bigint NOT NULL CHECK (revision > 0),
    actor_user_id varchar(26) NOT NULL REFERENCES users(id),
    private_rationale text NOT NULL,
    decided_at timestamptz NOT NULL,
    UNIQUE (submission_review_id, integrity_flag_id),
    UNIQUE (id, submission_review_id, integrity_flag_id),
    CONSTRAINT integrity_review_decisions_review_fkey
        FOREIGN KEY (submission_review_id, exam_attempt_id)
        REFERENCES submission_reviews(id, exam_attempt_id),
    CONSTRAINT integrity_review_decisions_flag_fkey
        FOREIGN KEY (integrity_flag_id, exam_attempt_id)
        REFERENCES integrity_flags(id, exam_attempt_id),
    CONSTRAINT integrity_review_decisions_rationale_check CHECK (
        private_rationale = btrim(private_rationale) AND char_length(private_rationale) BETWEEN 1 AND 1000 AND
        octet_length(private_rationale) <= 4000
    )
);

CREATE TABLE submission_review_inventory_flags (
    submission_review_id varchar(26) NOT NULL,
    exam_attempt_id varchar(26) NOT NULL,
    integrity_flag_id varchar(26) NOT NULL,
    decision_id varchar(26) NOT NULL,
    decision_revision bigint NOT NULL CHECK (decision_revision > 0),
    PRIMARY KEY (submission_review_id, integrity_flag_id),
    CONSTRAINT submission_review_inventory_flags_review_fkey
        FOREIGN KEY (submission_review_id, exam_attempt_id)
        REFERENCES submission_reviews(id, exam_attempt_id),
    CONSTRAINT submission_review_inventory_flags_flag_fkey
        FOREIGN KEY (integrity_flag_id, exam_attempt_id)
        REFERENCES integrity_flags(id, exam_attempt_id),
    CONSTRAINT submission_review_inventory_flags_decision_fkey
        FOREIGN KEY (decision_id, submission_review_id, integrity_flag_id)
        REFERENCES integrity_review_decisions(id, submission_review_id, integrity_flag_id)
);

CREATE TABLE submission_review_inventory_evidence (
    submission_review_id varchar(26) NOT NULL,
    exam_attempt_id varchar(26) NOT NULL,
    integrity_flag_id varchar(26) NOT NULL,
    integrity_evidence_id varchar(26) NOT NULL,
    PRIMARY KEY (submission_review_id, integrity_evidence_id),
    CONSTRAINT submission_review_inventory_evidence_review_fkey
        FOREIGN KEY (submission_review_id, exam_attempt_id)
        REFERENCES submission_reviews(id, exam_attempt_id),
    CONSTRAINT submission_review_inventory_evidence_evidence_fkey
        FOREIGN KEY (integrity_evidence_id, integrity_flag_id, exam_attempt_id)
        REFERENCES integrity_evidence(id, integrity_flag_id, exam_attempt_id)
);

CREATE TABLE submission_review_inventory_discrepancies (
    submission_review_id varchar(26) NOT NULL,
    submission_id varchar(26) NOT NULL,
    exam_attempt_id varchar(26) NOT NULL,
    integrity_discrepancy_id varchar(26) NOT NULL,
    PRIMARY KEY (submission_review_id, integrity_discrepancy_id),
    CONSTRAINT submission_review_inventory_discrepancies_review_fkey
        FOREIGN KEY (submission_review_id, submission_id, exam_attempt_id)
        REFERENCES submission_reviews(id, submission_id, exam_attempt_id),
    CONSTRAINT submission_review_inventory_discrepancies_discrepancy_fkey
        FOREIGN KEY (integrity_discrepancy_id, submission_id, exam_attempt_id)
        REFERENCES integrity_discrepancies(id, submission_id, exam_attempt_id)
);

CREATE FUNCTION guard_submission_review_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.submission_id IS DISTINCT FROM OLD.submission_id OR
       NEW.exam_attempt_id IS DISTINCT FROM OLD.exam_attempt_id OR NEW.created_by_user_id IS DISTINCT FROM OLD.created_by_user_id OR
       NEW.created_at IS DISTINCT FROM OLD.created_at OR NEW.revision <> OLD.revision + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'Submission Review immutable identity or revision changed' USING ERRCODE = '55000';
    END IF;
    IF OLD.state = 'draft' AND NEW.state = 'draft' AND NEW.release_state = 'withheld' AND
       NEW.flag_count = 0 AND NEW.evidence_count = 0 AND NEW.discrepancy_count = 0 AND NEW.evidence_inventory_digest IS NULL AND
       NEW.finalized_at IS NULL AND NEW.finalized_by_user_id IS NULL AND NEW.released_at IS NULL AND NEW.released_by_user_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF OLD.state = 'draft' AND NEW.state = 'finalized' AND NEW.release_state = 'withheld' AND
       NEW.manager_notes = OLD.manager_notes AND NEW.student_remarks_markdown = OLD.student_remarks_markdown THEN
        RETURN NEW;
    END IF;
    IF OLD.state = 'finalized' AND OLD.release_state = 'withheld' AND NEW.state = 'finalized' AND
       NEW.release_state = 'released' AND NEW.manager_notes = OLD.manager_notes AND
       NEW.student_remarks_markdown = OLD.student_remarks_markdown AND NEW.flag_count = OLD.flag_count AND
       NEW.evidence_count = OLD.evidence_count AND NEW.discrepancy_count = OLD.discrepancy_count AND
       NEW.evidence_inventory_digest = OLD.evidence_inventory_digest AND NEW.finalized_at = OLD.finalized_at AND
       NEW.finalized_by_user_id = OLD.finalized_by_user_id THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'Submission Review terminal or frozen state changed' USING ERRCODE = '55000';
END;
$$;

CREATE FUNCTION guard_integrity_review_decision_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    review_state varchar(16);
BEGIN
    SELECT state INTO review_state FROM submission_reviews WHERE id = COALESCE(NEW.submission_review_id, OLD.submission_review_id);
    IF TG_OP = 'DELETE' OR review_state IS DISTINCT FROM 'draft' THEN
        RAISE EXCEPTION 'Integrity Review decision is frozen' USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'UPDATE' AND (NEW.id IS DISTINCT FROM OLD.id OR
       NEW.submission_review_id IS DISTINCT FROM OLD.submission_review_id OR
       NEW.exam_attempt_id IS DISTINCT FROM OLD.exam_attempt_id OR NEW.integrity_flag_id IS DISTINCT FROM OLD.integrity_flag_id OR
       NEW.revision <> OLD.revision + 1 OR NEW.decided_at < OLD.decided_at) THEN
        RAISE EXCEPTION 'Integrity Review decision identity or revision changed' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION reject_submission_review_inventory_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Submission Review inventory is immutable' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER submission_reviews_guard BEFORE UPDATE OR DELETE ON submission_reviews
    FOR EACH ROW EXECUTE FUNCTION guard_submission_review_mutation();
CREATE TRIGGER integrity_review_decisions_guard BEFORE INSERT OR UPDATE OR DELETE ON integrity_review_decisions
    FOR EACH ROW EXECUTE FUNCTION guard_integrity_review_decision_mutation();
CREATE TRIGGER submission_review_inventory_flags_immutable BEFORE UPDATE OR DELETE ON submission_review_inventory_flags
    FOR EACH ROW EXECUTE FUNCTION reject_submission_review_inventory_mutation();
CREATE TRIGGER submission_review_inventory_evidence_immutable BEFORE UPDATE OR DELETE ON submission_review_inventory_evidence
    FOR EACH ROW EXECUTE FUNCTION reject_submission_review_inventory_mutation();
CREATE TRIGGER submission_review_inventory_discrepancies_immutable BEFORE UPDATE OR DELETE ON submission_review_inventory_discrepancies
    FOR EACH ROW EXECUTE FUNCTION reject_submission_review_inventory_mutation();

CREATE INDEX integrity_flags_attempt_id_idx ON integrity_flags (exam_attempt_id, id);
CREATE INDEX integrity_evidence_flag_id_idx ON integrity_evidence (integrity_flag_id, id);

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

-- A preparation is the bounded durable attempt for one PAT transition. It
-- holds only the safe audit draft until rendering completes. Fresh success or
-- explicit/maintenance failure replaces it with one terminal AuditEvent;
-- authoritative replay removes it without producing audit or mail noise.
CREATE TABLE personal_access_token_mutation_preparations (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    token_id varchar(26) REFERENCES personal_access_tokens(id),
    kind varchar(16) NOT NULL CHECK (kind IN ('create', 'enable', 'disable', 'revoke')),
    actor_id varchar(26) NOT NULL REFERENCES users(id),
    session_id varchar(26) NOT NULL REFERENCES sessions(id),
    action varchar(128) NOT NULL,
    resource_type varchar(32) NOT NULL,
    resource_id varchar(128) NOT NULL,
    scope_type varchar(32) NOT NULL,
    scope_id varchar(128) NOT NULL,
    request_id varchar(128) NOT NULL DEFAULT '',
    node_id varchar(128) NOT NULL,
    client_type varchar(32) NOT NULL DEFAULT '',
    authentication_method varchar(64) NOT NULL DEFAULT '',
    ip_address varchar(64) NOT NULL DEFAULT '',
    user_agent varchar(512) NOT NULL DEFAULT '',
    parameters jsonb,
    prior_state jsonb,
    CONSTRAINT personal_access_token_mutation_preparations_lifecycle_check CHECK (expires_at > created_at),
    CONSTRAINT personal_access_token_mutation_preparations_target_check CHECK (
        (kind = 'create' AND token_id IS NULL) OR
        (kind <> 'create' AND token_id IS NOT NULL)
    ),
    CONSTRAINT personal_access_token_mutation_preparations_actor_check CHECK (actor_id = user_id),
    CONSTRAINT personal_access_token_mutation_preparations_action_check CHECK (
        (kind = 'create' AND action = 'personal_access_token.create') OR
        (kind = 'enable' AND action = 'personal_access_token.enable') OR
        (kind = 'disable' AND action = 'personal_access_token.disable') OR
        (kind = 'revoke' AND action = 'personal_access_token.revoke')
    ),
    CONSTRAINT personal_access_token_mutation_preparations_payload_check CHECK (
        COALESCE(octet_length(parameters::text), 0) <= 16384 AND
        COALESCE(octet_length(prior_state::text), 0) <= 16384
    )
);

CREATE INDEX personal_access_token_mutation_preparations_expiry_idx
    ON personal_access_token_mutation_preparations (expires_at, id);

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
    encryption_key_id char(32) NOT NULL,
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
	purpose varchar(32) NOT NULL CHECK (purpose IN ('login', 'connect', 'invitation_admission')),
	target_user_id varchar(26) REFERENCES users(id),
	invitation_id varchar(26) REFERENCES invitations(id),
	audit_event_id varchar(26),
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
		),
	CONSTRAINT external_login_states_purpose_target_check CHECK (
		(purpose = 'login' AND target_user_id IS NULL AND invitation_id IS NULL AND audit_event_id IS NULL) OR
		(purpose = 'connect' AND target_user_id IS NOT NULL AND invitation_id IS NULL AND audit_event_id IS NOT NULL) OR
		(purpose = 'invitation_admission' AND target_user_id IS NULL AND invitation_id IS NOT NULL AND audit_event_id IS NULL)
	)
);

CREATE UNIQUE INDEX external_login_states_state_hash_key
    ON external_login_states (state_hash);

CREATE INDEX external_login_states_expires_at_idx
    ON external_login_states (expires_at);

CREATE TABLE browser_authentication_transactions (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    purpose varchar(32) NOT NULL CHECK (purpose = 'desktop_authorization'),
    state varchar(16) NOT NULL CHECK (state IN ('pending', 'code_issued', 'exchanged', 'cancelled', 'expired')),
    institution_id varchar(26) NOT NULL REFERENCES institutions(id),
    issuer varchar(2048) NOT NULL,
    handle_hash char(64),
    browser_proof_hash char(64),
    state_hash char(64),
    callback_url text,
    code_challenge varchar(128),
    expected_authentication_method varchar(64) NOT NULL,
    expected_provider_id varchar(64),
    client_type varchar(32) NOT NULL CHECK (client_type = 'desktop'),
    device_id varchar(128) NOT NULL DEFAULT '',
    device_name varchar(512) NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    user_id varchar(26) REFERENCES users(id),
    authentication_method varchar(64),
    authentication_provider_id varchar(64),
	external_identity_id varchar(26) REFERENCES external_identities(id),
    authentication_strength varchar(32),
    authenticated_at timestamptz,
    mfa_completed_at timestamptz,
    code_hash char(64),
    code_expires_at timestamptz,
    cancelled_at timestamptz,
    exchanged_at timestamptz,
    expired_at timestamptz,
    CONSTRAINT browser_authentication_transactions_lifecycle_check CHECK (
        updated_at >= created_at AND expires_at > created_at AND expires_at <= created_at + interval '5 minutes'
    ),
    CONSTRAINT browser_authentication_transactions_expected_path_check CHECK (
        (expected_authentication_method = 'password' AND expected_provider_id IS NULL) OR
        (expected_authentication_method <> 'password' AND expected_provider_id IS NOT NULL)
    ),
    CONSTRAINT browser_authentication_transactions_state_shape_check CHECK (
        (state = 'pending' AND handle_hash IS NOT NULL AND browser_proof_hash IS NOT NULL AND
         state_hash IS NOT NULL AND callback_url IS NOT NULL AND code_challenge IS NOT NULL AND
         user_id IS NULL AND authentication_method IS NULL AND authentication_provider_id IS NULL AND external_identity_id IS NULL AND
         authentication_strength IS NULL AND authenticated_at IS NULL AND mfa_completed_at IS NULL AND code_hash IS NULL AND
         code_expires_at IS NULL AND cancelled_at IS NULL AND exchanged_at IS NULL AND expired_at IS NULL) OR
        (state = 'code_issued' AND handle_hash IS NULL AND browser_proof_hash IS NULL AND
         state_hash IS NOT NULL AND callback_url IS NOT NULL AND code_challenge IS NOT NULL AND
         user_id IS NOT NULL AND authentication_method = expected_authentication_method AND
         authentication_provider_id IS NOT DISTINCT FROM expected_provider_id AND
		 ((authentication_provider_id IS NULL AND external_identity_id IS NULL) OR
		  (authentication_provider_id IS NOT NULL AND external_identity_id IS NOT NULL)) AND
         authentication_strength IN ('single_factor', 'multi_factor') AND authenticated_at IS NOT NULL AND
         ((authentication_strength = 'single_factor' AND mfa_completed_at IS NULL) OR
          (authentication_strength = 'multi_factor' AND mfa_completed_at BETWEEN authenticated_at AND updated_at)) AND
         code_hash IS NOT NULL AND code_expires_at > updated_at AND
         code_expires_at <= LEAST(expires_at, updated_at + interval '1 minute') AND
         cancelled_at IS NULL AND exchanged_at IS NULL AND expired_at IS NULL) OR
        (state = 'cancelled' AND handle_hash IS NULL AND browser_proof_hash IS NULL AND
         state_hash IS NULL AND callback_url IS NULL AND code_challenge IS NULL AND
         user_id IS NULL AND authentication_method IS NULL AND authentication_provider_id IS NULL AND external_identity_id IS NULL AND
         authentication_strength IS NULL AND authenticated_at IS NULL AND mfa_completed_at IS NULL AND code_hash IS NULL AND
         code_expires_at IS NULL AND cancelled_at = updated_at AND exchanged_at IS NULL AND expired_at IS NULL) OR
        (state = 'exchanged' AND handle_hash IS NULL AND browser_proof_hash IS NULL AND
         state_hash IS NULL AND callback_url IS NULL AND code_challenge IS NULL AND
         user_id IS NOT NULL AND authentication_method = expected_authentication_method AND
         authentication_provider_id IS NOT DISTINCT FROM expected_provider_id AND
		 ((authentication_provider_id IS NULL AND external_identity_id IS NULL) OR
		  (authentication_provider_id IS NOT NULL AND external_identity_id IS NOT NULL)) AND
         authentication_strength IN ('single_factor', 'multi_factor') AND authenticated_at IS NOT NULL AND
         ((authentication_strength = 'single_factor' AND mfa_completed_at IS NULL) OR
          (authentication_strength = 'multi_factor' AND mfa_completed_at BETWEEN authenticated_at AND updated_at)) AND
         code_hash IS NULL AND code_expires_at IS NULL AND cancelled_at IS NULL AND exchanged_at = updated_at AND expired_at IS NULL) OR
        (state = 'expired' AND handle_hash IS NULL AND browser_proof_hash IS NULL AND
         state_hash IS NULL AND callback_url IS NULL AND code_challenge IS NULL AND code_hash IS NULL AND
         code_expires_at IS NULL AND cancelled_at IS NULL AND exchanged_at IS NULL AND
         expired_at = updated_at AND updated_at <= expires_at AND
		 ((user_id IS NULL AND authentication_method IS NULL AND authentication_provider_id IS NULL AND external_identity_id IS NULL AND
		   authentication_strength IS NULL AND authenticated_at IS NULL AND mfa_completed_at IS NULL AND updated_at = expires_at) OR
          (user_id IS NOT NULL AND authentication_method = expected_authentication_method AND
           authentication_provider_id IS NOT DISTINCT FROM expected_provider_id AND
		   ((authentication_provider_id IS NULL AND external_identity_id IS NULL) OR
		    (authentication_provider_id IS NOT NULL AND external_identity_id IS NOT NULL)) AND
           authentication_strength IN ('single_factor', 'multi_factor') AND authenticated_at IS NOT NULL AND
           ((authentication_strength = 'single_factor' AND mfa_completed_at IS NULL) OR
            (authentication_strength = 'multi_factor' AND mfa_completed_at BETWEEN authenticated_at AND updated_at))))
        )
    )
);

CREATE UNIQUE INDEX browser_authentication_transactions_handle_hash_key
    ON browser_authentication_transactions (handle_hash) WHERE handle_hash IS NOT NULL;
CREATE UNIQUE INDEX browser_authentication_transactions_code_hash_key
    ON browser_authentication_transactions (code_hash) WHERE code_hash IS NOT NULL;
CREATE INDEX browser_authentication_transactions_expiry_idx
	ON browser_authentication_transactions (expires_at, id)
	WHERE state IN ('pending', 'code_issued');
CREATE INDEX browser_authentication_transactions_code_expiry_idx
	ON browser_authentication_transactions (code_expires_at, id)
	WHERE state = 'code_issued';
CREATE INDEX browser_authentication_transactions_terminal_retention_idx
    ON browser_authentication_transactions (updated_at, id)
    WHERE state IN ('cancelled', 'exchanged', 'expired');

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
        CHECK (resource_type IN ('institution', 'academic_unit', 'programme', 'programme_level', 'academic_period', 'class', 'user', 'exam', 'exam_sitting', 'submission', 'mail_delivery')),
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

ALTER TABLE external_login_states
    ADD CONSTRAINT external_login_states_audit_event_id_fkey
    FOREIGN KEY (audit_event_id) REFERENCES audit_events(id);

CREATE TABLE exam_sitting_private_actions (
    audit_event_id varchar(26) PRIMARY KEY REFERENCES audit_events(id),
    exam_sitting_id varchar(26) NOT NULL REFERENCES exam_sittings(id),
    actor_user_id varchar(26) NOT NULL REFERENCES users(id),
    action_code varchar(32) NOT NULL CHECK (action_code IN (
        'manager_canceled', 'manager_paused', 'manager_resumed', 'manager_extended', 'manager_closed'
    )),
    private_reason text NOT NULL,
    created_at timestamptz NOT NULL,
    sitting_revision bigint NOT NULL CHECK (sitting_revision > 1),
    UNIQUE (exam_sitting_id, sitting_revision),
    CONSTRAINT exam_sitting_private_actions_reason_check CHECK (
        private_reason = btrim(private_reason) AND
        char_length(private_reason) BETWEEN 1 AND 1000 AND
        octet_length(private_reason) <= 4000
    )
);

CREATE FUNCTION reject_exam_sitting_private_action_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'Exam Sitting private action provenance is append-only' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER exam_sitting_private_actions_append_only
    BEFORE UPDATE OR DELETE ON exam_sitting_private_actions
    FOR EACH ROW EXECUTE FUNCTION reject_exam_sitting_private_action_mutation();

CREATE TABLE exam_sitting_live_corrections (
    audit_event_id varchar(26) PRIMARY KEY REFERENCES audit_events(id),
    exam_id varchar(26) NOT NULL,
    exam_sitting_id varchar(26) NOT NULL,
    previous_revision_id varchar(26) NOT NULL,
    correction_revision_id varchar(26) NOT NULL,
    actor_user_id varchar(26) NOT NULL REFERENCES users(id),
    private_reason text NOT NULL,
    effective_at timestamptz NOT NULL,
    sitting_revision bigint NOT NULL CHECK (sitting_revision > 1),
    UNIQUE (exam_sitting_id, sitting_revision),
    CONSTRAINT exam_sitting_live_corrections_sitting_fkey
        FOREIGN KEY (exam_id, exam_sitting_id) REFERENCES exam_sittings(exam_id, id),
    CONSTRAINT exam_sitting_live_corrections_previous_fkey
        FOREIGN KEY (exam_id, previous_revision_id) REFERENCES exam_revisions(exam_id, id),
    CONSTRAINT exam_sitting_live_corrections_correction_fkey
        FOREIGN KEY (exam_id, correction_revision_id) REFERENCES exam_revisions(exam_id, id),
    CONSTRAINT exam_sitting_live_corrections_distinct_revision_check
        CHECK (previous_revision_id <> correction_revision_id),
    CONSTRAINT exam_sitting_live_corrections_reason_check CHECK (
        private_reason = btrim(private_reason) AND
        char_length(private_reason) BETWEEN 1 AND 1000 AND
        octet_length(private_reason) <= 4000
    )
);

CREATE TRIGGER exam_sitting_live_corrections_append_only
    BEFORE UPDATE OR DELETE ON exam_sitting_live_corrections
    FOR EACH ROW EXECUTE FUNCTION reject_exam_sitting_private_action_mutation();

CREATE TABLE command_outcomes (
    user_id varchar(26) NOT NULL REFERENCES users(id),
    operation varchar(128) NOT NULL,
    key_digest bytea NOT NULL CHECK (octet_length(key_digest) = 32),
    fingerprint_version integer NOT NULL CHECK (fingerprint_version > 0),
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint) = 32),
    outcome_version integer NOT NULL CHECK (outcome_version > 0),
    outcome jsonb NOT NULL CHECK (octet_length(outcome::text) <= 65536),
    batch_group_digest bytea CHECK (batch_group_digest IS NULL OR octet_length(batch_group_digest) = 32),
    duplicate_of_key_digest bytea CHECK (duplicate_of_key_digest IS NULL OR octet_length(duplicate_of_key_digest) = 32),
    original_audit_event_id varchar(26) REFERENCES audit_events(id),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT command_outcomes_audit_required_check CHECK (
        original_audit_event_id IS NOT NULL OR operation = 'user_settings.replace'
    ),
    PRIMARY KEY (user_id, operation, key_digest),
    CONSTRAINT command_outcomes_lifecycle_check CHECK (expires_at > created_at),
    CONSTRAINT command_outcomes_batch_duplicate_check CHECK (
        duplicate_of_key_digest IS NULL OR batch_group_digest IS NOT NULL
    ),
    CONSTRAINT command_outcomes_operation_check
        CHECK (operation ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$')
);

CREATE INDEX command_outcomes_expires_at_idx
    ON command_outcomes (expires_at, user_id, operation);

CREATE TABLE access_policies (
    singleton smallint PRIMARY KEY CHECK (singleton = 1),
    id varchar(26) NOT NULL UNIQUE,
    revision bigint NOT NULL CHECK (revision > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    local_login_enabled boolean NOT NULL,
    public_registration_enabled boolean NOT NULL,
    invitation_admission_enabled boolean NOT NULL,
    invitation_local_credential_enabled boolean NOT NULL,
    desktop_authorization_enabled boolean NOT NULL,
    provider_admissions jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT access_policies_lifecycle_check CHECK (updated_at >= created_at),
    CONSTRAINT access_policies_provider_admissions_check CHECK (
        jsonb_typeof(provider_admissions) = 'object' AND
        octet_length(provider_admissions::text) <= 16384
    )
);

CREATE TABLE access_policy_transitions (
    access_policy_id varchar(26) NOT NULL REFERENCES access_policies(id) ON DELETE CASCADE,
    from_revision bigint NOT NULL CHECK (from_revision > 0),
    to_revision bigint NOT NULL CHECK (to_revision = from_revision + 1),
    actor_user_id varchar(26) NOT NULL REFERENCES users(id),
    changed_fields text[] NOT NULL,
    changed_at timestamptz NOT NULL,
    outcome varchar(16) NOT NULL CHECK (outcome = 'applied'),
    PRIMARY KEY (access_policy_id, to_revision),
    UNIQUE (access_policy_id, from_revision),
    CONSTRAINT access_policy_transitions_changed_fields_check CHECK (
        cardinality(changed_fields) BETWEEN 1 AND 7 AND
        changed_fields <@ ARRAY[
            'local_login_enabled', 'public_registration_enabled',
            'invitation_admission_enabled', 'invitation_local_credential_enabled',
            'desktop_authorization_enabled', 'provider_admissions',
            'revoke_existing_sessions'
        ]::text[]
    ),
    CONSTRAINT access_policy_transitions_access_policy_id_canonical_check
        CHECK (access_policy_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    CONSTRAINT access_policy_transitions_actor_user_id_canonical_check
        CHECK (actor_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$')
);

CREATE FUNCTION reject_access_policy_transition_update() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'access policy transitions are append-only';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER access_policy_transitions_append_only
    BEFORE UPDATE ON access_policy_transitions
    FOR EACH ROW EXECUTE FUNCTION reject_access_policy_transition_update();

CREATE TABLE installation_states (
    singleton smallint PRIMARY KEY CHECK (singleton = 1),
    initialized_at timestamptz NOT NULL,
    institution_id varchar(26) NOT NULL REFERENCES institutions(id),
    administrator_user_id varchar(26) NOT NULL REFERENCES users(id),
    access_policy_id varchar(26) NOT NULL REFERENCES access_policies(id),
    bootstrap_secret_digest bytea NOT NULL CHECK (octet_length(bootstrap_secret_digest) = 32),
    bootstrap_command_fingerprint bytea NOT NULL CHECK (octet_length(bootstrap_command_fingerprint) = 32),
    bootstrap_result jsonb NOT NULL CHECK (octet_length(bootstrap_result::text) <= 65536)
);

-- Offline host recovery commits its security-sensitive fact with the
-- credential/policy mutation. A later normal startup reconciles the pending
-- row into audit before jobs or network transports begin serving.
CREATE TABLE administrator_recovery_records (
    id varchar(26) PRIMARY KEY,
    created_at timestamptz NOT NULL,
    institution_id varchar(26) NOT NULL REFERENCES institutions(id),
    user_id varchar(26) NOT NULL REFERENCES users(id),
    local_login_enabled boolean NOT NULL,
    password_rotated boolean NOT NULL,
    policy_from_revision bigint CHECK (policy_from_revision > 0),
    policy_to_revision bigint,
    reconciled_at timestamptz,
    audit_event_id varchar(26) UNIQUE REFERENCES audit_events(id),
    CONSTRAINT administrator_recovery_records_action_check
        CHECK (local_login_enabled OR password_rotated),
    CONSTRAINT administrator_recovery_records_lifecycle_check CHECK (
        (reconciled_at IS NULL AND audit_event_id IS NULL) OR
        (reconciled_at IS NOT NULL AND audit_event_id IS NOT NULL AND reconciled_at >= created_at)
    ),
    CONSTRAINT administrator_recovery_records_policy_revision_check CHECK (
        (local_login_enabled AND policy_from_revision IS NOT NULL AND policy_to_revision = policy_from_revision + 1) OR
        (NOT local_login_enabled AND policy_from_revision IS NULL AND policy_to_revision IS NULL)
    ),
    CONSTRAINT administrator_recovery_records_id_canonical_check
        CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    CONSTRAINT administrator_recovery_records_institution_id_canonical_check
        CHECK (institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    CONSTRAINT administrator_recovery_records_user_id_canonical_check
        CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    CONSTRAINT administrator_recovery_records_audit_event_id_canonical_check
        CHECK (audit_event_id IS NULL OR audit_event_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$')
);

CREATE UNIQUE INDEX administrator_recovery_records_one_pending_idx
    ON administrator_recovery_records ((1)) WHERE reconciled_at IS NULL;

-- ---------------------------------------------------------------------------
-- Cluster bootstrap discovery (disposable leases; not a message bus)
-- ---------------------------------------------------------------------------

-- Every backend, including the single-node local backend, holds this
-- PostgreSQL-clocked lease while it may serve. Offline administrator recovery
-- shares a transaction fence with lease renewal and fails while any row is
-- unexpired.
CREATE TABLE serving_node_leases (
    node_id varchar(128) PRIMARY KEY,
    lease_id varchar(26) NOT NULL UNIQUE,
    updated_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT serving_node_leases_node_id_check
        CHECK (char_length(btrim(node_id)) > 0),
    CONSTRAINT serving_node_leases_lease_id_canonical_check
        CHECK (lease_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    CONSTRAINT serving_node_leases_lifetime_check
        CHECK (expires_at > updated_at)
);

CREATE INDEX serving_node_leases_expires_at_node_id_idx
    ON serving_node_leases (expires_at, node_id);

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

-- ---------------------------------------------------------------------------
-- Canonical entity identifiers
-- ---------------------------------------------------------------------------

-- PostgreSQL varchar(26) limits length but does not enforce Proctor's exact
-- 26-character z-base-32 representation. Keep that durable invariant in the
-- pre-release baseline alongside the relationships it protects.

ALTER TABLE institutions
    ADD CONSTRAINT institutions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE invitations
    ADD CONSTRAINT invitations_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_class_id_canonical_check
    CHECK (class_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_academic_period_id_canonical_check
    CHECK (academic_period_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_academic_unit_id_canonical_check
    CHECK (academic_unit_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_role_id_canonical_check
    CHECK (role_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_inviter_user_id_canonical_check
    CHECK (inviter_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_scope_id_canonical_check
    CHECK (scope_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_accepted_user_id_canonical_check
    CHECK (accepted_user_id IS NULL OR accepted_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_accepted_affiliation_id_canonical_check
    CHECK (accepted_affiliation_id IS NULL OR accepted_affiliation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_accepted_class_member_id_canonical_check
    CHECK (accepted_class_member_id IS NULL OR accepted_class_member_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_accepted_academic_unit_member_id_canonical_check
    CHECK (accepted_academic_unit_member_id IS NULL OR accepted_academic_unit_member_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT invitations_accepted_role_binding_id_canonical_check
    CHECK (accepted_role_binding_id IS NULL OR accepted_role_binding_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE onboarding_imports
    ADD CONSTRAINT onboarding_imports_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_imports_scope_id_canonical_check
    CHECK (scope_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_imports_source_period_id_canonical_check
    CHECK (source_period_id IS NULL OR source_period_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_imports_source_class_id_canonical_check
    CHECK (source_class_id IS NULL OR source_class_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_imports_destination_period_id_canonical_check
    CHECK (destination_period_id IS NULL OR destination_period_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_imports_destination_class_id_canonical_check
    CHECK (destination_class_id IS NULL OR destination_class_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_imports_role_id_canonical_check
    CHECK (role_id IS NULL OR role_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_imports_actor_user_id_canonical_check
    CHECK (actor_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_imports_parse_job_id_canonical_check
    CHECK (parse_job_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_imports_execution_job_id_canonical_check
    CHECK (execution_job_id IS NULL OR execution_job_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE onboarding_import_rows
    ADD CONSTRAINT onboarding_import_rows_import_id_canonical_check
    CHECK (import_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_import_rows_scope_id_canonical_check
    CHECK (scope_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_import_rows_role_id_canonical_check
    CHECK (role_id IS NULL OR role_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_import_rows_user_id_canonical_check
    CHECK (user_id IS NULL OR user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_import_rows_relationship_ref_canonical_check
    CHECK (relationship_ref IS NULL OR relationship_ref ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_import_rows_destination_relationship_ref_canonical_check
    CHECK (destination_relationship_ref IS NULL OR destination_relationship_ref ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_import_rows_invitation_id_canonical_check
    CHECK (invitation_id IS NULL OR invitation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT onboarding_import_rows_result_ref_canonical_check
    CHECK (result_ref IS NULL OR result_ref ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE mail_occurrences
    ADD CONSTRAINT mail_occurrences_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT mail_occurrences_actor_user_id_canonical_check
    CHECK (actor_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE mail_deliveries
    ADD CONSTRAINT mail_deliveries_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT mail_deliveries_occurrence_id_canonical_check
    CHECK (occurrence_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT mail_deliveries_job_id_canonical_check
    CHECK (job_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT mail_deliveries_target_user_id_canonical_check
    CHECK (target_user_id IS NULL OR target_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT mail_deliveries_target_invitation_id_canonical_check
    CHECK (target_invitation_id IS NULL OR target_invitation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE mail_fanout_bundles
    ADD CONSTRAINT mail_fanout_bundles_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_sitting_mail_fanouts
    ADD CONSTRAINT exam_sitting_mail_fanouts_occurrence_id_canonical_check
    CHECK (occurrence_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_mail_fanouts_bundle_id_canonical_check
    CHECK (bundle_id IS NULL OR bundle_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_mail_fanouts_exam_sitting_id_canonical_check
    CHECK (exam_sitting_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_mail_fanouts_prior_class_id_canonical_check
    CHECK (prior_class_id IS NULL OR prior_class_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_sitting_mail_recipients
    ADD CONSTRAINT exam_sitting_mail_recipients_exam_sitting_id_canonical_check
    CHECK (exam_sitting_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_mail_recipients_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_mail_recipients_desired_occurrence_id_canonical_check
    CHECK (desired_occurrence_id IS NULL OR desired_occurrence_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_mail_recipients_desired_delivery_id_canonical_check
    CHECK (desired_delivery_id IS NULL OR desired_delivery_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_mail_recipients_communicated_class_id_canonical_check
    CHECK (communicated_class_id IS NULL OR communicated_class_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE mail_key_state
    ADD CONSTRAINT mail_key_state_active_rekey_job_id_canonical_check
    CHECK (active_rekey_job_id IS NULL OR active_rekey_job_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE command_outcomes
    ADD CONSTRAINT command_outcomes_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT command_outcomes_original_audit_event_id_canonical_check
    CHECK (original_audit_event_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE academic_units
    ADD CONSTRAINT academic_units_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_units_institution_id_canonical_check
    CHECK (institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_units_parent_id_canonical_check
    CHECK (parent_id IS NULL OR parent_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE programmes
    ADD CONSTRAINT programmes_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT programmes_academic_unit_id_canonical_check
    CHECK (academic_unit_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE programme_levels
    ADD CONSTRAINT programme_levels_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT programme_levels_programme_id_canonical_check
    CHECK (programme_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE academic_periods
    ADD CONSTRAINT academic_periods_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_periods_institution_id_canonical_check
	CHECK (institution_id IS NULL OR institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_periods_academic_unit_id_canonical_check
	CHECK (academic_unit_id IS NULL OR academic_unit_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exams
    ADD CONSTRAINT exams_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exams_academic_unit_id_canonical_check
    CHECK (academic_unit_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exams_creator_user_id_canonical_check
    CHECK (creator_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exams_owner_user_id_canonical_check
    CHECK (owner_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exams_default_revision_id_canonical_check
    CHECK (default_revision_id IS NULL OR default_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_drafts
    ADD CONSTRAINT exam_drafts_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_drafts_base_revision_id_canonical_check
    CHECK (base_revision_id IS NULL OR base_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_managers
    ADD CONSTRAINT exam_managers_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_managers_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_managers_granted_by_user_id_canonical_check
    CHECK (granted_by_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_resource_identities
    ADD CONSTRAINT exam_resource_identities_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_resource_identities_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_resource_identities_file_entry_id_canonical_check
    CHECK (file_entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_resources
    ADD CONSTRAINT exam_resources_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_resources_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_resources_file_entry_id_canonical_check
    CHECK (file_entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_resources_selected_file_revision_id_canonical_check
    CHECK (selected_file_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_starter_workspace_objects
    ADD CONSTRAINT exam_starter_workspace_objects_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_starter_workspace_objects_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_starter_workspace_objects_created_by_user_id_canonical_check
    CHECK (created_by_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_starter_workspace_entries
    ADD CONSTRAINT exam_starter_workspace_entries_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_starter_workspace_entries_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_starter_workspace_entries_current_object_id_canonical_check
    CHECK (current_object_id IS NULL OR current_object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_revisions
    ADD CONSTRAINT exam_revisions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revisions_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revisions_published_by_user_id_canonical_check
    CHECK (published_by_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revisions_base_revision_id_canonical_check
    CHECK (base_revision_id IS NULL OR base_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_revision_resources
    ADD CONSTRAINT exam_revision_resources_exam_revision_id_canonical_check CHECK (exam_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revision_resources_exam_id_canonical_check CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revision_resources_resource_id_canonical_check CHECK (resource_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revision_resources_file_entry_id_canonical_check CHECK (file_entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revision_resources_file_revision_id_canonical_check CHECK (file_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revision_resources_rendition_id_canonical_check CHECK (rendition_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_revision_starter_workspace_entries
    ADD CONSTRAINT exam_revision_starter_workspace_entries_exam_revision_id_canonical_check CHECK (exam_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revision_starter_workspace_entries_exam_id_canonical_check CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revision_starter_workspace_entries_entry_id_canonical_check CHECK (entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_revision_starter_workspace_entries_object_id_canonical_check CHECK (object_id IS NULL OR object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempts
    ADD CONSTRAINT exam_attempts_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempts_exam_id_canonical_check CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempts_exam_sitting_id_canonical_check CHECK (exam_sitting_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempts_candidate_user_id_canonical_check CHECK (candidate_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempts_admission_revision_id_canonical_check CHECK (admission_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE execution_grants
    ADD CONSTRAINT execution_grants_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT execution_grants_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempt_workspaces
    ADD CONSTRAINT exam_attempt_workspaces_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspaces_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspaces_admission_revision_id_canonical_check CHECK (admission_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempt_workspace_objects
    ADD CONSTRAINT exam_attempt_workspace_objects_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_objects_workspace_id_canonical_check CHECK (workspace_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_objects_admission_revision_id_canonical_check CHECK (admission_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_objects_source_starter_entry_id_canonical_check CHECK (source_starter_entry_id IS NULL OR source_starter_entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_objects_starter_object_id_canonical_check CHECK (starter_object_id IS NULL OR starter_object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_objects_content_version_canonical_check CHECK (content_version IS NULL OR content_version ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempt_workspace_entries
    ADD CONSTRAINT exam_attempt_workspace_entries_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_entries_workspace_id_canonical_check CHECK (workspace_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_entries_admission_revision_id_canonical_check CHECK (admission_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_entries_source_starter_entry_id_canonical_check CHECK (source_starter_entry_id IS NULL OR source_starter_entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_entries_current_object_id_canonical_check CHECK (current_object_id IS NULL OR current_object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempt_workspace_journal
    ADD CONSTRAINT exam_attempt_workspace_journal_workspace_id_canonical_check CHECK (workspace_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_journal_entry_id_canonical_check CHECK (entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_workspace_journal_content_version_canonical_check CHECK (content_version IS NULL OR content_version ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempt_participations
    ADD CONSTRAINT exam_attempt_participations_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_participations_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempt_connections
    ADD CONSTRAINT exam_attempt_connections_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_connections_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_connections_participation_id_canonical_check CHECK (participation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_connections_session_id_canonical_check CHECK (session_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE integrity_flags
    ADD CONSTRAINT integrity_flags_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_flags_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE integrity_evidence
    ADD CONSTRAINT integrity_evidence_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_evidence_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_evidence_participation_id_canonical_check CHECK (participation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_evidence_integrity_flag_id_canonical_check CHECK (integrity_flag_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_evidence_focus_loss_signal_id_canonical_check CHECK (focus_loss_signal_id IS NULL OR focus_loss_signal_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempt_focus_loss_evaluations
    ADD CONSTRAINT exam_attempt_focus_loss_evaluations_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_focus_loss_evaluations_participation_id_canonical_check CHECK (participation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_focus_loss_evaluations_last_signal_id_canonical_check CHECK (last_signal_id IS NULL OR last_signal_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_focus_loss_evaluations_last_connection_id_canonical_check CHECK (last_connection_id IS NULL OR last_connection_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_focus_loss_evaluations_integrity_flag_id_canonical_check CHECK (integrity_flag_id IS NULL OR integrity_flag_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_focus_loss_evaluations_last_suspension_id_canonical_check CHECK (last_suspension_id IS NULL OR last_suspension_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempt_focus_loss_pending
    ADD CONSTRAINT exam_attempt_focus_loss_pending_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_focus_loss_pending_participation_id_canonical_check CHECK (participation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_focus_loss_pending_signal_id_canonical_check CHECK (signal_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_focus_loss_pending_evidence_id_canonical_check CHECK (evidence_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_attempt_suspensions
    ADD CONSTRAINT exam_attempt_suspensions_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_suspensions_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_suspensions_participation_id_canonical_check CHECK (participation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_suspensions_integrity_flag_id_canonical_check CHECK (integrity_flag_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_attempt_suspensions_reallowed_by_user_id_canonical_check CHECK (reallowed_by_user_id IS NULL OR reallowed_by_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_submissions
    ADD CONSTRAINT exam_submissions_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submissions_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submissions_workspace_id_canonical_check CHECK (workspace_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submissions_participation_id_canonical_check CHECK (participation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submissions_connection_id_canonical_check CHECK (connection_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE integrity_discrepancies
    ADD CONSTRAINT integrity_discrepancies_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_discrepancies_submission_id_canonical_check CHECK (submission_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_discrepancies_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_discrepancies_participation_id_canonical_check CHECK (participation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_discrepancies_connection_id_canonical_check CHECK (connection_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_discrepancies_focus_loss_signal_id_canonical_check CHECK (focus_loss_signal_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_submission_manifest_entries
    ADD CONSTRAINT exam_submission_manifest_entries_submission_id_canonical_check CHECK (submission_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_workspace_id_canonical_check CHECK (workspace_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_entry_id_canonical_check CHECK (entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_content_version_canonical_check CHECK (content_version IS NULL OR content_version ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_starter_object_id_canonical_check CHECK (starter_object_id IS NULL OR starter_object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_attempt_object_id_canonical_check CHECK (attempt_object_id IS NULL OR attempt_object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_workspace_object_id_canonical_check CHECK (workspace_object_id IS NULL OR workspace_object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE submission_reviews
    ADD CONSTRAINT submission_reviews_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_reviews_submission_id_canonical_check CHECK (submission_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_reviews_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_reviews_created_by_user_id_canonical_check CHECK (created_by_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_reviews_finalized_by_user_id_canonical_check CHECK (finalized_by_user_id IS NULL OR finalized_by_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_reviews_released_by_user_id_canonical_check CHECK (released_by_user_id IS NULL OR released_by_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE integrity_review_decisions
    ADD CONSTRAINT integrity_review_decisions_id_canonical_check CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_review_decisions_submission_review_id_canonical_check CHECK (submission_review_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_review_decisions_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_review_decisions_integrity_flag_id_canonical_check CHECK (integrity_flag_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT integrity_review_decisions_actor_user_id_canonical_check CHECK (actor_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE submission_review_inventory_flags
    ADD CONSTRAINT submission_review_inventory_flags_submission_review_id_canonical_check CHECK (submission_review_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_review_inventory_flags_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_review_inventory_flags_integrity_flag_id_canonical_check CHECK (integrity_flag_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_review_inventory_flags_decision_id_canonical_check CHECK (decision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE submission_review_inventory_evidence
    ADD CONSTRAINT submission_review_inventory_evidence_submission_review_id_canonical_check CHECK (submission_review_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_review_inventory_evidence_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_review_inventory_evidence_integrity_flag_id_canonical_check CHECK (integrity_flag_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_review_inventory_evidence_integrity_evidence_id_canonical_check CHECK (integrity_evidence_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE submission_review_inventory_discrepancies
    ADD CONSTRAINT submission_review_inventory_discrepancies_submission_review_id_canonical_check CHECK (submission_review_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_review_inventory_discrepancies_submission_id_canonical_check CHECK (submission_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_review_inventory_discrepancies_exam_attempt_id_canonical_check CHECK (exam_attempt_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT submission_review_inventory_discrepancies_integrity_discrepancy_id_canonical_check CHECK (integrity_discrepancy_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_sittings
    ADD CONSTRAINT exam_sittings_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sittings_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sittings_exam_revision_id_canonical_check
    CHECK (exam_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sittings_class_id_canonical_check
    CHECK (class_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sittings_mail_reconciliation_actor_user_id_canonical_check
    CHECK (mail_reconciliation_actor_user_id IS NULL OR mail_reconciliation_actor_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_correction_resource_stages
    ADD CONSTRAINT exam_correction_resource_stages_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_correction_resource_stages_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_correction_resource_stages_exam_sitting_id_canonical_check
    CHECK (exam_sitting_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_correction_resource_stages_base_revision_id_canonical_check
    CHECK (base_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_correction_resource_stages_resource_id_canonical_check
    CHECK (resource_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_correction_resource_stages_file_entry_id_canonical_check
    CHECK (file_entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_correction_resource_stages_file_revision_id_canonical_check
    CHECK (file_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_correction_resource_stages_upload_lease_id_canonical_check
    CHECK (upload_lease_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_correction_resource_stages_rendition_id_canonical_check
    CHECK (rendition_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_correction_resource_stages_created_by_user_id_canonical_check
    CHECK (created_by_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_sitting_private_actions
    ADD CONSTRAINT exam_sitting_private_actions_audit_event_id_canonical_check
    CHECK (audit_event_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_private_actions_exam_sitting_id_canonical_check
    CHECK (exam_sitting_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_private_actions_actor_user_id_canonical_check
    CHECK (actor_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_sitting_live_corrections
    ADD CONSTRAINT exam_sitting_live_corrections_audit_event_id_canonical_check
    CHECK (audit_event_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_live_corrections_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_live_corrections_exam_sitting_id_canonical_check
    CHECK (exam_sitting_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_live_corrections_previous_revision_id_canonical_check
    CHECK (previous_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_live_corrections_correction_revision_id_canonical_check
    CHECK (correction_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sitting_live_corrections_actor_user_id_canonical_check
    CHECK (actor_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE classes
    ADD CONSTRAINT classes_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT classes_programme_level_id_canonical_check
    CHECK (programme_level_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT classes_academic_period_id_canonical_check
    CHECK (academic_period_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE jobs
    ADD CONSTRAINT jobs_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE job_permanent_occurrences
    ADD CONSTRAINT job_permanent_occurrences_job_id_canonical_check
    CHECK (job_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE job_attempts
    ADD CONSTRAINT job_attempts_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT job_attempts_job_id_canonical_check
    CHECK (job_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

-- Checksums, object keys, media metadata, and external claim-owner metadata
-- are not entity IDs and retain their distinct validation rules. Durable
-- purge_claim_id is a Proctor entity identifier and is constrained here.
ALTER TABLE file_entries
    ADD CONSTRAINT file_entries_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT file_entries_current_revision_id_canonical_check
    CHECK (current_revision_id IS NULL OR current_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE file_revisions
    ADD CONSTRAINT file_revisions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT file_revisions_file_entry_id_canonical_check
    CHECK (file_entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT file_revisions_purge_claim_id_canonical_check
    CHECK (purge_claim_id IS NULL OR purge_claim_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE file_renditions
    ADD CONSTRAINT file_renditions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT file_renditions_file_revision_id_canonical_check
    CHECK (file_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE users
    ADD CONSTRAINT users_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT users_default_profile_picture_file_id_canonical_check
    CHECK (default_profile_picture_file_id IS NULL OR default_profile_picture_file_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT users_custom_profile_picture_file_id_canonical_check
    CHECK (custom_profile_picture_file_id IS NULL OR custom_profile_picture_file_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE user_settings_documents
    ADD CONSTRAINT user_settings_documents_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT user_settings_documents_revision_canonical_check
    CHECK (revision ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE upload_leases
    ADD CONSTRAINT upload_leases_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT upload_leases_file_revision_id_canonical_check
    CHECK (file_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT upload_leases_created_by_user_id_canonical_check
    CHECK (created_by_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE file_legal_holds
    ADD CONSTRAINT file_legal_holds_file_entry_id_canonical_check
    CHECK (file_entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE external_identities
    ADD CONSTRAINT external_identities_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT external_identities_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE password_credentials
    ADD CONSTRAINT password_credentials_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT password_credentials_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE affiliations
    ADD CONSTRAINT affiliations_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT affiliations_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE academic_unit_members
    ADD CONSTRAINT academic_unit_members_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_unit_members_academic_unit_id_canonical_check
    CHECK (academic_unit_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT academic_unit_members_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE class_members
    ADD CONSTRAINT class_members_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT class_members_class_id_canonical_check
    CHECK (class_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT class_members_academic_period_id_canonical_check
    CHECK (academic_period_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT class_members_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE roles
    ADD CONSTRAINT roles_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE role_bindings
    ADD CONSTRAINT role_bindings_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT role_bindings_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT role_bindings_role_id_canonical_check
    CHECK (role_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT role_bindings_origin_invitation_id_canonical_check
    CHECK (origin_invitation_id IS NULL OR origin_invitation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT role_bindings_origin_academic_unit_member_id_canonical_check
    CHECK (origin_academic_unit_member_id IS NULL OR origin_academic_unit_member_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT role_bindings_scope_id_canonical_check
    CHECK (scope_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE sessions
    ADD CONSTRAINT sessions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT sessions_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
	ADD CONSTRAINT sessions_external_identity_id_canonical_check
	CHECK (external_identity_id IS NULL OR external_identity_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE session_credentials
    ADD CONSTRAINT session_credentials_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT session_credentials_session_id_canonical_check
    CHECK (session_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT session_credentials_family_id_canonical_check
    CHECK (family_id IS NULL OR family_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT session_credentials_parent_id_canonical_check
    CHECK (parent_id IS NULL OR parent_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT session_credentials_replaced_by_id_canonical_check
    CHECK (replaced_by_id IS NULL OR replaced_by_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE personal_access_tokens
    ADD CONSTRAINT personal_access_tokens_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_tokens_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_tokens_academic_unit_id_canonical_check
    CHECK (academic_unit_id IS NULL OR academic_unit_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE personal_access_token_mutation_preparations
    ADD CONSTRAINT personal_access_token_mutation_preparations_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_token_mutation_preparations_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_token_mutation_preparations_token_id_canonical_check
    CHECK (token_id IS NULL OR token_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_token_mutation_preparations_actor_id_canonical_check
    CHECK (actor_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_token_mutation_preparations_session_id_canonical_check
    CHECK (session_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_token_mutation_preparations_resource_id_canonical_check
    CHECK (resource_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT personal_access_token_mutation_preparations_scope_id_canonical_check
    CHECK (scope_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE user_tokens
    ADD CONSTRAINT user_tokens_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT user_tokens_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE mfa_credentials
    ADD CONSTRAINT mfa_credentials_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT mfa_credentials_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE mfa_recovery_codes
    ADD CONSTRAINT mfa_recovery_codes_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT mfa_recovery_codes_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE external_login_states
    ADD CONSTRAINT external_login_states_id_canonical_check
	CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
	ADD CONSTRAINT external_login_states_target_user_id_canonical_check
	CHECK (target_user_id IS NULL OR target_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
	ADD CONSTRAINT external_login_states_invitation_id_canonical_check
	CHECK (invitation_id IS NULL OR invitation_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
	ADD CONSTRAINT external_login_states_audit_event_id_canonical_check
	CHECK (audit_event_id IS NULL OR audit_event_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE browser_authentication_transactions
    ADD CONSTRAINT browser_authentication_transactions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT browser_authentication_transactions_institution_id_canonical_check
    CHECK (institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT browser_authentication_transactions_user_id_canonical_check
    CHECK (user_id IS NULL OR user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
	ADD CONSTRAINT browser_authentication_transactions_external_identity_id_canonical_check
	CHECK (external_identity_id IS NULL OR external_identity_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT audit_events_actor_id_canonical_check
    CHECK (actor_id IS NULL OR actor_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT audit_events_session_id_canonical_check
    CHECK (session_id IS NULL OR session_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT audit_events_resource_id_canonical_check
    CHECK (resource_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT audit_events_scope_id_canonical_check
    CHECK (scope_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE access_policies
    ADD CONSTRAINT access_policies_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE installation_states
    ADD CONSTRAINT installation_states_institution_id_canonical_check
    CHECK (institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT installation_states_administrator_user_id_canonical_check
    CHECK (administrator_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT installation_states_access_policy_id_canonical_check
    CHECK (access_policy_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
