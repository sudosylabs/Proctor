// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLExamRevisionStore struct{ *SQLStore }

func newSQLExamRevisionStore(sqlStore *SQLStore) store.ExamRevisionStore {
	return &SQLExamRevisionStore{SQLStore: sqlStore}
}

type examRevisionHeaderRow struct {
	ID                         string         `db:"id"`
	ExamID                     string         `db:"exam_id"`
	Number                     int64          `db:"number"`
	SnapshotSchemaVersion      int            `db:"snapshot_schema_version"`
	SourceDraftRevision        int64          `db:"source_draft_revision"`
	Title                      string         `db:"title"`
	InstructionsMarkdown       string         `db:"instructions_markdown"`
	PolicySchemaVersion        int            `db:"policy_schema_version"`
	PolicyDocument             jsonValue      `db:"policy_document"`
	PolicyCanonical            []byte         `db:"policy_canonical"`
	PolicyDigest               string         `db:"policy_digest"`
	ExecutionProfileDocument   jsonValue      `db:"execution_profile_document"`
	ExecutionProfileCanonical  []byte         `db:"execution_profile_canonical"`
	ExecutionProfileDigest     string         `db:"execution_profile_digest"`
	ResourceMaximumCount       int            `db:"exam_resource_max_count"`
	ResourceMaximumBytes       int64          `db:"exam_resource_max_bytes"`
	WorkspaceMaximumEntries    int            `db:"exam_workspace_max_entries"`
	WorkspaceMaximumFileBytes  int64          `db:"exam_workspace_max_file_bytes"`
	WorkspaceMaximumTotalBytes int64          `db:"exam_workspace_max_total_bytes"`
	StarterWorkspaceDigest     string         `db:"starter_workspace_digest"`
	ContentDigest              string         `db:"content_digest"`
	ResourceCount              int            `db:"resource_count"`
	StarterEntryCount          int            `db:"starter_entry_count"`
	StarterTotalBytes          int64          `db:"starter_total_bytes"`
	PublishedByUserID          string         `db:"published_by_user_id"`
	PublishedAt                time.Time      `db:"published_at"`
	BaseRevisionID             sql.NullString `db:"base_revision_id"`
	PublicationKind            string         `db:"publication_kind"`
	Sealed                     bool           `db:"sealed"`
}

type examRevisionResourceRow struct {
	ResourceID          string `db:"resource_id"`
	FileEntryID         string `db:"file_entry_id"`
	FileRevisionID      string `db:"file_revision_id"`
	RenditionID         string `db:"rendition_id"`
	DisplayName         string `db:"display_name"`
	DescriptionMarkdown string `db:"description_markdown"`
	Position            int    `db:"position"`
	MediaType           string `db:"media_type"`
	SizeBytes           int64  `db:"size_bytes"`
	SHA256              string `db:"sha256"`
}

type examRevisionWorkspaceRow struct {
	EntryID        string         `db:"entry_id"`
	Kind           string         `db:"kind"`
	Path           string         `db:"path"`
	ObjectID       sql.NullString `db:"object_id"`
	ContentVersion sql.NullString `db:"content_version"`
	MediaType      sql.NullString `db:"media_type"`
	SizeBytes      sql.NullInt64  `db:"size_bytes"`
	SHA256         sql.NullString `db:"sha256"`
}

type examRevisionPublicationOutcomeRow struct {
	RevisionID    string `json:"revision_id"`
	ExamID        string `json:"exam_id"`
	Number        int64  `json:"number"`
	ExamRevision  int64  `json:"exam_revision"`
	DraftRevision int64  `json:"draft_revision"`
	PolicyDigest  string `json:"policy_digest"`
}

const examRevisionHeaderSelect = `SELECT id,exam_id,number,snapshot_schema_version,source_draft_revision,
	title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,
	execution_profile_document,execution_profile_canonical,execution_profile_digest,
	exam_resource_max_count,exam_resource_max_bytes,exam_workspace_max_entries,exam_workspace_max_file_bytes,exam_workspace_max_total_bytes,
	starter_workspace_digest,content_digest,resource_count,starter_entry_count,starter_total_bytes,
	published_by_user_id,published_at,base_revision_id,publication_kind,sealed FROM exam_revisions`

