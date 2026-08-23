// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLExamCorrectionStore struct{ *SQLStore }

func newSQLExamCorrectionStore(sqlStore *SQLStore) store.ExamCorrectionStore {
	return &SQLExamCorrectionStore{SQLStore: sqlStore}
}

type examCorrectionStageRow struct {
	ID              string         `db:"id"`
	ExamID          string         `db:"exam_id"`
	SittingID       string         `db:"exam_sitting_id"`
	BaseRevisionID  string         `db:"base_revision_id"`
	Target          string         `db:"target"`
	ResourceID      string         `db:"resource_id"`
	FileEntryID     string         `db:"file_entry_id"`
	FileRevisionID  string         `db:"file_revision_id"`
	UploadLeaseID   string         `db:"upload_lease_id"`
	RenditionID     string         `db:"rendition_id"`
	CreatedByUserID string         `db:"created_by_user_id"`
	LeaseCreatedBy  string         `db:"lease_created_by_user_id"`
	State           string         `db:"state"`
	CreatedAt       time.Time      `db:"created_at"`
	ExpiresAt       time.Time      `db:"expires_at"`
	ReadyAt         sql.NullTime   `db:"ready_at"`
	ConsumedAt      sql.NullTime   `db:"consumed_at"`
	RenditionAt     sql.NullTime   `db:"rendition_at"`
	RenditionName   sql.NullString `db:"rendition_name"`
	MediaType       sql.NullString `db:"media_type"`
	SizeBytes       sql.NullInt64  `db:"size_bytes"`
	Width           sql.NullInt64  `db:"width"`
	Height          sql.NullInt64  `db:"height"`
	SHA256          sql.NullString `db:"sha256"`
}

const examCorrectionStageSelect = `SELECT s.id,s.exam_id,s.exam_sitting_id,s.base_revision_id,s.target,
	s.resource_id,s.file_entry_id,s.file_revision_id,s.upload_lease_id,s.rendition_id,s.created_by_user_id,
	s.state,s.created_at,l.expires_at,l.created_by_user_id AS lease_created_by_user_id,s.ready_at,s.consumed_at,r.created_at AS rendition_at,
	r.name AS rendition_name,r.media_type,r.size_bytes,r.width,r.height,r.sha256
	FROM exam_correction_resource_stages s
	JOIN upload_leases l ON l.id=s.upload_lease_id AND l.file_revision_id=s.file_revision_id
	LEFT JOIN file_renditions r ON r.id=s.rendition_id AND r.file_revision_id=s.file_revision_id`

type examCorrectionStageOutcome struct {
	StageID string `json:"stage_id"`
}

func (s SQLExamCorrectionStore) ReserveResourceStage(ctx context.Context, input *store.ExamCorrectionResourceStageReservation, command *store.CommandIdempotency) (*store.ExamCorrectionResourceStage, error) {
	if err := validateExamCorrectionStageReservation(input, command); err != nil {
		return nil, err
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "exam correction resource stage reservation", idempotentMutation[examCorrectionStageOutcome]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (examCorrectionStageOutcome, error) {
			if err := reserveExamCorrectionResourceStage(ctx, tx, input); err != nil {
				return examCorrectionStageOutcome{}, err
			}
			return examCorrectionStageOutcome{StageID: input.StageID.String()}, nil
		},
		encode: func(value examCorrectionStageOutcome) ([]byte, error) { return encodeCommandOutcome(value) },
		decode: func(version int, data []byte) (examCorrectionStageOutcome, error) {
			if version != 1 {
				return examCorrectionStageOutcome{}, fmt.Errorf("unsupported correction resource stage outcome version %d", version)
			}
			var value examCorrectionStageOutcome
			if err := decodeCommandOutcome(data, &value); err != nil {
				return value, err
			}
			if _, err := model.ParseExamCorrectionResourceStageID(value.StageID); err != nil {
				return value, invalidPersistedState("exam_correction_resource_stage", "outcome", err)
			}
			return value, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value examCorrectionStageOutcome, originalAuditID string) error {
			result, err := tx.Exec(ctx, `UPDATE exam_correction_resource_stages SET cleanup_protected_until=GREATEST(cleanup_protected_until,statement_timestamp()+(? * interval '1 millisecond')) WHERE id=?`, model.UploadLeaseMaximumLifetime.Milliseconds(), value.StageID)
			if err != nil {
				return err
			}
			if err = requireExamResourceAffected(result, 1, "exam_correction_resource_stage"); err != nil {
				return err
			}
			data, err := model.EncodeAuditData(map[string]any{"exam_id": input.ExamID.String(), "exam_sitting_id": input.SittingID.String(),
				"base_revision_id": input.BaseRevisionID.String(), "target": string(input.Target), "idempotency_replayed": true,
				"original_audit_event_id": originalAuditID})
			if err != nil {
				return err
			}
			_, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt)
			return err
		},
	})
	if err != nil {
		return nil, err
	}
	stageID, err := model.ParseExamCorrectionResourceStageID(result.Value.StageID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "outcome", err)
	}
	return getExamCorrectionStage(ctx, s.GetMaster(), stageID, false)
}

