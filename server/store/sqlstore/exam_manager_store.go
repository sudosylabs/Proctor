// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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

type examManagerRow struct {
	ExamID          string    `db:"exam_id"`
	UserID          string    `db:"user_id"`
	GrantedByUserID string    `db:"granted_by_user_id"`
	GrantedAt       time.Time `db:"granted_at"`
	IsCreator       bool      `db:"is_creator"`
	IsOwner         bool      `db:"is_owner"`
}

type examManagerOutcome struct {
	Exam    model.Exam        `json:"exam"`
	Manager model.ExamManager `json:"manager"`
}

func (s SQLExamAuthoringStore) ListManagers(ctx context.Context, options store.ExamManagerListOptions) ([]store.ExamManagerSummary, error) {
	if !options.ExamID.IsValid() || options.Limit < 1 || options.Limit > 200 ||
		(options.BeforeGrantedAt.IsZero() != options.BeforeUserID.IsZero()) {
		return nil, store.NewErrInvalidInput("exam_manager", "list_options", nil)
	}
	query := `SELECT m.exam_id, m.user_id, m.granted_by_user_id, m.granted_at,
		(m.user_id = e.creator_user_id) AS is_creator, (m.user_id = e.owner_user_id) AS is_owner
		FROM exam_managers m JOIN exams e ON e.id = m.exam_id WHERE m.exam_id = ?`
	args := []any{options.ExamID.String()}
	if !options.BeforeGrantedAt.IsZero() {
		query += ` AND (m.granted_at, m.user_id) < (?, ?)`
		args = append(args, model.TimeUTC(options.BeforeGrantedAt), options.BeforeUserID.String())
	}
	query += ` ORDER BY m.granted_at DESC, m.user_id DESC LIMIT ?`
	args = append(args, options.Limit)
	var rows []examManagerRow
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Exam Managers: %w", err)
	}
	items := make([]store.ExamManagerSummary, 0, len(rows))
	for _, row := range rows {
		manager, err := row.manager()
		if err != nil {
			return nil, err
		}
		items = append(items, store.ExamManagerSummary{Manager: *manager, IsCreator: row.IsCreator, IsOwner: row.IsOwner})
	}
	return items, nil
}

func (s SQLExamAuthoringStore) AddManager(ctx context.Context, input *store.ExamManagerMutation, command *store.CommandIdempotency) (*store.ExamManagerCommandResult, error) {
	return s.runManagerMutation(ctx, "Exam Manager addition", input, command, addExamManager)
}

func (s SQLExamAuthoringStore) RemoveManager(ctx context.Context, input *store.ExamManagerMutation, command *store.CommandIdempotency) (*store.ExamManagerCommandResult, error) {
	return s.runManagerMutation(ctx, "Exam Manager removal", input, command, removeExamManager)
}

func (s SQLExamAuthoringStore) TransferOwner(ctx context.Context, input *store.ExamManagerMutation, command *store.CommandIdempotency) (*store.ExamManagerCommandResult, error) {
	return s.runManagerMutation(ctx, "Exam ownership transfer", input, command, transferExamOwner)
}

type executeManagerMutation func(context.Context, *sqlxTxWrapper, *store.ExamManagerMutation) (*store.ExamManagerCommandResult, error)

