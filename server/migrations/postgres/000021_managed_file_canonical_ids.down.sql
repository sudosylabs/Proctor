-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only

ALTER TABLE file_legal_holds
    DROP CONSTRAINT IF EXISTS file_legal_holds_file_entry_id_canonical_check;

ALTER TABLE upload_leases
    DROP CONSTRAINT IF EXISTS upload_leases_created_by_user_id_canonical_check,
    DROP CONSTRAINT IF EXISTS upload_leases_file_revision_id_canonical_check,
    DROP CONSTRAINT IF EXISTS upload_leases_id_canonical_check;

ALTER TABLE file_renditions
    DROP CONSTRAINT IF EXISTS file_renditions_file_revision_id_canonical_check,
    DROP CONSTRAINT IF EXISTS file_renditions_id_canonical_check;

ALTER TABLE file_revisions
    DROP CONSTRAINT IF EXISTS file_revisions_purge_claim_id_canonical_check,
    DROP CONSTRAINT IF EXISTS file_revisions_file_entry_id_canonical_check,
    DROP CONSTRAINT IF EXISTS file_revisions_id_canonical_check;

ALTER TABLE file_entries
    DROP CONSTRAINT IF EXISTS file_entries_current_revision_id_canonical_check,
    DROP CONSTRAINT IF EXISTS file_entries_id_canonical_check;