func validateExamCorrectionStageReservation(input *store.ExamCorrectionResourceStageReservation, command *store.CommandIdempotency) error {
	if input == nil || command == nil || !input.StageID.IsValid() || !input.ExamID.IsValid() || !input.SittingID.IsValid() ||
		!input.BaseRevisionID.IsValid() || !input.Target.IsValid() || !input.ResourceID.IsValid() || !input.FileEntryID.IsValid() ||
		input.Revision == nil || input.Lease == nil || !input.RenditionID.IsValid() || !input.ActorUserID.IsValid() ||
		input.CreatedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("exam_correction", "resource_stage_reservation", nil)
	}
	if input.Revision.FileEntryID != input.FileEntryID || input.Revision.Availability != model.FileAvailabilityPending ||
		len(input.Revision.Renditions) != 0 || input.Lease.FileRevisionID != input.Revision.ID ||
		input.Lease.CreatedByUserID != input.ActorUserID || input.Lease.ConsumedAt.Valid {
		return store.NewErrInvalidInput("exam_correction", "resource_stage_file", nil)
	}
	if err := input.Revision.Validate(); err != nil {
		return store.NewErrInvalidInput("exam_correction", "resource_stage_revision", nil).Wrap(err)
	}
	if err := input.Lease.Validate(); err != nil {
		return store.NewErrInvalidInput("exam_correction", "resource_stage_lease", nil).Wrap(err)
	}
	switch input.Target {
	case store.ExamCorrectionResourceAddition:
		if input.Entry == nil || input.Entry.ID != input.FileEntryID || input.Entry.Purpose != model.FilePurposeExamResource ||
			!input.Entry.CurrentRevisionID.IsZero() || input.Entry.ArchivedAt.Valid {
			return store.NewErrInvalidInput("exam_correction", "resource_stage_addition", nil)
		}
		if err := input.Entry.Validate(); err != nil {
			return store.NewErrInvalidInput("exam_correction", "resource_stage_addition", nil).Wrap(err)
		}
	case store.ExamCorrectionResourceReplacement:
		if input.Entry != nil {
			return store.NewErrInvalidInput("exam_correction", "resource_stage_replacement", nil)
		}
	}
	return nil
}

