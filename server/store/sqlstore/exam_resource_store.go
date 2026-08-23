// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLExamResourceStore struct{ *SQLStore }

func newSQLExamResourceStore(sqlStore *SQLStore) store.ExamResourceStore {
	return &SQLExamResourceStore{SQLStore: sqlStore}
}

type examResourceRow struct {
	ID                  string       `db:"id" json:"id"`
	ExamID              string       `db:"exam_id" json:"exam_id"`
	FileEntryID         string       `db:"file_entry_id" json:"file_entry_id"`
	SelectedRevisionID  string       `db:"selected_file_revision_id" json:"selected_file_revision_id"`
	DisplayName         string       `db:"display_name" json:"display_name"`
	DescriptionMarkdown string       `db:"description_markdown" json:"description_markdown"`
	Position            int          `db:"position" json:"position"`
	CreatedAt           time.Time    `db:"created_at" json:"created_at"`
	UpdatedAt           time.Time    `db:"updated_at" json:"updated_at"`
	ArchivedAt          sql.NullTime `db:"archived_at" json:"archived_at"`
	DraftRevision       int64        `db:"draft_revision" json:"draft_revision"`
	RenditionID         string       `db:"rendition_id" json:"rendition_id"`
	RenditionRevisionID string       `db:"rendition_revision_id" json:"rendition_revision_id"`
	RenditionCreatedAt  time.Time    `db:"rendition_created_at" json:"rendition_created_at"`
	RenditionName       string       `db:"rendition_name" json:"rendition_name"`
	MediaType           string       `db:"media_type" json:"media_type"`
	Size                int64        `db:"size_bytes" json:"size_bytes"`
	Width               int          `db:"width" json:"width"`
	Height              int          `db:"height" json:"height"`
	SHA256              string       `db:"sha256" json:"sha256"`
}

const examResourceSelect = `SELECT r.id, r.exam_id, r.file_entry_id, r.selected_file_revision_id,
	r.display_name, r.description_markdown, r.position, r.created_at, r.updated_at, r.archived_at,
	d.revision AS draft_revision, x.id AS rendition_id, x.file_revision_id AS rendition_revision_id,
	x.created_at AS rendition_created_at, x.name AS rendition_name, x.media_type, x.size_bytes,
	x.width, x.height, x.sha256
FROM exam_resources r JOIN exam_drafts d ON d.exam_id = r.exam_id
JOIN file_revisions v ON v.id = r.selected_file_revision_id AND v.file_entry_id = r.file_entry_id AND v.availability = 'available'
JOIN file_renditions x ON x.file_revision_id = v.id AND x.name = 'original'`

func (r examResourceRow) record() (*store.ExamResourceRecord, error) {
	id, err := parsePersistedID("exam_resource", "id", r.ID, model.ParseExamResourceID)
	if err != nil {
		return nil, err
	}
	examID, err := parsePersistedID("exam_resource", "exam_id", r.ExamID, model.ParseExamID)
	if err != nil {
		return nil, err
	}
	entryID, err := parsePersistedID("exam_resource", "file_entry_id", r.FileEntryID, model.ParseFileEntryID)
	if err != nil {
		return nil, err
	}
	revisionID, err := parsePersistedID("exam_resource", "selected_file_revision_id", r.SelectedRevisionID, model.ParseFileRevisionID)
	if err != nil {
		return nil, err
	}
	resource := &model.ExamResource{ID: id, ExamID: examID, FileEntryID: entryID, SelectedFileRevisionID: revisionID,
		DisplayName: r.DisplayName, DescriptionMarkdown: r.DescriptionMarkdown, Position: r.Position,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), ArchivedAt: OptionalTimeFromNullTime(r.ArchivedAt)}
	if err = validatePersistedModel("exam_resource", resource); err != nil {
		return nil, err
	}
	rendition, err := (fileRenditionRow{ID: r.RenditionID, RevisionID: r.RenditionRevisionID, CreatedAt: r.RenditionCreatedAt,
		Name: r.RenditionName, MediaType: r.MediaType, Size: r.Size, Width: r.Width, Height: r.Height, SHA256: r.SHA256}).model("exam_resource_rendition")
	if err != nil {
		return nil, err
	}
	if rendition.RevisionID != resource.SelectedFileRevisionID || rendition.Name != "original" || !model.ExamResourceMediaType(rendition.MediaType).IsValid() || rendition.Size < 0 || rendition.Size > model.ExamResourceMaximumBytes || r.DraftRevision < 1 {
		return nil, invalidPersistedState("exam_resource", "aggregate", errors.New("invalid selected rendition"))
	}
	return &store.ExamResourceRecord{Resource: resource, Rendition: rendition, DraftRevision: r.DraftRevision}, nil
}