func (s SQLExamRevisionStore) Publish(ctx context.Context, input *store.ExamRevisionPublication, command *store.CommandIdempotency) (*store.ExamRevisionPublicationResult, error) {
	prepared, err := prepareExamRevisionPublication(input)
	if err != nil || command == nil {
		if err != nil {
			return nil, err
		}
		return nil, store.NewErrInvalidInput("exam_revision", "idempotency", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "exam revision publication", idempotentMutation[examRevisionPublicationOutcomeRow]{
		command: command, auditEventID: prepared.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (examRevisionPublicationOutcomeRow, error) {
			return publishExamRevision(ctx, tx, prepared)
		},
		encode: func(outcome examRevisionPublicationOutcomeRow) ([]byte, error) { return encodeCommandOutcome(outcome) },
		decode: func(version int, data []byte) (examRevisionPublicationOutcomeRow, error) {
			if version != 1 {
				return examRevisionPublicationOutcomeRow{}, fmt.Errorf("unsupported Exam Revision publication outcome version %d", version)
			}
			var outcome examRevisionPublicationOutcomeRow
			if err := decodeCommandOutcome(data, &outcome); err != nil {
				return examRevisionPublicationOutcomeRow{}, err
			}
			return outcome, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, outcome examRevisionPublicationOutcomeRow, originalAuditID string) error {
			data, err := model.EncodeAuditData(map[string]any{
				"exam_id": outcome.ExamID, "exam_revision_id": outcome.RevisionID, "number": outcome.Number,
				"policy_digest": outcome.PolicyDigest, "idempotency_replayed": true,
				"original_audit_event_id": originalAuditID,
			})
			if err != nil {
				return err
			}
			_, err = completeAuditEvent(ctx, tx, prepared.AuditEventID, model.AuditStatusSuccess, "", data, prepared.AuditAt)
			return err
		},
	})
	if err != nil {
		return nil, err
	}
	revisionID, err := model.ParseExamRevisionID(result.Value.RevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_revision", "outcome_revision_id", err)
	}
	summary, err := s.GetSummary(ctx, prepared.ExamID, revisionID)
	if err != nil {
		return nil, err
	}
	return &store.ExamRevisionPublicationResult{Revision: summary, ExamRevision: result.Value.ExamRevision, DraftRevision: result.Value.DraftRevision, Replayed: result.Replayed}, nil
}

func prepareExamRevisionPublication(input *store.ExamRevisionPublication) (*store.ExamRevisionPublication, error) {
	if input == nil || !input.RevisionID.IsValid() || !input.ExamID.IsValid() || !input.ActorUserID.IsValid() ||
		input.ExpectedDraftRevision < 1 || input.Kind != model.ExamRevisionPublicationStandard || input.PublishedAt.IsZero() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_revision", "publication", nil)
	}
	prepared := *input
	prepared.PublishedAt = model.TimeUTC(input.PublishedAt)
	return &prepared, nil
}

type examRevisionDraftRow struct {
	AcademicUnitID       string         `db:"academic_unit_id"`
	ArchivedAt           sql.NullTime   `db:"archived_at"`
	ExamRevision         int64          `db:"exam_revision"`
	ActorIsManager       bool           `db:"actor_is_manager"`
	Title                string         `db:"title"`
	InstructionsMarkdown string         `db:"instructions_markdown"`
	Policy               jsonValue      `db:"policy"`
	ExecutionProfile     jsonValue      `db:"execution_profile"`
	BaseRevisionID       sql.NullString `db:"base_revision_id"`
	DraftRevision        int64          `db:"draft_revision"`
}

