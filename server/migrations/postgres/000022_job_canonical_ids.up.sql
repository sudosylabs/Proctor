-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Validate durable Job identity columns before making the model's canonical
-- 26-character z-base-32 representation authoritative in SQL.

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