func (s SQLExamResourceStore) List(ctx context.Context, examID model.ExamID) ([]store.ExamResourceRecord, error) {
	if !examID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_resource", "exam_id", examID)
	}
	rows := []examResourceRow{}
	if err := s.GetMaster().Select(ctx, &rows, examResourceSelect+` WHERE r.exam_id = ? AND r.archived_at IS NULL ORDER BY r.position`, examID.String()); err != nil {
		return nil, fmt.Errorf("list exam resources: %w", err)
	}
	if len(rows) > model.ExamResourceMaximumCount {
		return nil, invalidPersistedState("exam_resource", "count", errors.New("too many active resources"))
	}
	items := make([]store.ExamResourceRecord, 0, len(rows))
	for index, row := range rows {
		record, err := row.record()
		if err != nil {
			return nil, err
		}
		if record.Resource.Position != index {
			return nil, invalidPersistedState("exam_resource", "position", errors.New("non-contiguous order"))
		}
		items = append(items, *record)
	}
	return items, nil
}

func (s SQLExamResourceStore) Get(ctx context.Context, examID model.ExamID, resourceID model.ExamResourceID) (*store.ExamResourceRecord, error) {
	if !examID.IsValid() || !resourceID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_resource", "identity", nil)
	}
	var row examResourceRow
	if err := s.GetMaster().Get(ctx, &row, examResourceSelect+` WHERE r.exam_id = ? AND r.id = ? AND r.archived_at IS NULL`, examID.String(), resourceID.String()); err != nil {
		return nil, translateError("exam_resource", resourceID.String(), err)
	}
	return row.record()
}