func publishExamRevision(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamRevisionPublication) (examRevisionPublicationOutcomeRow, error) {
	var draft examRevisionDraftRow
	if err := tx.Get(ctx, &draft, `SELECT e.academic_unit_id,e.archived_at,e.revision AS exam_revision,
		EXISTS (SELECT 1 FROM exam_managers m WHERE m.exam_id=e.id AND m.user_id=?) AS actor_is_manager,
		d.title,d.instructions_markdown,d.policy,d.execution_profile,d.base_revision_id,d.revision AS draft_revision
		FROM exams e JOIN exam_drafts d ON d.exam_id=e.id WHERE e.id=? FOR UPDATE OF e,d`,
		input.ActorUserID.String(), input.ExamID.String()); err != nil {
		return examRevisionPublicationOutcomeRow{}, translateError("exam", input.ExamID.String(), err)
	}
	if !draft.ActorIsManager && !input.ManagerOverride {
		return examRevisionPublicationOutcomeRow{}, store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	if draft.ArchivedAt.Valid {
		return examRevisionPublicationOutcomeRow{}, store.NewErrConflict("exam", "exam_archived", nil)
	}
	if draft.DraftRevision != input.ExpectedDraftRevision {
		return examRevisionPublicationOutcomeRow{}, store.NewErrConflict("exam_draft", "exam_draft_revision", nil)
	}
	policy, err := model.CanonicalizeExamRevisionPolicy([]byte(draft.Policy))
	if err != nil {
		return examRevisionPublicationOutcomeRow{}, invalidPersistedState("exam_draft", "policy", err)
	}
	executionProfile, err := model.DecodeExecutionProfile([]byte(draft.ExecutionProfile))
	if err != nil {
		return examRevisionPublicationOutcomeRow{}, invalidPersistedState("exam_draft", "execution_profile", err)
	}
	capacity, err := currentExamCapacityPolicy(ctx, tx)
	if err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	resources, err := listExamResourceRecords(ctx, tx, input.ExamID)
	if err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	var activeResources, invalidPurpose int
	if err = tx.Get(ctx, &activeResources, `SELECT COUNT(*) FROM exam_resources WHERE exam_id=? AND archived_at IS NULL`, input.ExamID.String()); err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	if err = tx.Get(ctx, &invalidPurpose, `SELECT COUNT(*) FROM exam_resources r JOIN file_entries e ON e.id=r.file_entry_id WHERE r.exam_id=? AND r.archived_at IS NULL AND e.purpose <> 'exam_resource'`, input.ExamID.String()); err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	if activeResources != len(resources) || invalidPurpose != 0 {
		return examRevisionPublicationOutcomeRow{}, invalidPersistedState("exam_revision", "resources", errors.New("resource selection is incomplete"))
	}
	resourceSnapshots := make([]model.ExamRevisionResource, len(resources))
	for index, record := range resources {
		resourceSnapshots[index] = model.ExamRevisionResource{ResourceID: record.Resource.ID, FileEntryID: record.Resource.FileEntryID,
			FileRevisionID: record.Resource.SelectedFileRevisionID, RenditionID: record.Rendition.ID,
			DisplayName: record.Resource.DisplayName, DescriptionMarkdown: record.Resource.DescriptionMarkdown,
			Position: record.Resource.Position, MediaType: model.ExamResourceMediaType(record.Rendition.MediaType),
			SizeBytes: record.Rendition.Size, SHA256: record.Rendition.SHA256}
	}
	var workspaceRows []starterWorkspaceRow
	if err = tx.Select(ctx, &workspaceRows, starterWorkspaceSelect+` WHERE e.exam_id=? AND e.archived_at IS NULL LIMIT ?`, input.ExamID.String(), model.StarterWorkspaceMaximumEntries+1); err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	if len(workspaceRows) > model.StarterWorkspaceMaximumEntries {
		return examRevisionPublicationOutcomeRow{}, invalidPersistedState("exam_revision", "starter_workspace", errors.New("entry limit exceeded"))
	}
	workspaceSnapshots := make([]model.ExamRevisionStarterWorkspaceEntry, len(workspaceRows))
	for index, row := range workspaceRows {
		item, itemErr := row.item()
		if itemErr != nil {
			return examRevisionPublicationOutcomeRow{}, invalidPersistedState("exam_revision", "starter_workspace", itemErr)
		}
		snapshot := model.ExamRevisionStarterWorkspaceEntry{EntryID: item.Entry.ID, Kind: item.Entry.Kind, Path: item.Entry.Path}
		if item.Object != nil {
			snapshot.ObjectID, snapshot.ContentVersion, snapshot.MediaType = item.Object.ID, item.Object.ContentVersion, item.Object.MediaType
			snapshot.SizeBytes, snapshot.SHA256 = item.Object.SizeBytes, item.Object.SHA256
		}
		workspaceSnapshots[index] = snapshot
	}
	if examRevisionCapacityExceeded(capacity, resourceSnapshots, workspaceSnapshots) {
		return examRevisionPublicationOutcomeRow{}, store.NewErrConflict("exam_revision", "exam_revision_capacity", nil)
	}
	baseRevisionID, err := parseNullablePersistedID[model.ExamRevisionID]("exam_draft", "base_revision_id", draft.BaseRevisionID, model.ParseExamRevisionID)
	if err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	var number int64
	if err = tx.Get(ctx, &number, `SELECT COALESCE(MAX(number),0)+1 FROM exam_revisions WHERE exam_id=?`, input.ExamID.String()); err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	revision, err := model.NewExamRevision(model.ExamRevisionSpecification{ID: input.RevisionID, ExamID: input.ExamID,
		Number: number, SourceDraftRevision: draft.DraftRevision, Title: draft.Title, InstructionsMarkdown: draft.InstructionsMarkdown,
		Policy: policy, ExecutionProfile: executionProfile, Capacity: capacity, Resources: resourceSnapshots, StarterWorkspace: workspaceSnapshots,
		PublishedByUserID: input.ActorUserID, PublishedAt: input.PublishedAt, BaseRevisionID: baseRevisionID, Kind: input.Kind})
	if err != nil {
		return examRevisionPublicationOutcomeRow{}, invalidPersistedState("exam_revision", "snapshot", err)
	}
	if baseRevisionID.IsValid() {
		var baseDigest string
		if err = tx.Get(ctx, &baseDigest, `SELECT content_digest FROM exam_revisions WHERE exam_id=? AND id=?`, input.ExamID.String(), baseRevisionID.String()); err != nil {
			return examRevisionPublicationOutcomeRow{}, translateError("exam_revision", baseRevisionID.String(), err)
		}
		if baseDigest == revision.ContentDigest {
			return examRevisionPublicationOutcomeRow{}, store.NewErrConflict("exam_revision", "exam_revision_no_changes", nil)
		}
	}
	if err = insertExamRevision(ctx, tx, revision); err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	examResult, err := tx.Exec(ctx, `UPDATE exams SET default_revision_id=?,updated_at=GREATEST(updated_at,?),revision=revision+1 WHERE id=? AND revision=?`, revision.ID.String(), revision.PublishedAt, revision.ExamID.String(), draft.ExamRevision)
	if err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	if err = requireExamResourceAffected(examResult, 1, "exam_revision"); err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	draftResult, err := tx.Exec(ctx, `UPDATE exam_drafts SET base_revision_id=?,updated_at=GREATEST(updated_at,?),revision=revision+1 WHERE exam_id=? AND revision=?`, revision.ID.String(), revision.PublishedAt, revision.ExamID.String(), draft.DraftRevision)
	if err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	if err = requireExamResourceAffected(draftResult, 1, "exam_draft_revision"); err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	examRevision, draftRevision := draft.ExamRevision+1, draft.DraftRevision+1
	auditData, err := model.EncodeAuditData(map[string]any{"exam_id": revision.ExamID.String(), "exam_revision_id": revision.ID.String(),
		"number": revision.Number, "policy_digest": revision.PolicyDigest, "publication_kind": string(revision.Kind),
		"published_at": model.MillisFromTime(revision.PublishedAt), "exam_revision": examRevision, "draft_revision": draftRevision})
	if err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", auditData, input.AuditAt); err != nil {
		return examRevisionPublicationOutcomeRow{}, err
	}
	return examRevisionPublicationOutcomeRow{RevisionID: revision.ID.String(), ExamID: revision.ExamID.String(), Number: revision.Number,
		ExamRevision: examRevision, DraftRevision: draftRevision, PolicyDigest: revision.PolicyDigest}, nil
}

