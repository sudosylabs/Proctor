// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type endedFocusLossAccessRow struct {
	ExamID                 string         `db:"exam_id"`
	SittingID              string         `db:"sitting_id"`
	ClassID                string         `db:"class_id"`
	CandidateID            string         `db:"candidate_user_id"`
	AttemptID              string         `db:"attempt_id"`
	AttemptState           string         `db:"attempt_state"`
	SubmissionID           string         `db:"submission_id"`
	FinalSequence          int64          `db:"final_focus_loss_sequence"`
	ParticipationID        string         `db:"participation_id"`
	ParticipationState     string         `db:"participation_state"`
	ParticipationEndReason sql.NullString `db:"participation_end_reason"`
	Generation             int64          `db:"generation"`
	CredentialHash         string         `db:"continuity_credential_hash"`
	ConnectionID           string         `db:"connection_id"`
	ConnectionSessionID    string         `db:"connection_session_id"`
	ConnectionState        string         `db:"connection_state"`
	ConnectionCloseReason  sql.NullString `db:"connection_close_reason"`
	SessionArchivedAt      sql.NullTime   `db:"session_archived_at"`
	SessionRevokedAt       sql.NullTime   `db:"session_revoked_at"`
	SessionIdleExpiresAt   time.Time      `db:"session_idle_expires_at"`
	SessionExpiresAt       time.Time      `db:"session_expires_at"`
	UserArchivedAt         sql.NullTime   `db:"user_archived_at"`
	UserDisabledAt         sql.NullTime   `db:"user_disabled_at"`
	DatabaseNow            time.Time      `db:"database_now"`
}

const endedFocusLossAccessSelect = `SELECT a.exam_id,a.exam_sitting_id AS sitting_id,s.class_id,
	a.candidate_user_id,a.id AS attempt_id,a.state AS attempt_state,sub.id AS submission_id,
	sub.final_focus_loss_sequence,p.id AS participation_id,p.state AS participation_state,
	p.end_reason AS participation_end_reason,p.generation,p.continuity_credential_hash,
	co.id AS connection_id,co.session_id AS connection_session_id,co.state AS connection_state,
	co.close_reason AS connection_close_reason,se.archived_at AS session_archived_at,se.revoked_at AS session_revoked_at,
	se.idle_expires_at AS session_idle_expires_at,se.expires_at AS session_expires_at,
	u.archived_at AS user_archived_at,u.disabled_at AS user_disabled_at
	FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
	JOIN exam_submissions sub ON sub.exam_attempt_id=a.id AND sub.sealed=true
	JOIN exam_attempt_participations p ON p.id=? AND p.id=sub.participation_id AND
		p.exam_attempt_id=a.id AND p.generation=sub.generation
	JOIN exam_attempt_connections co ON co.id=? AND co.id=sub.connection_id AND
		co.exam_attempt_id=a.id AND co.participation_id=p.id
	JOIN sessions se ON se.id=co.session_id JOIN users u ON u.id=se.user_id
	WHERE a.id=? AND a.candidate_user_id=?`

func (s *sqlExamAttemptStore) ResolveEndedFocusLossTarget(ctx context.Context,
	access store.ExamAttemptFocusLossAccess,
) (*store.ExamAttemptFocusLossDiscrepancyTarget, error) {
	if err := validateFocusLossAccess(access); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve ended Focus Loss target",
		func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptFocusLossDiscrepancyTarget, error) {
			row, err := lockEndedFocusLossAccess(ctx, tx, access, false)
			if err != nil {
				return nil, err
			}
			return endedFocusLossTarget(row)
		})
}

func validateEndedFocusLoss(input *store.ExamAttemptFocusLossDiscrepancy) error {
	if input == nil || validateFocusLossAccess(input.Access) != nil ||
		input.SchemaVersion != model.FocusLossSignalSchemaVersion || !input.DiscrepancyID.IsValid() ||
		!input.SignalID.IsValid() || input.Sequence < 1 || input.DurationMilliseconds < 1 ||
		input.DurationMilliseconds > model.FocusLossMaximumDurationMilliseconds || !input.Source.IsValid() ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("integrity_discrepancy", "signal", nil)
	}
	return nil
}

