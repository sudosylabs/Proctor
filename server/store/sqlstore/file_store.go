// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type filePurgeCandidateRow struct {
	Cursor       string         `db:"cursor"`
	Kind         string         `db:"kind"`
	LeaseID      sql.NullString `db:"lease_id"`
	EntryID      string         `db:"entry_id"`
	RevisionID   string         `db:"revision_id"`
	RenditionIDs pq.StringArray `db:"rendition_ids"`
}

type SQLFileStore struct{ *SQLStore }

type uploadLeaseRow struct {
	ID              string       `db:"id"`
	FileRevisionID  string       `db:"file_revision_id"`
	CreatedByUserID string       `db:"created_by_user_id"`
	CreatedAt       time.Time    `db:"created_at"`
	UpdatedAt       time.Time    `db:"updated_at"`
	ExpiresAt       time.Time    `db:"expires_at"`
	ConsumedAt      sql.NullTime `db:"consumed_at"`
	Revision        int64        `db:"revision"`
	BytesReceived   int64        `db:"bytes_received"`
	DatabaseNow     time.Time    `db:"database_now"`
}

type fileRenditionRow struct {
	ID         string    `db:"id"`
	RevisionID string    `db:"file_revision_id"`
	CreatedAt  time.Time `db:"created_at"`
	Name       string    `db:"name"`
	MediaType  string    `db:"media_type"`
	Size       int64     `db:"size_bytes"`
	Width      int       `db:"width"`
	Height     int       `db:"height"`
	SHA256     string    `db:"sha256"`
}

type profilePictureRenditionRow struct {
	EntryID string `db:"file_entry_id"`
	fileRenditionRow
}

func newSQLFileStore(sqlStore *SQLStore) store.FileStore { return &SQLFileStore{SQLStore: sqlStore} }

func (s SQLFileStore) CreateUpload(ctx context.Context, input *store.FileUploadCreation) (*store.FileUpload, error) {
	if input == nil || input.Entry == nil || input.Revision == nil || input.Lease == nil ||
		input.Revision.FileEntryID != input.Entry.ID || input.Revision.Availability != model.FileAvailabilityPending ||
		len(input.Revision.Renditions) != 0 || input.Entry.ArchivedAt.IsSet() || !input.Entry.CurrentRevisionID.IsZero() ||
		input.Lease.FileRevisionID != input.Revision.ID {
		return nil, store.NewErrInvalidInput("file", "upload", nil)
	}
	if err := input.Entry.Validate(); err != nil {
		return nil, err
	}
	if err := input.Revision.Validate(); err != nil {
		return nil, err
	}
	if err := input.Lease.Validate(); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "file upload", func(ctx context.Context, tx *sqlxTxWrapper) (*store.FileUpload, error) {
		if _, err := tx.Exec(ctx, `INSERT INTO file_entries (id, created_at, updated_at, archived_at, revision, current_revision_id, indexing_policy, purpose) VALUES (?, ?, ?, NULL, ?, NULL, ?, ?)`, input.Entry.ID.String(), input.Entry.CreatedAt, input.Entry.UpdatedAt, input.Entry.Revision, string(input.Entry.IndexingPolicy), string(input.Entry.Purpose)); err != nil {
			return nil, fmt.Errorf("create file entry: %w", translateError("file_entry", input.Entry.ID.String(), err))
		}
		if _, err := tx.Exec(ctx, `INSERT INTO file_revisions (id, file_entry_id, created_at, availability, indexing_state) VALUES (?, ?, ?, ?, ?)`, input.Revision.ID.String(), input.Entry.ID.String(), input.Revision.CreatedAt, string(input.Revision.Availability), string(input.Revision.IndexingState)); err != nil {
			return nil, fmt.Errorf("create file revision: %w", translateError("file_revision", input.Revision.ID.String(), err))
		}
		if _, err := tx.Exec(ctx, `INSERT INTO upload_leases (id, file_revision_id, created_by_user_id, created_at, updated_at, expires_at, consumed_at, revision, bytes_received) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?)`, input.Lease.ID.String(), input.Revision.ID.String(), input.Lease.CreatedByUserID.String(), input.Lease.CreatedAt, input.Lease.UpdatedAt, input.Lease.ExpiresAt, input.Lease.Revision, input.Lease.BytesReceived); err != nil {
			return nil, fmt.Errorf("create upload lease: %w", translateError("upload_lease", input.Lease.ID.String(), err))
		}
		return &store.FileUpload{Entry: input.Entry, Revision: input.Revision, Lease: input.Lease}, nil
	})
}