func (s SQLExamResourceStore) ReserveUpload(ctx context.Context, input *store.ExamResourceUploadReservation) (*store.FileUpload, error) {
	if err := validateResourceReservation(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "exam resource upload reservation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.FileUpload, error) {
		entryID := input.EntryID
		if !input.Replacement {
			entryID = input.Entry.ID
			if _, err := tx.Exec(ctx, `INSERT INTO file_entries (id, created_at, updated_at, archived_at, revision, current_revision_id, indexing_policy, purpose) VALUES (?, ?, ?, NULL, ?, NULL, ?, ?)`, entryID.String(), input.Entry.CreatedAt, input.Entry.UpdatedAt, input.Entry.Revision, string(input.Entry.IndexingPolicy), string(input.Entry.Purpose)); err != nil {
				return nil, fmt.Errorf("reserve exam resource entry: %w", translateError("file_entry", entryID.String(), err))
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO file_revisions (id, file_entry_id, created_at, availability, indexing_state) VALUES (?, ?, ?, 'pending', 'not_required')`, input.Revision.ID.String(), entryID.String(), input.Revision.CreatedAt); err != nil {
			return nil, fmt.Errorf("reserve exam resource revision: %w", translateError("file_revision", input.Revision.ID.String(), err))
		}
		if _, err := tx.Exec(ctx, `INSERT INTO upload_leases (id, file_revision_id, created_by_user_id, created_at, updated_at, expires_at, consumed_at, revision, bytes_received) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, 0)`, input.Lease.ID.String(), input.Revision.ID.String(), input.Lease.CreatedByUserID.String(), input.Lease.CreatedAt, input.Lease.UpdatedAt, input.Lease.ExpiresAt, input.Lease.Revision); err != nil {
			return nil, fmt.Errorf("reserve exam resource lease: %w", translateError("upload_lease", input.Lease.ID.String(), err))
		}
		return &store.FileUpload{Entry: input.Entry, Revision: input.Revision, Lease: input.Lease}, nil
	})
}

func validateResourceReservation(input *store.ExamResourceUploadReservation) error {
	if input == nil || !input.ExamID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedDraftRevision < 1 || !input.ResourceID.IsValid() || input.Revision == nil || input.Lease == nil || input.Revision.Validate() != nil || input.Lease.Validate() != nil || input.Revision.Availability != model.FileAvailabilityPending || input.Lease.FileRevisionID != input.Revision.ID || input.Lease.CreatedByUserID != input.ActorUserID {
		return store.NewErrInvalidInput("exam_resource", "reservation", nil)
	}
	if input.Replacement {
		if input.Entry != nil || !input.EntryID.IsValid() || input.Revision.FileEntryID != input.EntryID {
			return store.NewErrInvalidInput("exam_resource", "replacement", nil)
		}
	} else if input.Entry == nil || input.Entry.Validate() != nil || input.Entry.Purpose != model.FilePurposeExamResource || input.Entry.IndexingPolicy != model.FileIndexingNone || !input.EntryID.IsZero() || input.Revision.FileEntryID != input.Entry.ID {
		return store.NewErrInvalidInput("exam_resource", "entry", nil)
	}
	return nil
}

func lockExamResourceDraft(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, actorID model.UserID, override bool, expectedRevision int64) error {
	var row struct {
		ArchivedAt     sql.NullTime `db:"archived_at"`
		Revision       int64        `db:"revision"`
		ActorIsManager bool         `db:"actor_is_manager"`
	}
	if err := tx.Get(ctx, &row, `SELECT e.archived_at, d.revision, EXISTS (SELECT 1 FROM exam_managers m WHERE m.exam_id=e.id AND m.user_id=?) actor_is_manager FROM exams e JOIN exam_drafts d ON d.exam_id=e.id WHERE e.id=? FOR UPDATE OF e,d`, actorID.String(), examID.String()); err != nil {
		return translateError("exam", examID.String(), err)
	}
	if row.ArchivedAt.Valid {
		return store.NewErrConflict("exam", "exam_archived", nil)
	}
	if !override && !row.ActorIsManager {
		return store.NewErrNotFound("exam", examID.String())
	}
	if row.Revision != expectedRevision {
		return store.NewErrConflict("exam_draft", "exam_draft_revision", nil)
	}
	return nil
}

func (s SQLExamResourceStore) FinalizeUpload(ctx context.Context, input *store.ExamResourceUploadFinalization, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	if input == nil || input.Resource == nil || input.Rendition == nil || command == nil || input.Resource.Validate() != nil || input.Rendition.Validate() != nil || !input.LeaseID.IsValid() || input.ExpectedDraftRevision < 1 || !input.ActorUserID.IsValid() || input.Resource.ExamID != input.ExamID || input.Resource.SelectedFileRevisionID != input.Rendition.RevisionID || input.Rendition.Name != "original" || !model.ExamResourceMediaType(input.Rendition.MediaType).IsValid() || input.Rendition.Size < 0 || input.Rendition.Size > model.ExamResourceMaximumBytes || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || input.ChangedAt.IsZero() {
		return nil, store.NewErrInvalidInput("exam_resource", "finalization", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "exam resource upload finalization", idempotentMutation[*store.ExamResourceRecord]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamResourceRecord, error) {
			return finalizeExamResourceUpload(ctx, tx, input)
		},
		encode: func(value *store.ExamResourceRecord) ([]byte, error) {
			row, err := examResourceRecordRow(value)
			if err != nil {
				return nil, err
			}
			return encodeCommandOutcome(row)
		},
		decode: decodeExamResourceOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.ExamResourceRecord, original string) error {
			return completeExamResourceReplay(ctx, tx, input.AuditEventID, input.AuditAt, value, original)
		},
	})
	if err != nil {
		return nil, err
	}
	return &store.ExamResourceCommandResult{Value: result.Value, Replayed: result.Replayed}, nil
}

func finalizeExamResourceUpload(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamResourceUploadFinalization) (*store.ExamResourceRecord, error) {
	if err := lockExamResourceDraft(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, input.ExpectedDraftRevision); err != nil {
		return nil, err
	}
	capacity, err := currentExamCapacityPolicy(ctx, tx)
	if err != nil {
		return nil, err
	}
	if input.Rendition.Size > capacity.ResourceMaximumBytes {
		return nil, store.NewErrConflict("exam_resource", "exam_resource_limit", nil)
	}
	var lease struct {
		EntryID      string       `db:"file_entry_id"`
		Purpose      string       `db:"purpose"`
		Availability string       `db:"availability"`
		CreatedBy    string       `db:"created_by_user_id"`
		ExpiresAt    time.Time    `db:"expires_at"`
		ConsumedAt   sql.NullTime `db:"consumed_at"`
		DatabaseNow  time.Time    `db:"database_now"`
	}
	if err := tx.Get(ctx, &lease, `SELECT v.file_entry_id,e.purpose,v.availability,l.created_by_user_id,l.expires_at,l.consumed_at,CURRENT_TIMESTAMP AS database_now FROM upload_leases l JOIN file_revisions v ON v.id=l.file_revision_id JOIN file_entries e ON e.id=v.file_entry_id WHERE l.id=? AND v.id=? AND e.archived_at IS NULL AND v.purge_claim_id IS NULL FOR UPDATE OF l,v,e`, input.LeaseID.String(), input.Rendition.RevisionID.String()); err != nil {
		return nil, translateError("upload_lease", input.LeaseID.String(), err)
	}
	if lease.EntryID != input.Resource.FileEntryID.String() || lease.Purpose != string(model.FilePurposeExamResource) || lease.Availability != string(model.FileAvailabilityPending) || lease.CreatedBy != input.ActorUserID.String() || lease.ConsumedAt.Valid || !lease.DatabaseNow.Before(lease.ExpiresAt) {
		return nil, store.NewErrConflict("exam_resource", "exam_resource_upload", nil)
	}
	var existing string
	err = tx.Get(ctx, &existing, `SELECT file_entry_id FROM exam_resources WHERE exam_id=? AND id=? AND archived_at IS NULL FOR UPDATE`, input.ExamID.String(), input.Resource.ID.String())
	creating := errors.Is(err, sql.ErrNoRows)
	if err != nil && !creating {
		return nil, translateError("exam_resource", input.Resource.ID.String(), err)
	}
	var count int
	if err = tx.Get(ctx, &count, `SELECT COUNT(*) FROM exam_resources WHERE exam_id=? AND archived_at IS NULL`, input.ExamID.String()); err != nil {
		return nil, err
	}
	if count > capacity.ResourceMaximumCount || creating && count >= capacity.ResourceMaximumCount {
		return nil, store.NewErrConflict("exam_resource", "exam_resource_limit", nil)
	}
	if creating {
		if input.Resource.Position != count {
			return nil, store.NewErrConflict("exam_resource", "exam_resource_limit", nil)
		}
	} else if existing != input.Resource.FileEntryID.String() {
		return nil, store.NewErrConflict("exam_resource", "exam_resource_changed", nil)
	}
	r := input.Rendition
	if _, err = tx.Exec(ctx, `INSERT INTO file_renditions (id,file_revision_id,created_at,name,media_type,size_bytes,width,height,sha256) VALUES (?,?,?,?,?,?,?,?,?)`, r.ID.String(), r.RevisionID.String(), r.CreatedAt, r.Name, r.MediaType, r.Size, r.Width, r.Height, r.SHA256); err != nil {
		return nil, fmt.Errorf("persist exam resource rendition: %w", translateError("file_rendition", r.ID.String(), err))
	}
	result, err := tx.Exec(ctx, `UPDATE file_revisions SET availability='available' WHERE id=? AND availability='pending'`, r.RevisionID.String())
	if err != nil {
		return nil, err
	}
	if err = requireExamResourceAffected(result, 1, "file_revision_changed"); err != nil {
		return nil, err
	}
	result, err = tx.Exec(ctx, `UPDATE upload_leases SET consumed_at=?,updated_at=?,revision=revision+1,bytes_received=? WHERE id=? AND consumed_at IS NULL`, input.ChangedAt, input.ChangedAt, r.Size, input.LeaseID.String())
	if err != nil {
		return nil, err
	}
	if err = requireExamResourceAffected(result, 1, "upload_lease_changed"); err != nil {
		return nil, err
	}
	result, err = tx.Exec(ctx, `UPDATE file_entries SET current_revision_id=?,updated_at=?,revision=revision+1 WHERE id=?`, r.RevisionID.String(), input.ChangedAt, input.Resource.FileEntryID.String())
	if err != nil {
		return nil, err
	}
	if err = requireExamResourceAffected(result, 1, "file_entry_changed"); err != nil {
		return nil, err
	}
	if creating {
		if _, err = tx.Exec(ctx, `INSERT INTO exam_resource_identities (id,exam_id,file_entry_id) VALUES (?,?,?)`, input.Resource.ID.String(), input.ExamID.String(), input.Resource.FileEntryID.String()); err != nil {
			return nil, fmt.Errorf("create exam resource identity: %w", translateError("exam_resource", input.Resource.ID.String(), err))
		}
		if _, err = tx.Exec(ctx, `INSERT INTO exam_resources (id,exam_id,file_entry_id,selected_file_revision_id,display_name,description_markdown,position,created_at,updated_at,archived_at) VALUES (?,?,?,?,?,?,?,?,?,NULL)`, input.Resource.ID.String(), input.ExamID.String(), input.Resource.FileEntryID.String(), r.RevisionID.String(), input.Resource.DisplayName, input.Resource.DescriptionMarkdown, input.Resource.Position, input.Resource.CreatedAt, input.ChangedAt); err != nil {
			return nil, fmt.Errorf("attach exam resource: %w", translateError("exam_resource", input.Resource.ID.String(), err))
		}
	} else {
		result, err = tx.Exec(ctx, `UPDATE exam_resources SET selected_file_revision_id=?,updated_at=? WHERE exam_id=? AND id=? AND archived_at IS NULL`, r.RevisionID.String(), input.ChangedAt, input.ExamID.String(), input.Resource.ID.String())
		if err != nil {
			return nil, err
		}
		if err = requireExamResourceAffected(result, 1, "exam_resource_changed"); err != nil {
			return nil, err
		}
	}
	newDraftRevision := input.ExpectedDraftRevision + 1
	result, err = tx.Exec(ctx, `UPDATE exam_drafts SET updated_at=GREATEST(updated_at,?),revision=? WHERE exam_id=? AND revision=?`, input.ChangedAt, newDraftRevision, input.ExamID.String(), input.ExpectedDraftRevision)
	if err != nil {
		return nil, err
	}
	if err = requireExamResourceAffected(result, 1, "exam_draft_revision"); err != nil {
		return nil, err
	}
	data, _ := model.EncodeAuditData(map[string]any{"exam_id": input.ExamID.String(), "exam_resource_id": input.Resource.ID.String(), "file_revision_id": r.RevisionID.String(), "draft_revision": newDraftRevision})
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
		return nil, err
	}
	return getExamResourceRecord(ctx, tx, input.ExamID, input.Resource.ID)
}

func getExamResourceRecord(ctx context.Context, executor sqlxExecutor, examID model.ExamID, resourceID model.ExamResourceID) (*store.ExamResourceRecord, error) {
	var row examResourceRow
	if err := executor.Get(ctx, &row, examResourceSelect+` WHERE r.exam_id=? AND r.id=? AND r.archived_at IS NULL`, examID.String(), resourceID.String()); err != nil {
		return nil, translateError("exam_resource", resourceID.String(), err)
	}
	return row.record()
}

func examResourceRecordRow(value *store.ExamResourceRecord) (examResourceRow, error) {
	if value == nil || value.Resource == nil || value.Rendition == nil {
		return examResourceRow{}, store.NewErrInvalidInput("exam_resource", "outcome", nil)
	}
	r, x := value.Resource, value.Rendition
	return examResourceRow{ID: r.ID.String(), ExamID: r.ExamID.String(), FileEntryID: r.FileEntryID.String(), SelectedRevisionID: r.SelectedFileRevisionID.String(), DisplayName: r.DisplayName, DescriptionMarkdown: r.DescriptionMarkdown, Position: r.Position, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ArchivedAt: NullTimeFromOptional(r.ArchivedAt), DraftRevision: value.DraftRevision, RenditionID: x.ID.String(), RenditionRevisionID: x.RevisionID.String(), RenditionCreatedAt: x.CreatedAt, RenditionName: x.Name, MediaType: x.MediaType, Size: x.Size, Width: x.Width, Height: x.Height, SHA256: x.SHA256}, nil
}
func decodeExamResourceOutcome(version int, data []byte) (*store.ExamResourceRecord, error) {
	if version != 1 {
		return nil, fmt.Errorf("unsupported exam resource outcome version %d", version)
	}
	var row examResourceRow
	if err := decodeCommandOutcome(data, &row); err != nil {
		return nil, err
	}
	return row.record()
}
func completeExamResourceReplay(ctx context.Context, tx *sqlxTxWrapper, auditID string, auditAt int64, value *store.ExamResourceRecord, original string) error {
	data, err := model.EncodeAuditData(map[string]any{"exam_id": value.Resource.ExamID.String(), "exam_resource_id": value.Resource.ID.String(), "draft_revision": value.DraftRevision, "idempotency_replayed": true, "original_audit_event_id": original})
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", data, auditAt)
	return err
}

func (s SQLExamResourceStore) UpdateMetadata(ctx context.Context, input *store.ExamResourceMetadataUpdate, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	if input == nil || command == nil || !input.ExamID.IsValid() || !input.ActorUserID.IsValid() || !input.ResourceID.IsValid() || input.ExpectedDraftRevision < 1 || input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_resource", "metadata_update", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "exam resource metadata update", idempotentMutation[*store.ExamResourceRecord]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamResourceRecord, error) {
			if err := lockExamResourceDraft(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, input.ExpectedDraftRevision); err != nil {
				return nil, err
			}
			current, err := getExamResourceRecord(ctx, tx, input.ExamID, input.ResourceID)
			if err != nil {
				return nil, err
			}
			candidate := *current.Resource
			changed, err := candidate.ApplyMetadata(input.DisplayName, input.DescriptionMarkdown, input.ChangedAt)
			if err != nil {
				return nil, store.NewErrInvalidInput("exam_resource", "metadata", nil).Wrap(err)
			}
			if !changed {
				return nil, store.NewErrConflict("exam_resource", "exam_resource_no_changes", nil)
			}
			result, err := tx.Exec(ctx, `UPDATE exam_resources SET display_name=?,description_markdown=?,updated_at=? WHERE exam_id=? AND id=? AND archived_at IS NULL`, candidate.DisplayName, candidate.DescriptionMarkdown, candidate.UpdatedAt, input.ExamID.String(), input.ResourceID.String())
			if err != nil {
				return nil, err
			}
			if err = requireExamResourceAffected(result, 1, "exam_resource_changed"); err != nil {
				return nil, err
			}
			if err = advanceExamResourceDraft(ctx, tx, input.ExamID, input.ExpectedDraftRevision, input.ChangedAt); err != nil {
				return nil, err
			}
			data, _ := model.EncodeAuditData(map[string]any{"exam_id": input.ExamID.String(), "exam_resource_id": input.ResourceID.String(), "draft_revision": input.ExpectedDraftRevision + 1})
			if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
				return nil, err
			}
			return getExamResourceRecord(ctx, tx, input.ExamID, input.ResourceID)
		}, encode: func(value *store.ExamResourceRecord) ([]byte, error) {
			row, err := examResourceRecordRow(value)
			if err != nil {
				return nil, err
			}
			return encodeCommandOutcome(row)
		}, decode: decodeExamResourceOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.ExamResourceRecord, original string) error {
			return completeExamResourceReplay(ctx, tx, input.AuditEventID, input.AuditAt, value, original)
		}})
	if err != nil {
		return nil, err
	}
	return &store.ExamResourceCommandResult{Value: result.Value, Replayed: result.Replayed}, nil
}

func (s SQLExamResourceStore) Reorder(ctx context.Context, input *store.ExamResourceReorder, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	if input == nil || command == nil || !input.ExamID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedDraftRevision < 1 || len(input.ResourceIDs) > model.ExamResourceMaximumCount || input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_resource", "reorder", nil)
	}
	seen := map[model.ExamResourceID]bool{}
	for _, id := range input.ResourceIDs {
		if !id.IsValid() || seen[id] {
			return nil, store.NewErrInvalidInput("exam_resource", "resource_ids", nil)
		}
		seen[id] = true
	}
	type reorderOutcome struct {
		Rows          []examResourceRow `json:"rows"`
		DraftRevision int64             `json:"draft_revision"`
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "exam resource reorder", idempotentMutation[reorderOutcome]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (reorderOutcome, error) {
			if err := lockExamResourceDraft(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, input.ExpectedDraftRevision); err != nil {
				return reorderOutcome{}, err
			}
			var current []struct {
				ID       string `db:"id"`
				Position int    `db:"position"`
			}
			if err := tx.Select(ctx, &current, `SELECT id,position FROM exam_resources WHERE exam_id=? AND archived_at IS NULL ORDER BY position FOR UPDATE`, input.ExamID.String()); err != nil {
				return reorderOutcome{}, err
			}
			if len(current) != len(input.ResourceIDs) {
				return reorderOutcome{}, store.NewErrConflict("exam_resource", "exam_resource_order", nil)
			}
			changed := false
			for i, row := range current {
				if !seen[model.ExamResourceID(row.ID)] {
					return reorderOutcome{}, store.NewErrConflict("exam_resource", "exam_resource_order", nil)
				}
				if row.ID != input.ResourceIDs[i].String() {
					changed = true
				}
			}
			if !changed {
				return reorderOutcome{}, store.NewErrConflict("exam_resource", "exam_resource_no_changes", nil)
			}
			result, err := tx.Exec(ctx, `UPDATE exam_resources SET position=position+100 WHERE exam_id=? AND archived_at IS NULL`, input.ExamID.String())
			if err != nil {
				return reorderOutcome{}, err
			}
			if err = requireExamResourceAffected(result, int64(len(current)), "exam_resource_order"); err != nil {
				return reorderOutcome{}, err
			}
			for position, id := range input.ResourceIDs {
				result, err = tx.Exec(ctx, `UPDATE exam_resources SET position=?,updated_at=? WHERE exam_id=? AND id=? AND archived_at IS NULL`, position, input.ChangedAt, input.ExamID.String(), id.String())
				if err != nil {
					return reorderOutcome{}, err
				}
				if err = requireExamResourceAffected(result, 1, "exam_resource_order"); err != nil {
					return reorderOutcome{}, err
				}
			}
			if err := advanceExamResourceDraft(ctx, tx, input.ExamID, input.ExpectedDraftRevision, input.ChangedAt); err != nil {
				return reorderOutcome{}, err
			}
			data, _ := model.EncodeAuditData(map[string]any{"exam_id": input.ExamID.String(), "resource_count": len(input.ResourceIDs), "draft_revision": input.ExpectedDraftRevision + 1})
			if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
				return reorderOutcome{}, err
			}
			items, err := listExamResourceRecords(ctx, tx, input.ExamID)
			if err != nil {
				return reorderOutcome{}, err
			}
			rows := make([]examResourceRow, 0, len(items))
			for i := range items {
				row, rowErr := examResourceRecordRow(&items[i])
				if rowErr != nil {
					return reorderOutcome{}, rowErr
				}
				rows = append(rows, row)
			}
			return reorderOutcome{Rows: rows, DraftRevision: input.ExpectedDraftRevision + 1}, nil
		}, encode: func(v reorderOutcome) ([]byte, error) { return encodeCommandOutcome(v) }, decode: func(version int, data []byte) (reorderOutcome, error) {
			var v reorderOutcome
			if version != 1 {
				return v, fmt.Errorf("unsupported reorder outcome version %d", version)
			}
			return v, decodeCommandOutcome(data, &v)
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, v reorderOutcome, original string) error {
			data, _ := model.EncodeAuditData(map[string]any{"exam_id": input.ExamID.String(), "resource_count": len(v.Rows), "idempotency_replayed": true, "original_audit_event_id": original})
			_, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt)
			return err
		}})
	if err != nil {
		return nil, err
	}
	items := make([]store.ExamResourceRecord, 0, len(result.Value.Rows))
	for _, row := range result.Value.Rows {
		record, recordErr := row.record()
		if recordErr != nil {
			return nil, recordErr
		}
		items = append(items, *record)
	}
	return &store.ExamResourceCommandResult{Items: items, DraftRevision: result.Value.DraftRevision, Replayed: result.Replayed}, nil
}

func (s SQLExamResourceStore) Remove(ctx context.Context, input *store.ExamResourceRemoval, command *store.CommandIdempotency) (*store.ExamResourceCommandResult, error) {
	if input == nil || command == nil || !input.ExamID.IsValid() || !input.ActorUserID.IsValid() || !input.ResourceID.IsValid() || input.ExpectedDraftRevision < 1 || input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_resource", "removal", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "exam resource removal", idempotentMutation[*store.ExamResourceRecord]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamResourceRecord, error) {
			if err := lockExamResourceDraft(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, input.ExpectedDraftRevision); err != nil {
				return nil, err
			}
			current, err := getExamResourceRecord(ctx, tx, input.ExamID, input.ResourceID)
			if err != nil {
				return nil, err
			}
			removed := *current.Resource
			if err = removed.Archive(input.ChangedAt); err != nil {
				return nil, err
			}
			result, err := tx.Exec(ctx, `UPDATE exam_resources SET archived_at=?,updated_at=? WHERE exam_id=? AND id=? AND archived_at IS NULL`, removed.ArchivedAt.Time, removed.UpdatedAt, input.ExamID.String(), input.ResourceID.String())
			if err != nil {
				return nil, err
			}
			if err = requireExamResourceAffected(result, 1, "exam_resource_changed"); err != nil {
				return nil, err
			}
			result, err = tx.Exec(ctx, `UPDATE exam_resources SET position=position+100 WHERE exam_id=? AND archived_at IS NULL AND position>?`, input.ExamID.String(), current.Resource.Position)
			if err != nil {
				return nil, err
			}
			shifted, err := result.RowsAffected()
			if err != nil {
				return nil, fmt.Errorf("read exam resource affected rows: %w", err)
			}
			result, err = tx.Exec(ctx, `UPDATE exam_resources SET position=position-101,updated_at=? WHERE exam_id=? AND archived_at IS NULL AND position>?`, input.ChangedAt, input.ExamID.String(), 100+current.Resource.Position)
			if err != nil {
				return nil, err
			}
			if err = requireExamResourceAffected(result, shifted, "exam_resource_order"); err != nil {
				return nil, err
			}
			if err = advanceExamResourceDraft(ctx, tx, input.ExamID, input.ExpectedDraftRevision, input.ChangedAt); err != nil {
				return nil, err
			}
			data, _ := model.EncodeAuditData(map[string]any{"exam_id": input.ExamID.String(), "exam_resource_id": input.ResourceID.String(), "draft_revision": input.ExpectedDraftRevision + 1})
			if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
				return nil, err
			}
			return &store.ExamResourceRecord{Resource: &removed, Rendition: current.Rendition, DraftRevision: input.ExpectedDraftRevision + 1}, nil
		}, encode: func(value *store.ExamResourceRecord) ([]byte, error) {
			row, err := examResourceRecordRow(value)
			if err != nil {
				return nil, err
			}
			return encodeCommandOutcome(row)
		}, decode: decodeExamResourceOutcome,
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.ExamResourceRecord, original string) error {
			return completeExamResourceReplay(ctx, tx, input.AuditEventID, input.AuditAt, value, original)
		}})
	if err != nil {
		return nil, err
	}
	return &store.ExamResourceCommandResult{Value: result.Value, Replayed: result.Replayed}, nil
}

func advanceExamResourceDraft(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, expected int64, at time.Time) error {
	result, err := tx.Exec(ctx, `UPDATE exam_drafts SET updated_at=GREATEST(updated_at,?),revision=revision+1 WHERE exam_id=? AND revision=?`, at, examID.String(), expected)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return store.NewErrConflict("exam_draft", "exam_draft_revision", nil)
	}
	return nil
}

func requireExamResourceAffected(result sql.Result, expected int64, reason string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read exam resource affected rows: %w", err)
	}
	if affected != expected {
		return store.NewErrConflict("exam_resource", reason, nil)
	}
	return nil
}
func listExamResourceRecords(ctx context.Context, executor sqlxExecutor, examID model.ExamID) ([]store.ExamResourceRecord, error) {
	rows := []examResourceRow{}
	if err := executor.Select(ctx, &rows, examResourceSelect+` WHERE r.exam_id=? AND r.archived_at IS NULL ORDER BY r.position`, examID.String()); err != nil {
		return nil, err
	}
	items := make([]store.ExamResourceRecord, 0, len(rows))
	for _, row := range rows {
		record, err := row.record()
		if err != nil {
			return nil, err
		}
		items = append(items, *record)
	}
	return items, nil
}