func (s *sqlExamAttemptStore) RecordEndedFocusLoss(ctx context.Context,
	input *store.ExamAttemptFocusLossDiscrepancy,
) (*store.ExamAttemptFocusLossDiscrepancyResult, error) {
	if err := validateEndedFocusLoss(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "record ended Focus Loss discrepancy",
		func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptFocusLossDiscrepancyResult, error) {
			access, err := lockEndedFocusLossAccess(ctx, tx, input.Access, true)
			if err != nil {
				return nil, err
			}
			target, err := endedFocusLossTarget(access)
			if err != nil {
				return nil, err
			}
			var retained integrityDiscrepancyRow
			err = tx.Get(ctx, &retained, integrityDiscrepancySelect+` WHERE submission_id=? AND participation_id=? AND generation=? AND sequence=?`,
				access.SubmissionID, access.ParticipationID, access.Generation, input.Sequence)
			if err == nil {
				value, valueErr := retained.value()
				if valueErr != nil {
					return nil, valueErr
				}
				if value.SchemaVersion != input.SchemaVersion || value.DurationMilliseconds != input.DurationMilliseconds ||
					value.Source != input.Source {
					return nil, store.NewErrConflict("focus_loss", "focus_loss_sequence", nil)
				}
				result := &store.ExamAttemptFocusLossDiscrepancyResult{Target: *target, Discrepancy: value, Duplicate: true}
				if err = completeEndedFocusLossAudit(ctx, tx, result, input.AuditEventID, input.AuditAt); err != nil {
					return nil, err
				}
				return result, nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("load retained Integrity Discrepancy: %w", err)
			}
			var aggregate struct {
				Count        int           `db:"record_count"`
				LastSequence sql.NullInt64 `db:"last_sequence"`
			}
			if err = tx.Get(ctx, &aggregate, `SELECT count(*) AS record_count,max(sequence) AS last_sequence
				FROM integrity_discrepancies WHERE submission_id=?`, access.SubmissionID); err != nil {
				return nil, fmt.Errorf("count Integrity Discrepancies: %w", err)
			}
			if aggregate.Count >= model.SubmissionReviewMaximumDiscrepancies {
				return nil, store.NewErrConflict("integrity_discrepancy", "focus_loss_discrepancy_limit", nil)
			}
			previous := access.FinalSequence
			if aggregate.LastSequence.Valid && aggregate.LastSequence.Int64 > previous {
				previous = aggregate.LastSequence.Int64
			}
			if input.Sequence <= previous {
				return nil, store.NewErrConflict("focus_loss", "focus_loss_sequence", nil)
			}
			value, err := model.NewIntegrityDiscrepancy(model.IntegrityDiscrepancySpecification{
				ID: input.DiscrepancyID, SubmissionID: target.SubmissionID, AttemptID: input.Access.AttemptID,
				ParticipationID: input.Access.ParticipationID, Generation: input.Access.Generation,
				Kind: model.IntegrityDiscrepancyLateFocusLoss, SchemaVersion: input.SchemaVersion,
				SignalID: input.SignalID, Sequence: input.Sequence, DurationMilliseconds: input.DurationMilliseconds,
				Source: input.Source, MissingBefore: input.Sequence - previous - 1, ReceivedAt: access.DatabaseNow,
			})
			if err != nil {
				return nil, store.NewErrInvalidInput("integrity_discrepancy", "signal", err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO integrity_discrepancies
				(id,submission_id,exam_attempt_id,participation_id,connection_id,generation,kind,schema_version,
				 focus_loss_signal_id,sequence,duration_milliseconds,source,missing_before,received_at)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, value.ID.String(), value.SubmissionID.String(), value.AttemptID.String(),
				value.ParticipationID.String(), input.Access.ConnectionID.String(), value.Generation, string(value.Kind),
				value.SchemaVersion, value.SignalID.String(), value.Sequence, value.DurationMilliseconds,
				nullableFocusLossSource(value.Source), value.MissingBefore, value.ReceivedAt)
			if err != nil {
				return nil, fmt.Errorf("insert Integrity Discrepancy: %w", translateError("integrity_discrepancy", value.ID.String(), err))
			}
			result := &store.ExamAttemptFocusLossDiscrepancyResult{Target: *target, Discrepancy: value}
			if err = completeEndedFocusLossAudit(ctx, tx, result, input.AuditEventID, input.AuditAt); err != nil {
				return nil, err
			}
			return result, nil
		})
}