func examRevisionCapacityExceeded(capacity model.ExamCapacityPolicy, resources []model.ExamRevisionResource, workspace []model.ExamRevisionStarterWorkspaceEntry) bool {
	if len(resources) > capacity.ResourceMaximumCount || len(workspace) > capacity.WorkspaceMaximumEntries {
		return true
	}
	var workspaceBytes int64
	for _, resource := range resources {
		if resource.SizeBytes > capacity.ResourceMaximumBytes {
			return true
		}
	}
	for _, entry := range workspace {
		if entry.SizeBytes > capacity.WorkspaceMaximumFileBytes {
			return true
		}
		workspaceBytes += entry.SizeBytes
	}
	return workspaceBytes > capacity.WorkspaceMaximumTotalBytes
}

func insertExamRevision(ctx context.Context, tx *sqlxTxWrapper, revision *model.ExamRevision) error {
	var starterBytes int64
	for _, entry := range revision.StarterWorkspace {
		starterBytes += entry.SizeBytes
	}
	profile, err := model.EncodeExecutionProfile(revision.ExecutionProfile)
	if err != nil {
		return fmt.Errorf("encode Exam Revision execution profile: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exam_revisions (id,exam_id,number,snapshot_schema_version,source_draft_revision,
		title,instructions_markdown,policy_schema_version,policy_document,policy_canonical,policy_digest,
		execution_profile_document,execution_profile_canonical,execution_profile_digest,
		exam_resource_max_count,exam_resource_max_bytes,exam_workspace_max_entries,exam_workspace_max_file_bytes,exam_workspace_max_total_bytes,
		starter_workspace_digest,content_digest,resource_count,starter_entry_count,starter_total_bytes,
		published_by_user_id,published_at,base_revision_id,publication_kind)
		VALUES (?,?,?,?,?,?,?,?,?::jsonb,?,?,?::jsonb,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, revision.ID.String(), revision.ExamID.String(), revision.Number,
		model.ExamRevisionSnapshotSchemaVersion, revision.SourceDraftRevision, revision.Title, revision.InstructionsMarkdown,
		revision.Policy.SchemaVersion, string(revision.Policy.Bytes), revision.Policy.Bytes, revision.PolicyDigest,
		string(profile), profile, revision.ExecutionProfileDigest,
		revision.Capacity.ResourceMaximumCount, revision.Capacity.ResourceMaximumBytes, revision.Capacity.WorkspaceMaximumEntries,
		revision.Capacity.WorkspaceMaximumFileBytes, revision.Capacity.WorkspaceMaximumTotalBytes,
		revision.StarterWorkspaceDigest, revision.ContentDigest, len(revision.Resources), len(revision.StarterWorkspace), starterBytes,
		revision.PublishedByUserID.String(), revision.PublishedAt, nullableString(revision.BaseRevisionID.String()), string(revision.Kind)); err != nil {
		return fmt.Errorf("insert Exam Revision: %w", translateError("exam_revision", revision.ID.String(), err))
	}
	for _, resource := range revision.Resources {
		if _, err := tx.Exec(ctx, `INSERT INTO exam_revision_resources (exam_revision_id,exam_id,resource_id,file_entry_id,file_revision_id,rendition_id,
			display_name,description_markdown,position,media_type,size_bytes,sha256) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			revision.ID.String(), revision.ExamID.String(), resource.ResourceID.String(), resource.FileEntryID.String(), resource.FileRevisionID.String(), resource.RenditionID.String(),
			resource.DisplayName, resource.DescriptionMarkdown, resource.Position, string(resource.MediaType), resource.SizeBytes, resource.SHA256); err != nil {
			return fmt.Errorf("insert Exam Revision resource: %w", translateError("exam_revision_resource", resource.ResourceID.String(), err))
		}
	}
	for _, entry := range revision.StarterWorkspace {
		if _, err := tx.Exec(ctx, `INSERT INTO exam_revision_starter_workspace_entries (exam_revision_id,exam_id,entry_id,kind,path,object_id,content_version,media_type,size_bytes,sha256)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, revision.ID.String(), revision.ExamID.String(), entry.EntryID.String(), string(entry.Kind), entry.Path,
			nullableString(entry.ObjectID.String()), nullableString(string(entry.ContentVersion)), nullableString(entry.MediaType), nullableInt64(entry.Kind == model.StarterWorkspaceEntryFile, entry.SizeBytes), nullableString(entry.SHA256)); err != nil {
			return fmt.Errorf("insert Exam Revision Starter Workspace entry: %w", translateError("exam_revision_workspace", entry.EntryID.String(), err))
		}
	}
	sealed, err := tx.Exec(ctx, `UPDATE exam_revisions SET sealed=TRUE WHERE id=? AND sealed=FALSE`, revision.ID.String())
	if err != nil {
		return fmt.Errorf("seal Exam Revision: %w", err)
	}
	if err = requireExamResourceAffected(sealed, 1, "exam_revision_seal"); err != nil {
		return err
	}
	return nil
}

func nullableInt64(valid bool, value int64) any {
	if !valid {
		return nil
	}
	return value
}

func (s SQLExamRevisionStore) GetSummary(ctx context.Context, examID model.ExamID, revisionID model.ExamRevisionID) (*store.ExamRevisionSummary, error) {
	if !examID.IsValid() || !revisionID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_revision", "identity", nil)
	}
	var row examRevisionHeaderRow
	if err := s.GetMaster().Get(ctx, &row, examRevisionHeaderSelect+` WHERE exam_id=? AND id=?`, examID.String(), revisionID.String()); err != nil {
		return nil, translateError("exam_revision", revisionID.String(), err)
	}
	return row.summary()
}

