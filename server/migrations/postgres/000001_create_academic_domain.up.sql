CREATE TABLE institutions (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    singleton boolean NOT NULL DEFAULT true CHECK (singleton),
    CONSTRAINT institutions_name_key UNIQUE (name)
);

CREATE UNIQUE INDEX institutions_singleton_key
    ON institutions (singleton) WHERE delete_at = 0;

CREATE TABLE academic_units (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    institution_id varchar(26) NOT NULL REFERENCES institutions(id),
    parent_id varchar(26) REFERENCES academic_units(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    CONSTRAINT academic_units_not_self_parent CHECK (parent_id IS NULL OR parent_id <> id)
);

CREATE UNIQUE INDEX academic_units_active_name_key
    ON academic_units (institution_id, name) WHERE delete_at = 0;
CREATE INDEX academic_units_parent_id_idx ON academic_units (parent_id) WHERE delete_at = 0;

CREATE TABLE programmes (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    academic_unit_id varchar(26) NOT NULL REFERENCES academic_units(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX programmes_active_name_key
    ON programmes (academic_unit_id, name) WHERE delete_at = 0;

CREATE TABLE programme_levels (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    programme_id varchar(26) NOT NULL REFERENCES programmes(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX programme_levels_active_name_key
    ON programme_levels (programme_id, name) WHERE delete_at = 0;

CREATE TABLE academic_periods (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    institution_id varchar(26) NOT NULL REFERENCES institutions(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    start_at bigint NOT NULL,
    end_at bigint NOT NULL,
    CONSTRAINT academic_periods_valid_range CHECK (start_at > 0 AND end_at > start_at)
);

CREATE UNIQUE INDEX academic_periods_active_name_key
    ON academic_periods (institution_id, name) WHERE delete_at = 0;

CREATE TABLE classes (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    programme_level_id varchar(26) NOT NULL REFERENCES programme_levels(id),
    academic_period_id varchar(26) NOT NULL REFERENCES academic_periods(id),
    name varchar(64) NOT NULL,
    display_name varchar(512) NOT NULL,
    description varchar(4096) NOT NULL DEFAULT '',
    CONSTRAINT classes_id_period_key UNIQUE (id, academic_period_id)
);

CREATE UNIQUE INDEX classes_active_name_key
    ON classes (programme_level_id, academic_period_id, name) WHERE delete_at = 0;
CREATE INDEX classes_academic_period_id_idx
    ON classes (academic_period_id) WHERE delete_at = 0;
