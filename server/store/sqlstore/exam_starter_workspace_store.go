// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// SQLExamStarterWorkspaceStore is the PostgreSQL adapter for one Exam Draft's
// bounded logical Starter Workspace.
type SQLExamStarterWorkspaceStore struct{ *SQLStore }

// NewSQLExamStarterWorkspaceStore constructs the adapter for registration by
// the root SQL Store. It performs no I/O and owns no lifecycle.
func NewSQLExamStarterWorkspaceStore(sqlStore *SQLStore) store.ExamStarterWorkspaceStore {
	return &SQLExamStarterWorkspaceStore{SQLStore: sqlStore}
}

type starterWorkspaceRow struct {
	EntryID         string         `db:"entry_id" json:"entry_id"`
	ExamID          string         `db:"exam_id" json:"exam_id"`
	Kind            string         `db:"kind" json:"kind"`
	Path            string         `db:"path" json:"path"`
	CurrentObjectID sql.NullString `db:"current_object_id" json:"current_object_id"`
	EntryCreatedAt  time.Time      `db:"entry_created_at" json:"entry_created_at"`
	EntryUpdatedAt  time.Time      `db:"entry_updated_at" json:"entry_updated_at"`
	EntryArchivedAt sql.NullTime   `db:"entry_archived_at" json:"entry_archived_at"`
	ObjectID        sql.NullString `db:"object_id" json:"object_id"`
	CreatedByUserID sql.NullString `db:"created_by_user_id" json:"created_by_user_id"`
	ObjectCreatedAt sql.NullTime   `db:"object_created_at" json:"object_created_at"`
	ObjectUpdatedAt sql.NullTime   `db:"object_updated_at" json:"object_updated_at"`
	ExpiresAt       sql.NullTime   `db:"expires_at" json:"expires_at"`
	State           sql.NullString `db:"state" json:"state"`
	ContentVersion  sql.NullString `db:"content_version" json:"content_version"`
	MediaType       sql.NullString `db:"media_type" json:"media_type"`
	SizeBytes       sql.NullInt64  `db:"size_bytes" json:"size_bytes"`
	SHA256          sql.NullString `db:"sha256" json:"sha256"`
	ReclaimAfter    sql.NullTime   `db:"reclaim_after" json:"reclaim_after"`
	ClaimToken      sql.NullString `db:"claim_token" json:"claim_token"`
	ClaimedAt       sql.NullTime   `db:"claimed_at" json:"claimed_at"`
}