func (s SQLExamRevisionStore) List(ctx context.Context, options store.ExamRevisionListOptions) ([]store.ExamRevisionSummary, error) {
	if !options.ExamID.IsValid() || options.Limit < 1 || options.Limit > 201 || options.BeforeNumber < 0 ||
		(options.BeforeNumber == 0) != options.BeforeRevisionID.IsZero() || !options.BeforeRevisionID.IsZero() && !options.BeforeRevisionID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_revision", "list", nil)
	}
	query := examRevisionHeaderSelect + ` WHERE exam_id=?`
	args := []any{options.ExamID.String()}
	if options.BeforeNumber > 0 {
		query += ` AND (number,id) < (?,?)`
		args = append(args, options.BeforeNumber, options.BeforeRevisionID.String())
	}
	query += ` ORDER BY number DESC,id DESC LIMIT ?`
	args = append(args, options.Limit)
	var rows []examRevisionHeaderRow
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Exam Revisions: %w", err)
	}
	items := make([]store.ExamRevisionSummary, 0, len(rows))
	for _, row := range rows {
		summary, err := row.summary()
		if err != nil {
			return nil, err
		}
		items = append(items, *summary)
	}
	return items, nil
}

func (s SQLExamRevisionStore) GetSnapshot(ctx context.Context, examID model.ExamID, revisionID model.ExamRevisionID) (*model.ExamRevision, error) {
	if !examID.IsValid() || !revisionID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_revision", "identity", nil)
	}
	return getExamRevisionSnapshot(ctx, s.GetMaster(), examID, revisionID)
}