func lockEndedFocusLossAccess(ctx context.Context, tx *sqlxTxWrapper, input store.ExamAttemptFocusLossAccess,
	mutating bool,
) (endedFocusLossAccessRow, error) {
	var row endedFocusLossAccessRow
	lock := ` FOR SHARE OF a,p,co,sub,s,se,u`
	if mutating {
		lock = ` FOR UPDATE OF a,p,co,sub FOR SHARE OF s,se,u`
	}
	err := tx.Get(ctx, &row, endedFocusLossAccessSelect+lock, input.ParticipationID.String(),
		input.ConnectionID.String(), input.AttemptID.String(), input.CandidateUserID.String())
	if errors.Is(err, sql.ErrNoRows) {
		return row, focusLossAccessDenied(input.AttemptID)
	}
	if err != nil {
		return row, fmt.Errorf("lock ended Focus Loss access: %w", err)
	}
	if err = tx.Get(ctx, &row.DatabaseNow, `SELECT statement_timestamp()`); err != nil {
		return row, fmt.Errorf("read ended Focus Loss decision time: %w", err)
	}
	row.DatabaseNow = model.TimeUTC(row.DatabaseNow)
	if row.ConnectionSessionID != input.SessionID.String() || row.SessionArchivedAt.Valid || row.SessionRevokedAt.Valid ||
		!row.DatabaseNow.Before(row.SessionIdleExpiresAt) || !row.DatabaseNow.Before(row.SessionExpiresAt) ||
		row.UserArchivedAt.Valid || row.UserDisabledAt.Valid {
		return row, focusLossAccessDenied(input.AttemptID)
	}
	if subtle.ConstantTimeCompare([]byte(row.CredentialHash), []byte(input.ContinuityCredentialHash)) != 1 {
		return row, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	if row.Generation != input.Generation {
		return row, store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
	}
	if row.AttemptState != string(model.ExamAttemptSubmitted) || row.ParticipationState != string(model.AttemptParticipationEnded) ||
		row.ConnectionState != string(model.AttemptConnectionClosed) {
		return row, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	return row, nil
}

func endedFocusLossTarget(row endedFocusLossAccessRow) (*store.ExamAttemptFocusLossDiscrepancyTarget, error) {
	base, err := focusLossTarget(focusLossAccessRow{ExamID: row.ExamID, SittingID: row.SittingID, ClassID: row.ClassID,
		CandidateID: row.CandidateID, AttemptID: row.AttemptID, ParticipationID: row.ParticipationID, Generation: row.Generation})
	if err != nil {
		return nil, err
	}
	submissionID, err := model.ParseSubmissionID(row.SubmissionID)
	if err != nil {
		return nil, invalidPersistedState("integrity_discrepancy", "submission_id", err)
	}
	return &store.ExamAttemptFocusLossDiscrepancyTarget{ExamAttemptFocusLossTarget: *base, SubmissionID: submissionID}, nil
}

func completeEndedFocusLossAudit(ctx context.Context, tx *sqlxTxWrapper,
	result *store.ExamAttemptFocusLossDiscrepancyResult, auditID string, auditAt int64,
) error {
	data := map[string]any{"submission_id": result.Target.SubmissionID.String(),
		"exam_attempt_id": result.Target.AttemptID.String(), "integrity_discrepancy_id": result.Discrepancy.ID.String(),
		"generation": result.Discrepancy.Generation, "sequence": result.Discrepancy.Sequence}
	if result.Duplicate {
		data["sequence_replayed"] = true
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt)
	return err
}
