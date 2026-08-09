// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

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
	DatabaseNow     time.Time    `db:"database_now"`
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
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin file upload: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(ctx, `INSERT INTO file_entries (id, created_at, updated_at, archived_at, revision, current_revision_id, indexing_policy) VALUES (?, ?, ?, NULL, ?, NULL, ?)`, input.Entry.ID.String(), input.Entry.CreatedAt, input.Entry.UpdatedAt, input.Entry.Revision, string(input.Entry.IndexingPolicy)); err != nil {
		return nil, fmt.Errorf("create file entry: %w", translateError("file_entry", input.Entry.ID.String(), err))
	}
	if _, err = tx.Exec(ctx, `INSERT INTO file_revisions (id, file_entry_id, created_at, availability, indexing_state) VALUES (?, ?, ?, ?, ?)`, input.Revision.ID.String(), input.Entry.ID.String(), input.Revision.CreatedAt, string(input.Revision.Availability), string(input.Revision.IndexingState)); err != nil {
		return nil, fmt.Errorf("create file revision: %w", translateError("file_revision", input.Revision.ID.String(), err))
	}
	if _, err = tx.Exec(ctx, `INSERT INTO upload_leases (id, file_revision_id, created_by_user_id, created_at, updated_at, expires_at, consumed_at, revision) VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`, input.Lease.ID.String(), input.Revision.ID.String(), input.Lease.CreatedByUserID.String(), input.Lease.CreatedAt, input.Lease.UpdatedAt, input.Lease.ExpiresAt, input.Lease.Revision); err != nil {
		return nil, fmt.Errorf("create upload lease: %w", translateError("upload_lease", input.Lease.ID.String(), err))
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit file upload: %w", err)
	}
	return &store.FileUpload{Entry: input.Entry, Revision: input.Revision, Lease: input.Lease}, nil
}

func (s SQLFileStore) RenewUploadLease(ctx context.Context, id model.UploadLeaseID, actorID model.UserID, expectedRevision int64, expiresAt time.Time) (*model.UploadLease, error) {
	if !id.IsValid() || !actorID.IsValid() || expectedRevision <= 0 {
		return nil, store.NewErrInvalidInput("upload_lease", "renewal", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin upload lease renewal: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var row uploadLeaseRow
	if err = tx.Get(ctx, &row, `SELECT id, file_revision_id, created_by_user_id, created_at, updated_at, expires_at, consumed_at, revision, CURRENT_TIMESTAMP AS database_now FROM upload_leases WHERE id = ? AND created_by_user_id = ? FOR UPDATE`, id.String(), actorID.String()); err != nil {
		return nil, translateError("upload_lease", id.String(), err)
	}
	lease := row.model()
	if lease.Revision != expectedRevision {
		return nil, store.NewErrConflict("upload_lease", "changed", nil)
	}
	if expiresAt.After(row.DatabaseNow.Add(model.UploadLeaseMaximumLifetime)) {
		return nil, store.NewErrInvalidInput("upload_lease", "expires_at", nil)
	}
	renewed, err := lease.Renew(row.DatabaseNow, expiresAt)
	if err != nil {
		return nil, store.NewErrConflict("upload_lease", "not_renewable", err)
	}
	result, err := tx.Exec(ctx, `UPDATE upload_leases SET updated_at = ?, expires_at = ?, revision = ? WHERE id = ? AND revision = ?`, renewed.UpdatedAt, renewed.ExpiresAt, renewed.Revision, id.String(), expectedRevision)
	if err != nil {
		return nil, fmt.Errorf("renew upload lease: %w", err)
	}
	if err = requireAffected(result, "upload_lease", id.String()); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit upload lease renewal: %w", err)
	}
	return renewed, nil
}

func (r uploadLeaseRow) model() *model.UploadLease {
	return &model.UploadLease{ID: model.UploadLeaseID(r.ID), FileRevisionID: model.FileRevisionID(r.FileRevisionID), CreatedByUserID: model.UserID(r.CreatedByUserID), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), ExpiresAt: r.ExpiresAt.UTC(), ConsumedAt: OptionalTimeFromNullTime(r.ConsumedAt), Revision: r.Revision}
}

