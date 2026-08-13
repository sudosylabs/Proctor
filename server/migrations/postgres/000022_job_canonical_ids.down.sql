-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE job_attempts
    DROP CONSTRAINT IF EXISTS job_attempts_job_id_canonical_check,
    DROP CONSTRAINT IF EXISTS job_attempts_id_canonical_check;

ALTER TABLE job_permanent_occurrences
    DROP CONSTRAINT IF EXISTS job_permanent_occurrences_job_id_canonical_check;

ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_id_canonical_check;
