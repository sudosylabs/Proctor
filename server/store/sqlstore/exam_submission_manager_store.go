// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func validManagerEndRequest(request store.ExamSubmissionManagerEndRequest) bool {
	return request.ExamID.IsValid() && request.SittingID.IsValid() && request.AttemptID.IsValid() &&
		request.ActorUserID.IsValid() && request.ExpectedAttemptRevision > 0 && utf8.ValidString(request.PrivateReason) &&
		request.PrivateReason == strings.TrimSpace(request.PrivateReason) && utf8.RuneCountInString(request.PrivateReason) >= 1 &&
		utf8.RuneCountInString(request.PrivateReason) <= 1000 && len(request.PrivateReason) <= 4000
}

func (s *SQLExamSubmissionStore) PrepareManagerEnd(ctx context.Context,
	request store.ExamSubmissionManagerEndRequest,
) (*store.ExamSubmissionManagerEndPreparation, error) {
	if !validManagerEndRequest(request) {
		return nil, store.NewErrInvalidInput("exam_submission", "manager_end", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "prepare manager-ended Exam Submission", func(ctx context.Context,
		tx *sqlxTxWrapper,
	) (*store.ExamSubmissionManagerEndPreparation, error) {
		if err := guardExamSittingManagerExam(ctx, tx, request.ExamID, request.ActorUserID, request.ManagerOverride, false); err != nil {
			return nil, err
		}
		row, err := lockAutomaticExamSubmission(ctx, tx, request.AttemptID)
		if err != nil {
			return nil, err
		}
		target, err := automaticExamSubmissionTarget(row.automaticExamSubmissionTargetRow)
		if err != nil {
			return nil, err
		}
		if target.ExamID != request.ExamID || target.SittingID != request.SittingID {
			return nil, store.NewErrNotFound("exam_attempt", request.AttemptID.String())
		}
		if target.CandidateUserID == request.ActorUserID {
			return nil, store.NewErrNotFound("exam_attempt", request.AttemptID.String())
		}
		var decisionAt time.Time
		if err = tx.Get(ctx, &decisionAt, `SELECT statement_timestamp()`); err != nil {
			return nil, fmt.Errorf("read manager-ended Exam Submission time: %w", err)
		}
		sealAt := model.TimeFromMillis(model.MillisFromTime(decisionAt))
		if row.AttemptState == string(model.ExamAttemptSubmitted) {
			if row.AttemptRevision != request.ExpectedAttemptRevision+1 {
				return nil, store.NewErrConflict("exam_attempt", "exam_attempt_revision", nil)
			}
			var provenance string
			if err = tx.Get(ctx, &provenance, `SELECT provenance FROM exam_submissions
				WHERE exam_attempt_id=? AND sealed=true FOR UPDATE`, request.AttemptID.String()); err != nil {
				return nil, translateError("exam_submission", request.AttemptID.String(), err)
			}
			if provenance != string(model.ExamSubmissionManagerEndedAttempt) {
				return nil, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
			}
			return &store.ExamSubmissionManagerEndPreparation{Target: target,
				ExpectedAttemptRevision: request.ExpectedAttemptRevision, Replayed: true, SealAt: sealAt}, nil
		}
		if row.AttemptRevision != request.ExpectedAttemptRevision {
			return nil, store.NewErrConflict("exam_attempt", "exam_attempt_revision", nil)
		}
		if !model.ExamAttemptState(row.AttemptState).IsUnresolved() ||
			(row.SittingState != string(model.ExamSittingOpen) && row.SittingState != string(model.ExamSittingPaused)) ||
			!sealAt.Before(row.ScheduledEndAt) {
			return nil, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
		}
		return &store.ExamSubmissionManagerEndPreparation{Target: target,
			ExpectedAttemptRevision: request.ExpectedAttemptRevision, SealAt: sealAt}, nil
	})
}

func (s *SQLExamSubmissionStore) EndByManager(ctx context.Context, input *store.ExamSubmissionManagerEnd,
	command *store.CommandIdempotency,
) (*store.ExamSubmissionManagerEndResult, error) {
	if input == nil || !validManagerEndRequest(input.Request) || !validAutomaticExamSubmissionTarget(input.Target) ||
		input.Target.ExamID != input.Request.ExamID || input.Target.SittingID != input.Request.SittingID ||
		input.Target.AttemptID != input.Request.AttemptID || !input.SubmissionID.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || command == nil ||
		command.Operation != store.ExamSubmissionManagerEndOperation || command.OutcomeVersion != 1 ||
		command.UserID != input.Request.ActorUserID {
		return nil, store.NewErrInvalidInput("exam_submission", "manager_end", nil)
	}
	prepared := *input
	result, err := runIdempotentMutation(ctx, s.SQLStore, "manager-end Exam Attempt", idempotentMutation[examSubmissionSealOutcomeV1]{
		command: command, auditEventID: prepared.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (examSubmissionSealOutcomeV1, error) {
			return endExamAttemptByManager(ctx, tx, &prepared)
		},
		encode: func(outcome examSubmissionSealOutcomeV1) ([]byte, error) { return encodeCommandOutcome(outcome) },
		decode: func(version int, data []byte) (examSubmissionSealOutcomeV1, error) {
			var outcome examSubmissionSealOutcomeV1
			if version != 1 {
				return outcome, fmt.Errorf("unsupported manager-ended Exam Submission outcome version %d", version)
			}
			if err := decodeCommandOutcome(data, &outcome); err != nil {
				return outcome, err
			}
			if _, err := examSubmissionSealResult(outcome); err != nil {
				return outcome, err
			}
			return outcome, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, outcome examSubmissionSealOutcomeV1, originalAuditID string) error {
			if err := guardExamSittingManagerExam(ctx, tx, prepared.Request.ExamID, prepared.Request.ActorUserID,
				prepared.Request.ManagerOverride, false); err != nil {
				return err
			}
			row, err := lockAutomaticExamSubmission(ctx, tx, prepared.Request.AttemptID)
			if err != nil {
				return err
			}
			target, err := automaticExamSubmissionTarget(row.automaticExamSubmissionTargetRow)
			if err != nil {
				return err
			}
			if target.CandidateUserID == prepared.Request.ActorUserID {
				return store.NewErrNotFound("exam_attempt", prepared.Request.AttemptID.String())
			}
			if row.AttemptState != string(model.ExamAttemptSubmitted) ||
				row.AttemptRevision != prepared.Request.ExpectedAttemptRevision+1 || outcome.ExamID != prepared.Request.ExamID.String() ||
				outcome.SittingID != prepared.Request.SittingID.String() || outcome.Receipt.AttemptID != prepared.Request.AttemptID {
				return store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
			}
			var provenance string
			if err = tx.Get(ctx, &provenance, `SELECT provenance FROM exam_submissions WHERE id=? AND sealed=true FOR UPDATE`,
				outcome.Receipt.SubmissionID.String()); err != nil {
				return translateError("exam_submission", outcome.Receipt.SubmissionID.String(), err)
			}
			if provenance != string(model.ExamSubmissionManagerEndedAttempt) {
				return store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
			}
			return completeExamSubmissionAudit(ctx, tx, outcome, prepared.AuditEventID, prepared.AuditAt, true, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	response, err := examSubmissionSealResult(result.Value)
	if err != nil {
		return nil, err
	}
	response.Replayed = result.Replayed
	return &store.ExamSubmissionManagerEndResult{ExamSubmissionSealResult: *response,
		ConnectionClosed: !result.Replayed && result.Value.ConnectionClosed}, nil
}

func endExamAttemptByManager(ctx context.Context, tx *sqlxTxWrapper,
	input *store.ExamSubmissionManagerEnd,
) (examSubmissionSealOutcomeV1, error) {
	var zero examSubmissionSealOutcomeV1
	recipient, err := lockMailRecipientUser(ctx, tx, input.Target.CandidateUserID)
	if err != nil {
		return zero, err
	}
	if err = guardExamSittingManagerExam(ctx, tx, input.Request.ExamID, input.Request.ActorUserID,
		input.Request.ManagerOverride, false); err != nil {
		return zero, err
	}
	row, err := lockAutomaticExamSubmission(ctx, tx, input.Request.AttemptID)
	if err != nil {
		return zero, err
	}
	target, err := automaticExamSubmissionTarget(row.automaticExamSubmissionTargetRow)
	if err != nil {
		return zero, err
	}
	if target.CandidateUserID == input.Request.ActorUserID {
		return zero, store.NewErrNotFound("exam_attempt", input.Request.AttemptID.String())
	}
	if !sameManagerEndTargetIdentity(target, input.Target) || row.AttemptRevision != input.Request.ExpectedAttemptRevision {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_revision", nil)
	}
	if !model.ExamAttemptState(row.AttemptState).IsUnresolved() ||
		(row.SittingState != string(model.ExamSittingOpen) && row.SittingState != string(model.ExamSittingPaused)) {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	sealAt := model.TimeFromMillis(input.AuditAt)
	var databaseNow time.Time
	if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
		return zero, fmt.Errorf("read manager-ended Exam Submission decision time: %w", err)
	}
	databaseNow = model.TimeUTC(databaseNow)
	if sealAt.After(databaseNow) || !databaseNow.Before(row.ScheduledEndAt) {
		return zero, store.NewErrConflict("exam_sitting", "exam_sitting_deadline_reached", nil)
	}
	if input.ExpectedRecipientRevision < 1 || recipient.Revision != input.ExpectedRecipientRevision {
		return zero, store.NewErrConflict("exam_submission", "receipt_recipient_changed", nil)
	}
	payloadKeyID, err := validateExamSubmissionReceiptMail(input.Notice, recipient, input.SubmissionID, input.AuditAt,
		model.MailTemplateExamSubmissionManagerEnded)
	if err != nil {
		return zero, err
	}
	if payloadKeyID != "" {
		if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return zero, err
		}
	}
	integrity, err := lockAutomaticExamSubmissionIntegrity(ctx, tx, target.AttemptID)
	if err != nil {
		return zero, err
	}
	unresolved := integrity.TotalUnresolved
	policy, err := model.DecodeExamPolicySet(row.PolicyCanonical)
	if err != nil {
		return zero, invalidPersistedState("exam_submission", "current_policy", err)
	}
	if policy.FocusLoss.Enabled {
		if unresolved == math.MaxInt64 {
			return zero, invalidPersistedState("exam_submission", "integrity_gaps", errors.New("unresolved gap count overflows"))
		}
		unresolved++
	}
	focusUnresolved := unresolved
	missingCorrections, err := listAutomaticPendingCorrectionAcknowledgements(ctx, tx, row)
	if err != nil {
		return zero, err
	}
	pendingAcknowledgements := int64(len(missingCorrections))
	if pendingAcknowledgements > math.MaxInt64-unresolved {
		return zero, invalidPersistedState("exam_submission", "integrity_gaps", errors.New("unresolved gap count overflows"))
	}
	unresolved += pendingAcknowledgements
	browserUnresolved, browserActivity, err := settleAutomaticBrowserActivity(ctx, tx, target.AttemptID, sealAt)
	if err != nil {
		return zero, err
	}
	if browserUnresolved > math.MaxInt64-unresolved {
		return zero, invalidPersistedState("exam_submission", "integrity_gaps", errors.New("unresolved gap count overflows"))
	}
	unresolved += browserUnresolved
	manifestRows, entries, err := loadAuthoritativeExamSubmissionManifest(ctx, tx, target.WorkspaceID.String())
	if err != nil {
		return zero, err
	}
	manifest, err := model.NewExamSubmissionManifest(row.WorkspaceCursor, entries)
	if err != nil {
		return zero, invalidPersistedState("exam_submission_manifest", "value", err)
	}
	attempt, participation, connection, err := row.domain(target)
	if err != nil {
		return zero, err
	}
	beforeParticipation, beforeConnection := participation.State, connection.State
	if err = model.EndExamAttemptByManager(attempt, participation, connection, sealAt); err != nil {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_state", err)
	}
	submission, err := model.NewExamSubmission(model.ExamSubmissionSpecification{ID: input.SubmissionID,
		AttemptID: target.AttemptID, ExamRevisionID: target.CurrentRevisionID, WorkspaceID: target.WorkspaceID,
		Manifest: manifest, FinalFocusLossSequence: integrity.LatestAcceptedSequence, BrowserActivity: browserActivity,
		UnresolvedIntegrityCount: unresolved, Provenance: model.ExamSubmissionManagerEndedAttempt, SubmittedAt: sealAt})
	if err != nil {
		return zero, store.NewErrInvalidInput("exam_submission", "value", nil).Wrap(err)
	}
	if err = insertAutomaticExamSubmission(ctx, tx, submission, target, manifestRows, entries); err != nil {
		return zero, err
	}
	focusReason := model.IntegrityDiscrepancyFocusLossSequenceGap
	if policy.FocusLoss.Enabled {
		focusReason = model.IntegrityDiscrepancyFocusLossSourceNotFinalized
		if integrity.TotalUnresolved > 0 {
			focusReason = model.IntegrityDiscrepancyFocusLossSequenceGapAndSourceNotFinalized
		}
	}
	if err = insertTerminalIntegrityDiscrepancies(ctx, tx, submission, automaticIntegrityDiscrepancyTarget(target),
		terminalIntegrityDiscrepancies{FocusUnresolved: focusUnresolved, FocusReason: focusReason,
			BrowserUnresolved: browserUnresolved, BrowserActivity: browserActivity,
			MissingCorrections: missingCorrections}); err != nil {
		return zero, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM exam_attempt_focus_loss_pending WHERE exam_attempt_id=?`, target.AttemptID.String()); err != nil {
		return zero, fmt.Errorf("clear manager-ended Exam Submission pending Focus Loss: %w", err)
	}
	if err = persistManagerEndedExamAttempt(ctx, tx, attempt, participation, connection, beforeParticipation, beforeConnection); err != nil {
		return zero, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_manager_end_actions
		(submission_id,exam_attempt_id,actor_user_id,private_reason,audit_event_id,ended_at) VALUES (?,?,?,?,?,?)`,
		submission.ID.String(), submission.AttemptID.String(), input.Request.ActorUserID.String(), input.Request.PrivateReason,
		input.AuditEventID, submission.SubmittedAt); err != nil {
		return zero, fmt.Errorf("record manager-ended Exam Attempt action: %w", err)
	}
	outcome := automaticExamSubmissionOutcome(submission, target)
	outcome.ConnectionClosed = beforeConnection == model.AttemptConnectionOpen
	if err = completeExamSubmissionAudit(ctx, tx, outcome, input.AuditEventID, input.AuditAt, false, ""); err != nil {
		return zero, err
	}
	if err = insertExamSubmissionReceiptMail(ctx, tx, input.Notice, payloadKeyID); err != nil {
		return zero, err
	}
	return outcome, nil
}

func sameManagerEndTargetIdentity(left, right store.ExamSubmissionAutomaticSealTarget) bool {
	return left.ExamID == right.ExamID && left.SittingID == right.SittingID && left.ClassID == right.ClassID &&
		left.AcademicUnitID == right.AcademicUnitID && left.CandidateUserID == right.CandidateUserID &&
		left.AttemptID == right.AttemptID && left.WorkspaceID == right.WorkspaceID &&
		left.ParticipationID == right.ParticipationID && left.Generation == right.Generation && left.ConnectionID == right.ConnectionID
}

func persistManagerEndedExamAttempt(ctx context.Context, tx *sqlxTxWrapper, attempt *model.ExamAttempt,
	participation *model.AttemptParticipation, connection *model.AttemptConnection,
	beforeParticipation model.AttemptParticipationState, beforeConnection model.AttemptConnectionState,
) error {
	result, err := tx.Exec(ctx, `UPDATE exam_attempts SET state=?,updated_at=?,submitted_at=?,revision=?
		WHERE id=? AND state IN ('active','ready','suspended') AND revision=?`, string(attempt.State), attempt.UpdatedAt,
		attempt.SubmittedAt.Time, attempt.Revision, attempt.ID.String(), attempt.Revision-1)
	if err != nil {
		return fmt.Errorf("manager-end Exam Attempt: %w", err)
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		return store.NewErrConflict("exam_attempt", "exam_attempt_state", rowsErr)
	}
	if beforeParticipation == model.AttemptParticipationActive {
		result, err = tx.Exec(ctx, `UPDATE exam_attempt_participations SET state=?,updated_at=?,ended_at=?,end_reason=?
			WHERE id=? AND exam_attempt_id=? AND generation=? AND state='active'`, string(participation.State), participation.UpdatedAt,
			participation.EndedAt.Time, string(participation.EndReason), participation.ID.String(), participation.AttemptID.String(), participation.Generation)
		if err != nil {
			return fmt.Errorf("end manager-ended Attempt Participation: %w", err)
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return store.NewErrConflict("attempt_participation", "attempt_participation_expired", rowsErr)
		}
	}
	if beforeConnection == model.AttemptConnectionOpen {
		result, err = tx.Exec(ctx, `UPDATE exam_attempt_connections SET state=?,closed_at=?,close_reason=?
			WHERE id=? AND exam_attempt_id=? AND participation_id=? AND state='open'`, string(connection.State),
			connection.ClosedAt.Time, string(connection.CloseReason), connection.ID.String(), connection.AttemptID.String(),
			connection.ParticipationID.String())
		if err != nil {
			return fmt.Errorf("close manager-ended Attempt Connection: %w", err)
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
			return store.NewErrConflict("attempt_connection", "attempt_connection_closed", rowsErr)
		}
	}
	return nil
}