func reserveExamCorrectionResourceStage(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamCorrectionResourceStageReservation) error {
	if err := guardExamSittingManagerExam(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, false); err != nil {
		return err
	}
	sitting, err := lockExamSitting(ctx, tx, input.ExamID, input.SittingID)
	if err != nil {
		return err
	}
	if sitting.Revision < 1 || sitting.State != model.ExamSittingOpen && sitting.State != model.ExamSittingPaused {
		return store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	if sitting.ExamRevisionID != input.BaseRevisionID {
		return store.NewErrConflict("exam_sitting", "exam_sitting_revision_selection", nil)
	}
	var beforeDeadline bool
	if err = tx.Get(ctx, &beforeDeadline, `SELECT statement_timestamp() < ?`, sitting.ScheduledEndAt); err != nil {
		return err
	}
	if !beforeDeadline {
		return store.NewErrConflict("exam_sitting", "exam_sitting_deadline_reached", nil)
	}
	var leaseIsCurrent bool
	if err = tx.Get(ctx, &leaseIsCurrent, `SELECT statement_timestamp() < ?`, input.Lease.ExpiresAt); err != nil {
		return err
	}
	if !leaseIsCurrent {
		return store.NewErrConflict("exam_correction", "exam_correction_resource_stage", nil)
	}
	var lockedBaseID string
	if err = tx.Get(ctx, &lockedBaseID, `SELECT id FROM exam_revisions WHERE exam_id=? AND id=? AND sealed=true FOR KEY SHARE`, input.ExamID.String(), input.BaseRevisionID.String()); errors.Is(err, sql.ErrNoRows) {
		return store.NewErrConflict("exam_correction", "exam_correction_base_revision", nil)
	} else if err != nil {
		return err
	}
	if input.Target == store.ExamCorrectionResourceReplacement {
		var exact bool
		if err = tx.Get(ctx, &exact, `SELECT EXISTS (SELECT 1 FROM exam_revision_resources WHERE exam_id=? AND exam_revision_id=? AND resource_id=? AND file_entry_id=?)`,
			input.ExamID.String(), input.BaseRevisionID.String(), input.ResourceID.String(), input.FileEntryID.String()); err != nil {
			return err
		}
		if !exact {
			return store.NewErrConflict("exam_correction", "exam_correction_resource_manifest", nil)
		}
	} else {
		entry := input.Entry
		if _, err = tx.Exec(ctx, `INSERT INTO file_entries (id,created_at,updated_at,archived_at,revision,current_revision_id,indexing_policy,purpose) VALUES (?,?,?,NULL,?,NULL,?,?)`,
			entry.ID.String(), entry.CreatedAt, entry.UpdatedAt, entry.Revision, string(entry.IndexingPolicy), string(entry.Purpose)); err != nil {
			return fmt.Errorf("create correction file entry: %w", translateError("file_entry", entry.ID.String(), err))
		}
		if _, err = tx.Exec(ctx, `INSERT INTO exam_resource_identities (id,exam_id,file_entry_id) VALUES (?,?,?)`, input.ResourceID.String(), input.ExamID.String(), input.FileEntryID.String()); err != nil {
			return fmt.Errorf("create correction resource identity: %w", translateError("exam_resource", input.ResourceID.String(), err))
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO file_revisions (id,file_entry_id,created_at,availability,indexing_state) VALUES (?,?,?,?,?)`,
		input.Revision.ID.String(), input.FileEntryID.String(), input.Revision.CreatedAt, string(input.Revision.Availability), string(input.Revision.IndexingState)); err != nil {
		return fmt.Errorf("create correction file revision: %w", translateError("file_revision", input.Revision.ID.String(), err))
	}
	lease := input.Lease
	if _, err = tx.Exec(ctx, `INSERT INTO upload_leases (id,file_revision_id,created_by_user_id,created_at,updated_at,expires_at,consumed_at,revision,bytes_received) VALUES (?,?,?,?,?,?,NULL,?,?)`,
		lease.ID.String(), lease.FileRevisionID.String(), lease.CreatedByUserID.String(), lease.CreatedAt, lease.UpdatedAt, lease.ExpiresAt, lease.Revision, lease.BytesReceived); err != nil {
		return fmt.Errorf("create correction upload lease: %w", translateError("upload_lease", lease.ID.String(), err))
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_correction_resource_stages (id,exam_id,exam_sitting_id,base_revision_id,target,resource_id,file_entry_id,file_revision_id,upload_lease_id,rendition_id,created_by_user_id,state,created_at,cleanup_protected_until) VALUES (?,?,?,?,?,?,?,?,?,?,?,'pending',?,GREATEST(?,statement_timestamp())+(? * interval '1 millisecond'))`,
		input.StageID.String(), input.ExamID.String(), input.SittingID.String(), input.BaseRevisionID.String(), string(input.Target), input.ResourceID.String(), input.FileEntryID.String(), input.Revision.ID.String(), input.Lease.ID.String(), input.RenditionID.String(), input.ActorUserID.String(), input.CreatedAt, input.Lease.ExpiresAt, model.UploadLeaseMaximumLifetime.Milliseconds()); err != nil {
		return fmt.Errorf("create correction resource stage: %w", translateError("exam_correction_resource_stage", input.StageID.String(), err))
	}
	data, err := model.EncodeAuditData(map[string]any{"exam_id": input.ExamID.String(), "exam_sitting_id": input.SittingID.String(),
		"base_revision_id": input.BaseRevisionID.String(), "target": string(input.Target)})
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt)
	return err
}

func (s SQLExamCorrectionStore) MarkResourceStageReady(ctx context.Context, input *store.ExamCorrectionResourceStageReadyInput) (*store.ExamCorrectionResourceStage, error) {
	if input == nil || !input.StageID.IsValid() || !input.ActorUserID.IsValid() || input.Rendition == nil || input.ReadyAt.IsZero() {
		return nil, store.NewErrInvalidInput("exam_correction", "resource_stage_ready", nil)
	}
	if err := input.Rendition.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("exam_correction", "resource_stage_rendition", nil).Wrap(err)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "mark correction resource stage ready", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamCorrectionResourceStage, error) {
		stage, err := getExamCorrectionStage(ctx, tx, input.StageID, true)
		if err != nil {
			return nil, err
		}
		if stage.CreatedByUserID != input.ActorUserID {
			return nil, store.NewErrNotFound("exam_correction_resource_stage", input.StageID.String())
		}
		if stage.FileRevisionID != input.Rendition.RevisionID || stage.RenditionID != input.Rendition.ID {
			return nil, store.NewErrConflict("exam_correction", "exam_correction_resource_stage", nil)
		}
		if stage.State == store.ExamCorrectionResourceStageReady || stage.State == store.ExamCorrectionResourceStageConsumed {
			if stage.Rendition == nil || !sameExamCorrectionRendition(stage.Rendition, input.Rendition) {
				return nil, store.NewErrConflict("exam_correction", "exam_correction_resource_stage", nil)
			}
			return stage, nil
		}
		var eligible bool
		if err = tx.Get(ctx, &eligible, `SELECT EXISTS (SELECT 1 FROM upload_leases l JOIN file_revisions v ON v.id=l.file_revision_id WHERE l.id=? AND l.file_revision_id=? AND l.consumed_at IS NULL AND statement_timestamp()<l.expires_at AND v.availability='pending' AND v.purge_claim_id IS NULL)`, stage.UploadLeaseID.String(), stage.FileRevisionID.String()); err != nil {
			return nil, err
		}
		if !eligible {
			return nil, store.NewErrConflict("exam_correction", "exam_correction_resource_stage", nil)
		}
		r := input.Rendition
		if _, err = tx.Exec(ctx, `INSERT INTO file_renditions (id,file_revision_id,created_at,name,media_type,size_bytes,width,height,sha256) VALUES (?,?,?,?,?,?,?,?,?)`,
			r.ID.String(), r.RevisionID.String(), r.CreatedAt, r.Name, r.MediaType, r.Size, r.Width, r.Height, r.SHA256); err != nil {
			return nil, fmt.Errorf("persist correction rendition: %w", translateError("file_rendition", r.ID.String(), err))
		}
		if _, err = tx.Exec(ctx, `UPDATE upload_leases SET updated_at=GREATEST(updated_at,statement_timestamp()),bytes_received=?,revision=revision+1 WHERE id=? AND consumed_at IS NULL`, r.Size, stage.UploadLeaseID.String()); err != nil {
			return nil, err
		}
		result, err := tx.Exec(ctx, `UPDATE exam_correction_resource_stages SET state='ready',ready_at=GREATEST(created_at,statement_timestamp()) WHERE id=? AND state='pending'`, input.StageID.String())
		if err != nil {
			return nil, err
		}
		if err = requireExamResourceAffected(result, 1, "exam_correction_resource_stage"); err != nil {
			return nil, err
		}
		return getExamCorrectionStage(ctx, tx, input.StageID, false)
	})
}