func (s SQLExamAuthoringStore) runManagerMutation(ctx context.Context, label string, input *store.ExamManagerMutation, command *store.CommandIdempotency, execute executeManagerMutation) (*store.ExamManagerCommandResult, error) {
	prepared, err := prepareExamManagerMutation(input)
	if err != nil {
		return nil, err
	}
	if command == nil {
		return nil, store.NewErrInvalidInput("exam_manager", "idempotency", nil)
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, label, idempotentMutation[*store.ExamManagerCommandResult]{
		command: command, auditEventID: prepared.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamManagerCommandResult, error) {
			return execute(ctx, tx, prepared)
		},
		encode: func(result *store.ExamManagerCommandResult) ([]byte, error) {
			return encodeCommandOutcome(examManagerOutcome{Exam: *result.Exam, Manager: *result.Manager})
		},
		decode: func(version int, data []byte) (*store.ExamManagerCommandResult, error) {
			if version != 1 {
				return nil, fmt.Errorf("unsupported Exam Manager outcome version %d", version)
			}
			var outcome examManagerOutcome
			if err := decodeCommandOutcome(data, &outcome); err != nil {
				return nil, err
			}
			if err := outcome.Exam.Validate(); err != nil {
				return nil, fmt.Errorf("decode Exam Manager outcome Exam: %w", err)
			}
			if err := outcome.Manager.Validate(); err != nil {
				return nil, fmt.Errorf("decode Exam Manager outcome relationship: %w", err)
			}
			return &store.ExamManagerCommandResult{Exam: &outcome.Exam, Manager: &outcome.Manager}, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, result *store.ExamManagerCommandResult, originalAuditID string) error {
			encoded, err := model.EncodeAuditData(map[string]any{
				"exam_id": result.Exam.ID.String(), "user_id": result.Manager.UserID.String(), "exam_revision": result.Exam.Revision,
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

func prepareExamManagerMutation(input *store.ExamManagerMutation) (*store.ExamManagerMutation, error) {
	if input == nil || !input.ExamID.IsValid() || !input.ActorUserID.IsValid() || !input.TargetUserID.IsValid() ||
		input.ExpectedRevision < 1 || input.ChangedAt <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("exam_manager", "mutation", nil)
	}
	prepared := *input
	return &prepared, nil
}

func addExamManager(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamManagerMutation) (*store.ExamManagerCommandResult, error) {
	if err := lockExamManagerCandidateFence(ctx, tx, input.TargetUserID); err != nil {
		return nil, err
	}
	exam, actorIsManager, err := lockExamForManagerMutation(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	if err := guardExamManagerMutation(ctx, tx, exam, actorIsManager, input, true); err != nil {
		return nil, err
	}
	manager, err := model.NewExamManager(input.ExamID, input.TargetUserID, input.ActorUserID, model.TimeFromMillis(input.ChangedAt))
	if err != nil {
		return nil, store.NewErrInvalidInput("exam_manager", "value", nil).Wrap(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exam_managers (exam_id, user_id, granted_by_user_id, granted_at) VALUES (?, ?, ?, ?)`,
		manager.ExamID.String(), manager.UserID.String(), manager.GrantedByUserID.String(), manager.GrantedAt); err != nil {
		translated := translateError("exam_manager", manager.UserID.String(), err)
		var conflict *store.ErrConflict
		if errors.As(translated, &conflict) && conflict.Constraint == "exam_managers_pkey" {
			return nil, store.NewErrConflict("exam_manager", "exam_manager_exists", nil)
		}
		return nil, fmt.Errorf("add Exam Manager: %w", translated)
	}
	if err := insertExamManagerMail(ctx, tx, input.Notices, []examManagerMailExpectation{{userID: input.TargetUserID, key: model.MailTemplateExamManagerAdded}}, input.ChangedAt); err != nil {
		return nil, err
	}
	return completeExamManagerMutation(ctx, tx, exam, manager, input)
}

func removeExamManager(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamManagerMutation) (*store.ExamManagerCommandResult, error) {
	exam, actorIsManager, err := lockExamForManagerMutation(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	if err := guardExamManagerMutation(ctx, tx, exam, actorIsManager, input, false); err != nil {
		return nil, err
	}
	manager, err := getExamManager(ctx, tx, input.ExamID, input.TargetUserID)
	if err != nil {
		return nil, err
	}
	if exam.OwnerUserID == input.TargetUserID {
		return nil, store.NewErrConflict("exam_manager", "exam_owner_manager", nil)
	}
	result, err := tx.Exec(ctx, `DELETE FROM exam_managers WHERE exam_id = ? AND user_id = ?`, input.ExamID.String(), input.TargetUserID.String())
	if err != nil {
		return nil, fmt.Errorf("remove Exam Manager: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, fmt.Errorf("inspect Exam Manager removal: %w", err)
		}
		return nil, store.NewErrConflict("exam_manager", "exam_manager_missing", nil)
	}
	if err := insertExamManagerMail(ctx, tx, input.Notices, []examManagerMailExpectation{{userID: input.TargetUserID, key: model.MailTemplateExamManagerRemoved}}, input.ChangedAt); err != nil {
		return nil, err
	}
	return completeExamManagerMutation(ctx, tx, exam, manager, input)
}

func transferExamOwner(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamManagerMutation) (*store.ExamManagerCommandResult, error) {
	if err := lockExamManagerCandidateFence(ctx, tx, input.TargetUserID); err != nil {
		return nil, err
	}
	exam, actorIsManager, err := lockExamForManagerMutation(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	if err := guardExamManagerMutation(ctx, tx, exam, actorIsManager, input, true); err != nil {
		return nil, err
	}
	if exam.OwnerUserID == input.TargetUserID {
		return nil, store.NewErrConflict("exam", "exam_owner_no_changes", nil)
	}
	manager, err := getExamManager(ctx, tx, input.ExamID, input.TargetUserID)
	if err != nil {
		return nil, err
	}
	previousOwnerID := exam.OwnerUserID
	exam.OwnerUserID = input.TargetUserID
	if err := insertExamManagerMail(ctx, tx, input.Notices, []examManagerMailExpectation{
		{userID: previousOwnerID, key: model.MailTemplateExamOwnershipTransferredFromYou},
		{userID: input.TargetUserID, key: model.MailTemplateExamOwnershipTransferredToYou},
	}, input.ChangedAt); err != nil {
		return nil, err
	}
	return completeExamManagerMutation(ctx, tx, exam, manager, input)
}

type examManagerMailExpectation struct {
	userID model.UserID
	key    model.MailTemplateKey
}

func insertExamManagerMail(ctx context.Context, tx *sqlxTxWrapper, notices []store.ExamManagerMail, expected []examManagerMailExpectation, at int64) error {
	if len(notices) != len(expected) {
		return store.NewErrInvalidInput("exam_manager", "mail_count", nil)
	}
	when := model.TimeFromMillis(at)
	seenUsers := make(map[model.UserID]struct{}, len(expected))
	for index, expectation := range expected {
		notice := notices[index]
		if _, exists := seenUsers[expectation.userID]; exists || notice.Occurrence == nil || notice.Delivery == nil || notice.Job == nil ||
			notice.Occurrence.Kind != model.MailOccurrenceExamManagement || notice.Occurrence.TemplateKey != expectation.key ||
			notice.Occurrence.ActorUserID != expectation.userID || notice.Delivery.TargetUserID != expectation.userID ||
			notice.Delivery.TemplateKey != expectation.key || notice.Job.Type != model.JobTypeMailDeliver ||
			!notice.Occurrence.CreatedAt.Equal(when) || notice.Delivery.Deadline.Sub(notice.Delivery.CreatedAt) != 72*time.Hour {
			return store.NewErrInvalidInput("exam_manager", "mail", nil)
		}
		seenUsers[expectation.userID] = struct{}{}
		if err := validateRecoveryMail(notice.Occurrence, notice.Delivery, notice.Job); err != nil {
			return err
		}
		payloadKeyID, err := mailPayloadKeyID(notice.Delivery.EncryptedPayload)
		if err != nil {
			return store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
		}
		if payloadKeyID != "" {
			if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
				return err
			}
		}
		if err = insertRecoveryMail(ctx, tx, notice.Occurrence, notice.Delivery, notice.Job, payloadKeyID); err != nil {
			return fmt.Errorf("insert Exam Manager mail: %w", err)
		}
	}
	return nil
}

func lockExamForManagerMutation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamManagerMutation) (*model.Exam, bool, error) {
	var row examAccessRow
	if err := tx.Get(ctx, &row, examAccessSelect+` FOR UPDATE OF e`, input.ActorUserID.String(), input.ExamID.String()); err != nil {
		return nil, false, translateError("exam", input.ExamID.String(), err)
	}
	exam, err := row.model()
	return exam, row.ActorIsManager, err
}

func guardExamManagerMutation(ctx context.Context, tx *sqlxTxWrapper, exam *model.Exam, actorIsManager bool, input *store.ExamManagerMutation, requireEligibility bool) error {
	if !actorIsManager && !input.ManagerOverride {
		return store.NewErrNotFound("exam_manager", input.ActorUserID.String())
	}
	if exam.IsArchived() {
		return store.NewErrConflict("exam", "exam_archived", nil)
	}
	if exam.Revision != input.ExpectedRevision {
		return store.NewErrConflict("exam", "exam_revision", nil)
	}
	if requireEligibility {
		var unresolved bool
		if err := tx.Get(ctx, &unresolved, `SELECT EXISTS(SELECT 1 FROM exam_attempts
			WHERE exam_id=? AND candidate_user_id=? AND state IN ('ready','active','suspended'))`,
			exam.ID.String(), input.TargetUserID.String()); err != nil {
			return fmt.Errorf("inspect target User unresolved Exam Attempt: %w", err)
		}
		if unresolved {
			return store.NewErrConflict("exam_manager", "exam_manager_candidate_conflict", nil)
		}
		var eligible int
		if err := tx.Get(ctx, &eligible, `SELECT 1 FROM users u JOIN academic_unit_members m ON m.user_id = u.id
			WHERE u.id = ? AND u.archived_at IS NULL AND u.disabled_at IS NULL
				AND m.academic_unit_id = ? AND m.archived_at IS NULL AND m.start_at <= statement_timestamp()
				AND (m.end_at IS NULL OR m.end_at > statement_timestamp())
			LIMIT 1 FOR SHARE OF u, m`, input.TargetUserID.String(), exam.AcademicUnitID.String()); err != nil {
			if err == sql.ErrNoRows {
				return store.NewErrConflict("exam_manager", "exam_manager_ineligible", nil)
			}
			return fmt.Errorf("recheck Exam Manager eligibility: %w", err)
		}
	}
	return nil
}

func lockExamManagerCandidateFence(ctx context.Context, tx *sqlxTxWrapper, userID model.UserID) error {
	lockKey := "proctor:exam-attempt-admission:" + userID.String()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`, lockKey); err != nil {
		return fmt.Errorf("lock Exam Manager candidate fence: %w", err)
	}
	return nil
}

func getExamManager(ctx context.Context, tx *sqlxTxWrapper, examID model.ExamID, userID model.UserID) (*model.ExamManager, error) {
	var row examManagerRow
	if err := tx.Get(ctx, &row, `SELECT exam_id, user_id, granted_by_user_id, granted_at, false AS is_creator, false AS is_owner
		FROM exam_managers WHERE exam_id = ? AND user_id = ?`, examID.String(), userID.String()); err != nil {
		if err == sql.ErrNoRows {
			return nil, store.NewErrConflict("exam_manager", "exam_manager_missing", nil)
		}
		return nil, fmt.Errorf("get Exam Manager: %w", err)
	}
	return row.manager()
}

func completeExamManagerMutation(ctx context.Context, tx *sqlxTxWrapper, exam *model.Exam, manager *model.ExamManager, input *store.ExamManagerMutation) (*store.ExamManagerCommandResult, error) {
	exam.UpdatedAt = model.TimeFromMillis(input.ChangedAt)
	exam.Revision++
	result, err := tx.Exec(ctx, `UPDATE exams SET owner_user_id = ?, updated_at = ?, revision = ? WHERE id = ? AND revision = ?`,
		exam.OwnerUserID.String(), exam.UpdatedAt, exam.Revision, exam.ID.String(), input.ExpectedRevision)
	if err != nil {
		return nil, fmt.Errorf("update Exam management revision: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, fmt.Errorf("inspect Exam management revision: %w", err)
		}
		return nil, store.NewErrConflict("exam", "exam_revision", nil)
	}
	encoded, err := model.EncodeAuditData(map[string]any{
		"exam_id": exam.ID.String(), "user_id": manager.UserID.String(), "exam_revision": exam.Revision,
	})
	if err != nil {
		return nil, err
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete Exam management audit: %w", err)
	}
	return &store.ExamManagerCommandResult{Exam: exam, Manager: manager}, nil
}

func (r examManagerRow) manager() (*model.ExamManager, error) {
	examID, err := model.ParseExamID(r.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_manager", "exam_id", err)
	}
	userID, err := model.ParseUserID(r.UserID)
	if err != nil {
		return nil, invalidPersistedState("exam_manager", "user_id", err)
	}
	grantedBy, err := model.ParseUserID(r.GrantedByUserID)
	if err != nil {
		return nil, invalidPersistedState("exam_manager", "granted_by_user_id", err)
	}
	manager := &model.ExamManager{ExamID: examID, UserID: userID, GrantedByUserID: grantedBy, GrantedAt: model.TimeUTC(r.GrantedAt)}
	if err := manager.Validate(); err != nil {
		return nil, invalidPersistedState("exam_manager", "value", err)
	}
	return manager, nil
}