func (s SQLFileStore) PublishProfilePicture(ctx context.Context, input *store.ProfilePicturePublication) (*store.ProfilePicturePublicationResult, error) {
	if input == nil || !input.ActorID.IsValid() || !input.UserID.IsValid() || input.ExpectedUserRevision <= 0 || !input.EntryID.IsValid() || !input.RevisionID.IsValid() || !input.LeaseID.IsValid() || input.ChangedAt.IsZero() || !validProfilePictureRenditions(input.RevisionID, input.Renditions) {
		return nil, store.NewErrInvalidInput("file", "profile_picture_publication", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin profile picture publication: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var leaseActive bool
	if err = tx.Get(ctx, &leaseActive, `SELECT expires_at > CURRENT_TIMESTAMP FROM upload_leases WHERE id = ? AND file_revision_id = ? AND created_by_user_id = ? AND consumed_at IS NULL FOR UPDATE`, input.LeaseID.String(), input.RevisionID.String(), input.ActorID.String()); err != nil {
		return nil, translateError("upload_lease", input.LeaseID.String(), err)
	}
	if !leaseActive {
		return nil, store.NewErrConflict("upload_lease", "expired", nil)
	}
	for index := range input.Renditions {
		r := &input.Renditions[index]
		if _, err = tx.Exec(ctx, `INSERT INTO file_renditions (id, file_revision_id, created_at, name, media_type, size_bytes, width, height, sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID.String(), r.RevisionID.String(), r.CreatedAt, r.Name, r.MediaType, r.Size, r.Width, r.Height, r.SHA256); err != nil {
			return nil, fmt.Errorf("insert file rendition: %w", translateError("file_rendition", r.ID.String(), err))
		}
	}
	result, err := tx.Exec(ctx, `UPDATE file_revisions SET availability = 'available' WHERE id = ? AND file_entry_id = ? AND availability = 'pending'`, input.RevisionID.String(), input.EntryID.String())
	if err != nil {
		return nil, fmt.Errorf("publish file revision: %w", err)
	}
	if err = requireAffected(result, "file_revision", input.RevisionID.String()); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE file_entries SET current_revision_id = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ?`, input.RevisionID.String(), input.ChangedAt, input.EntryID.String()); err != nil {
		return nil, fmt.Errorf("publish file entry: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE upload_leases SET consumed_at = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ?`, input.ChangedAt, input.ChangedAt, input.LeaseID.String()); err != nil {
		return nil, fmt.Errorf("consume upload lease: %w", err)
	}
	result, err = tx.Exec(ctx, `UPDATE users SET custom_profile_picture_file_id = ?, profile_picture_changed_at = ?, updated_at = GREATEST(updated_at, ?), revision = revision + 1 WHERE id = ? AND archived_at IS NULL AND custom_profile_picture_file_id IS NULL AND revision = ?`, input.EntryID.String(), input.ChangedAt, input.ChangedAt, input.UserID.String(), input.ExpectedUserRevision)
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
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit profile picture publication: %w", err)
	}
	revision := &model.FileRevision{ID: input.RevisionID, FileEntryID: input.EntryID, CreatedAt: input.Renditions[0].CreatedAt, Availability: model.FileAvailabilityAvailable, IndexingState: model.FileIndexingNotRequired, Renditions: append([]model.FileRendition(nil), input.Renditions...)}
	return &store.ProfilePicturePublicationResult{User: user, Revision: revision}, nil
}

func (s SQLFileStore) GetProfilePictureRendition(ctx context.Context, userID model.UserID, name string) (*model.FileRendition, error) {
	if !userID.IsValid() || name == "" {
		return nil, store.NewErrInvalidInput("file", "profile_picture", nil)
	}
	var row struct {
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
	err := s.GetMaster().Get(ctx, &row, `SELECT r.id, r.file_revision_id, r.created_at, r.name, r.media_type, r.size_bytes, r.width, r.height, r.sha256 FROM users u JOIN file_entries e ON e.id = u.custom_profile_picture_file_id JOIN file_revisions v ON v.id = e.current_revision_id AND v.availability = 'available' JOIN file_renditions r ON r.file_revision_id = v.id AND r.name = ? WHERE u.id = ? AND u.archived_at IS NULL`, name, userID.String())
	if err != nil {
		return nil, translateError("profile_picture", userID.String(), err)
	}
	return &model.FileRendition{ID: model.FileRenditionID(row.ID), RevisionID: model.FileRevisionID(row.RevisionID), CreatedAt: row.CreatedAt.UTC(), Name: row.Name, MediaType: row.MediaType, Size: row.Size, Width: row.Width, Height: row.Height, SHA256: row.SHA256}, nil
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
