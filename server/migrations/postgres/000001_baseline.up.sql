-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Pre-release schema baseline. Existing development databases must be
-- recreated; there is no upgrade path from earlier development migration
-- sets. Temporal columns use timestamptz. Soft archive uses nullable
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
    status varchar(24) NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'canceled', 'lease_expired')),
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
    base_revision_id varchar(26),
    updated_at timestamptz NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    CONSTRAINT exam_drafts_title_check CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT exam_drafts_instructions_markdown_check
        CHECK (octet_length(instructions_markdown) <= 65536),
    CONSTRAINT exam_drafts_policy_size_check CHECK (octet_length(policy::text) <= 65536)
);

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
    UNIQUE (exam_id, id),
    CONSTRAINT exam_sittings_revision_fkey
        FOREIGN KEY (exam_id, exam_revision_id, exam_revision_sealed)
        REFERENCES exam_revisions(exam_id, id, sealed),
    CONSTRAINT exam_sittings_schedule_check CHECK (scheduled_start_at < scheduled_end_at),
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
        CHECK (resource_type IN ('institution', 'academic_unit', 'class', 'user', 'exam', 'exam_sitting', 'submission')),
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
    original_audit_event_id varchar(26) NOT NULL REFERENCES audit_events(id),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, operation, key_digest),
    CONSTRAINT command_outcomes_lifecycle_check CHECK (expires_at > created_at),
    CONSTRAINT command_outcomes_operation_check
        CHECK (operation ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$')
);

CREATE INDEX command_outcomes_expires_at_idx
    ON command_outcomes (expires_at, user_id, operation);

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

-- ---------------------------------------------------------------------------
-- Canonical entity identifiers
-- ---------------------------------------------------------------------------

-- PostgreSQL varchar(26) limits length but does not enforce Proctor's exact
-- 26-character z-base-32 representation. Keep that durable invariant in the
-- pre-release baseline alongside the relationships it protects.

ALTER TABLE institutions
    ADD CONSTRAINT institutions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

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
    CHECK (institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

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

ALTER TABLE exam_submission_manifest_entries
    ADD CONSTRAINT exam_submission_manifest_entries_submission_id_canonical_check CHECK (submission_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_workspace_id_canonical_check CHECK (workspace_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_entry_id_canonical_check CHECK (entry_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_content_version_canonical_check CHECK (content_version IS NULL OR content_version ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_starter_object_id_canonical_check CHECK (starter_object_id IS NULL OR starter_object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_attempt_object_id_canonical_check CHECK (attempt_object_id IS NULL OR attempt_object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_submission_manifest_entries_workspace_object_id_canonical_check CHECK (workspace_object_id IS NULL OR workspace_object_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE exam_sittings
    ADD CONSTRAINT exam_sittings_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sittings_exam_id_canonical_check
    CHECK (exam_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sittings_exam_revision_id_canonical_check
    CHECK (exam_revision_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT exam_sittings_class_id_canonical_check
    CHECK (class_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

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
    ADD CONSTRAINT role_bindings_scope_id_canonical_check
    CHECK (scope_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

ALTER TABLE sessions
    ADD CONSTRAINT sessions_id_canonical_check
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT sessions_user_id_canonical_check
    CHECK (user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

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
    CHECK (id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');

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

ALTER TABLE installation_states
    ADD CONSTRAINT installation_states_institution_id_canonical_check
    CHECK (institution_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$'),
    ADD CONSTRAINT installation_states_administrator_user_id_canonical_check
    CHECK (administrator_user_id ~ '^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$');