func getExamRevisionSnapshot(ctx context.Context, executor sqlxExecutor, examID model.ExamID, revisionID model.ExamRevisionID) (*model.ExamRevision, error) {
	var header examRevisionHeaderRow
	if err := executor.Get(ctx, &header, examRevisionHeaderSelect+` WHERE exam_id=? AND id=?`, examID.String(), revisionID.String()); err != nil {
		return nil, translateError("exam_revision", revisionID.String(), err)
	}
	var resourceRows []examRevisionResourceRow
	if err := executor.Select(ctx, &resourceRows, `SELECT resource_id,file_entry_id,file_revision_id,rendition_id,display_name,description_markdown,position,media_type,size_bytes,sha256
		FROM exam_revision_resources WHERE exam_id=? AND exam_revision_id=? ORDER BY position`, examID.String(), revisionID.String()); err != nil {
		return nil, err
	}
	resources := make([]model.ExamRevisionResource, len(resourceRows))
	for index, row := range resourceRows {
		resourceID, err := model.ParseExamResourceID(row.ResourceID)
		if err != nil {
			return nil, invalidPersistedState("exam_revision_resource", "resource_id", err)
		}
		entryID, err := model.ParseFileEntryID(row.FileEntryID)
		if err != nil {
			return nil, invalidPersistedState("exam_revision_resource", "file_entry_id", err)
		}
		fileRevisionID, err := model.ParseFileRevisionID(row.FileRevisionID)
		if err != nil {
			return nil, invalidPersistedState("exam_revision_resource", "file_revision_id", err)
		}
		renditionID, err := model.ParseFileRenditionID(row.RenditionID)
		if err != nil {
			return nil, invalidPersistedState("exam_revision_resource", "rendition_id", err)
		}
		resources[index] = model.ExamRevisionResource{ResourceID: resourceID, FileEntryID: entryID, FileRevisionID: fileRevisionID, RenditionID: renditionID,
			DisplayName: row.DisplayName, DescriptionMarkdown: row.DescriptionMarkdown, Position: row.Position,
			MediaType: model.ExamResourceMediaType(row.MediaType), SizeBytes: row.SizeBytes, SHA256: row.SHA256}
	}
	var workspaceRows []examRevisionWorkspaceRow
	if err := executor.Select(ctx, &workspaceRows, `SELECT entry_id,kind,path,object_id,content_version,media_type,size_bytes,sha256
		FROM exam_revision_starter_workspace_entries WHERE exam_id=? AND exam_revision_id=? ORDER BY convert_to(path,'UTF8'),entry_id`, examID.String(), revisionID.String()); err != nil {
		return nil, err
	}
	workspace := make([]model.ExamRevisionStarterWorkspaceEntry, len(workspaceRows))
	for index, row := range workspaceRows {
		entryID, err := model.ParseStarterWorkspaceEntryID(row.EntryID)
		if err != nil {
			return nil, invalidPersistedState("exam_revision_workspace", "entry_id", err)
		}
		entry := model.ExamRevisionStarterWorkspaceEntry{EntryID: entryID, Kind: model.StarterWorkspaceEntryKind(row.Kind), Path: row.Path,
			ContentVersion: model.WorkspaceContentVersion(row.ContentVersion.String), MediaType: row.MediaType.String, SizeBytes: row.SizeBytes.Int64, SHA256: row.SHA256.String}
		if row.ObjectID.Valid {
			entry.ObjectID, err = model.ParseStarterWorkspaceObjectID(row.ObjectID.String)
			if err != nil {
				return nil, invalidPersistedState("exam_revision_workspace", "object_id", err)
			}
		}
		workspace[index] = entry
	}
	id, err := model.ParseExamRevisionID(header.ID)
	if err != nil {
		return nil, invalidPersistedState("exam_revision", "id", err)
	}
	publisherID, err := model.ParseUserID(header.PublishedByUserID)
	if err != nil {
		return nil, invalidPersistedState("exam_revision", "published_by_user_id", err)
	}
	baseID, err := parseNullablePersistedID[model.ExamRevisionID]("exam_revision", "base_revision_id", header.BaseRevisionID, model.ParseExamRevisionID)
	if err != nil {
		return nil, err
	}
	policy, err := model.CanonicalizeExamRevisionPolicy(header.PolicyCanonical)
	if err != nil {
		return nil, invalidPersistedState("exam_revision", "policy_canonical", err)
	}
	documentPolicy, err := model.CanonicalizeExamRevisionPolicy([]byte(header.PolicyDocument))
	if err != nil || !bytes.Equal(documentPolicy.Bytes, policy.Bytes) {
		return nil, invalidPersistedState("exam_revision", "policy_document", errors.New("canonical policy mismatch"))
	}
	profile, err := model.DecodeExecutionProfile(header.ExecutionProfileCanonical)
	if err != nil {
		return nil, invalidPersistedState("exam_revision", "execution_profile_canonical", err)
	}
	documentProfile, err := model.DecodeExecutionProfile([]byte(header.ExecutionProfileDocument))
	if err != nil || documentProfile != profile {
		return nil, invalidPersistedState("exam_revision", "execution_profile_document", errors.New("canonical execution profile mismatch"))
	}
	revision, err := model.NewExamRevision(model.ExamRevisionSpecification{ID: id, ExamID: examID, Number: header.Number,
		SourceDraftRevision: header.SourceDraftRevision, Title: header.Title, InstructionsMarkdown: header.InstructionsMarkdown,
		Policy: policy, ExecutionProfile: profile, Capacity: model.ExamCapacityPolicy{
			ResourceMaximumCount: header.ResourceMaximumCount, ResourceMaximumBytes: header.ResourceMaximumBytes,
			WorkspaceMaximumEntries: header.WorkspaceMaximumEntries, WorkspaceMaximumFileBytes: header.WorkspaceMaximumFileBytes,
			WorkspaceMaximumTotalBytes: header.WorkspaceMaximumTotalBytes,
		}, Resources: resources, StarterWorkspace: workspace, PublishedByUserID: publisherID,
		PublishedAt: header.PublishedAt, BaseRevisionID: baseID, Kind: model.ExamRevisionPublicationKind(header.PublicationKind)})
	if err != nil {
		return nil, invalidPersistedState("exam_revision", "snapshot", err)
	}
	var starterBytes int64
	for _, entry := range workspace {
		starterBytes += entry.SizeBytes
	}
	if !header.Sealed || header.SnapshotSchemaVersion != model.ExamRevisionSnapshotSchemaVersion || header.PolicySchemaVersion != policy.SchemaVersion ||
		header.PolicyDigest != revision.PolicyDigest || header.ExecutionProfileDigest != revision.ExecutionProfileDigest || header.StarterWorkspaceDigest != revision.StarterWorkspaceDigest || header.ContentDigest != revision.ContentDigest ||
		header.ResourceCount != len(resources) || header.StarterEntryCount != len(workspace) || header.StarterTotalBytes != starterBytes {
		return nil, invalidPersistedState("exam_revision", "header", errors.New("snapshot metadata mismatch"))
	}
	return revision, nil
}

