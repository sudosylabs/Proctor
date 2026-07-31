-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

CREATE TABLE mfa_credentials (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    state varchar(16) NOT NULL CHECK (state IN ('pending', 'active')),
    encrypted_secret varchar(4096) NOT NULL,
    encryption_key_id char(16) NOT NULL,
    pending_expires_at bigint NOT NULL DEFAULT 0,
    enabled_at bigint NOT NULL DEFAULT 0,
    last_used_time_step bigint NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX mfa_credentials_user_key
    ON mfa_credentials (user_id) WHERE delete_at = 0;

CREATE TABLE mfa_recovery_codes (
    id varchar(26) PRIMARY KEY,
    create_at bigint NOT NULL,
    update_at bigint NOT NULL,
    delete_at bigint NOT NULL DEFAULT 0,
    user_id varchar(26) NOT NULL REFERENCES users(id),
    code_hash char(64) NOT NULL,
    used_at bigint NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX mfa_recovery_codes_hash_key
    ON mfa_recovery_codes (code_hash);
CREATE INDEX mfa_recovery_codes_active_user_idx
    ON mfa_recovery_codes (user_id)
    WHERE delete_at = 0 AND used_at = 0;
