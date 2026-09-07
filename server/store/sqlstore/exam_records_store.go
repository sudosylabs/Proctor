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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLExamRecordsStore struct{ *SQLStore }

func newSQLExamRecordsStore(s *SQLStore) store.ExamRecordsStore { return &SQLExamRecordsStore{s} }

func (s *SQLExamRecordsStore) Resolve(ctx context.Context, scope model.RetentionHoldScope) (*store.ExamRecordsScope, error) {
	return resolveExamRecordsScope(ctx, s.GetMaster(), scope)
}

func resolveExamRecordsScope(ctx context.Context, executor sqlxExecutor, scope model.RetentionHoldScope) (*store.ExamRecordsScope, error) {
	if scope.Validate() != nil {
		return nil, store.NewErrInvalidInput("exam_records", "scope", nil)
	}
	var row struct {
		InstitutionID  string         `db:"institution_id"`
		AcademicUnitID string         `db:"academic_unit_id"`
		CandidateID    sql.NullString `db:"candidate_id"`
		AttemptID      sql.NullString `db:"attempt_id"`
	}
	err := executor.Get(ctx, &row, `SELECT u.institution_id,e.academic_unit_id,a.candidate_user_id AS candidate_id,a.id AS attempt_id
		FROM exams e JOIN academic_units u ON u.id=e.academic_unit_id
		LEFT JOIN exam_sittings s ON s.exam_id=e.id AND s.id=?
		LEFT JOIN exam_submissions sub ON sub.id=? AND sub.sealed=true
		LEFT JOIN exam_attempts a ON a.id=sub.exam_attempt_id AND a.exam_id=e.id AND a.exam_sitting_id=s.id
		WHERE e.id=? AND (?='' OR s.id IS NOT NULL) AND (?='' OR a.id IS NOT NULL)`,
		scope.SittingID.String(), scope.SubmissionID.String(), scope.ExamID.String(), scope.SittingID.String(), scope.SubmissionID.String())
	if err != nil {
		return nil, translateError("exam_records", scope.Resource().ID, err)
	}
	result := &store.ExamRecordsScope{Scope: scope, InstitutionID: model.InstitutionID(row.InstitutionID),
		AcademicUnitID: model.AcademicUnitID(row.AcademicUnitID), CandidateUserID: model.UserID(row.CandidateID.String), AttemptID: model.ExamAttemptID(row.AttemptID.String)}
	if !result.InstitutionID.IsValid() || !result.AcademicUnitID.IsValid() ||
		scope.SubmissionID.IsValid() && (!result.CandidateUserID.IsValid() || !result.AttemptID.IsValid()) {
		return nil, invalidPersistedState("exam_records", "scope", errors.New("invalid lineage identity"))
	}
	return result, nil
}

type recordsCompletionRow struct {
	SittingID                 string         `db:"exam_sitting_id"`
	Revision                  int64          `db:"revision"`
	EvidenceRevision          int64          `db:"evidence_revision"`
	CompletedEvidenceRevision int64          `db:"completed_evidence_revision"`
	CompletedAt               sql.NullTime   `db:"completed_at"`
	CompletedBy               sql.NullString `db:"completed_by_user_id"`
	StaleAt                   sql.NullTime   `db:"stale_at"`
}

func (r recordsCompletionRow) value() (*model.ExamSittingRecordsCompletion, error) {
	c := &model.ExamSittingRecordsCompletion{SittingID: model.ExamSittingID(r.SittingID), Revision: r.Revision,
		EvidenceRevision: r.EvidenceRevision, CompletedEvidenceRevision: r.CompletedEvidenceRevision,
		CompletedAt: optionalTime(r.CompletedAt), CompletedByUserID: model.UserID(r.CompletedBy.String), StaleAt: optionalTime(r.StaleAt)}
	if err := c.Validate(); err != nil {
		return nil, invalidPersistedState("exam_records_completion", "value", err)
	}
	return c, nil
}

const recordsCompletionColumns = `exam_sitting_id,revision,evidence_revision,completed_evidence_revision,completed_at,completed_by_user_id,stale_at`

func getRecordsCompletion(ctx context.Context, executor sqlxExecutor, id model.ExamSittingID) (*model.ExamSittingRecordsCompletion, error) {
	var row recordsCompletionRow
	err := executor.Get(ctx, &row, `SELECT `+recordsCompletionColumns+` FROM exam_sitting_records_completions WHERE exam_sitting_id=?`, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return model.NewExamSittingRecordsCompletion(id), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Sitting records completion: %w", err)
	}
	return row.value()
}

const recordsPendingReviewPredicate = `sub.integrity_retired_at IS NULL AND NOT EXISTS (SELECT 1 FROM submission_reviews r WHERE r.submission_id=sub.id AND r.state='finalized')
	AND NOT EXISTS (SELECT 1 FROM submission_review_waivers w
		WHERE w.submission_id=sub.id
		AND w.review_revision=COALESCE((SELECT r.revision FROM submission_reviews r WHERE r.submission_id=sub.id),0)
		AND w.discrepancy_count=(SELECT count(*) FROM integrity_discrepancies d WHERE d.submission_id=sub.id)
		AND NOT EXISTS (SELECT 1 FROM integrity_flags f WHERE f.exam_attempt_id=a.id AND NOT EXISTS
			(SELECT 1 FROM integrity_review_decisions d JOIN submission_reviews r ON r.id=d.submission_review_id
			 WHERE r.submission_id=sub.id AND d.integrity_flag_id=f.id)))`