func (s SQLFileStore) CreateRevisionUpload(ctx context.Context, input *store.FileRevisionUploadCreation) (*store.FileUpload, error) {
	if input == nil || !input.EntryID.IsValid() || input.Revision == nil || input.Lease == nil ||
		input.Revision.FileEntryID != input.EntryID || input.Revision.Availability != model.FileAvailabilityPending ||
		len(input.Revision.Renditions) != 0 || input.Lease.FileRevisionID != input.Revision.ID {
		return nil, store.NewErrInvalidInput("file", "revision_upload", nil)
	}
	if err := input.Revision.Validate(); err != nil {
		return nil, err
	}
	if err := input.Lease.Validate(); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "file revision upload", func(ctx context.Context, tx *sqlxTxWrapper) (*store.FileUpload, error) {
		result, err := tx.Exec(ctx, `INSERT INTO file_revisions (id, file_entry_id, created_at, availability, indexing_state) SELECT ?, id, ?, ?, ? FROM file_entries WHERE id = ? AND archived_at IS NULL`, input.Revision.ID.String(), input.Revision.CreatedAt, string(input.Revision.Availability), string(input.Revision.IndexingState), input.EntryID.String())
		if err != nil {
			return nil, fmt.Errorf("create file revision: %w", translateError("file_revision", input.Revision.ID.String(), err))
		}
		if err = requireAffected(result, "file_entry", input.EntryID.String()); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO upload_leases (id, file_revision_id, created_by_user_id, created_at, updated_at, expires_at, consumed_at, revision, bytes_received) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?)`, input.Lease.ID.String(), input.Revision.ID.String(), input.Lease.CreatedByUserID.String(), input.Lease.CreatedAt, input.Lease.UpdatedAt, input.Lease.ExpiresAt, input.Lease.Revision, input.Lease.BytesReceived); err != nil {
			return nil, fmt.Errorf("create upload lease: %w", translateError("upload_lease", input.Lease.ID.String(), err))
		}
		return &store.FileUpload{Revision: input.Revision, Lease: input.Lease}, nil
	})
}

func (s SQLFileStore) DiscardProfilePictureUpload(ctx context.Context, input *store.ProfilePictureUploadDiscard) error {
	if input == nil || !input.ActorID.IsValid() || !input.UserID.IsValid() || input.ExpectedUserRevision <= 0 || !input.ExpectedActiveEntryID.IsValid() || !input.ExpectedCurrentRevisionID.IsValid() || !input.UploadEntryID.IsValid() || !input.RevisionID.IsValid() || !input.LeaseID.IsValid() {
		return store.NewErrInvalidInput("file", "profile_picture_discard", nil)
	}
	_, err := runSQLTransaction(ctx, s.GetMaster().Begin, "profile picture upload discard", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
		var row struct {
			UserRevision      int64  `db:"user_revision"`
			ActiveEntryID     string `db:"active_entry_id"`
			CurrentRevisionID string `db:"current_revision_id"`
		}
		if err := tx.Get(ctx, &row, `SELECT u.revision AS user_revision, COALESCE(u.custom_profile_picture_file_id, u.default_profile_picture_file_id) AS active_entry_id, e.current_revision_id FROM users u JOIN file_entries e ON e.id = COALESCE(u.custom_profile_picture_file_id, u.default_profile_picture_file_id) WHERE u.id = ? AND u.archived_at IS NULL FOR UPDATE OF u, e`, input.UserID.String()); err != nil {
			return struct{}{}, translateError("user", input.UserID.String(), err)
		}
		activeEntryID, err := parsePersistedID("profile_picture_discard", "active_entry_id", row.ActiveEntryID, model.ParseFileEntryID)
		if err != nil {
			return struct{}{}, err
		}
		currentRevisionID, err := parsePersistedID("profile_picture_discard", "current_revision_id", row.CurrentRevisionID, model.ParseFileRevisionID)
		if err != nil {
			return struct{}{}, err
		}
		if row.UserRevision != input.ExpectedUserRevision || activeEntryID != input.ExpectedActiveEntryID || currentRevisionID != input.ExpectedCurrentRevisionID {
			return struct{}{}, store.NewErrConflict("profile_picture", "changed", nil)
		}
		result, err := tx.Exec(ctx, `DELETE FROM upload_leases l WHERE l.id = ? AND l.file_revision_id = ? AND l.created_by_user_id = ? AND l.consumed_at IS NULL AND NOT EXISTS (SELECT 1 FROM file_revisions v WHERE v.id = l.file_revision_id AND v.purge_claim_id IS NOT NULL)`, input.LeaseID.String(), input.RevisionID.String(), input.ActorID.String())
		if err != nil {
			return struct{}{}, fmt.Errorf("discard upload lease: %w", err)
		}
		if err = requireAffected(result, "upload_lease", input.LeaseID.String()); err != nil {
			return struct{}{}, err
		}
		result, err = tx.Exec(ctx, `DELETE FROM file_revisions WHERE id = ? AND file_entry_id = ? AND availability = 'pending'`, input.RevisionID.String(), input.UploadEntryID.String())
		if err != nil {
			return struct{}{}, fmt.Errorf("discard file revision: %w", err)
		}
		if err = requireAffected(result, "file_revision", input.RevisionID.String()); err != nil {
			return struct{}{}, err
		}
		if input.UploadEntryID != input.ExpectedActiveEntryID {
			result, err = tx.Exec(ctx, `DELETE FROM file_entries WHERE id = ? AND current_revision_id IS NULL AND archived_at IS NULL`, input.UploadEntryID.String())
			if err != nil {
				return struct{}{}, fmt.Errorf("discard pristine upload file entry: %w", err)
			}
			if err = requireAffected(result, "file_entry", input.UploadEntryID.String()); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

func (s SQLFileStore) RenewUploadLease(ctx context.Context, id model.UploadLeaseID, actorID model.UserID, expectedRevision, bytesReceived int64, expiresAt time.Time) (*model.UploadLease, error) {
	if !id.IsValid() || !actorID.IsValid() || expectedRevision <= 0 || bytesReceived <= 0 {
		return nil, store.NewErrInvalidInput("upload_lease", "renewal", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "upload lease renewal", func(ctx context.Context, tx *sqlxTxWrapper) (*model.UploadLease, error) {
		var row uploadLeaseRow
		if err := tx.Get(ctx, &row, `SELECT l.id, l.file_revision_id, l.created_by_user_id, l.created_at, l.updated_at, l.expires_at, l.consumed_at, l.revision, l.bytes_received, CURRENT_TIMESTAMP AS database_now FROM upload_leases l JOIN file_revisions v ON v.id = l.file_revision_id AND v.purge_claim_id IS NULL WHERE l.id = ? AND l.created_by_user_id = ? FOR UPDATE OF l, v`, id.String(), actorID.String()); err != nil {
			return nil, translateError("upload_lease", id.String(), err)
		}
		lease, err := row.model()
		if err != nil {
			return nil, err
		}
		if lease.Revision != expectedRevision {
			return nil, store.NewErrConflict("upload_lease", "changed", nil)
		}
		if expiresAt.After(row.DatabaseNow.Add(model.UploadLeaseMaximumLifetime)) {
			return nil, store.NewErrInvalidInput("upload_lease", "expires_at", nil)
		}
		// Expiry eligibility and the renewal horizon remain owned by the primary
		// database clock. Clamp the persisted lifecycle timestamp to its prior value
		// so modest forward skew on the application node that created the lease
		// cannot make UpdatedAt move backwards during an otherwise valid renewal.
		renewalAt := row.DatabaseNow
		if renewalAt.Before(lease.UpdatedAt) {
			renewalAt = lease.UpdatedAt
		}
		renewed, err := lease.Renew(renewalAt, expiresAt, bytesReceived)
		if err != nil {
			return nil, store.NewErrConflict("upload_lease", "not_renewable", err)
		}
		result, err := tx.Exec(ctx, `UPDATE upload_leases SET updated_at = ?, expires_at = ?, revision = ?, bytes_received = ? WHERE id = ? AND revision = ?`, renewed.UpdatedAt, renewed.ExpiresAt, renewed.Revision, renewed.BytesReceived, id.String(), expectedRevision)
		if err != nil {
			return nil, fmt.Errorf("renew upload lease: %w", err)
		}
		if err = requireAffected(result, "upload_lease", id.String()); err != nil {
			return nil, err
		}
		return renewed, nil
	})
}

func (r uploadLeaseRow) model() (*model.UploadLease, error) {
	id, err := parsePersistedID("upload_lease", "id", r.ID, model.ParseUploadLeaseID)
	if err != nil {
		return nil, err
	}
	revisionID, err := parsePersistedID("upload_lease", "file_revision_id", r.FileRevisionID, model.ParseFileRevisionID)
	if err != nil {
		return nil, err
	}
	createdBy, err := parsePersistedID("upload_lease", "created_by_user_id", r.CreatedByUserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	value := &model.UploadLease{ID: id, FileRevisionID: revisionID, CreatedByUserID: createdBy, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), ExpiresAt: r.ExpiresAt.UTC(), ConsumedAt: OptionalTimeFromNullTime(r.ConsumedAt), Revision: r.Revision, BytesReceived: r.BytesReceived}
	if err := validatePersistedModel("upload_lease", value); err != nil {
		return nil, err
	}
	return value, nil
}

func (r filePurgeCandidateRow) model() (*store.FilePurgeCandidate, error) {
	leaseID, err := parseNullablePersistedID("file_purge_candidate", "lease_id", r.LeaseID, model.ParseUploadLeaseID)
	if err != nil {
		return nil, err
	}
	entryID, err := parsePersistedID("file_purge_candidate", "entry_id", r.EntryID, model.ParseFileEntryID)
	if err != nil {
		return nil, err
	}
	revisionID, err := parsePersistedID("file_purge_candidate", "revision_id", r.RevisionID, model.ParseFileRevisionID)
	if err != nil {
		return nil, err
	}
	renditionIDs := make([]model.FileRenditionID, 0, len(r.RenditionIDs))
	for _, raw := range r.RenditionIDs {
		id, parseErr := parsePersistedID("file_purge_candidate", "rendition_ids", raw, model.ParseFileRenditionID)
		if parseErr != nil {
			return nil, parseErr
		}
		renditionIDs = append(renditionIDs, id)
	}
	value := &store.FilePurgeCandidate{Cursor: r.Cursor, Kind: store.FilePurgeCandidateKind(r.Kind), LeaseID: leaseID, EntryID: entryID, RevisionID: revisionID, RenditionIDs: renditionIDs}
	if !validFilePurgeCandidate(value) {
		return nil, invalidPersistedState("file_purge_candidate", "value", fmt.Errorf("invalid purge candidate projection"))
	}
	return value, nil
}

func (r fileRenditionRow) model(entity string) (*model.FileRendition, error) {
	id, err := parsePersistedID(entity, "id", r.ID, model.ParseFileRenditionID)
	if err != nil {
		return nil, err
	}
	revisionID, err := parsePersistedID(entity, "file_revision_id", r.RevisionID, model.ParseFileRevisionID)
	if err != nil {
		return nil, err
	}
	value := &model.FileRendition{ID: id, RevisionID: revisionID, CreatedAt: r.CreatedAt.UTC(), Name: r.Name, MediaType: r.MediaType, Size: r.Size, Width: r.Width, Height: r.Height, SHA256: r.SHA256}
	if err := validatePersistedModel(entity, value); err != nil {
		return nil, err
	}
	return value, nil
}

func (s SQLFileStore) ListPurgeCandidates(ctx context.Context, input *store.FilePurgeCandidateRequest) (*store.FilePurgeCandidatePage, error) {
	if input == nil || input.Limit < 1 || input.Limit > 100 || (input.After != "" && len(input.After) > 255) {
		return nil, store.NewErrInvalidInput("file", "purge_candidates", nil)
	}
	rows := []filePurgeCandidateRow{}
	err := s.GetMaster().Select(ctx, &rows, `
		SELECT cursor, kind, lease_id, entry_id, revision_id, rendition_ids FROM (
			SELECT 'lease:' || l.id AS cursor, 'expired_lease' AS kind, l.id AS lease_id,
			       e.id AS entry_id, v.id AS revision_id,
			       ARRAY_REMOVE(ARRAY_AGG(r.id ORDER BY r.id), NULL) AS rendition_ids
			FROM upload_leases l
			JOIN file_revisions v ON v.id = l.file_revision_id AND v.availability = 'pending'
			JOIN file_entries e ON e.id = v.file_entry_id
			LEFT JOIN file_renditions r ON r.file_revision_id = v.id
			WHERE v.purge_claim_id IS NOT NULL OR (l.consumed_at IS NULL AND l.expires_at <= CURRENT_TIMESTAMP - INTERVAL '24 hours')
			GROUP BY l.id, e.id, v.id
			UNION ALL
			SELECT 'archived:' || v.id AS cursor, 'archived_custom' AS kind, NULL AS lease_id,
			       e.id AS entry_id, v.id AS revision_id,
			       ARRAY_REMOVE(ARRAY_AGG(r.id ORDER BY r.id), NULL) AS rendition_ids
			FROM file_entries e
			JOIN file_revisions v ON v.file_entry_id = e.id
			LEFT JOIN file_renditions r ON r.file_revision_id = v.id
			WHERE e.purpose = 'profile_picture_custom' AND (v.purge_claim_id IS NOT NULL OR (
			  e.archived_at <= CURRENT_TIMESTAMP - INTERVAL '30 days'
			  AND NOT EXISTS (SELECT 1 FROM users u WHERE u.default_profile_picture_file_id = e.id OR u.custom_profile_picture_file_id = e.id)
			  AND NOT EXISTS (SELECT 1 FROM file_legal_holds h WHERE h.file_entry_id = e.id)))
			GROUP BY e.id, v.id
		) candidates
		WHERE cursor > ? ORDER BY cursor LIMIT ?`, input.After, input.Limit)
	if err != nil {
		return nil, fmt.Errorf("list file purge candidates: %w", err)
	}
	page := &store.FilePurgeCandidatePage{Candidates: make([]store.FilePurgeCandidate, 0, len(rows))}
	for _, row := range rows {
		candidate, err := row.model()
		if err != nil {
			return nil, err
		}
		page.Candidates = append(page.Candidates, *candidate)
	}
	return page, nil
}

func (s SQLFileStore) ClaimPurgeCandidate(ctx context.Context, candidate *store.FilePurgeCandidate) (*store.FilePurgeClaim, error) {
	if !validFilePurgeCandidate(candidate) {
		return nil, store.NewErrInvalidInput("file", "purge_claim", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "file purge claim", func(ctx context.Context, tx *sqlxTxWrapper) (*store.FilePurgeClaim, error) {
		var row struct {
			ClaimID      sql.NullString `db:"purge_claim_id"`
			Eligible     bool           `db:"eligible"`
			RenditionIDs pq.StringArray `db:"rendition_ids"`
		}
		var err error
		if candidate.Kind == store.FilePurgeCandidateExpiredLease {
			err = tx.Get(ctx, &row, `SELECT v.purge_claim_id,
			(v.purge_claim_id IS NOT NULL OR (l.consumed_at IS NULL AND l.expires_at <= CURRENT_TIMESTAMP - INTERVAL '24 hours')) AS eligible,
			ARRAY(SELECT r.id FROM file_renditions r WHERE r.file_revision_id = v.id ORDER BY r.id) AS rendition_ids
			FROM file_revisions v JOIN file_entries e ON e.id = v.file_entry_id
			JOIN upload_leases l ON l.file_revision_id = v.id
			WHERE v.id = ? AND v.file_entry_id = ? AND l.id = ? AND v.availability = 'pending'
			FOR UPDATE OF v, e, l`, candidate.RevisionID.String(), candidate.EntryID.String(), candidate.LeaseID.String())
		} else {
			err = tx.Get(ctx, &row, `SELECT v.purge_claim_id,
			(v.purge_claim_id IS NOT NULL OR (e.purpose = 'profile_picture_custom'
			 AND e.archived_at <= CURRENT_TIMESTAMP - INTERVAL '30 days'
			 AND NOT EXISTS (SELECT 1 FROM users u WHERE u.default_profile_picture_file_id = e.id OR u.custom_profile_picture_file_id = e.id)
			 AND NOT EXISTS (SELECT 1 FROM file_legal_holds h WHERE h.file_entry_id = e.id))) AS eligible,
			ARRAY(SELECT r.id FROM file_renditions r WHERE r.file_revision_id = v.id ORDER BY r.id) AS rendition_ids
			FROM file_revisions v JOIN file_entries e ON e.id = v.file_entry_id
			WHERE v.id = ? AND v.file_entry_id = ? FOR UPDATE OF v, e`, candidate.RevisionID.String(), candidate.EntryID.String())
		}
		if err != nil {
			return nil, translateError("file_revision", candidate.RevisionID.String(), err)
		}
		if !row.Eligible {
			return nil, store.NewErrConflict("file_revision", "retained_or_renewed", nil)
		}
		if _, err = tx.Exec(ctx, `UPDATE file_entries SET purge_claimed = TRUE WHERE id = ?`, candidate.EntryID.String()); err != nil {
			if constraintError, ok := err.(*pq.Error); ok && constraintError.Code == "23503" {
				return nil, store.NewErrConflict("file_entry", "legally_held", err)
			}
			return nil, fmt.Errorf("tombstone file entry for purge: %w", err)
		}
		claimID := row.ClaimID.String
		if claimID != "" && !model.IsValidId(claimID) {
			return nil, invalidPersistedState("file_purge_claim", "id", fmt.Errorf("invalid purge claim identifier"))
		}
		if claimID == "" {
			claimID = model.NewId()
			result, execErr := tx.Exec(ctx, `UPDATE file_revisions SET purge_claim_id = ?, purge_claimed_at = CURRENT_TIMESTAMP WHERE id = ? AND purge_claim_id IS NULL`, claimID, candidate.RevisionID.String())
			if execErr != nil {
				return nil, fmt.Errorf("claim file purge: %w", execErr)
			}
			if err = requireAffected(result, "file_revision", candidate.RevisionID.String()); err != nil {
				return nil, err
			}
		}
		claimedCandidate := *candidate
		claimedCandidate.RenditionIDs = make([]model.FileRenditionID, 0, len(row.RenditionIDs))
		for _, raw := range row.RenditionIDs {
			id, err := parsePersistedID("file_purge_claim", "rendition_ids", raw, model.ParseFileRenditionID)
			if err != nil {
				return nil, err
			}
			claimedCandidate.RenditionIDs = append(claimedCandidate.RenditionIDs, id)
		}
		return &store.FilePurgeClaim{ID: claimID, Candidate: claimedCandidate}, nil
	})
}

func (s SQLFileStore) CompletePurge(ctx context.Context, claim *store.FilePurgeClaim) error {
	if claim == nil || !model.IsValidId(claim.ID) || !validFilePurgeCandidate(&claim.Candidate) {
		return store.NewErrInvalidInput("file", "purge_completion", nil)
	}
	candidate := &claim.Candidate
	_, err := runSQLTransaction(ctx, s.GetMaster().Begin, "file purge completion", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
		var persistedClaimID string
		if err := tx.Get(ctx, &persistedClaimID, `SELECT purge_claim_id FROM file_revisions WHERE id = ? AND file_entry_id = ? FOR UPDATE`, candidate.RevisionID.String(), candidate.EntryID.String()); err != nil {
			return struct{}{}, translateError("file_revision", candidate.RevisionID.String(), err)
		}
		if persistedClaimID != claim.ID {
			return struct{}{}, store.NewErrConflict("file_revision", "purge_claim_changed", nil)
		}
		if candidate.Kind == store.FilePurgeCandidateExpiredLease {
			result, execErr := tx.Exec(ctx, `DELETE FROM upload_leases WHERE id = ? AND file_revision_id = ?`, candidate.LeaseID.String(), candidate.RevisionID.String())
			if execErr != nil {
				return struct{}{}, fmt.Errorf("delete claimed upload lease: %w", execErr)
			}
			if affected, _ := result.RowsAffected(); affected != 1 {
				return struct{}{}, store.NewErrConflict("upload_lease", "purge_claim_changed", nil)
			}
		} else {
			if _, err := tx.Exec(ctx, `DELETE FROM upload_leases WHERE file_revision_id = ?`, candidate.RevisionID.String()); err != nil {
				return struct{}{}, fmt.Errorf("delete archived revision lease: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM file_renditions WHERE file_revision_id = ?`, candidate.RevisionID.String()); err != nil {
			return struct{}{}, fmt.Errorf("delete purged rendition metadata: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE file_entries SET current_revision_id = NULL WHERE id = ? AND current_revision_id = ?`, candidate.EntryID.String(), candidate.RevisionID.String()); err != nil {
			return struct{}{}, fmt.Errorf("detach purged revision: %w", err)
		}
		result, err := tx.Exec(ctx, `DELETE FROM file_revisions WHERE id = ? AND file_entry_id = ?`, candidate.RevisionID.String(), candidate.EntryID.String())
		if err != nil {
			return struct{}{}, fmt.Errorf("delete purged revision metadata: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return struct{}{}, store.NewErrConflict("file_revision", "changed", nil)
		}
		if _, err := tx.Exec(ctx, `UPDATE file_entries e SET purge_claimed = FALSE WHERE e.id = ? AND NOT EXISTS (SELECT 1 FROM file_revisions v WHERE v.file_entry_id = e.id AND v.purge_claim_id IS NOT NULL)`, candidate.EntryID.String()); err != nil {
			return struct{}{}, fmt.Errorf("release file entry purge tombstone: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM file_entries e WHERE e.id = ? AND e.current_revision_id IS NULL AND NOT EXISTS (SELECT 1 FROM file_revisions v WHERE v.file_entry_id = e.id) AND NOT EXISTS (SELECT 1 FROM users u WHERE u.default_profile_picture_file_id = e.id OR u.custom_profile_picture_file_id = e.id)`, candidate.EntryID.String()); err != nil {
			return struct{}{}, fmt.Errorf("delete empty file entry: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

func validFilePurgeCandidate(candidate *store.FilePurgeCandidate) bool {
	if candidate == nil || !candidate.EntryID.IsValid() || !candidate.RevisionID.IsValid() {
		return false
	}
	switch candidate.Kind {
	case store.FilePurgeCandidateExpiredLease:
		return candidate.LeaseID.IsValid() && candidate.Cursor == "lease:"+candidate.LeaseID.String()
	case store.FilePurgeCandidateArchivedCustom:
		return candidate.LeaseID.IsZero() && candidate.Cursor == "archived:"+candidate.RevisionID.String()
	default:
		return false
	}
}

func (s SQLFileStore) PublishProfilePicture(ctx context.Context, input *store.ProfilePicturePublication) (*store.ProfilePicturePublicationResult, error) {
	if input == nil || !input.ActorID.IsValid() || !input.UserID.IsValid() || input.ExpectedUserRevision <= 0 || !input.EntryID.IsValid() || !input.RevisionID.IsValid() || !input.LeaseID.IsValid() || input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || !validProfilePictureRenditions(input.RevisionID, input.Renditions) {
		return nil, store.NewErrInvalidInput("file", "profile_picture_publication", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "profile picture publication", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ProfilePicturePublicationResult, error) {
		revision, err := publishProfilePictureRevision(ctx, tx, input.ActorID, input.EntryID, input.RevisionID, input.LeaseID, input.Renditions, input.ChangedAt)
		if err != nil {
			return nil, err
		}
		result, err := tx.Exec(ctx, `UPDATE users SET custom_profile_picture_file_id = ?, profile_picture_changed_at = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ? AND archived_at IS NULL AND (custom_profile_picture_file_id IS NULL OR custom_profile_picture_file_id = ?) AND revision = ?`, input.EntryID.String(), input.ChangedAt, input.ChangedAt, input.UserID.String(), input.EntryID.String(), input.ExpectedUserRevision)
		if err != nil {
			return nil, fmt.Errorf("attach profile picture: %w", err)
		}
		if err = requireUserRevisionAffected(ctx, tx, result, input.UserID.String()); err != nil {
			return nil, err
		}
		user, err := getUserByID(ctx, tx, input.UserID.String())
		if err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(map[string]any{"user_id": user.ID.String(), "active_file_entry_id": input.EntryID.String(), "active_revision_id": input.RevisionID.String(), "user_revision": user.Revision})
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete profile picture audit: %w", err)
		}
		return &store.ProfilePicturePublicationResult{User: user, Revision: revision}, nil
	})
}

func (s SQLFileStore) PublishDefaultProfilePicture(ctx context.Context, input *store.DefaultProfilePicturePublication) (*store.ProfilePicturePublicationResult, error) {
	if input == nil || !input.UserID.IsValid() || input.ExpectedUserRevision <= 0 || !input.EntryID.IsValid() || !input.RevisionID.IsValid() || !input.LeaseID.IsValid() || input.AttachedAt.IsZero() || !validProfilePictureRenditions(input.RevisionID, input.Renditions) {
		return nil, store.NewErrInvalidInput("file", "default_profile_picture_publication", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "default profile picture publication", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ProfilePicturePublicationResult, error) {
		revision, err := publishProfilePictureRevision(ctx, tx, input.UserID, input.EntryID, input.RevisionID, input.LeaseID, input.Renditions, input.AttachedAt)
		if err != nil {
			return nil, err
		}
		result, err := tx.Exec(ctx, `UPDATE users SET default_profile_picture_file_id = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ? AND archived_at IS NULL AND default_profile_picture_file_id IS NULL AND revision = ?`, input.EntryID.String(), input.AttachedAt, input.UserID.String(), input.ExpectedUserRevision)
		if err != nil {
			return nil, fmt.Errorf("attach default profile picture: %w", err)
		}
		if err = requireUserRevisionAffected(ctx, tx, result, input.UserID.String()); err != nil {
			return nil, err
		}
		user, err := getUserByID(ctx, tx, input.UserID.String())
		if err != nil {
			return nil, err
		}
		return &store.ProfilePicturePublicationResult{User: user, Revision: revision}, nil
	})
}

func publishProfilePictureRevision(ctx context.Context, tx *sqlxTxWrapper, actorID model.UserID, entryID model.FileEntryID, revisionID model.FileRevisionID, leaseID model.UploadLeaseID, renditions []model.FileRendition, at time.Time) (*model.FileRevision, error) {
	var leaseActive bool
	if err := tx.Get(ctx, &leaseActive, `SELECT l.expires_at > CURRENT_TIMESTAMP FROM upload_leases l JOIN file_revisions v ON v.id = l.file_revision_id AND v.purge_claim_id IS NULL WHERE l.id = ? AND l.file_revision_id = ? AND l.created_by_user_id = ? AND l.consumed_at IS NULL FOR UPDATE OF l, v`, leaseID.String(), revisionID.String(), actorID.String()); err != nil {
		return nil, translateError("upload_lease", leaseID.String(), err)
	}
	if !leaseActive {
		return nil, store.NewErrConflict("upload_lease", "expired", nil)
	}
	for index := range renditions {
		r := &renditions[index]
		if _, err := tx.Exec(ctx, `INSERT INTO file_renditions (id, file_revision_id, created_at, name, media_type, size_bytes, width, height, sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID.String(), r.RevisionID.String(), r.CreatedAt, r.Name, r.MediaType, r.Size, r.Width, r.Height, r.SHA256); err != nil {
			return nil, fmt.Errorf("insert file rendition: %w", translateError("file_rendition", r.ID.String(), err))
		}
	}
	result, err := tx.Exec(ctx, `UPDATE file_revisions SET availability = 'available' WHERE id = ? AND file_entry_id = ? AND availability = 'pending' AND purge_claim_id IS NULL`, revisionID.String(), entryID.String())
	if err != nil {
		return nil, fmt.Errorf("publish file revision: %w", err)
	}
	if err = requireAffected(result, "file_revision", revisionID.String()); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE file_entries SET current_revision_id = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ?`, revisionID.String(), at, entryID.String()); err != nil {
		return nil, fmt.Errorf("publish file entry: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE upload_leases SET consumed_at = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ?`, at, at, leaseID.String()); err != nil {
		return nil, fmt.Errorf("consume upload lease: %w", err)
	}
	return &model.FileRevision{ID: revisionID, FileEntryID: entryID, CreatedAt: renditions[0].CreatedAt, Availability: model.FileAvailabilityAvailable, IndexingState: model.FileIndexingNotRequired, Renditions: append([]model.FileRendition(nil), renditions...)}, nil
}

func (s SQLFileStore) GetProfilePictureRendition(ctx context.Context, userID model.UserID, name string) (*model.FileRendition, error) {
	if !userID.IsValid() || name == "" {
		return nil, store.NewErrInvalidInput("file", "profile_picture", nil)
	}
	var row fileRenditionRow
	err := s.GetMaster().Get(ctx, &row, `SELECT r.id, r.file_revision_id, r.created_at, r.name, r.media_type, r.size_bytes, r.width, r.height, r.sha256 FROM users u JOIN file_entries e ON e.id = COALESCE(u.custom_profile_picture_file_id, u.default_profile_picture_file_id) JOIN file_revisions v ON v.id = e.current_revision_id AND v.availability = 'available' JOIN file_renditions r ON r.file_revision_id = v.id AND r.name = ? WHERE u.id = ? AND u.archived_at IS NULL`, name, userID.String())
	if err != nil {
		return nil, translateError("profile_picture", userID.String(), err)
	}
	return row.model("file_rendition")
}

func (s SQLFileStore) GetProfilePictureState(ctx context.Context, userID model.UserID) (*store.ProfilePictureState, error) {
	if !userID.IsValid() {
		return nil, store.NewErrInvalidInput("file", "profile_picture", nil)
	}
	rows := []profilePictureRenditionRow{}
	err := s.GetMaster().Select(ctx, &rows, `SELECT e.id AS file_entry_id, r.file_revision_id, r.id, r.created_at, r.name, r.media_type, r.size_bytes, r.width, r.height, r.sha256 FROM users u JOIN file_entries e ON e.id = COALESCE(u.custom_profile_picture_file_id, u.default_profile_picture_file_id) JOIN file_revisions v ON v.id = e.current_revision_id AND v.availability = 'available' JOIN file_renditions r ON r.file_revision_id = v.id WHERE u.id = ? AND u.archived_at IS NULL ORDER BY r.name`, userID.String())
	if err != nil {
		return nil, fmt.Errorf("get profile picture state: %w", err)
	}
	if len(rows) == 0 {
		return nil, store.NewErrNotFound("profile_picture", userID.String())
	}
	entryID, err := parsePersistedID("profile_picture_state", "file_entry_id", rows[0].EntryID, model.ParseFileEntryID)
	if err != nil {
		return nil, err
	}
	revisionID, err := parsePersistedID("profile_picture_state", "file_revision_id", rows[0].RevisionID, model.ParseFileRevisionID)
	if err != nil {
		return nil, err
	}
	state := &store.ProfilePictureState{EntryID: entryID, RevisionID: revisionID, Renditions: make([]model.FileRendition, 0, len(rows))}
	for _, row := range rows {
		if row.EntryID != state.EntryID.String() || row.RevisionID != state.RevisionID.String() {
			return nil, invalidPersistedState("profile_picture_state", "file_revision_id", fmt.Errorf("mixed revisions"))
		}
		rendition, err := row.fileRenditionRow.model("profile_picture_state")
		if err != nil {
			return nil, err
		}
		state.Renditions = append(state.Renditions, *rendition)
	}
	if !validProfilePictureRenditions(state.RevisionID, state.Renditions) {
		return nil, invalidPersistedState("profile_picture_state", "value", fmt.Errorf("incomplete or invalid rendition set"))
	}
	return state, nil
}

func (s SQLFileStore) RemoveProfilePictureWithAudit(ctx context.Context, input *store.ProfilePictureRemoval) (*model.User, error) {
	if input == nil || !input.ActorID.IsValid() || !input.UserID.IsValid() || input.ExpectedUserRevision <= 0 || !input.EntryID.IsValid() || !input.ExpectedCurrentRevisionID.IsValid() || len(input.ExpectedSHA256) != 64 || input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("file", "profile_picture_removal", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "profile picture removal", func(ctx context.Context, tx *sqlxTxWrapper) (*model.User, error) {
		var row struct {
			UserRevision      int64  `db:"user_revision"`
			CustomEntryID     string `db:"custom_entry_id"`
			CurrentRevisionID string `db:"current_revision_id"`
			ChecksumMatches   bool   `db:"checksum_matches"`
		}
		if err := tx.Get(ctx, &row, `SELECT u.revision AS user_revision, u.custom_profile_picture_file_id AS custom_entry_id, e.current_revision_id, EXISTS (SELECT 1 FROM file_renditions r WHERE r.file_revision_id = e.current_revision_id AND r.sha256 = ?) AS checksum_matches FROM users u JOIN file_entries e ON e.id = u.custom_profile_picture_file_id WHERE u.id = ? AND u.archived_at IS NULL FOR UPDATE OF u, e`, input.ExpectedSHA256, input.UserID.String()); err != nil {
			return nil, translateError("user", input.UserID.String(), err)
		}
		customEntryID, err := parsePersistedID("profile_picture_removal", "custom_entry_id", row.CustomEntryID, model.ParseFileEntryID)
		if err != nil {
			return nil, err
		}
		currentRevisionID, err := parsePersistedID("profile_picture_removal", "current_revision_id", row.CurrentRevisionID, model.ParseFileRevisionID)
		if err != nil {
			return nil, err
		}
		if row.UserRevision != input.ExpectedUserRevision || customEntryID != input.EntryID || currentRevisionID != input.ExpectedCurrentRevisionID || !row.ChecksumMatches {
			return nil, store.NewErrConflict("profile_picture", "changed", nil)
		}
		result, err := tx.Exec(ctx, `UPDATE file_entries SET archived_at = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ? AND archived_at IS NULL`, input.ChangedAt, input.ChangedAt, input.EntryID.String())
		if err != nil {
			return nil, fmt.Errorf("archive profile picture file entry: %w", err)
		}
		if err = requireAffected(result, "file_entry", input.EntryID.String()); err != nil {
			return nil, err
		}
		result, err = tx.Exec(ctx, `UPDATE users SET custom_profile_picture_file_id = NULL, profile_picture_changed_at = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ? AND archived_at IS NULL AND custom_profile_picture_file_id = ? AND revision = ?`, input.ChangedAt, input.ChangedAt, input.UserID.String(), input.EntryID.String(), input.ExpectedUserRevision)
		if err != nil {
			return nil, fmt.Errorf("clear custom profile picture: %w", err)
		}
		if err = requireUserRevisionAffected(ctx, tx, result, input.UserID.String()); err != nil {
			return nil, err
		}
		user, err := getUserByID(ctx, tx, input.UserID.String())
		if err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(map[string]any{"user_id": user.ID.String(), "active_file_entry_id": user.DefaultProfilePictureFileID.String(), "archived_file_entry_id": input.EntryID.String(), "user_revision": user.Revision})
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, fmt.Errorf("complete profile picture removal audit: %w", err)
		}
		return user, nil
	})
}

var _ store.FileStore = (*SQLFileStore)(nil)

func validProfilePictureRenditions(revisionID model.FileRevisionID, renditions []model.FileRendition) bool {
	if len(renditions) != 3 {
		return false
	}
	want := map[string]int{"profile_128": 128, "profile_256": 256, "profile_512": 512}
	seen := make(map[string]bool, 3)
	for index := range renditions {
		r := &renditions[index]
		if r.Validate() != nil || r.RevisionID != revisionID || r.MediaType != "image/webp" || r.Size <= 0 {
			return false
		}
		nominal, exists := want[r.Name]
		if !exists || seen[r.Name] || r.Width != r.Height || r.Width > nominal {
			return false
		}
		seen[r.Name] = true
	}
	return true
}