func sameExamCorrectionRendition(left, right *model.FileRendition) bool {
	return left != nil && right != nil && left.ID == right.ID && left.RevisionID == right.RevisionID &&
		left.CreatedAt.Truncate(time.Microsecond).Equal(right.CreatedAt.Truncate(time.Microsecond)) &&
		left.Name == right.Name && left.MediaType == right.MediaType && left.Size == right.Size &&
		left.Width == right.Width && left.Height == right.Height && left.SHA256 == right.SHA256
}

func getExamCorrectionStage(ctx context.Context, executor sqlxExecutor, id model.ExamCorrectionResourceStageID, forUpdate bool) (*store.ExamCorrectionResourceStage, error) {
	var row examCorrectionStageRow
	query := examCorrectionStageSelect + ` WHERE s.id=?`
	if forUpdate {
		query += ` FOR UPDATE OF s,l`
	}
	if err := executor.Get(ctx, &row, query, id.String()); err != nil {
		return nil, translateError("exam_correction_resource_stage", id.String(), err)
	}
	return row.value()
}

func (row examCorrectionStageRow) value() (*store.ExamCorrectionResourceStage, error) {
	id, err := model.ParseExamCorrectionResourceStageID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "id", err)
	}
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "sitting_id", err)
	}
	baseID, err := model.ParseExamRevisionID(row.BaseRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "base_revision_id", err)
	}
	resourceID, err := model.ParseExamResourceID(row.ResourceID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "resource_id", err)
	}
	entryID, err := model.ParseFileEntryID(row.FileEntryID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "file_entry_id", err)
	}
	fileRevisionID, err := model.ParseFileRevisionID(row.FileRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "file_revision_id", err)
	}
	leaseID, err := model.ParseUploadLeaseID(row.UploadLeaseID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "upload_lease_id", err)
	}
	renditionID, err := model.ParseFileRenditionID(row.RenditionID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "rendition_id", err)
	}
	creatorID, err := model.ParseUserID(row.CreatedByUserID)
	if err != nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "created_by_user_id", err)
	}
	leaseCreatorID, err := model.ParseUserID(row.LeaseCreatedBy)
	if err != nil || leaseCreatorID != creatorID {
		if err == nil {
			err = errors.New("stage and lease actor mismatch")
		}
		return nil, invalidPersistedState("exam_correction_resource_stage", "lease_created_by_user_id", err)
	}
	target, state := store.ExamCorrectionResourceStageTarget(row.Target), store.ExamCorrectionResourceStageState(row.State)
	if !target.IsValid() || state != store.ExamCorrectionResourceStagePending && state != store.ExamCorrectionResourceStageReady && state != store.ExamCorrectionResourceStageConsumed {
		return nil, invalidPersistedState("exam_correction_resource_stage", "state", errors.New("invalid target or state"))
	}
	value := &store.ExamCorrectionResourceStage{ID: id, ExamID: examID, SittingID: sittingID, BaseRevisionID: baseID, Target: target,
		ResourceID: resourceID, FileEntryID: entryID, FileRevisionID: fileRevisionID, UploadLeaseID: leaseID, RenditionID: renditionID,
		CreatedByUserID: creatorID, State: state, CreatedAt: model.TimeUTC(row.CreatedAt), ExpiresAt: model.TimeUTC(row.ExpiresAt)}
	if row.ReadyAt.Valid {
		value.ReadyAt = model.TimeUTC(row.ReadyAt.Time)
	}
	if row.ConsumedAt.Valid {
		value.ConsumedAt = model.TimeUTC(row.ConsumedAt.Time)
	}
	if row.RenditionAt.Valid {
		r, createErr := model.NewFileRendition(renditionID, fileRevisionID, row.RenditionName.String, row.MediaType.String,
			row.SizeBytes.Int64, int(row.Width.Int64), int(row.Height.Int64), row.SHA256.String, row.RenditionAt.Time)
		if createErr != nil {
			return nil, invalidPersistedState("exam_correction_resource_stage", "rendition", createErr)
		}
		value.Rendition = r
	}
	if state != store.ExamCorrectionResourceStagePending && value.Rendition == nil {
		return nil, invalidPersistedState("exam_correction_resource_stage", "rendition", errors.New("ready stage has no rendition"))
	}
	return value, nil
}