func (row examRevisionHeaderRow) summary() (*store.ExamRevisionSummary, error) {
	id, err := model.ParseExamRevisionID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("exam_revision", "id", err)
	}
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_revision", "exam_id", err)
	}
	publisherID, err := model.ParseUserID(row.PublishedByUserID)
	if err != nil {
		return nil, invalidPersistedState("exam_revision", "published_by_user_id", err)
	}
	baseID, err := parseNullablePersistedID[model.ExamRevisionID]("exam_revision", "base_revision_id", row.BaseRevisionID, model.ParseExamRevisionID)
	if err != nil {
		return nil, err
	}
	if !row.Sealed || row.SnapshotSchemaVersion != model.ExamRevisionSnapshotSchemaVersion || row.Number < 1 || row.SourceDraftRevision < 1 || row.PolicySchemaVersion < 1 ||
		row.ResourceCount < 0 || row.ResourceCount > model.ExamResourceMaximumCount || row.StarterEntryCount < 0 || row.StarterEntryCount > model.StarterWorkspaceMaximumEntries ||
		row.StarterTotalBytes < 0 || row.StarterTotalBytes > model.StarterWorkspaceMaximumTotalBytes || !model.ExamRevisionPublicationKind(row.PublicationKind).IsValid() {
		return nil, invalidPersistedState("exam_revision", "summary", errors.New("invalid summary"))
	}
	return &store.ExamRevisionSummary{ID: id, ExamID: examID, Number: row.Number, SourceDraftRevision: row.SourceDraftRevision, Title: row.Title,
		PolicySchemaVersion: row.PolicySchemaVersion, PolicyDigest: row.PolicyDigest, ExecutionProfileDigest: row.ExecutionProfileDigest, StarterWorkspaceDigest: row.StarterWorkspaceDigest,
		Capacity: model.ExamCapacityPolicy{ResourceMaximumCount: row.ResourceMaximumCount, ResourceMaximumBytes: row.ResourceMaximumBytes,
			WorkspaceMaximumEntries: row.WorkspaceMaximumEntries, WorkspaceMaximumFileBytes: row.WorkspaceMaximumFileBytes, WorkspaceMaximumTotalBytes: row.WorkspaceMaximumTotalBytes},
		ContentDigest: row.ContentDigest, ResourceCount: row.ResourceCount, StarterWorkspaceEntries: row.StarterEntryCount,
		StarterWorkspaceBytes: row.StarterTotalBytes, PublishedByUserID: publisherID, PublishedAt: model.TimeUTC(row.PublishedAt),
		BaseRevisionID: baseID, Kind: model.ExamRevisionPublicationKind(row.PublicationKind)}, nil
}

var _ store.ExamRevisionStore = (*SQLExamRevisionStore)(nil)
