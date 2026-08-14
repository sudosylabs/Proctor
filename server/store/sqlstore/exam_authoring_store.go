// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLExamAuthoringStore struct{ *SQLStore }

type examAuthoringRow struct {
	ID                   string         `db:"id" json:"id"`
	AcademicUnitID       string         `db:"academic_unit_id" json:"academic_unit_id"`
	CreatorUserID        string         `db:"creator_user_id" json:"creator_user_id"`
	OwnerUserID          string         `db:"owner_user_id" json:"owner_user_id"`
	DefaultRevisionID    sql.NullString `db:"default_revision_id" json:"default_revision_id"`
	CreatedAt            time.Time      `db:"created_at" json:"created_at"`
	UpdatedAt            time.Time      `db:"updated_at" json:"updated_at"`
	ArchivedAt           sql.NullTime   `db:"archived_at" json:"archived_at"`
	ExamRevision         int64          `db:"exam_revision" json:"exam_revision"`
	DraftTitle           string         `db:"draft_title" json:"draft_title"`
	InstructionsMarkdown string         `db:"instructions_markdown" json:"instructions_markdown"`
	Policy               jsonValue      `db:"policy" json:"policy"`
	BaseRevisionID       sql.NullString `db:"base_revision_id" json:"base_revision_id"`
	DraftUpdatedAt       time.Time      `db:"draft_updated_at" json:"draft_updated_at"`
	DraftRevision        int64          `db:"draft_revision" json:"draft_revision"`
	ManagerCount         int            `db:"manager_count" json:"manager_count"`
	ActorIsManager       bool           `db:"actor_is_manager" json:"actor_is_manager"`
	OwnerIsManager       bool           `db:"owner_is_manager" json:"owner_is_manager"`
	ResourceCount        int            `db:"resource_count" json:"resource_count"`
	HasStarterWorkspace  bool           `db:"has_starter_workspace" json:"has_starter_workspace"`
}

type examAccessRow struct {
	ID                string         `db:"id"`
	AcademicUnitID    string         `db:"academic_unit_id"`
	CreatorUserID     string         `db:"creator_user_id"`
	OwnerUserID       string         `db:"owner_user_id"`
	DefaultRevisionID sql.NullString `db:"default_revision_id"`
	CreatedAt         time.Time      `db:"created_at"`
	UpdatedAt         time.Time      `db:"updated_at"`
	ArchivedAt        sql.NullTime   `db:"archived_at"`
	Revision          int64          `db:"revision"`
	ActorIsManager    bool           `db:"actor_is_manager"`
}

func newSQLExamAuthoringStore(sqlStore *SQLStore) store.ExamAuthoringStore {
	return &SQLExamAuthoringStore{SQLStore: sqlStore}
}

func (s SQLExamAuthoringStore) Create(ctx context.Context, input *store.ExamAuthoringCreation) (*store.ExamAuthoringSnapshot, error) {
	prepared, auditData, err := prepareExamAuthoringCreation(input)
	if err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "exam creation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAuthoringSnapshot, error) {
		return createExamAuthoring(ctx, tx, prepared, auditData)
	})
}