func (s SQLExamCorrectionStore) Apply(ctx context.Context, input *store.ExamCorrectionApplication, command *store.CommandIdempotency) (*store.ExamCorrectionResult, error) {
	if err := validateExamCorrectionApplication(input, command); err != nil {
		return nil, err
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "apply live Exam correction", idempotentMutation[*store.ExamCorrectionResult]{
		command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamCorrectionResult, error) {
			return applyExamCorrection(ctx, tx, input)
		},
		encode: func(value *store.ExamCorrectionResult) ([]byte, error) { return encodeCommandOutcome(value) },
		decode: func(version int, data []byte) (*store.ExamCorrectionResult, error) {
			if version != 1 {
				return nil, fmt.Errorf("unsupported Exam correction outcome version %d", version)
			}
			var value store.ExamCorrectionResult
			if err := decodeCommandOutcome(data, &value); err != nil {
				return nil, err
			}
			if err := validateExamCorrectionOutcome(&value); err != nil {
				return nil, err
			}
			return &value, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *store.ExamCorrectionResult, originalAuditID string) error {
			data, err := encodeExamCorrectionAudit(value, true, originalAuditID)
			if err != nil {
				return err
			}
			_, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt)
			return err
		},
	})
	if err != nil {
		return nil, err
	}
	result.Value.Replayed = result.Replayed
	return result.Value, nil
}

func validateExamCorrectionApplication(input *store.ExamCorrectionApplication, command *store.CommandIdempotency) error {
	if input == nil || command == nil || !input.RevisionID.IsValid() || !input.ExamID.IsValid() || !input.SittingID.IsValid() ||
		!input.CurrentRevisionID.IsValid() || input.ExpectedSittingRevision < 1 || !input.ActorUserID.IsValid() ||
		!validExamSittingPrivateReason(input.PrivateReason) || input.AppliedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("exam_correction", "application", nil)
	}
	if input.InstructionsMarkdown != nil && (!utf8.ValidString(*input.InstructionsMarkdown) || len(*input.InstructionsMarkdown) > 65536) {
		return store.NewErrInvalidInput("exam_correction", "instructions_markdown", nil)
	}
	if len(input.Resources) > model.ExamResourceMaximumCount {
		return store.NewErrConflict("exam_correction", "exam_correction_resource_limit", nil)
	}
	resourceIDs := make(map[model.ExamResourceID]struct{}, len(input.Resources))
	stageIDs := make(map[model.ExamCorrectionResourceStageID]struct{}, len(input.Resources))
	for position, item := range input.Resources {
		if !item.ResourceID.IsValid() || !utf8.ValidString(item.DisplayName) || !utf8.ValidString(item.DescriptionMarkdown) {
			return store.NewErrInvalidInput("exam_correction", "resource_manifest", nil)
		}
		probe, err := model.NewExamResource(item.ResourceID, model.NewExamID(), model.NewFileEntryID(), model.NewFileRevisionID(), item.DisplayName, item.DescriptionMarkdown, position, input.AppliedAt)
		if err != nil || probe.DisplayName != item.DisplayName || probe.DescriptionMarkdown != item.DescriptionMarkdown {
			return store.NewErrInvalidInput("exam_correction", "resource_manifest", nil)
		}
		if _, duplicate := resourceIDs[item.ResourceID]; duplicate {
			return store.NewErrConflict("exam_correction", "exam_correction_resource_manifest", nil)
		}
		resourceIDs[item.ResourceID] = struct{}{}
		if !item.StageID.IsZero() {
			if !item.StageID.IsValid() {
				return store.NewErrInvalidInput("exam_correction", "resource_stage_id", nil)
			}
			if _, duplicate := stageIDs[item.StageID]; duplicate {
				return store.NewErrConflict("exam_correction", "exam_correction_resource_manifest", nil)
			}
			stageIDs[item.StageID] = struct{}{}
		}
	}
	return nil
}