type starterWorkspaceObjectRow struct {
	ID              string         `db:"id"`
	ExamID          string         `db:"exam_id"`
	CreatedByUserID string         `db:"created_by_user_id"`
	CreatedAt       time.Time      `db:"created_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
	ExpiresAt       time.Time      `db:"expires_at"`
	State           string         `db:"state"`
	ContentVersion  sql.NullString `db:"content_version"`
	MediaType       sql.NullString `db:"media_type"`
	SizeBytes       sql.NullInt64  `db:"size_bytes"`
	SHA256          sql.NullString `db:"sha256"`
	ReclaimAfter    sql.NullTime   `db:"reclaim_after"`
	ClaimToken      sql.NullString `db:"claim_token"`
	ClaimedAt       sql.NullTime   `db:"claimed_at"`
}

func (row starterWorkspaceObjectRow) model() (*model.StarterWorkspaceObject, error) {
	object := &model.StarterWorkspaceObject{ID: model.StarterWorkspaceObjectID(row.ID), ExamID: model.ExamID(row.ExamID),
		CreatedByUserID: model.UserID(row.CreatedByUserID), CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
		ExpiresAt: model.TimeUTC(row.ExpiresAt), State: model.StarterWorkspaceObjectState(row.State),
		ContentVersion: model.WorkspaceContentVersion(row.ContentVersion.String), MediaType: row.MediaType.String,
		SizeBytes: row.SizeBytes.Int64, SHA256: row.SHA256.String, ReclaimAfter: optionalTime(row.ReclaimAfter),
		ClaimToken: row.ClaimToken.String, ClaimedAt: optionalTime(row.ClaimedAt)}
	if err := object.Validate(); err != nil {
		return nil, fmt.Errorf("rehydrate Starter Workspace cleanup object: %w", err)
	}
	return object, nil
}

const starterWorkspaceSelect = `SELECT
	e.id AS entry_id, e.exam_id, e.kind, e.path, e.current_object_id,
	e.created_at AS entry_created_at, e.updated_at AS entry_updated_at, e.archived_at AS entry_archived_at,
	o.id AS object_id, o.created_by_user_id, o.created_at AS object_created_at,
	o.updated_at AS object_updated_at, o.expires_at, o.state, o.content_version,
	o.media_type, o.size_bytes, o.sha256, o.reclaim_after, o.claim_token, o.claimed_at
	FROM exam_starter_workspace_entries e
	LEFT JOIN exam_starter_workspace_objects o ON o.exam_id = e.exam_id AND o.id = e.current_object_id`

func (s SQLExamStarterWorkspaceStore) List(ctx context.Context, examID model.ExamID) ([]store.ExamStarterWorkspaceItem, error) {
	if !examID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_starter_workspace", "exam_id", nil)
	}
	var rows []starterWorkspaceRow
	if err := s.GetMaster().Select(ctx, &rows, starterWorkspaceSelect+`
		WHERE e.exam_id = ? AND e.archived_at IS NULL ORDER BY e.path LIMIT ?`, examID.String(), model.StarterWorkspaceMaximumEntries+1); err != nil {
		return nil, fmt.Errorf("list Starter Workspace: %w", err)
	}
	if len(rows) > model.StarterWorkspaceMaximumEntries {
		return nil, fmt.Errorf("Starter Workspace exceeds bounded entry limit")
	}
	items := make([]store.ExamStarterWorkspaceItem, 0, len(rows))
	for _, row := range rows {
		item, err := row.item()
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, nil
}

func (s SQLExamStarterWorkspaceStore) GetFile(ctx context.Context, examID model.ExamID, entryID model.StarterWorkspaceEntryID) (*store.ExamStarterWorkspaceItem, error) {
	if !examID.IsValid() || !entryID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_starter_workspace", "identity", nil)
	}
	var row starterWorkspaceRow
	if err := s.GetMaster().Get(ctx, &row, starterWorkspaceSelect+`
		WHERE e.exam_id = ? AND e.id = ? AND e.kind = 'file' AND e.archived_at IS NULL`, examID.String(), entryID.String()); err != nil {
		return nil, translateError("exam_starter_workspace_entry", entryID.String(), err)
	}
	return row.item()
}

func (s SQLExamStarterWorkspaceStore) ReserveObject(ctx context.Context, input *store.ExamStarterWorkspaceReservation) (*model.StarterWorkspaceObject, error) {
	if err := validateStarterWorkspaceReservation(input); err != nil {
		return nil, err
	}
	object := *input.Object
	_, err := s.GetMaster().Exec(ctx, `INSERT INTO exam_starter_workspace_objects
		(id, exam_id, created_by_user_id, created_at, updated_at, expires_at, state)
		VALUES (?, ?, ?, ?, ?, ?, 'staged')`, object.ID.String(), object.ExamID.String(), object.CreatedByUserID.String(),
		object.CreatedAt, object.UpdatedAt, object.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("reserve Starter Workspace object: %w", translateError("exam_starter_workspace_object", object.ID.String(), err))
	}
	return &object, nil
}

func (s SQLExamStarterWorkspaceStore) CreateDirectory(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.runMutation(ctx, "create Starter Workspace directory", input, command, createStarterWorkspaceDirectory)
}

func (s SQLExamStarterWorkspaceStore) CreateFile(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.runMutation(ctx, "create Starter Workspace file", input, command, createStarterWorkspaceFile)
}

func (s SQLExamStarterWorkspaceStore) MoveEntry(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.runMutation(ctx, "move Starter Workspace entry", input, command, moveStarterWorkspaceEntry)
}

func (s SQLExamStarterWorkspaceStore) ReplaceFile(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.runMutation(ctx, "replace Starter Workspace file", input, command, replaceStarterWorkspaceFile)
}

func (s SQLExamStarterWorkspaceStore) RemoveEntry(ctx context.Context, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamStarterWorkspaceMutationResult, error) {
	return s.runMutation(ctx, "remove Starter Workspace entry", input, command, removeStarterWorkspaceEntry)
}

type starterWorkspaceMutationFunc func(context.Context, *sqlxTxWrapper, *store.ExamStarterWorkspaceMutation) (*store.ExamStarterWorkspaceMutationResult, error)

func (s SQLExamStarterWorkspaceStore) runMutation(ctx context.Context, label string, input *store.ExamStarterWorkspaceMutation, command *store.CommandIdempotency, execute starterWorkspaceMutationFunc) (*store.ExamStarterWorkspaceMutationResult, error) {
	prepared, err := prepareStarterWorkspaceMutation(input)
	if err != nil {
		return nil, err
	}
	if command == nil {
		return nil, store.NewErrInvalidInput("exam_starter_workspace", "idempotency", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, label, idempotentMutation[*store.ExamStarterWorkspaceMutationResult]{
		command: command, auditEventID: prepared.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamStarterWorkspaceMutationResult, error) {
			return execute(ctx, tx, prepared)
		},
		encode: func(value *store.ExamStarterWorkspaceMutationResult) ([]byte, error) {
			return encodeCommandOutcome(value)
		},
		decode: func(version int, data []byte) (*store.ExamStarterWorkspaceMutationResult, error) {
			if version != 1 {
				return nil, fmt.Errorf("unsupported Starter Workspace outcome version %d", version)
			}
			var value store.ExamStarterWorkspaceMutationResult
			if err := decodeCommandOutcome(data, &value); err != nil {
				return nil, err
			}
			if value.DraftRevision < 1 || value.Entry == nil || value.Entry.Validate() != nil || value.Object != nil && value.Object.Validate() != nil {
				return nil, fmt.Errorf("invalid Starter Workspace command outcome")
			}
			return &value, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.ExamStarterWorkspaceMutationResult, originalAuditID string) error {
			encoded, err := model.EncodeAuditData(map[string]any{
				"exam_id": value.Entry.ExamID.String(), "entry_id": value.Entry.ID.String(), "draft_revision": value.DraftRevision,
				"idempotency_replayed": true, "original_audit_event_id": originalAuditID,
			})
			if err != nil {
				return err
			}
			_, err = completeAuditEvent(ctx, tx, prepared.AuditEventID, model.AuditStatusSuccess, "", encoded, prepared.AuditAt)
			return err
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func (s SQLExamStarterWorkspaceStore) MarkObjectReclaimable(ctx context.Context, objectID model.StarterWorkspaceObjectID, at time.Time) error {
	if !objectID.IsValid() || at.IsZero() {
		return store.NewErrInvalidInput("exam_starter_workspace_object", "reclaim", nil)
	}
	at = model.TimeUTC(at)
	result, err := s.GetMaster().Exec(ctx, `UPDATE exam_starter_workspace_objects
		SET state = 'reclaimable', updated_at = ?, reclaim_after = ?
		WHERE id = ? AND state = 'staged'`, at, at.Add(model.StarterWorkspaceReclaimSafetyWindow), objectID.String())
	if err != nil {
		return fmt.Errorf("mark Starter Workspace object reclaimable: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("inspect Starter Workspace object reclamation: %w", err)
	} else if affected == 0 {
		return store.NewErrConflict("exam_starter_workspace_object", "workspace_object_state", nil)
	}
	return nil
}

func (s SQLExamStarterWorkspaceStore) ClaimObjectsForCleanup(ctx context.Context, limit int, claimToken string) ([]model.StarterWorkspaceObject, error) {
	if limit < 1 || limit > 100 || strings.TrimSpace(claimToken) == "" || len(claimToken) > 128 {
		return nil, store.NewErrInvalidInput("exam_starter_workspace_object", "cleanup_claim", nil)
	}
	var rows []starterWorkspaceObjectRow
	err := s.GetMaster().Select(ctx, &rows, `WITH candidates AS (
		SELECT objects.id FROM exam_starter_workspace_objects objects
		WHERE ((state = 'staged' AND expires_at + (? * interval '1 millisecond') <= CURRENT_TIMESTAMP)
		   OR (state = 'reclaimable' AND reclaim_after <= CURRENT_TIMESTAMP)
		   OR (state = 'claimed' AND claimed_at + (? * interval '1 millisecond') <= CURRENT_TIMESTAMP))
		  AND NOT EXISTS (
			SELECT 1 FROM exam_revision_starter_workspace_entries pinned
			WHERE pinned.object_id = objects.id
		  )
		ORDER BY COALESCE(reclaim_after, expires_at), id
		FOR UPDATE SKIP LOCKED LIMIT ?
	) UPDATE exam_starter_workspace_objects AS objects
		SET state = 'claimed', updated_at = GREATEST(objects.updated_at, CURRENT_TIMESTAMP),
			reclaim_after = COALESCE(objects.reclaim_after, CURRENT_TIMESTAMP), claim_token = ?, claimed_at = CURRENT_TIMESTAMP
		FROM candidates WHERE objects.id = candidates.id
		RETURNING objects.id, objects.exam_id, objects.created_by_user_id, objects.created_at, objects.updated_at,
			objects.expires_at, objects.state, objects.content_version, objects.media_type, objects.size_bytes,
			objects.sha256, objects.reclaim_after, objects.claim_token, objects.claimed_at`,
		model.StarterWorkspaceReclaimSafetyWindow.Milliseconds(), model.StarterWorkspaceCleanupClaimLease.Milliseconds(), limit, claimToken)
	if err != nil {
		return nil, fmt.Errorf("claim Starter Workspace objects for cleanup: %w", err)
	}
	objects := make([]model.StarterWorkspaceObject, 0, len(rows))
	for _, row := range rows {
		object, err := row.model()
		if err != nil {
			return nil, err
		}
		objects = append(objects, *object)
	}
	return objects, nil
}

func (s SQLExamStarterWorkspaceStore) CompleteObjectCleanup(ctx context.Context, objectID model.StarterWorkspaceObjectID, claimToken string) error {
	if !objectID.IsValid() || strings.TrimSpace(claimToken) == "" || len(claimToken) > 128 {
		return store.NewErrInvalidInput("exam_starter_workspace_object", "cleanup_completion", nil)
	}
	_, err := runSQLTransaction(ctx, s.GetMaster().Begin, "complete Starter Workspace object cleanup", func(ctx context.Context, tx *sqlxTxWrapper) (struct{}, error) {
		if _, err := tx.Exec(ctx, `UPDATE exam_starter_workspace_entries SET current_object_id = NULL
			WHERE current_object_id = ? AND archived_at IS NOT NULL`, objectID.String()); err != nil {
			return struct{}{}, fmt.Errorf("clear archived Starter Workspace object reference: %w", err)
		}
		result, err := tx.Exec(ctx, `DELETE FROM exam_starter_workspace_objects objects
			WHERE id = ? AND state = 'claimed' AND claim_token = ?
			AND NOT EXISTS (
				SELECT 1 FROM exam_revision_starter_workspace_entries pinned
				WHERE pinned.object_id = objects.id
			)`, objectID.String(), claimToken)
		if err != nil {
			return struct{}{}, fmt.Errorf("complete Starter Workspace object cleanup: %w", translateError("exam_starter_workspace_object", objectID.String(), err))
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return struct{}{}, fmt.Errorf("inspect Starter Workspace object cleanup: %w", err)
		}
		if affected != 1 {
			var exists bool
			if err = tx.Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM exam_starter_workspace_objects WHERE id = ?)`, objectID.String()); err != nil {
				return struct{}{}, fmt.Errorf("inspect completed Starter Workspace object cleanup: %w", err)
			}
			if exists {
				return struct{}{}, store.NewErrConflict("exam_starter_workspace_object", "workspace_cleanup_claim", nil)
			}
		}
		return struct{}{}, nil
	})
	return err
}

