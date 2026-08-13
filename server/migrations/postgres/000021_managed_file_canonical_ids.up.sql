-- Copyright 2026 SudoSylabs
-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Enforce canonical identifiers for managed-file entities and references.
-- Checksums, object keys, media metadata, and external claim-owner metadata
-- are not IDs and intentionally retain their distinct validation rules. The
-- durable purge_claim_id is a Proctor entity identifier and is constrained.

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