func (s *SQLExamRecordsStore) GetCompletion(ctx context.Context, examID model.ExamID, sittingID model.ExamSittingID) (*store.ExamRecordsCompletionSnapshot, error) {
	if !examID.IsValid() || !sittingID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_records", "identity", nil)
	}
	var row struct {
		recordsCompletionRow
		State           model.ExamSittingState `db:"state"`
		SubmissionCount int64                  `db:"submission_count"`
		PendingReviews  int64                  `db:"pending_reviews"`
	}
	err := s.GetMaster().Get(ctx, &row, `SELECT s.id AS exam_sitting_id,COALESCE(c.revision,1) AS revision,
		COALESCE(c.evidence_revision,0) AS evidence_revision,COALESCE(c.completed_evidence_revision,0) AS completed_evidence_revision,
		c.completed_at,c.completed_by_user_id,c.stale_at,s.state,
		(SELECT count(*) FROM exam_attempts a JOIN exam_submissions sub ON sub.exam_attempt_id=a.id WHERE a.exam_sitting_id=s.id) AS submission_count,
		(SELECT count(*) FROM exam_attempts a JOIN exam_submissions sub ON sub.exam_attempt_id=a.id WHERE a.exam_sitting_id=s.id AND `+recordsPendingReviewPredicate+`) AS pending_reviews
		FROM exam_sittings s LEFT JOIN exam_sitting_records_completions c ON c.exam_sitting_id=s.id WHERE s.exam_id=? AND s.id=?`, examID.String(), sittingID.String())
	if err != nil {
		return nil, translateError("exam_records", sittingID.String(), err)
	}
	c, err := row.recordsCompletionRow.value()
	if err != nil {
		return nil, err
	}
	return &store.ExamRecordsCompletionSnapshot{Completion: *c, SittingState: row.State, SubmissionCount: row.SubmissionCount, PendingReviews: row.PendingReviews}, nil
}

type recordsWaiverRow struct {
	SubmissionID     string    `db:"submission_id"`
	Revision         int64     `db:"revision"`
	ReviewRevision   int64     `db:"review_revision"`
	DiscrepancyCount int64     `db:"discrepancy_count"`
	ActorID          string    `db:"actor_user_id"`
	RecordedAt       time.Time `db:"recorded_at"`
	ReasonCode       string    `db:"reason_code"`
	PrivateReason    string    `db:"private_reason"`
}

func (r recordsWaiverRow) value() (*model.SubmissionReviewWaiver, error) {
	w := &model.SubmissionReviewWaiver{SubmissionID: model.SubmissionID(r.SubmissionID), Revision: r.Revision, ReviewRevision: r.ReviewRevision,
		DiscrepancyCount: r.DiscrepancyCount, ActorUserID: model.UserID(r.ActorID), RecordedAt: model.TimeUTC(r.RecordedAt), ReasonCode: r.ReasonCode, PrivateReason: r.PrivateReason}
	if err := w.Validate(); err != nil {
		return nil, invalidPersistedState("submission_review_waiver", "value", err)
	}
	return w, nil
}

func (s *SQLExamRecordsStore) FindReviewWaiver(ctx context.Context, scope model.RetentionHoldScope) (*model.SubmissionReviewWaiver, bool, error) {
	if _, err := s.Resolve(ctx, scope); err != nil {
		return nil, false, err
	}
	return findRecordsWaiver(ctx, s.GetMaster(), scope.SubmissionID)
}