func (s SQLExamStarterWorkspaceStore) ReleaseObjectCleanup(ctx context.Context, objectID model.StarterWorkspaceObjectID, claimToken string) error {
	if !objectID.IsValid() || strings.TrimSpace(claimToken) == "" || len(claimToken) > 128 {
		return store.NewErrInvalidInput("exam_starter_workspace_object", "cleanup_release", nil)
	}
	result, err := s.GetMaster().Exec(ctx, `UPDATE exam_starter_workspace_objects
		SET state = 'reclaimable',
			updated_at = GREATEST(updated_at, CURRENT_TIMESTAMP),
			claim_token = NULL, claimed_at = NULL
		WHERE id = ? AND state = 'claimed' AND claim_token = ?`, objectID.String(), claimToken)
	if err != nil {
		return fmt.Errorf("release Starter Workspace object cleanup: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("inspect Starter Workspace cleanup release: %w", err)
	} else if affected != 1 {
		return store.NewErrConflict("exam_starter_workspace_object", "workspace_cleanup_claim", nil)
	}
	return nil
}

func createStarterWorkspaceDirectory(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamStarterWorkspaceMutation) (*store.ExamStarterWorkspaceMutationResult, error) {
	if _, err := lockStarterWorkspaceDraft(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, input.ExpectedDraftRevision); err != nil {
		return nil, err
	}
	if err := ensureStarterWorkspaceCapacity(ctx, tx, input.ExamID, 1, 0); err != nil {
		return nil, err
	}
	if err := ensureStarterWorkspaceParent(ctx, tx, input.ExamID, input.Path); err != nil {
		return nil, err
	}
	entry, err := model.NewStarterWorkspaceDirectory(input.EntryID, input.ExamID, input.Path, model.TimeFromMillis(input.ChangedAt))
	if err != nil {
		return nil, store.NewErrInvalidInput("exam_starter_workspace_entry", "value", nil).Wrap(err)
	}
	if err := insertStarterWorkspaceEntry(ctx, tx, entry); err != nil {
		return nil, err
	}
	return completeStarterWorkspaceMutation(ctx, tx, input, entry, nil, "directory_created", "")
}

func createStarterWorkspaceFile(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamStarterWorkspaceMutation) (*store.ExamStarterWorkspaceMutationResult, error) {
	if _, err := lockStarterWorkspaceDraft(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, input.ExpectedDraftRevision); err != nil {
		return nil, err
	}
	if err := ensureStarterWorkspaceCapacity(ctx, tx, input.ExamID, 1, input.SizeBytes); err != nil {
		return nil, err
	}
	if err := ensureStarterWorkspaceParent(ctx, tx, input.ExamID, input.Path); err != nil {
		return nil, err
	}
	object, err := consumeStarterWorkspaceObject(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	entry, err := model.NewStarterWorkspaceFile(input.EntryID, input.ExamID, input.Path, input.ObjectID, model.TimeFromMillis(input.ChangedAt))
	if err != nil {
		return nil, store.NewErrInvalidInput("exam_starter_workspace_entry", "value", nil).Wrap(err)
	}
	if err := insertStarterWorkspaceEntry(ctx, tx, entry); err != nil {
		return nil, err
	}
	return completeStarterWorkspaceMutation(ctx, tx, input, entry, object, "file_created", "")
}

func moveStarterWorkspaceEntry(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamStarterWorkspaceMutation) (*store.ExamStarterWorkspaceMutationResult, error) {
	if _, err := lockStarterWorkspaceDraft(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, input.ExpectedDraftRevision); err != nil {
		return nil, err
	}
	entries, err := loadActiveStarterWorkspaceEntries(ctx, tx, input.ExamID, true)
	if err != nil {
		return nil, err
	}
	index := -1
	for i := range entries {
		if entries[i].ID == input.EntryID {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, store.NewErrNotFound("exam_starter_workspace_entry", input.EntryID.String())
	}
	entry := entries[index]
	if entry.Path == input.Path {
		return nil, store.NewErrConflict("exam_starter_workspace_entry", "workspace_no_changes", nil)
	}
	if entry.Kind == model.StarterWorkspaceEntryDirectory && strings.HasPrefix(input.Path, entry.Path+"/") {
		return nil, store.NewErrConflict("exam_starter_workspace_entry", "workspace_descendant_move", nil)
	}
	if err := ensureStarterWorkspaceParentFromEntries(entries, input.Path, entry.Path); err != nil {
		return nil, err
	}
	newPaths := make(map[model.StarterWorkspaceEntryID]string)
	occupied := make(map[string]model.StarterWorkspaceEntryID, len(entries))
	for _, candidate := range entries {
		occupied[candidate.Path] = candidate.ID
	}
	for _, candidate := range entries {
		if candidate.ID != entry.ID && !strings.HasPrefix(candidate.Path, entry.Path+"/") {
			continue
		}
		suffix := strings.TrimPrefix(candidate.Path, entry.Path)
		target := input.Path + suffix
		if _, err := model.NormalizeStarterWorkspacePath(target); err != nil {
			return nil, store.NewErrConflict("exam_starter_workspace_entry", "workspace_path_limit", err)
		}
		newPaths[candidate.ID] = target
	}
	for movingID, target := range newPaths {
		if occupiedID, exists := occupied[target]; exists {
			if _, moving := newPaths[occupiedID]; !moving || occupiedID != movingID {
				return nil, store.NewErrConflict("exam_starter_workspace_entry", "workspace_path_collision", nil)
			}
		}
	}
	at := model.TimeFromMillis(input.ChangedAt)
	for movingID, target := range newPaths {
		if _, err := tx.Exec(ctx, `UPDATE exam_starter_workspace_entries SET path = ?, updated_at = ? WHERE exam_id = ? AND id = ? AND archived_at IS NULL`,
			target, at, input.ExamID.String(), movingID.String()); err != nil {
			return nil, translateStarterWorkspaceWriteError("move Starter Workspace entry", err)
		}
	}
	entry.Path = input.Path
	entry.UpdatedAt = at
	var object *model.StarterWorkspaceObject
	if entry.Kind == model.StarterWorkspaceEntryFile {
		item, getErr := getStarterWorkspaceItem(ctx, tx, input.ExamID, entry.ID)
		if getErr != nil {
			return nil, getErr
		}
		object = item.Object
	}
	return completeStarterWorkspaceMutation(ctx, tx, input, &entry, object, "entry_moved", "")
}

func replaceStarterWorkspaceFile(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamStarterWorkspaceMutation) (*store.ExamStarterWorkspaceMutationResult, error) {
	if _, err := lockStarterWorkspaceDraft(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, input.ExpectedDraftRevision); err != nil {
		return nil, err
	}
	item, err := getStarterWorkspaceItemForUpdate(ctx, tx, input.ExamID, input.EntryID)
	if err != nil {
		return nil, err
	}
	if item.Entry.Kind != model.StarterWorkspaceEntryFile || item.Object == nil {
		return nil, store.NewErrConflict("exam_starter_workspace_entry", "workspace_entry_kind", nil)
	}
	if !input.ExpectedContentVersion.IsValid() || input.ExpectedContentVersion != item.Object.ContentVersion {
		return nil, store.NewErrConflict("exam_starter_workspace_entry", "workspace_content_version", nil)
	}
	if err := ensureStarterWorkspaceCapacity(ctx, tx, input.ExamID, 0, input.SizeBytes-item.Object.SizeBytes); err != nil {
		return nil, err
	}
	object, err := consumeStarterWorkspaceObject(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	at := model.TimeFromMillis(input.ChangedAt)
	reclaimAt := at.Add(model.StarterWorkspaceReclaimSafetyWindow)
	if _, err = tx.Exec(ctx, `UPDATE exam_starter_workspace_objects SET state = 'reclaimable', updated_at = ?, reclaim_after = ? WHERE id = ? AND state = 'current'`,
		at, reclaimAt, item.Object.ID.String()); err != nil {
		return nil, fmt.Errorf("retire prior Starter Workspace object: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_starter_workspace_entries SET current_object_id = ?, updated_at = ? WHERE exam_id = ? AND id = ? AND archived_at IS NULL`,
		object.ID.String(), at, input.ExamID.String(), input.EntryID.String()); err != nil {
		return nil, translateStarterWorkspaceWriteError("replace Starter Workspace file", err)
	}
	item.Entry.CurrentObjectID = object.ID
	item.Entry.UpdatedAt = at
	return completeStarterWorkspaceMutation(ctx, tx, input, &item.Entry, object, "file_replaced", item.Object.ID)
}

func removeStarterWorkspaceEntry(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamStarterWorkspaceMutation) (*store.ExamStarterWorkspaceMutationResult, error) {
	if _, err := lockStarterWorkspaceDraft(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, input.ExpectedDraftRevision); err != nil {
		return nil, err
	}
	item, err := getStarterWorkspaceItemForUpdate(ctx, tx, input.ExamID, input.EntryID)
	if err != nil {
		return nil, err
	}
	if item.Entry.Kind == model.StarterWorkspaceEntryDirectory {
		var descendants int
		if err = tx.Get(ctx, &descendants, `SELECT count(*) FROM exam_starter_workspace_entries WHERE exam_id = ? AND archived_at IS NULL AND path LIKE ? ESCAPE '!'`,
			input.ExamID.String(), escapeLike(item.Entry.Path)+"/%"); err != nil {
			return nil, fmt.Errorf("count Starter Workspace descendants: %w", err)
		}
		if descendants != 0 {
			return nil, store.NewErrConflict("exam_starter_workspace_entry", "workspace_directory_not_empty", nil)
		}
	}
	at := model.TimeFromMillis(input.ChangedAt)
	if _, err = tx.Exec(ctx, `UPDATE exam_starter_workspace_entries SET archived_at = ?, updated_at = ? WHERE exam_id = ? AND id = ? AND archived_at IS NULL`,
		at, at, input.ExamID.String(), input.EntryID.String()); err != nil {
		return nil, fmt.Errorf("remove Starter Workspace entry: %w", err)
	}
	reclaimID := model.StarterWorkspaceObjectID("")
	if item.Object != nil {
		reclaimID = item.Object.ID
		if _, err = tx.Exec(ctx, `UPDATE exam_starter_workspace_objects SET state = 'reclaimable', updated_at = ?, reclaim_after = ? WHERE id = ? AND state = 'current'`,
			at, at.Add(model.StarterWorkspaceReclaimSafetyWindow), reclaimID.String()); err != nil {
			return nil, fmt.Errorf("retire removed Starter Workspace object: %w", err)
		}
	}
	item.Entry.ArchivedAt = model.OptionalTimeFrom(at)
	item.Entry.UpdatedAt = at
	return completeStarterWorkspaceMutation(ctx, tx, input, &item.Entry, item.Object, "entry_removed", reclaimID)
}

type starterWorkspaceDraftLock struct {
	Revision int64 `db:"revision"`
}

func lockStarterWorkspaceDraft(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, actorID model.UserID, override bool, expectedRevision int64) (*starterWorkspaceDraftLock, error) {
	var row struct {
		Revision       int64        `db:"revision"`
		ArchivedAt     sql.NullTime `db:"archived_at"`
		ActorIsManager bool         `db:"actor_is_manager"`
	}
	err := tx.Get(ctx, &row, `SELECT d.revision, e.archived_at,
		EXISTS (SELECT 1 FROM exam_managers m WHERE m.exam_id = e.id AND m.user_id = ?) AS actor_is_manager
		FROM exams e JOIN exam_drafts d ON d.exam_id = e.id WHERE e.id = ? FOR UPDATE OF e, d`, actorID.String(), examID.String())
	if err != nil {
		return nil, translateError("exam", examID.String(), err)
	}
	if !row.ActorIsManager && !override {
		return nil, store.NewErrNotFound("exam_manager", actorID.String())
	}
	if row.ArchivedAt.Valid {
		return nil, store.NewErrConflict("exam", "exam_archived", nil)
	}
	if row.Revision != expectedRevision {
		return nil, store.NewErrConflict("exam_draft", "exam_draft_revision", nil)
	}
	return &starterWorkspaceDraftLock{Revision: row.Revision}, nil
}

func ensureStarterWorkspaceCapacity(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, entryDelta int, byteDelta int64) error {
	var usage struct {
		Entries int   `db:"entries"`
		Bytes   int64 `db:"bytes"`
	}
	if err := tx.Get(ctx, &usage, `SELECT count(*) AS entries, COALESCE(sum(o.size_bytes), 0) AS bytes
		FROM exam_starter_workspace_entries e LEFT JOIN exam_starter_workspace_objects o ON o.id = e.current_object_id
		WHERE e.exam_id = ? AND e.archived_at IS NULL`, examID.String()); err != nil {
		return fmt.Errorf("read Starter Workspace usage: %w", err)
	}
	if usage.Entries+entryDelta > model.StarterWorkspaceMaximumEntries {
		return store.NewErrConflict("exam_starter_workspace", "workspace_entry_limit", nil)
	}
	if usage.Bytes+byteDelta > model.StarterWorkspaceMaximumTotalBytes {
		return store.NewErrConflict("exam_starter_workspace", "workspace_total_size", nil)
	}
	return nil
}

func ensureStarterWorkspaceParent(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, path string) error {
	parent := starterWorkspaceParent(path)
	if parent == "" {
		return nil
	}
	var exists bool
	if err := tx.Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM exam_starter_workspace_entries WHERE exam_id = ? AND path = ? AND kind = 'directory' AND archived_at IS NULL)`, examID.String(), parent); err != nil {
		return fmt.Errorf("find Starter Workspace parent: %w", err)
	}
	if !exists {
		return store.NewErrConflict("exam_starter_workspace_entry", "workspace_parent_missing", nil)
	}
	return nil
}

func ensureStarterWorkspaceParentFromEntries(entries []model.StarterWorkspaceEntry, path, movedRoot string) error {
	parent := starterWorkspaceParent(path)
	if parent == "" {
		return nil
	}
	for _, entry := range entries {
		if entry.Path == parent && entry.Kind == model.StarterWorkspaceEntryDirectory && entry.Path != movedRoot && !strings.HasPrefix(entry.Path, movedRoot+"/") {
			return nil
		}
	}
	return store.NewErrConflict("exam_starter_workspace_entry", "workspace_parent_missing", nil)
}

func consumeStarterWorkspaceObject(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamStarterWorkspaceMutation) (*model.StarterWorkspaceObject, error) {
	var row struct {
		ID              string    `db:"id"`
		ExamID          string    `db:"exam_id"`
		CreatedByUserID string    `db:"created_by_user_id"`
		CreatedAt       time.Time `db:"created_at"`
		UpdatedAt       time.Time `db:"updated_at"`
		ExpiresAt       time.Time `db:"expires_at"`
		State           string    `db:"state"`
	}
	if err := tx.Get(ctx, &row, `SELECT id, exam_id, created_by_user_id, created_at, updated_at, expires_at, state
		FROM exam_starter_workspace_objects WHERE id = ? AND exam_id = ? FOR UPDATE`, input.ObjectID.String(), input.ExamID.String()); err != nil {
		return nil, translateError("exam_starter_workspace_object", input.ObjectID.String(), err)
	}
	if row.State != string(model.StarterWorkspaceObjectStaged) {
		return nil, store.NewErrConflict("exam_starter_workspace_object", "workspace_object_state", nil)
	}
	var expired bool
	if err := tx.Get(ctx, &expired, `SELECT ? <= CURRENT_TIMESTAMP`, row.ExpiresAt); err != nil {
		return nil, fmt.Errorf("check Starter Workspace object expiry: %w", err)
	}
	if expired {
		return nil, store.NewErrConflict("exam_starter_workspace_object", "workspace_object_expired", nil)
	}
	object := &model.StarterWorkspaceObject{ID: model.StarterWorkspaceObjectID(row.ID), ExamID: model.ExamID(row.ExamID),
		CreatedByUserID: model.UserID(row.CreatedByUserID), CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
		ExpiresAt: model.TimeUTC(row.ExpiresAt), State: model.StarterWorkspaceObjectStaged}
	if err := object.MarkCurrent(input.ContentVersion, input.MediaType, input.SizeBytes, input.SHA256, model.TimeFromMillis(input.ChangedAt)); err != nil {
		return nil, store.NewErrInvalidInput("exam_starter_workspace_object", "content", nil).Wrap(err)
	}
	result, err := tx.Exec(ctx, `UPDATE exam_starter_workspace_objects SET state = 'current', updated_at = ?, content_version = ?, media_type = ?, size_bytes = ?, sha256 = ? WHERE id = ? AND state = 'staged'`,
		object.UpdatedAt, object.ContentVersion.String(), object.MediaType, object.SizeBytes, object.SHA256, object.ID.String())
	if err != nil {
		return nil, fmt.Errorf("publish Starter Workspace object: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, fmt.Errorf("inspect Starter Workspace object publication: %w", err)
		}
		return nil, store.NewErrConflict("exam_starter_workspace_object", "workspace_object_state", nil)
	}
	return object, nil
}

func insertStarterWorkspaceEntry(ctx context.Context, tx *sqlxTxWrapper, entry *model.StarterWorkspaceEntry) error {
	_, err := tx.Exec(ctx, `INSERT INTO exam_starter_workspace_entries (id, exam_id, kind, path, current_object_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?)`, entry.ID.String(), entry.ExamID.String(), string(entry.Kind), entry.Path,
		entry.CurrentObjectID.String(), entry.CreatedAt, entry.UpdatedAt)
	if err != nil {
		return translateStarterWorkspaceWriteError("create Starter Workspace entry", err)
	}
	return nil
}

func completeStarterWorkspaceMutation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamStarterWorkspaceMutation, entry *model.StarterWorkspaceEntry, object *model.StarterWorkspaceObject, operation string, reclaimable model.StarterWorkspaceObjectID) (*store.ExamStarterWorkspaceMutationResult, error) {
	var revision int64
	if err := tx.Get(ctx, &revision, `UPDATE exam_drafts SET revision = revision + 1, updated_at = ? WHERE exam_id = ? RETURNING revision`,
		model.TimeFromMillis(input.ChangedAt), input.ExamID.String()); err != nil {
		return nil, fmt.Errorf("advance Starter Workspace Draft revision: %w", err)
	}
	encoded, err := model.EncodeAuditData(map[string]any{
		"exam_id": input.ExamID.String(), "entry_id": entry.ID.String(), "operation": operation, "draft_revision": revision,
	})
	if err != nil {
		return nil, err
	}
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, err
	}
	return &store.ExamStarterWorkspaceMutationResult{Entry: entry, Object: object, DraftRevision: revision, ReclaimableObject: reclaimable}, nil
}

func loadActiveStarterWorkspaceEntries(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, lock bool) ([]model.StarterWorkspaceEntry, error) {
	query := `SELECT id, exam_id, kind, path, current_object_id, created_at, updated_at, archived_at FROM exam_starter_workspace_entries WHERE exam_id = ? AND archived_at IS NULL ORDER BY path`
	if lock {
		query += ` FOR UPDATE`
	}
	var rows []struct {
		ID              string         `db:"id"`
		ExamID          string         `db:"exam_id"`
		Kind            string         `db:"kind"`
		Path            string         `db:"path"`
		CurrentObjectID sql.NullString `db:"current_object_id"`
		CreatedAt       time.Time      `db:"created_at"`
		UpdatedAt       time.Time      `db:"updated_at"`
		ArchivedAt      sql.NullTime   `db:"archived_at"`
	}
	if err := tx.Select(ctx, &rows, query, examID.String()); err != nil {
		return nil, fmt.Errorf("load Starter Workspace hierarchy: %w", err)
	}
	if len(rows) > model.StarterWorkspaceMaximumEntries {
		return nil, fmt.Errorf("Starter Workspace exceeds bounded entry limit")
	}
	entries := make([]model.StarterWorkspaceEntry, 0, len(rows))
	for _, row := range rows {
		entry := model.StarterWorkspaceEntry{ID: model.StarterWorkspaceEntryID(row.ID), ExamID: model.ExamID(row.ExamID),
			Kind: model.StarterWorkspaceEntryKind(row.Kind), Path: row.Path, CurrentObjectID: model.StarterWorkspaceObjectID(row.CurrentObjectID.String),
			CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt), ArchivedAt: optionalTime(row.ArchivedAt)}
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("rehydrate Starter Workspace entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func getStarterWorkspaceItem(ctx context.Context, executor sqlxExecutor, examID model.ExamID, entryID model.StarterWorkspaceEntryID) (*store.ExamStarterWorkspaceItem, error) {
	var row starterWorkspaceRow
	if err := executor.Get(ctx, &row, starterWorkspaceSelect+` WHERE e.exam_id = ? AND e.id = ? AND e.archived_at IS NULL`, examID.String(), entryID.String()); err != nil {
		return nil, translateError("exam_starter_workspace_entry", entryID.String(), err)
	}
	return row.item()
}

func getStarterWorkspaceItemForUpdate(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, entryID model.StarterWorkspaceEntryID) (*store.ExamStarterWorkspaceItem, error) {
	var row starterWorkspaceRow
	if err := tx.Get(ctx, &row, starterWorkspaceSelect+` WHERE e.exam_id = ? AND e.id = ? AND e.archived_at IS NULL FOR UPDATE OF e`, examID.String(), entryID.String()); err != nil {
		return nil, translateError("exam_starter_workspace_entry", entryID.String(), err)
	}
	item, err := row.item()
	if err != nil {
		return nil, err
	}
	if item.Object != nil {
		var locked bool
		if err := tx.Get(ctx, &locked, `SELECT true FROM exam_starter_workspace_objects WHERE id = ? FOR UPDATE`, item.Object.ID.String()); err != nil {
			return nil, translateError("exam_starter_workspace_object", item.Object.ID.String(), err)
		}
	}
	return item, nil
}

func (row starterWorkspaceRow) item() (*store.ExamStarterWorkspaceItem, error) {
	entry := model.StarterWorkspaceEntry{ID: model.StarterWorkspaceEntryID(row.EntryID), ExamID: model.ExamID(row.ExamID),
		Kind: model.StarterWorkspaceEntryKind(row.Kind), Path: row.Path, CurrentObjectID: model.StarterWorkspaceObjectID(row.CurrentObjectID.String),
		CreatedAt: model.TimeUTC(row.EntryCreatedAt), UpdatedAt: model.TimeUTC(row.EntryUpdatedAt), ArchivedAt: optionalTime(row.EntryArchivedAt)}
	if err := entry.Validate(); err != nil {
		return nil, fmt.Errorf("rehydrate Starter Workspace entry: %w", err)
	}
	item := &store.ExamStarterWorkspaceItem{Entry: entry}
	if entry.Kind == model.StarterWorkspaceEntryDirectory {
		return item, nil
	}
	if !row.ObjectID.Valid || !row.CreatedByUserID.Valid || !row.ObjectCreatedAt.Valid || !row.ObjectUpdatedAt.Valid || !row.ExpiresAt.Valid ||
		!row.State.Valid || !row.ContentVersion.Valid || !row.MediaType.Valid || !row.SizeBytes.Valid || !row.SHA256.Valid {
		return nil, fmt.Errorf("Starter Workspace file has incomplete object metadata")
	}
	object := model.StarterWorkspaceObject{ID: model.StarterWorkspaceObjectID(row.ObjectID.String), ExamID: entry.ExamID,
		CreatedByUserID: model.UserID(row.CreatedByUserID.String), CreatedAt: model.TimeUTC(row.ObjectCreatedAt.Time),
		UpdatedAt: model.TimeUTC(row.ObjectUpdatedAt.Time), ExpiresAt: model.TimeUTC(row.ExpiresAt.Time), State: model.StarterWorkspaceObjectState(row.State.String),
		ContentVersion: model.WorkspaceContentVersion(row.ContentVersion.String), MediaType: row.MediaType.String, SizeBytes: row.SizeBytes.Int64,
		SHA256: row.SHA256.String, ReclaimAfter: optionalTime(row.ReclaimAfter), ClaimToken: row.ClaimToken.String, ClaimedAt: optionalTime(row.ClaimedAt)}
	if err := object.Validate(); err != nil {
		return nil, fmt.Errorf("rehydrate Starter Workspace object: %w", err)
	}
	if object.State != model.StarterWorkspaceObjectCurrent || object.ID != entry.CurrentObjectID {
		return nil, fmt.Errorf("Starter Workspace entry selects non-current object")
	}
	item.Object = &object
	return item, nil
}

func validateStarterWorkspaceReservation(input *store.ExamStarterWorkspaceReservation) error {
	if input == nil || input.Object == nil || input.Object.Validate() != nil || input.Object.State != model.StarterWorkspaceObjectStaged {
		return store.NewErrInvalidInput("exam_starter_workspace_object", "reservation", nil)
	}
	return nil
}

func prepareStarterWorkspaceMutation(input *store.ExamStarterWorkspaceMutation) (*store.ExamStarterWorkspaceMutation, error) {
	if input == nil || !input.ExamID.IsValid() || !input.ActorUserID.IsValid() || !input.EntryID.IsValid() ||
		input.ExpectedDraftRevision < 1 || input.ChangedAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_starter_workspace", "mutation", nil)
	}
	prepared := *input
	if !prepared.ExpectedContentVersion.IsZero() && !prepared.ExpectedContentVersion.IsValid() {
		return nil, store.NewErrInvalidInput("exam_starter_workspace_object", "expected_content_version", nil)
	}
	if prepared.Path != "" {
		normalized, err := model.NormalizeStarterWorkspacePath(prepared.Path)
		if err != nil || normalized != prepared.Path {
			return nil, store.NewErrInvalidInput("exam_starter_workspace_entry", "path", prepared.Path).Wrap(err)
		}
	}
	if prepared.ObjectID.IsZero() {
		if !prepared.ContentVersion.IsZero() || prepared.MediaType != "" || prepared.SizeBytes != 0 || prepared.SHA256 != "" {
			return nil, store.NewErrInvalidInput("exam_starter_workspace_object", "content", nil)
		}
	} else if !prepared.ObjectID.IsValid() || !prepared.ContentVersion.IsValid() || strings.TrimSpace(prepared.MediaType) == "" ||
		prepared.SizeBytes < 0 || prepared.SizeBytes > model.StarterWorkspaceMaximumFileBytes || len(prepared.SHA256) != 64 {
		return nil, store.NewErrInvalidInput("exam_starter_workspace_object", "content", nil)
	}
	return &prepared, nil
}

func translateStarterWorkspaceWriteError(operation string, err error) error {
	translated := translateError("exam_starter_workspace_entry", "", err)
	var conflict *store.ErrConflict
	if errors.As(translated, &conflict) && conflict.Constraint == "exam_starter_workspace_entries_active_path_key" {
		return store.NewErrConflict("exam_starter_workspace_entry", "workspace_path_collision", err)
	}
	return fmt.Errorf("%s: %w", operation, translated)
}

func starterWorkspaceParent(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index < 0 {
		return ""
	}
	return path[:index]
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `!`, `!!`)
	value = strings.ReplaceAll(value, `%`, `!%`)
	return strings.ReplaceAll(value, `_`, `!_`)
}

func optionalTime(value sql.NullTime) model.OptionalTime {
	if !value.Valid {
		return model.OptionalTime{}
	}
	return model.OptionalTimeFrom(value.Time)
}

var _ store.ExamStarterWorkspaceStore = (*SQLExamStarterWorkspaceStore)(nil)