func applyExamCorrection(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamCorrectionApplication) (*store.ExamCorrectionResult, error) {
	if err := guardExamSittingManagerExam(ctx, tx, input.ExamID, input.ActorUserID, input.ManagerOverride, false); err != nil {
		return nil, err
	}
	var academicUnitIDRaw string
	if err := tx.Get(ctx, &academicUnitIDRaw, `SELECT academic_unit_id FROM exams WHERE id=?`, input.ExamID.String()); err != nil {
		return nil, translateError("exam", input.ExamID.String(), err)
	}
	academicUnitID, err := model.ParseAcademicUnitID(academicUnitIDRaw)
	if err != nil {
		return nil, invalidPersistedState("exam", "academic_unit_id", err)
	}
	sitting, err := lockExamSitting(ctx, tx, input.ExamID, input.SittingID)
	if err != nil {
		return nil, err
	}
	if sitting.Revision != input.ExpectedSittingRevision {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_revision", nil)
	}
	if sitting.State != model.ExamSittingOpen && sitting.State != model.ExamSittingPaused {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	if sitting.ExamRevisionID != input.CurrentRevisionID {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_revision_selection", nil)
	}
	var databaseNow time.Time
	if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
		return nil, err
	}
	databaseNow = model.TimeUTC(databaseNow)
	if !databaseNow.Before(sitting.ScheduledEndAt) {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_deadline_reached", nil)
	}
	var lockedBaseID string
	if err = tx.Get(ctx, &lockedBaseID, `SELECT id FROM exam_revisions WHERE exam_id=? AND id=? AND sealed=true FOR KEY SHARE`, input.ExamID.String(), input.CurrentRevisionID.String()); errors.Is(err, sql.ErrNoRows) {
		return nil, store.NewErrConflict("exam_correction", "exam_correction_base_revision", nil)
	} else if err != nil {
		return nil, err
	}
	base, err := getExamRevisionSnapshot(ctx, tx, input.ExamID, input.CurrentRevisionID)
	if err != nil {
		return nil, err
	}
	stageIDs := make([]model.ExamCorrectionResourceStageID, 0, len(input.Resources))
	for _, item := range input.Resources {
		if !item.StageID.IsZero() {
			stageIDs = append(stageIDs, item.StageID)
		}
	}
	slices.SortFunc(stageIDs, func(left, right model.ExamCorrectionResourceStageID) int {
		return strings.Compare(left.String(), right.String())
	})
	stages := make(map[model.ExamCorrectionResourceStageID]*store.ExamCorrectionResourceStage, len(stageIDs))
	for _, stageID := range stageIDs {
		stage, stageErr := getExamCorrectionStage(ctx, tx, stageID, true)
		if stageErr != nil {
			if store.IsNotFound(stageErr) {
				return nil, store.NewErrConflict("exam_correction", "exam_correction_resource_stage", nil)
			}
			return nil, stageErr
		}
		if stage.ExamID != input.ExamID || stage.SittingID != input.SittingID || stage.BaseRevisionID != input.CurrentRevisionID ||
			stage.CreatedByUserID != input.ActorUserID ||
			stage.State != store.ExamCorrectionResourceStageReady || stage.Rendition == nil || !databaseNow.Before(stage.ExpiresAt) {
			return nil, store.NewErrConflict("exam_correction", "exam_correction_resource_stage", nil)
		}
		stages[stageID] = stage
	}
	baseResources := make(map[model.ExamResourceID]model.ExamRevisionResource, len(base.Resources))
	for _, resource := range base.Resources {
		baseResources[resource.ResourceID] = resource
	}
	resources := make([]model.ExamRevisionResource, len(input.Resources))
	for position, item := range input.Resources {
		baseResource, inBase := baseResources[item.ResourceID]
		if item.StageID.IsZero() {
			if !inBase {
				return nil, store.NewErrConflict("exam_correction", "exam_correction_resource_manifest", nil)
			}
			baseResource.DisplayName, baseResource.DescriptionMarkdown, baseResource.Position = item.DisplayName, item.DescriptionMarkdown, position
			resources[position] = baseResource
			continue
		}
		stage := stages[item.StageID]
		if stage.ResourceID != item.ResourceID || stage.Target == store.ExamCorrectionResourceAddition && inBase ||
			stage.Target == store.ExamCorrectionResourceReplacement && (!inBase || stage.FileEntryID != baseResource.FileEntryID) {
			return nil, store.NewErrConflict("exam_correction", "exam_correction_resource_manifest", nil)
		}
		resources[position] = model.ExamRevisionResource{ResourceID: item.ResourceID, FileEntryID: stage.FileEntryID,
			FileRevisionID: stage.FileRevisionID, RenditionID: stage.RenditionID, DisplayName: item.DisplayName,
			DescriptionMarkdown: item.DescriptionMarkdown, Position: position, MediaType: model.ExamResourceMediaType(stage.Rendition.MediaType),
			SizeBytes: stage.Rendition.Size, SHA256: stage.Rendition.SHA256}
	}
	if examRevisionCapacityExceeded(base.Capacity, resources, nil) {
		return nil, store.NewErrConflict("exam_correction", "exam_correction_resource_limit", nil)
	}
	instructions := base.InstructionsMarkdown
	if input.InstructionsMarkdown != nil {
		instructions = *input.InstructionsMarkdown
	}
	var number int64
	if err = tx.Get(ctx, &number, `SELECT COALESCE(MAX(number),0)+1 FROM exam_revisions WHERE exam_id=?`, input.ExamID.String()); err != nil {
		return nil, err
	}
	revision, err := model.NewLiveCorrectionExamRevision(base, input.RevisionID, number, instructions, resources, input.ActorUserID, databaseNow)
	if err != nil {
		return nil, store.NewErrInvalidInput("exam_correction", "snapshot", nil).Wrap(err)
	}
	if model.SameExamRevisionCandidatePresentation(base, revision) {
		return nil, store.NewErrConflict("exam_correction", "exam_correction_no_changes", nil)
	}
	if err = insertExamRevision(ctx, tx, revision); err != nil {
		return nil, err
	}
	for _, stageID := range stageIDs {
		stage := stages[stageID]
		result, execErr := tx.Exec(ctx, `UPDATE file_revisions SET availability='available' WHERE id=? AND file_entry_id=? AND availability='pending' AND purge_claim_id IS NULL`, stage.FileRevisionID.String(), stage.FileEntryID.String())
		if execErr != nil {
			return nil, execErr
		}
		if err = requireExamResourceAffected(result, 1, "exam_correction_resource_stage"); err != nil {
			return nil, err
		}
		result, err = tx.Exec(ctx, `UPDATE upload_leases SET consumed_at=?,updated_at=GREATEST(updated_at,?),revision=revision+1,bytes_received=? WHERE id=? AND consumed_at IS NULL`, databaseNow, databaseNow, stage.Rendition.Size, stage.UploadLeaseID.String())
		if err != nil {
			return nil, err
		}
		if err = requireExamResourceAffected(result, 1, "exam_correction_resource_stage"); err != nil {
			return nil, err
		}
		result, err = tx.Exec(ctx, `UPDATE file_entries SET current_revision_id=?,updated_at=GREATEST(updated_at,?),revision=revision+1 WHERE id=?`, stage.FileRevisionID.String(), databaseNow, stage.FileEntryID.String())
		if err != nil {
			return nil, err
		}
		if err = requireExamResourceAffected(result, 1, "exam_correction_resource_stage"); err != nil {
			return nil, err
		}
		result, err = tx.Exec(ctx, `UPDATE exam_correction_resource_stages SET state='consumed',consumed_at=? WHERE id=? AND state='ready'`, databaseNow, stage.ID.String())
		if err != nil {
			return nil, err
		}
		if err = requireExamResourceAffected(result, 1, "exam_correction_resource_stage"); err != nil {
			return nil, err
		}
	}
	previousRevisionID := sitting.ExamRevisionID
	if err = sitting.RetargetRevision(revision.ID, databaseNow); err != nil {
		return nil, store.NewErrConflict("exam_sitting", "exam_sitting_state", err)
	}
	result, err := tx.Exec(ctx, `UPDATE exam_sittings SET exam_revision_id=?,updated_at=?,revision=? WHERE exam_id=? AND id=? AND revision=? AND exam_revision_id=? AND state IN ('open','paused')`,
		sitting.ExamRevisionID.String(), sitting.UpdatedAt, sitting.Revision, sitting.ExamID.String(), sitting.ID.String(), input.ExpectedSittingRevision, previousRevisionID.String())
	if err != nil {
		return nil, err
	}
	if err = requireExamResourceAffected(result, 1, "exam_sitting_revision"); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_sitting_live_corrections (audit_event_id,exam_id,exam_sitting_id,previous_revision_id,correction_revision_id,actor_user_id,private_reason,effective_at,sitting_revision) VALUES (?,?,?,?,?,?,?,?,?)`,
		input.AuditEventID, input.ExamID.String(), input.SittingID.String(), previousRevisionID.String(), revision.ID.String(), input.ActorUserID.String(), input.PrivateReason, databaseNow, sitting.Revision); err != nil {
		return nil, fmt.Errorf("record live correction provenance: %w", err)
	}
	value := &store.ExamCorrectionResult{Revision: examCorrectionRevisionSummary(revision),
		Sitting:            &store.ExamSittingSnapshot{Sitting: sitting, AcademicUnitID: academicUnitID},
		PreviousRevisionID: previousRevisionID, EffectiveAt: databaseNow}
	data, err := encodeExamCorrectionAudit(value, false, "")
	if err != nil {
		return nil, err
	}
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", data, input.AuditAt); err != nil {
		return nil, err
	}
	return value, nil
}

func examCorrectionRevisionSummary(revision *model.ExamRevision) *store.ExamRevisionSummary {
	var starterBytes int64
	for _, entry := range revision.StarterWorkspace {
		starterBytes += entry.SizeBytes
	}
	return &store.ExamRevisionSummary{ID: revision.ID, ExamID: revision.ExamID, Number: revision.Number,
		SourceDraftRevision: revision.SourceDraftRevision, Title: revision.Title, PolicySchemaVersion: revision.Policy.SchemaVersion,
		PolicyDigest: revision.PolicyDigest, ExecutionProfileDigest: revision.ExecutionProfileDigest, Capacity: revision.Capacity,
		StarterWorkspaceDigest: revision.StarterWorkspaceDigest, ContentDigest: revision.ContentDigest,
		ResourceCount: len(revision.Resources), StarterWorkspaceEntries: len(revision.StarterWorkspace), StarterWorkspaceBytes: starterBytes,
		PublishedByUserID: revision.PublishedByUserID, PublishedAt: revision.PublishedAt, BaseRevisionID: revision.BaseRevisionID, Kind: revision.Kind}
}

func validateExamCorrectionOutcome(value *store.ExamCorrectionResult) error {
	if value == nil || value.Revision == nil || value.Sitting == nil || value.Sitting.Sitting == nil ||
		!value.Revision.ID.IsValid() || !value.Revision.ExamID.IsValid() || value.Revision.Kind != model.ExamRevisionPublicationLiveCorrection ||
		value.Revision.Capacity.Validate() != nil ||
		!value.PreviousRevisionID.IsValid() || value.Sitting.Sitting.ExamRevisionID != value.Revision.ID ||
		value.Sitting.Sitting.ExamID != value.Revision.ExamID || !value.Sitting.AcademicUnitID.IsValid() ||
		value.EffectiveAt.IsZero() || !model.TimeUTC(value.EffectiveAt).Equal(model.TimeUTC(value.Revision.PublishedAt)) {
		return invalidPersistedState("exam_correction", "outcome", errors.New("invalid bounded outcome"))
	}
	if err := value.Sitting.Sitting.Validate(); err != nil {
		return invalidPersistedState("exam_correction", "outcome_sitting", err)
	}
	return nil
}

func encodeExamCorrectionAudit(value *store.ExamCorrectionResult, replayed bool, originalAuditID string) ([]byte, error) {
	data := map[string]any{"exam_id": value.Revision.ExamID.String(), "exam_sitting_id": value.Sitting.Sitting.ID.String(),
		"previous_revision_id": value.PreviousRevisionID.String(), "exam_revision_id": value.Revision.ID.String(),
		"number": value.Revision.Number, "sitting_revision": value.Sitting.Sitting.Revision}
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	return model.EncodeAuditData(data)
}

var _ store.ExamCorrectionStore = (*SQLExamCorrectionStore)(nil)