func findRecordsWaiver(ctx context.Context, executor sqlxExecutor, id model.SubmissionID) (*model.SubmissionReviewWaiver, bool, error) {
	if !id.IsValid() {
		return nil, false, store.NewErrInvalidInput("submission_review_waiver", "identity", nil)
	}
	var row recordsWaiverRow
	err := executor.Get(ctx, &row, `SELECT submission_id,revision,review_revision,discrepancy_count,actor_user_id,recorded_at,reason_code,private_reason FROM submission_review_waivers WHERE submission_id=?
		AND EXISTS(SELECT 1 FROM exam_submissions sub WHERE sub.id=submission_review_waivers.submission_id AND sub.integrity_retired_at IS NULL)`, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	w, err := row.value()
	return w, err == nil, err
}

// lockExamRecordsAuthority preserves User-before-hierarchy-before-Exam order.
// Wall time and credential validity are rechecked after every serialization
// boundary. The selected action is reauthorized rather than trusting a boolean.
func lockExamRecordsAuthority(ctx context.Context, tx *sqlxTxWrapper, input store.ExamRecordsMutation, recentTTL time.Duration) (*store.ExamRecordsScope, time.Time, error) {
	if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
		return nil, time.Time{}, err
	}
	if _, err := requireCurrentPrincipalCredential(ctx, tx, input.Principal); err != nil {
		return nil, time.Time{}, err
	}
	if err := lockAcademicUnitHierarchy(ctx, tx); err != nil {
		return nil, time.Time{}, err
	}
	scope, err := resolveExamRecordsScope(ctx, tx, input.Scope)
	if err != nil {
		return nil, time.Time{}, err
	}
	var lockedID string
	if err = tx.Get(ctx, &lockedID, `SELECT id FROM exams WHERE id=? FOR UPDATE`, input.Scope.ExamID.String()); err != nil {
		return nil, time.Time{}, err
	}
	if input.Scope.SittingID.IsValid() {
		if err = tx.Get(ctx, &lockedID, `SELECT id FROM exam_sittings WHERE id=? AND exam_id=? FOR UPDATE`, input.Scope.SittingID.String(), input.Scope.ExamID.String()); err != nil {
			return nil, time.Time{}, err
		}
	}
	if input.Scope.SubmissionID.IsValid() {
		if err = tx.Get(ctx, &lockedID, `SELECT id FROM exam_submissions WHERE id=? FOR UPDATE`, input.Scope.SubmissionID.String()); err != nil {
			return nil, time.Time{}, err
		}
		if input.Action == model.ActionExamRecordsComplete || input.Action == model.ActionExamRecordsCompleteOverride {
			if err = requireLiveSubmissionIntegrity(ctx, tx, input.Scope.SubmissionID); err != nil {
				return nil, time.Time{}, err
			}
		}
	}
	var at time.Time
	if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, time.Time{}, err
	}
	at = model.TimeUTC(at)
	if err = requirePrincipalActionAtScope(ctx, tx, input.Principal, input.Action, model.RoleScopeAcademicUnit, scope.AcademicUnitID.String(), at, false); err != nil {
		return nil, time.Time{}, err
	}
	ordinary := input.Action == model.ActionExamRecordsComplete || input.Action == model.ActionExamRecordsHold
	if ordinary {
		var member bool
		err = tx.Get(ctx, &member, `SELECT true FROM exam_managers m JOIN academic_unit_members u ON u.user_id=m.user_id
			WHERE m.exam_id=? AND m.user_id=? AND u.academic_unit_id=? AND u.archived_at IS NULL
			AND u.start_at<=? AND (u.end_at IS NULL OR u.end_at>?) LIMIT 1 FOR SHARE OF m,u`, input.Scope.ExamID.String(), input.Principal.UserID.String(), scope.AcademicUnitID.String(), at, at)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, time.Time{}, store.NewErrConflict("authorization", "authority", nil)
		}
		if err != nil {
			return nil, time.Time{}, err
		}
	}
	// A role/member lock may have waited while its time window expired. Recheck
	// all temporal guards after acquiring the selected rows.
	if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, time.Time{}, err
	}
	at = model.TimeUTC(at)
	if err = requirePrincipalActionAtScope(ctx, tx, input.Principal, input.Action, model.RoleScopeAcademicUnit, scope.AcademicUnitID.String(), at, false); err != nil {
		return nil, time.Time{}, err
	}
	var current bool
	err = tx.Get(ctx, &current, `SELECT true FROM sessions s JOIN session_credentials c ON c.session_id=s.id
		WHERE s.id=? AND s.user_id=? AND s.archived_at IS NULL AND s.revoked_at IS NULL AND s.idle_expires_at>? AND s.expires_at>?
		AND c.id=? AND c.archived_at IS NULL AND c.revoked_at IS NULL AND c.kind='access' AND c.expires_at>?`,
		input.Principal.SessionID.String(), input.Principal.UserID.String(), at, at, input.Principal.CredentialID.String(), at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, store.NewErrConflict("authorization", "credential", nil)
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	if ordinary {
		var member bool
		err = tx.Get(ctx, &member, `SELECT EXISTS(SELECT 1 FROM academic_unit_members WHERE user_id=? AND academic_unit_id=? AND archived_at IS NULL AND start_at<=? AND (end_at IS NULL OR end_at>?))`, input.Principal.UserID.String(), scope.AcademicUnitID.String(), at, at)
		if err != nil {
			return nil, time.Time{}, err
		}
		if !member {
			return nil, time.Time{}, store.NewErrConflict("authorization", "authority", nil)
		}
	}
	if input.Action == model.ActionRetentionHoldRelease {
		admin, adminErr := isActiveSystemAdministrator(ctx, tx, input.Principal.UserID.String(), at)
		if adminErr != nil {
			return nil, time.Time{}, adminErr
		}
		if !admin {
			return nil, time.Time{}, store.NewErrConflict("authorization", "authority", nil)
		}
		if err = requireStrongRecentSessionAt(ctx, tx, input.Principal.SessionID, at, recentTTL); err != nil {
			return nil, time.Time{}, err
		}
	}
	return scope, at, nil
}