func (s SQLExamAuthoringStore) CreateIdempotently(ctx context.Context, input *store.ExamAuthoringCreation, command *store.CommandIdempotency) (*store.ExamAuthoringCommandResult, error) {
	prepared, auditData, err := prepareExamAuthoringCreation(input)
	if err != nil || command == nil {
		if err != nil {
			return nil, err
		}
		return nil, store.NewErrInvalidInput("exam", "idempotency", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent exam creation", idempotentMutation[*store.ExamAuthoringSnapshot]{
		command: command, auditEventID: prepared.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAuthoringSnapshot, error) {
			return createExamAuthoring(ctx, tx, prepared, auditData)
		},
		encode: func(snapshot *store.ExamAuthoringSnapshot) ([]byte, error) {
			row, err := newExamAuthoringRow(snapshot, true)
			if err != nil {
				return nil, err
			}
			return encodeCommandOutcome(row)
		},
		decode: func(version int, data []byte) (*store.ExamAuthoringSnapshot, error) {
			if version != 1 {
				return nil, fmt.Errorf("unsupported exam creation outcome version %d", version)
			}
			var row examAuthoringRow
			if err := decodeCommandOutcome(data, &row); err != nil {
				return nil, err
			}
			return row.model()
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, snapshot *store.ExamAuthoringSnapshot, originalAuditID string) error {
			data := snapshot.Exam.Auditable()
			data["idempotency_replayed"] = true
			data["original_audit_event_id"] = originalAuditID
			encoded, err := model.EncodeAuditData(data)
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
	return &store.ExamAuthoringCommandResult{Value: result.Value, Replayed: result.Replayed}, nil
}

func (s SQLExamAuthoringStore) Get(ctx context.Context, examID model.ExamID, actorID model.UserID) (*store.ExamAuthoringSnapshot, error) {
	if !examID.IsValid() || !actorID.IsValid() {
		return nil, store.NewErrInvalidInput("exam", "identity", nil)
	}
	var row examAuthoringRow
	if err := s.GetMaster().Get(ctx, &row, examAuthoringSelect+` WHERE e.id = ?`, actorID.String(), examID.String()); err != nil {
		return nil, translateError("exam", examID.String(), err)
	}
	return row.model()
}

func (s SQLExamAuthoringStore) Access(ctx context.Context, examID model.ExamID, actorID model.UserID) (*store.ExamAccessSnapshot, error) {
	if !examID.IsValid() || !actorID.IsValid() {
		return nil, store.NewErrInvalidInput("exam", "identity", nil)
	}
	return s.getAccess(ctx, examID, actorID.String())
}

func (s SQLExamAuthoringStore) Resolve(ctx context.Context, examID model.ExamID) (*model.Exam, error) {
	if !examID.IsValid() {
		return nil, store.NewErrInvalidInput("exam", "id", nil)
	}
	access, err := s.getAccess(ctx, examID, "")
	if err != nil {
		return nil, err
	}
	return access.Exam, nil
}

func (s SQLExamAuthoringStore) getAccess(ctx context.Context, examID model.ExamID, actorID string) (*store.ExamAccessSnapshot, error) {
	var row examAccessRow
	if err := s.GetMaster().Get(ctx, &row, examAccessSelect, actorID, examID.String()); err != nil {
		return nil, translateError("exam", examID.String(), err)
	}
	exam, err := row.model()
	if err != nil {
		return nil, err
	}
	return &store.ExamAccessSnapshot{Exam: exam, ActorIsManager: row.ActorIsManager}, nil
}

const examAccessSelect = `SELECT
	e.id, e.academic_unit_id, e.creator_user_id, e.owner_user_id, e.default_revision_id,
	e.created_at, e.updated_at, e.archived_at, e.revision,
	EXISTS (SELECT 1 FROM exam_managers actor_manager WHERE actor_manager.exam_id = e.id AND actor_manager.user_id = ?) AS actor_is_manager
FROM exams e WHERE e.id = ?`

const examAuthoringSelect = `SELECT
	e.id, e.academic_unit_id, e.creator_user_id, e.owner_user_id, e.default_revision_id,
	e.created_at, e.updated_at, e.archived_at, e.revision AS exam_revision,
	d.title AS draft_title, d.instructions_markdown, d.policy, d.base_revision_id,
	d.updated_at AS draft_updated_at, d.revision AS draft_revision,
	(SELECT COUNT(*) FROM exam_managers count_managers WHERE count_managers.exam_id = e.id) AS manager_count,
	EXISTS (SELECT 1 FROM exam_managers actor_manager WHERE actor_manager.exam_id = e.id AND actor_manager.user_id = ?) AS actor_is_manager,
	EXISTS (SELECT 1 FROM exam_managers owner_manager WHERE owner_manager.exam_id = e.id AND owner_manager.user_id = e.owner_user_id) AS owner_is_manager,
	0 AS resource_count, false AS has_starter_workspace
FROM exams e JOIN exam_drafts d ON d.exam_id = e.id`

func prepareExamAuthoringCreation(input *store.ExamAuthoringCreation) (*store.ExamAuthoringCreation, json.RawMessage, error) {
	if input == nil || input.Exam == nil || input.Draft == nil || input.Manager == nil || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, nil, store.NewErrInvalidInput("exam", "creation", nil)
	}
	exam := *input.Exam
	draft := *input.Draft
	manager := *input.Manager
	if err := exam.Validate(); err != nil {
		return nil, nil, store.NewErrInvalidInput("exam", "value", nil).Wrap(err)
	}
	if err := draft.Validate(); err != nil {
		return nil, nil, store.NewErrInvalidInput("exam", "draft", nil).Wrap(err)
	}
	if err := manager.Validate(); err != nil {
		return nil, nil, store.NewErrInvalidInput("exam", "manager", nil).Wrap(err)
	}
	if draft.ExamID != exam.ID || manager.ExamID != exam.ID || manager.UserID != exam.OwnerUserID || manager.UserID != exam.CreatorUserID || manager.GrantedByUserID != exam.CreatorUserID {
		return nil, nil, store.NewErrInvalidInput("exam", "aggregate", nil)
	}
	auditData, err := model.EncodeAuditData(exam.Auditable())
	if err != nil {
		return nil, nil, err
	}
	return &store.ExamAuthoringCreation{Exam: &exam, Draft: &draft, Manager: &manager, AuditEventID: input.AuditEventID, AuditAt: input.AuditAt}, auditData, nil
}

func createExamAuthoring(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAuthoringCreation, auditData json.RawMessage) (*store.ExamAuthoringSnapshot, error) {
	policy, err := model.EncodeExamPolicySet(input.Draft.Policy)
	if err != nil {
		return nil, err
	}
	row, err := newExamAuthoringRow(&store.ExamAuthoringSnapshot{Exam: input.Exam, Draft: input.Draft, OwnerUserID: input.Exam.OwnerUserID, ManagerCount: 1, ActorIsManager: true}, true)
	if err != nil {
		return nil, err
	}
	row.Policy = jsonValue(policy)
	if _, err := tx.NamedExec(ctx, `INSERT INTO exams (
		id, academic_unit_id, creator_user_id, owner_user_id, default_revision_id,
		created_at, updated_at, archived_at, revision
	) VALUES (:id, :academic_unit_id, :creator_user_id, :owner_user_id, :default_revision_id,
		:created_at, :updated_at, :archived_at, :exam_revision)`, &row); err != nil {
		return nil, fmt.Errorf("create exam: %w", translateError("exam", input.Exam.ID.String(), err))
	}
	if _, err := tx.NamedExec(ctx, `INSERT INTO exam_drafts (
		exam_id, title, instructions_markdown, policy, base_revision_id, updated_at, revision
	) VALUES (:id, :draft_title, :instructions_markdown, :policy, :base_revision_id,
		:draft_updated_at, :draft_revision)`, &row); err != nil {
		return nil, fmt.Errorf("create exam draft: %w", translateError("exam_draft", input.Exam.ID.String(), err))
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exam_managers (exam_id, user_id, granted_by_user_id, granted_at) VALUES (?, ?, ?, ?)`,
		input.Manager.ExamID.String(), input.Manager.UserID.String(), input.Manager.GrantedByUserID.String(), input.Manager.GrantedAt); err != nil {
		return nil, fmt.Errorf("create exam manager: %w", translateError("exam_manager", input.Manager.UserID.String(), err))
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", auditData, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete exam creation audit: %w", err)
	}
	return row.model()
}

func newExamAuthoringRow(snapshot *store.ExamAuthoringSnapshot, actorIsManager bool) (examAuthoringRow, error) {
	if snapshot == nil || snapshot.Exam == nil || snapshot.Draft == nil {
		return examAuthoringRow{}, store.NewErrInvalidInput("exam", "snapshot", nil)
	}
	policy, err := model.EncodeExamPolicySet(snapshot.Draft.Policy)
	if err != nil {
		return examAuthoringRow{}, err
	}
	return examAuthoringRow{
		ID: snapshot.Exam.ID.String(), AcademicUnitID: snapshot.Exam.AcademicUnitID.String(),
		CreatorUserID: snapshot.Exam.CreatorUserID.String(), OwnerUserID: snapshot.Exam.OwnerUserID.String(),
		DefaultRevisionID: nullableString(snapshot.Exam.DefaultRevisionID.String()), CreatedAt: snapshot.Exam.CreatedAt,
		UpdatedAt: snapshot.Exam.UpdatedAt, ArchivedAt: NullTimeFromOptional(snapshot.Exam.ArchivedAt), ExamRevision: snapshot.Exam.Revision,
		DraftTitle: snapshot.Draft.Title, InstructionsMarkdown: snapshot.Draft.InstructionsMarkdown,
		Policy: jsonValue(policy), BaseRevisionID: nullableString(snapshot.Draft.BaseRevisionID.String()),
		DraftUpdatedAt: snapshot.Draft.UpdatedAt, DraftRevision: snapshot.Draft.Revision,
		ManagerCount: snapshot.ManagerCount, ActorIsManager: actorIsManager,
		OwnerIsManager: true,
		ResourceCount:  snapshot.ResourceCount, HasStarterWorkspace: snapshot.HasStarterWorkspace,
	}, nil
}

func (r examAuthoringRow) model() (*store.ExamAuthoringSnapshot, error) {
	examID, err := parsePersistedID[model.ExamID]("exam", "id", r.ID, model.ParseExamID)
	if err != nil {
		return nil, err
	}
	unitID, err := parsePersistedID[model.AcademicUnitID]("exam", "academic_unit_id", r.AcademicUnitID, model.ParseAcademicUnitID)
	if err != nil {
		return nil, err
	}
	creatorID, err := parsePersistedID[model.UserID]("exam", "creator_user_id", r.CreatorUserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	ownerID, err := parsePersistedID[model.UserID]("exam", "owner_user_id", r.OwnerUserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	defaultRevisionID, err := parseNullablePersistedID[model.ExamRevisionID]("exam", "default_revision_id", r.DefaultRevisionID, model.ParseExamRevisionID)
	if err != nil {
		return nil, err
	}
	baseRevisionID, err := parseNullablePersistedID[model.ExamRevisionID]("exam_draft", "base_revision_id", r.BaseRevisionID, model.ParseExamRevisionID)
	if err != nil {
		return nil, err
	}
	policy, err := model.DecodeExamPolicySet([]byte(r.Policy))
	if err != nil {
		return nil, invalidPersistedState("exam_draft", "policy", err)
	}
	exam := &model.Exam{ID: examID, AcademicUnitID: unitID, CreatorUserID: creatorID, OwnerUserID: ownerID,
		DefaultRevisionID: defaultRevisionID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		ArchivedAt: OptionalTimeFromNullTime(r.ArchivedAt), Revision: r.ExamRevision}
	if err := validatePersistedModel("exam", exam); err != nil {
		return nil, err
	}
	draft := &model.ExamDraft{ExamID: examID, Title: r.DraftTitle, InstructionsMarkdown: r.InstructionsMarkdown,
		Policy: policy, BaseRevisionID: baseRevisionID, UpdatedAt: r.DraftUpdatedAt, Revision: r.DraftRevision}
	if err := validatePersistedModel("exam_draft", draft); err != nil {
		return nil, err
	}
	if r.ManagerCount < 1 || !r.OwnerIsManager || r.ResourceCount < 0 {
		return nil, invalidPersistedState("exam", "aggregate", fmt.Errorf("invalid aggregate counts"))
	}
	return &store.ExamAuthoringSnapshot{Exam: exam, Draft: draft, OwnerUserID: ownerID, ManagerCount: r.ManagerCount,
		ActorIsManager: r.ActorIsManager, ResourceCount: r.ResourceCount, HasStarterWorkspace: r.HasStarterWorkspace}, nil
}

func (r examAccessRow) model() (*model.Exam, error) {
	examID, err := parsePersistedID[model.ExamID]("exam", "id", r.ID, model.ParseExamID)
	if err != nil {
		return nil, err
	}
	unitID, err := parsePersistedID[model.AcademicUnitID]("exam", "academic_unit_id", r.AcademicUnitID, model.ParseAcademicUnitID)
	if err != nil {
		return nil, err
	}
	creatorID, err := parsePersistedID[model.UserID]("exam", "creator_user_id", r.CreatorUserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	ownerID, err := parsePersistedID[model.UserID]("exam", "owner_user_id", r.OwnerUserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	defaultRevisionID, err := parseNullablePersistedID[model.ExamRevisionID]("exam", "default_revision_id", r.DefaultRevisionID, model.ParseExamRevisionID)
	if err != nil {
		return nil, err
	}
	exam := &model.Exam{
		ID: examID, AcademicUnitID: unitID, CreatorUserID: creatorID, OwnerUserID: ownerID,
		DefaultRevisionID: defaultRevisionID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		ArchivedAt: OptionalTimeFromNullTime(r.ArchivedAt), Revision: r.Revision,
	}
	if err := validatePersistedModel("exam", exam); err != nil {
		return nil, err
	}
	return exam, nil
}

var _ store.ExamAuthoringStore = (*SQLExamAuthoringStore)(nil)