func validExamRecordsMutation(input store.ExamRecordsMutation, command *store.CommandIdempotency, operation string, actions ...model.Action) bool {
	if input.Scope.Validate() != nil || input.Principal.Validate() != nil || input.Principal.CredentialType != model.CredentialSessionAccess ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 || command == nil || command.Operation != operation || command.UserID != input.Principal.UserID || command.Authorization != nil || command.Batch != nil {
		return false
	}
	for _, action := range actions {
		if input.Action == action {
			return true
		}
	}
	return false
}

func runExamRecordsMutation[T any](ctx context.Context, s *SQLStore, input store.ExamRecordsMutation, command *store.CommandIdempotency,
	ttl time.Duration, execute func(context.Context, *sqlxTxWrapper, *store.ExamRecordsScope, time.Time) (T, error), validate func(T) error, projection func(T) map[string]any,
) (*idempotentResult[T], error) {
	return runIdempotentMutation(ctx, s, command.Operation, idempotentMutation[T]{command: command, auditEventID: input.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (T, error) {
			var zero T
			scope, at, err := lockExamRecordsAuthority(ctx, tx, input, ttl)
			if err != nil {
				return zero, err
			}
			value, err := execute(ctx, tx, scope, at)
			if err != nil {
				return zero, err
			}
			if err = completeExamRecordsAudit(ctx, tx, input, projection(value), ""); err != nil {
				return zero, err
			}
			return value, nil
		},
		encode: func(v T) ([]byte, error) { return json.Marshal(v) },
		decode: func(version int, data []byte) (T, error) {
			var v T
			if version != 1 {
				return v, errors.New("unsupported records outcome version")
			}
			if err := json.Unmarshal(data, &v); err != nil {
				return v, err
			}
			return v, validate(v)
		},
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, v T) (T, error) {
			_, _, err := lockExamRecordsAuthority(ctx, tx, input, ttl)
			return v, err
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, v T, original string) error {
			return completeExamRecordsAudit(ctx, tx, input, projection(v), original)
		},
	})
}

func completeExamRecordsAudit(ctx context.Context, tx *sqlxTxWrapper, input store.ExamRecordsMutation, data map[string]any, original string) error {
	data["exam_id"] = input.Scope.ExamID.String()
	if input.Scope.SittingID.IsValid() {
		data["exam_sitting_id"] = input.Scope.SittingID.String()
	}
	if input.Scope.SubmissionID.IsValid() {
		data["submission_id"] = input.Scope.SubmissionID.String()
	}
	if original != "" {
		data["replayed"] = true
		data["original_audit_event_id"] = original
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt)
	return err
}

func saveRecordsCompletion(ctx context.Context, tx *sqlxTxWrapper, c *model.ExamSittingRecordsCompletion) error {
	if err := c.Validate(); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO exam_sitting_records_completions (`+recordsCompletionColumns+`) VALUES (?,?,?,?,?,?,?)
		ON CONFLICT (exam_sitting_id) DO UPDATE SET revision=EXCLUDED.revision,evidence_revision=EXCLUDED.evidence_revision,
		completed_evidence_revision=EXCLUDED.completed_evidence_revision,completed_at=EXCLUDED.completed_at,completed_by_user_id=EXCLUDED.completed_by_user_id,stale_at=EXCLUDED.stale_at`,
		c.SittingID.String(), c.Revision, c.EvidenceRevision, c.CompletedEvidenceRevision, optionalTimeValue(c.CompletedAt), nullableID(c.CompletedByUserID.String()), optionalTimeValue(c.StaleAt))
	return err
}

func (s *SQLExamRecordsStore) CompleteRecords(ctx context.Context, input *store.ExamRecordsCompletion, command *store.CommandIdempotency) (*store.ExamRecordsCompletionResult, error) {
	if input == nil || !validExamRecordsMutation(input.ExamRecordsMutation, command, store.ExamRecordsCompleteOperation, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride) ||
		!input.Scope.SittingID.IsValid() || !input.Scope.SubmissionID.IsZero() || input.ExpectedRevision < 1 || input.AcknowledgedEvidenceRevision < 0 {
		return nil, store.NewErrInvalidInput("exam_records", "completion", nil)
	}
	r, err := runExamRecordsMutation(ctx, s.SQLStore, input.ExamRecordsMutation, command, 0,
		func(ctx context.Context, tx *sqlxTxWrapper, _ *store.ExamRecordsScope, at time.Time) (*model.ExamSittingRecordsCompletion, error) {
			var state model.ExamSittingState
			if err := tx.Get(ctx, &state, `SELECT state FROM exam_sittings WHERE id=?`, input.Scope.SittingID.String()); err != nil {
				return nil, err
			}
			if state != model.ExamSittingClosed {
				return nil, store.NewErrConflict("exam_records", "sitting_not_closed", nil)
			}
			c, err := getRecordsCompletion(ctx, tx, input.Scope.SittingID)
			if err != nil {
				return nil, err
			}
			if c.Revision != input.ExpectedRevision || c.EvidenceRevision != input.AcknowledgedEvidenceRevision {
				return nil, store.NewErrConflict("exam_records", "revision", nil)
			}
			var pending bool
			if err = tx.Get(ctx, &pending, `SELECT EXISTS(SELECT 1 FROM exam_attempts a LEFT JOIN exam_submissions sub ON sub.exam_attempt_id=a.id
				WHERE a.exam_sitting_id=? AND (sub.id IS NULL OR `+recordsPendingReviewPredicate+`))`, input.Scope.SittingID.String()); err != nil {
				return nil, err
			}
			if pending {
				return nil, store.NewErrConflict("exam_records", "reviews_pending", nil)
			}
			changed, err := c.Complete(input.ExpectedRevision, input.AcknowledgedEvidenceRevision, input.Principal.UserID, at)
			if err != nil {
				return nil, store.NewErrConflict("exam_records", "revision", err)
			}
			if changed {
				err = saveRecordsCompletion(ctx, tx, c)
			}
			return c, err
		}, func(c *model.ExamSittingRecordsCompletion) error { return c.Validate() }, func(c *model.ExamSittingRecordsCompletion) map[string]any {
			return map[string]any{"records_revision": c.Revision, "evidence_revision": c.EvidenceRevision}
		})
	if err != nil {
		return nil, err
	}
	return &store.ExamRecordsCompletionResult{Completion: r.Value, Replayed: r.Replayed}, nil
}

func (s *SQLExamRecordsStore) WaiveReview(ctx context.Context, input *store.ExamRecordsReviewWaiver, command *store.CommandIdempotency) (*store.ExamRecordsWaiverResult, error) {
	if input == nil || !validExamRecordsMutation(input.ExamRecordsMutation, command, store.ExamRecordsWaiveReviewOperation, model.ActionExamRecordsComplete, model.ActionExamRecordsCompleteOverride) ||
		!input.Scope.SubmissionID.IsValid() || input.ExpectedRevision < 0 || input.ExpectedReviewRevision < 0 || input.ExpectedDiscrepancyCount < 0 || model.ValidateRecordsReason(input.ReasonCode, input.PrivateReason) != nil {
		return nil, store.NewErrInvalidInput("exam_records", "review_waiver", nil)
	}
	r, err := runExamRecordsMutation(ctx, s.SQLStore, input.ExamRecordsMutation, command, 0,
		func(ctx context.Context, tx *sqlxTxWrapper, scope *store.ExamRecordsScope, at time.Time) (*model.SubmissionReviewWaiver, error) {
			if scope.CandidateUserID == input.Principal.UserID {
				return nil, store.NewErrConflict("exam_records", "self_waiver", nil)
			}
			var state model.ExamSittingState
			if err := tx.Get(ctx, &state, `SELECT state FROM exam_sittings WHERE id=?`, input.Scope.SittingID.String()); err != nil {
				return nil, err
			}
			if state != model.ExamSittingClosed {
				return nil, store.NewErrConflict("exam_records", "sitting_not_closed", nil)
			}
			var inventory struct {
				Revision  int64 `db:"revision"`
				Count     int64 `db:"count"`
				Undecided bool  `db:"undecided"`
				Finalized bool  `db:"finalized"`
			}
			err := tx.Get(ctx, &inventory, `SELECT COALESCE((SELECT revision FROM submission_reviews WHERE submission_id=?),0) AS revision,
				(SELECT count(*) FROM integrity_discrepancies WHERE submission_id=?) AS count,
				EXISTS(SELECT 1 FROM integrity_flags f WHERE f.exam_attempt_id=? AND NOT EXISTS(SELECT 1 FROM integrity_review_decisions d JOIN submission_reviews r ON r.id=d.submission_review_id WHERE r.submission_id=? AND d.integrity_flag_id=f.id)) AS undecided,
				EXISTS(SELECT 1 FROM submission_reviews WHERE submission_id=? AND state='finalized') AS finalized`, input.Scope.SubmissionID.String(), input.Scope.SubmissionID.String(), scope.AttemptID.String(), input.Scope.SubmissionID.String(), input.Scope.SubmissionID.String())
			if err != nil {
				return nil, err
			}
			if inventory.Undecided {
				return nil, store.NewErrConflict("exam_records", "undecided_flags", nil)
			}
			if inventory.Finalized {
				return nil, store.NewErrConflict("exam_records", "review_finalized", nil)
			}
			if inventory.Revision != input.ExpectedReviewRevision || inventory.Count != input.ExpectedDiscrepancyCount {
				return nil, store.NewErrConflict("exam_records", "inventory_changed", nil)
			}
			prior, found, err := findRecordsWaiver(ctx, tx, input.Scope.SubmissionID)
			if err != nil {
				return nil, err
			}
			if found && prior.Revision != input.ExpectedRevision || !found && input.ExpectedRevision != 0 || input.ExpectedRevision == math.MaxInt64 {
				return nil, store.NewErrConflict("exam_records", "revision", nil)
			}
			w := &model.SubmissionReviewWaiver{SubmissionID: input.Scope.SubmissionID, Revision: input.ExpectedRevision + 1, ReviewRevision: inventory.Revision, DiscrepancyCount: inventory.Count, ActorUserID: input.Principal.UserID, RecordedAt: at, ReasonCode: input.ReasonCode, PrivateReason: input.PrivateReason}
			if err = w.Validate(); err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO submission_review_waivers (submission_id,revision,review_revision,discrepancy_count,actor_user_id,recorded_at,reason_code,private_reason,audit_event_id) VALUES (?,?,?,?,?,?,?,?,?)
				ON CONFLICT (submission_id) DO UPDATE SET revision=EXCLUDED.revision,review_revision=EXCLUDED.review_revision,discrepancy_count=EXCLUDED.discrepancy_count,
				actor_user_id=EXCLUDED.actor_user_id,recorded_at=EXCLUDED.recorded_at,reason_code=EXCLUDED.reason_code,private_reason=EXCLUDED.private_reason,audit_event_id=EXCLUDED.audit_event_id`, w.SubmissionID.String(), w.Revision, w.ReviewRevision, w.DiscrepancyCount, w.ActorUserID.String(), w.RecordedAt, w.ReasonCode, w.PrivateReason, input.AuditEventID)
			return w, err
		}, func(w *model.SubmissionReviewWaiver) error { return w.Validate() }, func(w *model.SubmissionReviewWaiver) map[string]any {
			return map[string]any{"waiver_revision": w.Revision, "review_revision": w.ReviewRevision, "reason_code": w.ReasonCode}
		})
	if err != nil {
		return nil, err
	}
	return &store.ExamRecordsWaiverResult{Waiver: r.Value, Replayed: r.Replayed}, nil
}

// invalidateWaivedReviewRecords invalidates a completion that could have relied
// on a waiver for the previous Review revision. The owning Review mutation has
// already locked the Sitting, and idempotent replay never reaches this helper.
func invalidateWaivedReviewRecords(ctx context.Context, tx *sqlxTxWrapper, auth *store.ExamIntegrityReviewAuthorization, at time.Time) error {
	var waived bool
	if err := tx.Get(ctx, &waived, `SELECT EXISTS(SELECT 1 FROM submission_review_waivers WHERE submission_id=?)`, auth.SubmissionID.String()); err != nil {
		return err
	}
	if !waived {
		return nil
	}
	return invalidateSittingRecordsForIntegrity(ctx, tx, auth.SittingID, at)
}

// invalidateSittingRecordsForIntegrity is called inside the accepted-data
// transaction while its Sitting is locked. Exact replay never calls it.
func invalidateSittingRecordsForIntegrity(ctx context.Context, tx *sqlxTxWrapper, id model.ExamSittingID, at time.Time) error {
	c, err := getRecordsCompletion(ctx, tx, id)
	if err != nil {
		return err
	}
	if c.CompletedAt.Valid && at.Before(c.CompletedAt.Time) {
		at = c.CompletedAt.Time
	}
	if err = c.ObserveIntegrity(at); err != nil {
		return err
	}
	if err = saveRecordsCompletion(ctx, tx, c); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH cancelled AS (UPDATE retention_retirements
		SET state='cancelled',cancelled_at=GREATEST(scheduled_at,?),cancellation_reason='records_stale'
		WHERE exam_sitting_id=? AND state='grace' RETURNING id)
		UPDATE retention_notices SET cancelled_at=GREATEST(created_at,?),delivery_state=CASE WHEN mail_delivery_id IS NULL AND delivery_state='pending' THEN 'suppressed' ELSE delivery_state END
		WHERE retirement_id IN (SELECT id FROM cancelled) AND cancelled_at IS NULL`, at, id.String(), at)
	if err != nil {
		return err
	}
	var examID string
	if err = tx.Get(ctx, &examID, `SELECT exam_id FROM exam_sittings WHERE id=?`, id.String()); err != nil {
		return err
	}
	return cancelScopeExpirySchedules(ctx, tx, model.RetentionHoldScope{ExamID: model.ExamID(examID), SittingID: id})
}

type retentionHoldRow struct {
	ID                    string         `db:"id"`
	ExamID                string         `db:"exam_id"`
	SittingID             sql.NullString `db:"exam_sitting_id"`
	SubmissionID          sql.NullString `db:"submission_id"`
	Revision              int64          `db:"revision"`
	CreatedAt             time.Time      `db:"created_at"`
	CreatedBy             string         `db:"created_by_user_id"`
	ReasonCode            string         `db:"reason_code"`
	PrivateReason         string         `db:"private_reason"`
	ReleasedAt            sql.NullTime   `db:"released_at"`
	ReleasedBy            sql.NullString `db:"released_by_user_id"`
	ReleaseCode           sql.NullString `db:"release_reason_code"`
	ReleaseReason         sql.NullString `db:"release_private_reason"`
	WorkRetiredCount      int64          `db:"work_retired_submission_count"`
	IntegrityRetiredCount int64          `db:"integrity_retired_submission_count"`
}

const retentionHoldColumns = `id,exam_id,exam_sitting_id,submission_id,revision,created_at,created_by_user_id,reason_code,private_reason,released_at,released_by_user_id,release_reason_code,release_private_reason,work_retired_submission_count,integrity_retired_submission_count`

func (r retentionHoldRow) value() (*model.RetentionHold, error) {
	h := &model.RetentionHold{ID: model.RetentionHoldID(r.ID), Scope: model.RetentionHoldScope{ExamID: model.ExamID(r.ExamID), SittingID: model.ExamSittingID(r.SittingID.String), SubmissionID: model.SubmissionID(r.SubmissionID.String)}, Revision: r.Revision,
		CreatedAt: model.TimeUTC(r.CreatedAt), CreatedByUserID: model.UserID(r.CreatedBy), ReasonCode: r.ReasonCode, PrivateReason: r.PrivateReason,
		ReleasedAt: optionalTime(r.ReleasedAt), ReleasedByUserID: model.UserID(r.ReleasedBy.String), ReleaseReasonCode: r.ReleaseCode.String, ReleasePrivateReason: r.ReleaseReason.String,
		WorkRetiredSubmissionCount: r.WorkRetiredCount, IntegrityRetiredSubmissionCount: r.IntegrityRetiredCount}
	if err := h.Validate(); err != nil {
		return nil, invalidPersistedState("retention_hold", "value", err)
	}
	return h, nil
}

func (s *SQLExamRecordsStore) ListHolds(ctx context.Context, options store.ExamRecordsHoldListOptions) (*store.ExamRecordsHoldPage, error) {
	if options.Scope.Validate() != nil || options.Limit < 1 || options.Limit > 200 || !options.After.IsZero() && !options.After.IsValid() {
		return nil, store.NewErrInvalidInput("retention_hold", "list", nil)
	}
	if _, err := s.Resolve(ctx, options.Scope); err != nil {
		return nil, err
	}
	var rows []retentionHoldRow
	err := s.GetMaster().Select(ctx, &rows, `SELECT `+retentionHoldColumns+` FROM retention_holds WHERE exam_id=? AND id>?
		AND (? OR released_at IS NULL) AND (?='' OR exam_sitting_id IS NULL OR exam_sitting_id=?)
		AND (?='' OR submission_id IS NULL OR submission_id=?) ORDER BY id LIMIT ?`, options.Scope.ExamID.String(), options.After.String(), options.IncludeReleased, options.Scope.SittingID.String(), options.Scope.SittingID.String(), options.Scope.SubmissionID.String(), options.Scope.SubmissionID.String(), options.Limit+1)
	if err != nil {
		return nil, err
	}
	page := &store.ExamRecordsHoldPage{Holds: make([]model.RetentionHold, 0, options.Limit), HasMore: len(rows) > options.Limit}
	if page.HasMore {
		rows = rows[:options.Limit]
	}
	for _, row := range rows {
		h, err := row.value()
		if err != nil {
			return nil, err
		}
		page.Holds = append(page.Holds, *h)
	}
	return page, nil
}

func (s *SQLExamRecordsStore) CreateHold(ctx context.Context, input *store.ExamRecordsHoldCreation, command *store.CommandIdempotency) (*store.ExamRecordsHoldResult, error) {
	if input == nil || !validExamRecordsMutation(input.ExamRecordsMutation, command, store.ExamRecordsCreateHoldOperation, model.ActionExamRecordsHold, model.ActionExamRecordsHoldOverride) || !input.HoldID.IsValid() || model.ValidateRecordsReason(input.ReasonCode, input.PrivateReason) != nil {
		return nil, store.NewErrInvalidInput("retention_hold", "creation", nil)
	}
	r, err := runExamRecordsMutation(ctx, s.SQLStore, input.ExamRecordsMutation, command, 0, func(ctx context.Context, tx *sqlxTxWrapper, _ *store.ExamRecordsScope, at time.Time) (*model.RetentionHold, error) {
		h := &model.RetentionHold{ID: input.HoldID, Scope: input.Scope, Revision: 1, CreatedAt: at, CreatedByUserID: input.Principal.UserID, ReasonCode: input.ReasonCode, PrivateReason: input.PrivateReason}
		var coverage struct {
			Work      int64 `db:"work"`
			Integrity int64 `db:"integrity"`
		}
		if err := tx.Get(ctx, &coverage, `SELECT count(*) FILTER (WHERE sub.work_retired_at IS NOT NULL) AS work,
			count(*) FILTER (WHERE sub.integrity_retired_at IS NOT NULL) AS integrity
			FROM exam_submissions sub JOIN exam_attempts a ON a.id=sub.exam_attempt_id
			WHERE a.exam_id=? AND (?='' OR a.exam_sitting_id=?) AND (?='' OR sub.id=?)`, input.Scope.ExamID.String(),
			input.Scope.SittingID.String(), input.Scope.SittingID.String(), input.Scope.SubmissionID.String(), input.Scope.SubmissionID.String()); err != nil {
			return nil, err
		}
		if input.Scope.SubmissionID.IsValid() && coverage.Work > 0 && coverage.Integrity > 0 {
			return nil, store.NewErrConflict("retention_hold", "records_retired", nil)
		}
		h.WorkRetiredSubmissionCount, h.IntegrityRetiredSubmissionCount = coverage.Work, coverage.Integrity
		if err := h.Validate(); err != nil {
			return nil, err
		}
		_, err := tx.Exec(ctx, `INSERT INTO retention_holds(id,exam_id,exam_sitting_id,submission_id,revision,created_at,created_by_user_id,reason_code,private_reason,creation_audit_event_id,work_retired_submission_count,integrity_retired_submission_count) VALUES (?,?,?,?,1,?,?,?,?,?,?,?)`, h.ID.String(), h.Scope.ExamID.String(), nullableID(h.Scope.SittingID.String()), nullableID(h.Scope.SubmissionID.String()), at, h.CreatedByUserID.String(), h.ReasonCode, h.PrivateReason, input.AuditEventID, h.WorkRetiredSubmissionCount, h.IntegrityRetiredSubmissionCount)
		if err != nil {
			return nil, translateError("retention_hold", h.ID.String(), err)
		}
		// Invalidate the advertised dates in the same commit as the hold.
		// Reconciliation may only establish a new grace after the hold ends.
		_, err = tx.Exec(ctx, `WITH cancelled AS (UPDATE retention_retirements
			SET state='cancelled',cancelled_at=GREATEST(scheduled_at,?),cancellation_reason='preservation_hold'
			WHERE exam_id=? AND (?='' OR exam_sitting_id=?) AND (?='' OR submission_id=?)
			AND state='grace' RETURNING id)
			UPDATE retention_notices SET cancelled_at=GREATEST(created_at,?),delivery_state=CASE WHEN mail_delivery_id IS NULL AND delivery_state='pending' THEN 'suppressed' ELSE delivery_state END
			WHERE retirement_id IN (SELECT id FROM cancelled) AND cancelled_at IS NULL`, at, h.Scope.ExamID.String(),
			h.Scope.SittingID.String(), h.Scope.SittingID.String(), h.Scope.SubmissionID.String(), h.Scope.SubmissionID.String(), at)
		if err != nil {
			return nil, err
		}
		if err = cancelScopeExpirySchedules(ctx, tx, h.Scope); err != nil {
			return nil, err
		}
		return h, nil
	}, func(h *model.RetentionHold) error { return h.Validate() }, retentionHoldAudit)
	if err != nil {
		return nil, err
	}
	return &store.ExamRecordsHoldResult{Hold: r.Value, Replayed: r.Replayed}, nil
}

func (s *SQLExamRecordsStore) ReleaseHold(ctx context.Context, input *store.ExamRecordsHoldRelease, command *store.CommandIdempotency) (*store.ExamRecordsHoldResult, error) {
	if input == nil || !validExamRecordsMutation(input.ExamRecordsMutation, command, store.ExamRecordsReleaseHoldOperation, model.ActionRetentionHoldRelease) || !input.HoldID.IsValid() || input.ExpectedRevision < 1 || input.RecentAuthenticationTTL <= 0 || model.ValidateRecordsReason(input.ReasonCode, input.PrivateReason) != nil {
		return nil, store.NewErrInvalidInput("retention_hold", "release", nil)
	}
	r, err := runExamRecordsMutation(ctx, s.SQLStore, input.ExamRecordsMutation, command, input.RecentAuthenticationTTL, func(ctx context.Context, tx *sqlxTxWrapper, _ *store.ExamRecordsScope, at time.Time) (*model.RetentionHold, error) {
		var row retentionHoldRow
		if err := tx.Get(ctx, &row, `SELECT `+retentionHoldColumns+` FROM retention_holds WHERE id=? FOR UPDATE`, input.HoldID.String()); err != nil {
			return nil, translateError("retention_hold", input.HoldID.String(), err)
		}
		h, err := row.value()
		if err != nil {
			return nil, err
		}
		if h.Scope != input.Scope {
			return nil, store.NewErrNotFound("retention_hold", input.HoldID.String())
		}
		if err = h.Release(input.ExpectedRevision, input.Principal.UserID, input.ReasonCode, input.PrivateReason, at); err != nil {
			return nil, store.NewErrConflict("retention_hold", "revision", err)
		}
		_, err = tx.Exec(ctx, `UPDATE retention_holds SET revision=?,released_at=?,released_by_user_id=?,release_reason_code=?,release_private_reason=?,release_audit_event_id=? WHERE id=?`, h.Revision, at, h.ReleasedByUserID.String(), h.ReleaseReasonCode, h.ReleasePrivateReason, input.AuditEventID, h.ID.String())
		return h, err
	}, func(h *model.RetentionHold) error { return h.Validate() }, retentionHoldAudit)
	if err != nil {
		return nil, err
	}
	return &store.ExamRecordsHoldResult{Hold: r.Value, Replayed: r.Replayed}, nil
}

func retentionHoldAudit(h *model.RetentionHold) map[string]any {
	return map[string]any{"retention_hold_id": h.ID.String(), "hold_revision": h.Revision, "reason_code": h.ReasonCode, "released": h.ReleasedAt.Valid, "release_reason_code": h.ReleaseReasonCode,
		"work_retired_submission_count": h.WorkRetiredSubmissionCount, "integrity_retired_submission_count": h.IntegrityRetiredSubmissionCount}
}

var _ store.ExamRecordsStore = (*SQLExamRecordsStore)(nil)
