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

type SQLExamIntegrityReviewStore struct{ *SQLStore }

func NewSQLExamIntegrityReviewStore(sqlStore *SQLStore) store.ExamIntegrityReviewStore {
	return &SQLExamIntegrityReviewStore{SQLStore: sqlStore}
}

type examIntegrityReviewAuthorizationRow struct {
	SubmissionID    string `db:"submission_id"`
	ExamID          string `db:"exam_id"`
	SittingID       string `db:"exam_sitting_id"`
	AttemptID       string `db:"exam_attempt_id"`
	CandidateUserID string `db:"candidate_user_id"`
	AcademicUnitID  string `db:"academic_unit_id"`
}

const examIntegrityReviewAuthorizationSelect = `SELECT sub.id AS submission_id,a.exam_id,a.exam_sitting_id,
	a.id AS exam_attempt_id,a.candidate_user_id,e.academic_unit_id FROM exam_submissions sub
	JOIN exam_attempts a ON a.id=sub.exam_attempt_id JOIN exams e ON e.id=a.exam_id
	WHERE sub.id=? AND sub.sealed=true`

func (row examIntegrityReviewAuthorizationRow) value() (*store.ExamIntegrityReviewAuthorization, error) {
	var value store.ExamIntegrityReviewAuthorization
	var err error
	if value.SubmissionID, err = model.ParseSubmissionID(row.SubmissionID); err != nil {
		return nil, invalidPersistedState("submission_review", "submission_id", err)
	}
	if value.ExamID, err = model.ParseExamID(row.ExamID); err != nil {
		return nil, invalidPersistedState("submission_review", "exam_id", err)
	}
	if value.SittingID, err = model.ParseExamSittingID(row.SittingID); err != nil {
		return nil, invalidPersistedState("submission_review", "exam_sitting_id", err)
	}
	if value.AttemptID, err = model.ParseExamAttemptID(row.AttemptID); err != nil {
		return nil, invalidPersistedState("submission_review", "exam_attempt_id", err)
	}
	if value.CandidateUserID, err = model.ParseUserID(row.CandidateUserID); err != nil {
		return nil, invalidPersistedState("submission_review", "candidate_user_id", err)
	}
	if value.AcademicUnitID, err = model.ParseAcademicUnitID(row.AcademicUnitID); err != nil {
		return nil, invalidPersistedState("submission_review", "academic_unit_id", err)
	}
	return &value, nil
}

func (s *SQLExamIntegrityReviewStore) Resolve(ctx context.Context, submissionID model.SubmissionID) (*store.ExamIntegrityReviewAuthorization, error) {
	if !submissionID.IsValid() {
		return nil, store.NewErrInvalidInput("submission_review", "submission_id", nil)
	}
	var row examIntegrityReviewAuthorizationRow
	if err := s.GetMaster().Get(ctx, &row, examIntegrityReviewAuthorizationSelect, submissionID.String()); err != nil {
		return nil, translateError("submission", submissionID.String(), err)
	}
	return row.value()
}

type examSubmissionReviewRow struct {
	ID                      string         `db:"id"`
	SubmissionID            string         `db:"submission_id"`
	State                   string         `db:"state"`
	ReleaseState            string         `db:"release_state"`
	Revision                int64          `db:"revision"`
	CreatedByUserID         string         `db:"created_by_user_id"`
	ManagerNotes            string         `db:"manager_notes"`
	StudentRemarksMarkdown  string         `db:"student_remarks_markdown"`
	FlagCount               int            `db:"flag_count"`
	EvidenceCount           int            `db:"evidence_count"`
	DiscrepancyCount        int            `db:"discrepancy_count"`
	EvidenceInventoryDigest sql.NullString `db:"evidence_inventory_digest"`
	CreatedAt               time.Time      `db:"created_at"`
	UpdatedAt               time.Time      `db:"updated_at"`
	FinalizedAt             sql.NullTime   `db:"finalized_at"`
	FinalizedByUserID       sql.NullString `db:"finalized_by_user_id"`
	ReleasedAt              sql.NullTime   `db:"released_at"`
	ReleasedByUserID        sql.NullString `db:"released_by_user_id"`
}

const examSubmissionReviewSelect = `SELECT id,submission_id,state,release_state,revision,created_by_user_id,
	manager_notes,student_remarks_markdown,flag_count,evidence_count,discrepancy_count,evidence_inventory_digest,
	created_at,updated_at,finalized_at,finalized_by_user_id,released_at,released_by_user_id FROM submission_reviews`

func (row examSubmissionReviewRow) value() (*model.SubmissionReview, error) {
	var review model.SubmissionReview
	var err error
	if review.ID, err = model.ParseSubmissionReviewID(row.ID); err != nil {
		return nil, invalidPersistedState("submission_review", "id", err)
	}
	if review.SubmissionID, err = model.ParseSubmissionID(row.SubmissionID); err != nil {
		return nil, invalidPersistedState("submission_review", "submission_id", err)
	}
	if review.CreatedByUserID, err = model.ParseUserID(row.CreatedByUserID); err != nil {
		return nil, invalidPersistedState("submission_review", "created_by_user_id", err)
	}
	if row.FinalizedByUserID.Valid {
		if review.FinalizedByUserID, err = model.ParseUserID(row.FinalizedByUserID.String); err != nil {
			return nil, invalidPersistedState("submission_review", "finalized_by_user_id", err)
		}
	}
	if row.ReleasedByUserID.Valid {
		if review.ReleasedByUserID, err = model.ParseUserID(row.ReleasedByUserID.String); err != nil {
			return nil, invalidPersistedState("submission_review", "released_by_user_id", err)
		}
	}
	review.State = model.SubmissionReviewState(row.State)
	review.ReleaseState = model.SubmissionReviewReleaseState(row.ReleaseState)
	review.Revision = row.Revision
	review.ManagerNotes = row.ManagerNotes
	review.StudentRemarksMarkdown = row.StudentRemarksMarkdown
	review.FlagCount, review.EvidenceCount, review.DiscrepancyCount = row.FlagCount, row.EvidenceCount, row.DiscrepancyCount
	review.EvidenceInventoryDigest = row.EvidenceInventoryDigest.String
	review.CreatedAt, review.UpdatedAt = model.TimeUTC(row.CreatedAt), model.TimeUTC(row.UpdatedAt)
	review.FinalizedAt, review.ReleasedAt = OptionalTimeFromNullTime(row.FinalizedAt), OptionalTimeFromNullTime(row.ReleasedAt)
	if err = review.Validate(); err != nil {
		return nil, invalidPersistedState("submission_review", "value", err)
	}
	return &review, nil
}

type examIntegrityReviewDecisionRow struct {
	ID               string    `db:"id"`
	ReviewID         string    `db:"submission_review_id"`
	FlagID           string    `db:"integrity_flag_id"`
	Outcome          string    `db:"outcome"`
	Revision         int64     `db:"revision"`
	ActorUserID      string    `db:"actor_user_id"`
	PrivateRationale string    `db:"private_rationale"`
	DecidedAt        time.Time `db:"decided_at"`
}

const examIntegrityReviewDecisionSelect = `SELECT id,submission_review_id,integrity_flag_id,outcome,revision,
	actor_user_id,private_rationale,decided_at FROM integrity_review_decisions`

func (row examIntegrityReviewDecisionRow) value() (*model.IntegrityReviewDecision, error) {
	id, err := model.ParseIntegrityReviewDecisionID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("integrity_review_decision", "id", err)
	}
	reviewID, err := model.ParseSubmissionReviewID(row.ReviewID)
	if err != nil {
		return nil, invalidPersistedState("integrity_review_decision", "submission_review_id", err)
	}
	flagID, err := model.ParseIntegrityFlagID(row.FlagID)
	if err != nil {
		return nil, invalidPersistedState("integrity_review_decision", "integrity_flag_id", err)
	}
	actorID, err := model.ParseUserID(row.ActorUserID)
	if err != nil {
		return nil, invalidPersistedState("integrity_review_decision", "actor_user_id", err)
	}
	decision := &model.IntegrityReviewDecision{ID: id, ReviewID: reviewID, FlagID: flagID,
		Outcome: model.IntegrityReviewOutcome(row.Outcome), Revision: row.Revision, ActorUserID: actorID,
		PrivateRationale: row.PrivateRationale, DecidedAt: model.TimeUTC(row.DecidedAt)}
	if err = decision.Validate(); err != nil {
		return nil, invalidPersistedState("integrity_review_decision", "value", err)
	}
	return decision, nil
}

func (s *SQLExamIntegrityReviewStore) Get(ctx context.Context, submissionID model.SubmissionID) (*store.ExamSubmissionReviewSnapshot, error) {
	if !submissionID.IsValid() {
		return nil, store.NewErrInvalidInput("submission_review", "submission_id", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "get Exam Integrity Review", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamSubmissionReviewSnapshot, error) {
		var authorizationRow examIntegrityReviewAuthorizationRow
		if err := tx.Get(ctx, &authorizationRow, examIntegrityReviewAuthorizationSelect+` FOR SHARE OF sub,a`, submissionID.String()); err != nil {
			return nil, translateError("submission", submissionID.String(), err)
		}
		authorization, err := authorizationRow.value()
		if err != nil {
			return nil, err
		}
		var submissionRow examSubmissionHeaderRow
		if err = tx.Get(ctx, &submissionRow, examSubmissionHeaderSelect+` WHERE id=? AND sealed=true`, submissionID.String()); err != nil {
			return nil, translateError("submission", submissionID.String(), err)
		}
		submission, err := submissionRow.model()
		if err != nil {
			return nil, err
		}
		snapshot := &store.ExamSubmissionReviewSnapshot{Authorization: *authorization, Submission: submission}
		var reviewRow examSubmissionReviewRow
		err = tx.Get(ctx, &reviewRow, examSubmissionReviewSelect+` WHERE submission_id=?`, submissionID.String())
		if errors.Is(err, sql.ErrNoRows) {
			return snapshot, nil
		}
		if err != nil {
			return nil, fmt.Errorf("load Submission Review: %w", err)
		}
		snapshot.Review, err = reviewRow.value()
		if err != nil {
			return nil, err
		}
		var rows []examIntegrityReviewDecisionRow
		if err = tx.Select(ctx, &rows, examIntegrityReviewDecisionSelect+` WHERE submission_review_id=? ORDER BY integrity_flag_id`, reviewRow.ID); err != nil {
			return nil, fmt.Errorf("list Integrity Review decisions: %w", err)
		}
		if len(rows) > model.SubmissionReviewMaximumFlags {
			return nil, invalidPersistedState("submission_review", "decisions", errors.New("decision count exceeds bound"))
		}
		snapshot.Decisions = make([]model.IntegrityReviewDecision, 0, len(rows))
		for _, row := range rows {
			decision, rowErr := row.value()
			if rowErr != nil {
				return nil, rowErr
			}
			snapshot.Decisions = append(snapshot.Decisions, *decision)
		}
		return snapshot, nil
	})
}

func (s *SQLExamIntegrityReviewStore) ListFlags(ctx context.Context, options store.ExamIntegrityFlagListOptions) (*store.ExamIntegrityFlagPage, error) {
	if !options.SubmissionID.IsValid() || options.Limit < 1 || options.Limit > store.ExamIntegrityReviewFlagReadMaximum ||
		(!options.AfterFlagID.IsZero() && !options.AfterFlagID.IsValid()) {
		return nil, store.NewErrInvalidInput("integrity_flag", "list_options", nil)
	}
	query := `SELECT f.id,f.exam_attempt_id AS attempt_id,f.generation,f.policy_kind AS kind,f.state,f.created_at,
		COUNT(ev.id) AS evidence_count,COALESCE(eval.overflow_count,0) AS overflow_count,
		COALESCE(eval.unresolved_missing_count,0) AS unresolved_missing_count
		FROM exam_submissions sub JOIN integrity_flags f ON f.exam_attempt_id=sub.exam_attempt_id
		LEFT JOIN integrity_evidence ev ON ev.integrity_flag_id=f.id
		LEFT JOIN exam_attempt_focus_loss_evaluations eval ON eval.exam_attempt_id=f.exam_attempt_id AND
			eval.generation=f.generation AND f.policy_kind='focus_loss'
		WHERE sub.id=? AND sub.sealed=true`
	args := []any{options.SubmissionID.String()}
	if !options.AfterFlagID.IsZero() {
		query += ` AND f.id>?`
		args = append(args, options.AfterFlagID.String())
	}
	query += ` GROUP BY f.id,f.exam_attempt_id,f.generation,f.policy_kind,f.state,f.created_at,eval.overflow_count,eval.unresolved_missing_count ORDER BY f.id LIMIT ?`
	args = append(args, options.Limit+1)
	var rows []struct {
		ID                     string    `db:"id"`
		AttemptID              string    `db:"attempt_id"`
		Kind                   string    `db:"kind"`
		State                  string    `db:"state"`
		Generation             int64     `db:"generation"`
		EvidenceCount          int       `db:"evidence_count"`
		OverflowCount          int64     `db:"overflow_count"`
		UnresolvedMissingCount int64     `db:"unresolved_missing_count"`
		CreatedAt              time.Time `db:"created_at"`
	}
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Integrity Flags: %w", err)
	}
	page := &store.ExamIntegrityFlagPage{HasMore: len(rows) > options.Limit}
	if page.HasMore {
		rows = rows[:options.Limit]
	}
	page.Items = make([]store.ExamIntegrityFlagSummary, 0, len(rows))
	for _, row := range rows {
		flagID, err := model.ParseIntegrityFlagID(row.ID)
		if err != nil {
			return nil, invalidPersistedState("integrity_flag", "id", err)
		}
		attemptID, err := model.ParseExamAttemptID(row.AttemptID)
		if err != nil {
			return nil, invalidPersistedState("integrity_flag", "attempt_id", err)
		}
		flag := model.IntegrityFlag{ID: flagID, AttemptID: attemptID, Generation: row.Generation,
			Kind: model.IntegrityPolicyKind(row.Kind), State: model.IntegrityFlagState(row.State), CreatedAt: model.TimeUTC(row.CreatedAt)}
		if err = flag.Validate(); err != nil {
			return nil, invalidPersistedState("integrity_flag", "value", err)
		}
		page.Items = append(page.Items, store.ExamIntegrityFlagSummary{Flag: flag, EvidenceCount: row.EvidenceCount,
			OverflowCount: row.OverflowCount, UnresolvedMissingCount: row.UnresolvedMissingCount})
	}
	return page, nil
}

func (s *SQLExamIntegrityReviewStore) ListEvidence(ctx context.Context, options store.ExamIntegrityEvidenceListOptions) (*store.ExamIntegrityEvidencePage, error) {
	if !options.SubmissionID.IsValid() || !options.FlagID.IsValid() || options.Limit < 1 ||
		options.Limit > store.ExamIntegrityReviewEvidenceReadMaximum || (!options.AfterEvidenceID.IsZero() && !options.AfterEvidenceID.IsValid()) {
		return nil, store.NewErrInvalidInput("integrity_evidence", "list_options", nil)
	}
	query := `SELECT ev.id,ev.exam_attempt_id,ev.participation_id,ev.integrity_flag_id,ev.generation,ev.policy_kind,
		ev.focus_loss_signal_id,ev.sequence,ev.duration_milliseconds,ev.source,ev.missing_before,ev.observed_at,ev.recorded_at
		FROM exam_submissions sub JOIN integrity_evidence ev ON ev.exam_attempt_id=sub.exam_attempt_id
		WHERE sub.id=? AND sub.sealed=true AND ev.integrity_flag_id=?`
	args := []any{options.SubmissionID.String(), options.FlagID.String()}
	if !options.AfterEvidenceID.IsZero() {
		query += ` AND ev.id>?`
		args = append(args, options.AfterEvidenceID.String())
	}
	query += ` ORDER BY ev.id LIMIT ?`
	args = append(args, options.Limit+1)
	var rows []examIntegrityEvidenceRow
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Integrity Evidence: %w", err)
	}
	page := &store.ExamIntegrityEvidencePage{HasMore: len(rows) > options.Limit}
	if page.HasMore {
		rows = rows[:options.Limit]
	}
	page.Items = make([]model.IntegrityEvidence, 0, len(rows))
	for _, row := range rows {
		evidence, err := row.value()
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, *evidence)
	}
	return page, nil
}

type examIntegrityEvidenceRow struct {
	ID                   string         `db:"id"`
	AttemptID            string         `db:"exam_attempt_id"`
	ParticipationID      string         `db:"participation_id"`
	FlagID               string         `db:"integrity_flag_id"`
	Kind                 string         `db:"policy_kind"`
	Generation           int64          `db:"generation"`
	SignalID             sql.NullString `db:"focus_loss_signal_id"`
	Sequence             sql.NullInt64  `db:"sequence"`
	DurationMilliseconds sql.NullInt64  `db:"duration_milliseconds"`
	MissingBefore        sql.NullInt64  `db:"missing_before"`
	Source               sql.NullString `db:"source"`
	ObservedAt           time.Time      `db:"observed_at"`
	RecordedAt           time.Time      `db:"recorded_at"`
}

func (row examIntegrityEvidenceRow) value() (*model.IntegrityEvidence, error) {
	id, err := model.ParseIntegrityEvidenceID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("integrity_evidence", "id", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("integrity_evidence", "attempt_id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return nil, invalidPersistedState("integrity_evidence", "participation_id", err)
	}
	flagID, err := model.ParseIntegrityFlagID(row.FlagID)
	if err != nil {
		return nil, invalidPersistedState("integrity_evidence", "flag_id", err)
	}
	var signalID model.FocusLossSignalID
	if row.SignalID.Valid {
		signalID, err = model.ParseFocusLossSignalID(row.SignalID.String)
		if err != nil {
			return nil, invalidPersistedState("integrity_evidence", "signal_id", err)
		}
	}
	evidence := &model.IntegrityEvidence{ID: id, AttemptID: attemptID, ParticipationID: participationID, FlagID: flagID, Generation: row.Generation,
		Kind: model.IntegrityPolicyKind(row.Kind), SignalID: signalID, Sequence: row.Sequence.Int64, DurationMilliseconds: row.DurationMilliseconds.Int64,
		Source: model.FocusLossSource(row.Source.String), MissingBefore: row.MissingBefore.Int64, ObservedAt: model.TimeUTC(row.ObservedAt), RecordedAt: model.TimeUTC(row.RecordedAt)}
	if err = evidence.Validate(); err != nil {
		return nil, invalidPersistedState("integrity_evidence", "value", err)
	}
	return evidence, nil
}

type integrityDiscrepancyRow struct {
	ID                   string         `db:"id"`
	SubmissionID         string         `db:"submission_id"`
	AttemptID            string         `db:"exam_attempt_id"`
	ParticipationID      string         `db:"participation_id"`
	Kind                 string         `db:"kind"`
	Generation           int64          `db:"generation"`
	SchemaVersion        int            `db:"schema_version"`
	SignalID             sql.NullString `db:"focus_loss_signal_id"`
	Sequence             sql.NullInt64  `db:"sequence"`
	DurationMilliseconds sql.NullInt64  `db:"duration_milliseconds"`
	Source               sql.NullString `db:"source"`
	MissingBefore        sql.NullInt64  `db:"missing_before"`
	CorrectionRevisionID sql.NullString `db:"correction_revision_id"`
	BrowserSourceID      sql.NullString `db:"browser_activity_source_session_id"`
	FinalSequence        sql.NullInt64  `db:"final_sequence"`
	GapReason            sql.NullString `db:"gap_reason"`
	UnresolvedCount      sql.NullInt64  `db:"unresolved_count"`
	ReceivedAt           time.Time      `db:"received_at"`
}

const integrityDiscrepancySelect = `SELECT id,submission_id,exam_attempt_id,participation_id,kind,generation,
	schema_version,focus_loss_signal_id,sequence,duration_milliseconds,source,missing_before,correction_revision_id,
	browser_activity_source_session_id::text,final_sequence,gap_reason,unresolved_count,received_at
	FROM integrity_discrepancies`

func (row integrityDiscrepancyRow) value() (*model.IntegrityDiscrepancy, error) {
	id, err := model.ParseIntegrityDiscrepancyID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("integrity_discrepancy", "id", err)
	}
	submissionID, err := model.ParseSubmissionID(row.SubmissionID)
	if err != nil {
		return nil, invalidPersistedState("integrity_discrepancy", "submission_id", err)
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("integrity_discrepancy", "attempt_id", err)
	}
	participationID, err := model.ParseAttemptParticipationID(row.ParticipationID)
	if err != nil {
		return nil, invalidPersistedState("integrity_discrepancy", "participation_id", err)
	}
	var signalID model.FocusLossSignalID
	if row.SignalID.Valid {
		signalID, err = model.ParseFocusLossSignalID(row.SignalID.String)
		if err != nil {
			return nil, invalidPersistedState("integrity_discrepancy", "signal_id", err)
		}
	}
	var correctionRevisionID model.ExamRevisionID
	if row.CorrectionRevisionID.Valid {
		correctionRevisionID, err = model.ParseExamRevisionID(row.CorrectionRevisionID.String)
		if err != nil {
			return nil, invalidPersistedState("integrity_discrepancy", "correction_revision_id", err)
		}
	}
	var browserSourceID model.BrowserSourceSessionID
	if row.BrowserSourceID.Valid {
		browserSourceID = model.BrowserSourceSessionID(row.BrowserSourceID.String)
		if !browserSourceID.IsValid() {
			return nil, invalidPersistedState("integrity_discrepancy", "browser_activity_source_session_id", errors.New("invalid Browser Source Session ID"))
		}
	}
	var finalSequence *int64
	if row.FinalSequence.Valid {
		sequence := row.FinalSequence.Int64
		finalSequence = &sequence
	}
	value, err := model.NewIntegrityDiscrepancy(model.IntegrityDiscrepancySpecification{ID: id,
		SubmissionID: submissionID, AttemptID: attemptID, ParticipationID: participationID, Generation: row.Generation,
		Kind: model.IntegrityDiscrepancyKind(row.Kind), SchemaVersion: row.SchemaVersion, SignalID: signalID,
		Sequence: row.Sequence.Int64, DurationMilliseconds: row.DurationMilliseconds.Int64, Source: model.FocusLossSource(row.Source.String),
		MissingBefore: row.MissingBefore.Int64, CorrectionRevisionID: correctionRevisionID, BrowserSourceSessionID: browserSourceID,
		FinalSequence: finalSequence, GapReason: row.GapReason.String, UnresolvedCount: row.UnresolvedCount.Int64,
		ReceivedAt: row.ReceivedAt})
	if err != nil {
		return nil, invalidPersistedState("integrity_discrepancy", "value", err)
	}
	return value, nil
}

func (s *SQLExamIntegrityReviewStore) ListDiscrepancies(ctx context.Context,
	options store.ExamIntegrityDiscrepancyListOptions,
) (*store.ExamIntegrityDiscrepancyPage, error) {
	if !options.SubmissionID.IsValid() || options.Limit < 1 || options.Limit > store.ExamIntegrityReviewDiscrepancyReadMaximum ||
		(!options.AfterDiscrepancyID.IsZero() && !options.AfterDiscrepancyID.IsValid()) {
		return nil, store.NewErrInvalidInput("integrity_discrepancy", "list_options", nil)
	}
	query := integrityDiscrepancySelect + ` WHERE submission_id=?`
	args := []any{options.SubmissionID.String()}
	if !options.AfterDiscrepancyID.IsZero() {
		query += ` AND id>?`
		args = append(args, options.AfterDiscrepancyID.String())
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, options.Limit+1)
	var rows []integrityDiscrepancyRow
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Integrity Discrepancies: %w", err)
	}
	page := &store.ExamIntegrityDiscrepancyPage{HasMore: len(rows) > options.Limit}
	if page.HasMore {
		rows = rows[:options.Limit]
	}
	page.Items = make([]model.IntegrityDiscrepancy, 0, len(rows))
	for _, row := range rows {
		value, err := row.value()
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, *value)
	}
	return page, nil
}

type examIntegrityReviewOutcome struct {
	Authorization store.ExamIntegrityReviewAuthorization `json:"authorization"`
	Review        model.SubmissionReview                 `json:"review"`
	Decision      *model.IntegrityReviewDecision         `json:"decision,omitempty"`
}

func (s *SQLExamIntegrityReviewStore) SaveDecision(ctx context.Context, input *store.ExamIntegrityReviewDecisionMutation, command *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	if err := validateReviewDecisionInput(input, command); err != nil {
		return nil, err
	}
	return s.runReviewMutation(ctx, "save Integrity Review decision", command, input.AuditEventID, input.AuditAt,
		func(ctx context.Context, tx *sqlxTxWrapper) (examIntegrityReviewOutcome, error) {
			return saveReviewDecision(ctx, tx, input)
		},
		func(ctx context.Context, tx *sqlxTxWrapper, value examIntegrityReviewOutcome, original string) error {
			if err := guardExamSittingManagerExam(ctx, tx, value.Authorization.ExamID, input.ActorUserID, input.ManagerOverride, true); err != nil {
				return err
			}
			return completeIntegrityReviewAudit(ctx, tx, value, input.AuditEventID, input.AuditAt, true, original)
		})
}

func (s *SQLExamIntegrityReviewStore) UpdateDraft(ctx context.Context, input *store.ExamIntegrityReviewDraftMutation, command *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	if err := validateReviewDraftInput(input, command); err != nil {
		return nil, err
	}
	return s.runReviewMutation(ctx, "update Integrity Review draft", command, input.AuditEventID, input.AuditAt,
		func(ctx context.Context, tx *sqlxTxWrapper) (examIntegrityReviewOutcome, error) {
			return updateReviewDraft(ctx, tx, input)
		},
		func(ctx context.Context, tx *sqlxTxWrapper, value examIntegrityReviewOutcome, original string) error {
			if err := guardExamSittingManagerExam(ctx, tx, value.Authorization.ExamID, input.ActorUserID, input.ManagerOverride, true); err != nil {
				return err
			}
			return completeIntegrityReviewAudit(ctx, tx, value, input.AuditEventID, input.AuditAt, true, original)
		})
}

func (s *SQLExamIntegrityReviewStore) Finalize(ctx context.Context, input *store.ExamIntegrityReviewFinalize, command *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	if err := validateReviewFinalizeInput(input, command, store.ExamIntegrityReviewFinalizeOperation); err != nil {
		return nil, err
	}
	return s.runReviewMutation(ctx, "finalize Integrity Review", command, input.AuditEventID, input.AuditAt,
		func(ctx context.Context, tx *sqlxTxWrapper) (examIntegrityReviewOutcome, error) {
			return finalizeReview(ctx, tx, input)
		},
		func(ctx context.Context, tx *sqlxTxWrapper, value examIntegrityReviewOutcome, original string) error {
			if err := guardExamSittingManagerExam(ctx, tx, value.Authorization.ExamID, input.ActorUserID, input.ManagerOverride, true); err != nil {
				return err
			}
			return completeIntegrityReviewAudit(ctx, tx, value, input.AuditEventID, input.AuditAt, true, original)
		})
}

func (s *SQLExamIntegrityReviewStore) Release(ctx context.Context, input *store.ExamIntegrityReviewRelease, command *store.CommandIdempotency) (*store.ExamIntegrityReviewMutationResult, error) {
	if input == nil || !input.CandidateUserID.IsValid() {
		return nil, store.NewErrInvalidInput("submission_review", "release", nil)
	}
	probe := &store.ExamIntegrityReviewFinalize{SubmissionID: input.SubmissionID, ReviewID: input.ReviewID, ActorUserID: input.ActorUserID, ManagerOverride: input.ManagerOverride, ExpectedReviewRevision: input.ExpectedReviewRevision, ChangedAt: input.ChangedAt, AuditEventID: input.AuditEventID, AuditAt: input.AuditAt}
	if err := validateReviewFinalizeInput(probe, command, store.ExamIntegrityReviewReleaseOperation); err != nil {
		return nil, err
	}
	return s.runReviewMutation(ctx, "release Student Result", command, input.AuditEventID, input.AuditAt,
		func(ctx context.Context, tx *sqlxTxWrapper) (examIntegrityReviewOutcome, error) {
			return releaseReview(ctx, tx, input)
		},
		func(ctx context.Context, tx *sqlxTxWrapper, value examIntegrityReviewOutcome, original string) error {
			if err := guardExamSittingManagerExam(ctx, tx, value.Authorization.ExamID, input.ActorUserID, input.ManagerOverride, true); err != nil {
				return err
			}
			return completeIntegrityReviewAudit(ctx, tx, value, input.AuditEventID, input.AuditAt, true, original)
		})
}

func (s *SQLExamIntegrityReviewStore) PrepareRelease(ctx context.Context, submissionID model.SubmissionID,
	reviewID model.SubmissionReviewID, expectedRevision int64,
) (*store.ExamIntegrityReviewReleasePreparation, error) {
	if !submissionID.IsValid() || !reviewID.IsValid() || expectedRevision < 1 {
		return nil, store.NewErrInvalidInput("submission_review", "release_preparation", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "prepare Student Result release", func(ctx context.Context,
		tx *sqlxTxWrapper,
	) (*store.ExamIntegrityReviewReleasePreparation, error) {
		review, err := loadReviewForMutation(ctx, tx, submissionID)
		if err != nil {
			return nil, err
		}
		if review.ID != reviewID {
			return nil, store.NewErrConflict("submission_review", "integrity_review_revision", nil)
		}
		replayed := review.State == model.SubmissionReviewFinalized &&
			review.ReleaseState == model.SubmissionReviewReleased && review.Revision == expectedRevision+1
		fresh := review.State == model.SubmissionReviewFinalized &&
			review.ReleaseState == model.SubmissionReviewWithheld && review.Revision == expectedRevision
		if !fresh && !replayed {
			return nil, store.NewErrConflict("submission_review", "integrity_review_state", nil)
		}
		var databaseNow time.Time
		if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
			return nil, fmt.Errorf("read Student Result release time: %w", err)
		}
		releaseAt := model.TimeFromMillis(model.MillisFromTime(databaseNow))
		if replayed {
			return &store.ExamIntegrityReviewReleasePreparation{Replayed: true, ReleaseAt: releaseAt}, nil
		}
		if _, err = tx.Exec(ctx, `INSERT INTO submission_review_release_preparations
			(submission_review_id,submission_id,expected_review_revision,release_at)
			VALUES (?,?,?,?) ON CONFLICT (submission_review_id) DO NOTHING`,
			reviewID.String(), submissionID.String(), expectedRevision, releaseAt); err != nil {
			return nil, fmt.Errorf("reserve Student Result release time: %w", err)
		}
		preparation, err := lockResultReleasePreparation(ctx, tx, reviewID)
		if err != nil {
			return nil, err
		}
		if preparation.SubmissionID != submissionID.String() || preparation.ExpectedReviewRevision != expectedRevision {
			return nil, store.NewErrConflict("submission_review", "result_release_time", nil)
		}
		return &store.ExamIntegrityReviewReleasePreparation{ReleaseAt: model.TimeUTC(preparation.ReleaseAt)}, nil
	})
}

func (s *SQLExamIntegrityReviewStore) runReviewMutation(ctx context.Context, name string, command *store.CommandIdempotency, auditID string, auditAt int64,
	execute func(context.Context, *sqlxTxWrapper) (examIntegrityReviewOutcome, error), replay func(context.Context, *sqlxTxWrapper, examIntegrityReviewOutcome, string) error,
) (*store.ExamIntegrityReviewMutationResult, error) {
	result, err := runIdempotentMutation(ctx, s.SQLStore, name, idempotentMutation[examIntegrityReviewOutcome]{command: command, auditEventID: auditID, execute: execute,
		encode: func(value examIntegrityReviewOutcome) ([]byte, error) { return encodeCommandOutcome(value) },
		decode: func(version int, data []byte) (examIntegrityReviewOutcome, error) {
			var value examIntegrityReviewOutcome
			if version != 1 {
				return value, fmt.Errorf("unsupported Integrity Review outcome version %d", version)
			}
			if err := decodeCommandOutcome(data, &value); err != nil {
				return value, err
			}
			if err := validateReviewOutcome(value); err != nil {
				return value, err
			}
			return value, nil
		}, completeReplay: replay})
	if err != nil {
		return nil, err
	}
	value := result.Value
	return &store.ExamIntegrityReviewMutationResult{Authorization: value.Authorization, Review: &value.Review, Decision: value.Decision, Replayed: result.Replayed}, nil
}

func validateReviewDecisionInput(input *store.ExamIntegrityReviewDecisionMutation, command *store.CommandIdempotency) error {
	if input == nil || command == nil || command.Operation != store.ExamIntegrityReviewDecisionOperation || !input.SubmissionID.IsValid() || !input.ReviewID.IsValid() || !input.DecisionID.IsValid() || !input.FlagID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedReviewRevision < 0 || input.ExpectedDecisionRevision < 0 || input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("submission_review", "decision", nil)
	}
	_, err := model.NewIntegrityReviewDecision(input.DecisionID, input.ReviewID, input.FlagID, input.Outcome, input.ActorUserID, input.PrivateRationale, input.ChangedAt)
	if err != nil {
		return store.NewErrInvalidInput("submission_review", "decision", nil).Wrap(err)
	}
	return nil
}

func validateReviewDraftInput(input *store.ExamIntegrityReviewDraftMutation, command *store.CommandIdempotency) error {
	if input == nil || command == nil || command.Operation != store.ExamIntegrityReviewDraftOperation || !input.SubmissionID.IsValid() || !input.ReviewID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedReviewRevision < 0 || input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("submission_review", "draft", nil)
	}
	review, err := model.NewSubmissionReview(input.ReviewID, input.SubmissionID, input.ActorUserID, input.ChangedAt)
	if err != nil {
		return store.NewErrInvalidInput("submission_review", "draft", nil).Wrap(err)
	}
	if err = review.UpdateDraft(1, input.ManagerNotes, input.StudentRemarksMarkdown, input.ChangedAt); err != nil {
		return store.NewErrInvalidInput("submission_review", "draft", nil).Wrap(err)
	}
	return nil
}

func validateReviewFinalizeInput(input *store.ExamIntegrityReviewFinalize, command *store.CommandIdempotency, operation string) error {
	if input == nil || command == nil || command.Operation != operation || !input.SubmissionID.IsValid() || !input.ReviewID.IsValid() || !input.ActorUserID.IsValid() || input.ExpectedReviewRevision < 1 || input.ChangedAt.IsZero() || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return store.NewErrInvalidInput("submission_review", "terminal", nil)
	}
	return nil
}

func lockReviewScope(ctx context.Context, tx *sqlxTxWrapper, submissionID model.SubmissionID, actorID model.UserID, override bool) (*store.ExamIntegrityReviewAuthorization, error) {
	// Submission identity is immutable. Resolve its owning Exam without taking
	// a lower-aggregate lock, then preserve the installation lock order by
	// authorizing/locking the Exam before locking Submission and Attempt.
	var examIDString string
	if err := tx.Get(ctx, &examIDString, `SELECT a.exam_id FROM exam_submissions sub
		JOIN exam_attempts a ON a.id=sub.exam_attempt_id
		WHERE sub.id=? AND sub.sealed=true`, submissionID.String()); err != nil {
		return nil, translateError("submission", submissionID.String(), err)
	}
	examID, err := model.ParseExamID(examIDString)
	if err != nil {
		return nil, invalidPersistedState("submission", "exam_id", err)
	}
	if err = guardExamSittingManagerExam(ctx, tx, examID, actorID, override, true); err != nil {
		return nil, err
	}
	var row examIntegrityReviewAuthorizationRow
	if err := tx.Get(ctx, &row, examIntegrityReviewAuthorizationSelect+` FOR UPDATE OF sub,a`, submissionID.String()); err != nil {
		return nil, translateError("submission", submissionID.String(), err)
	}
	auth, err := row.value()
	if err != nil {
		return nil, err
	}
	if auth.ExamID != examID {
		return nil, invalidPersistedState("submission", "exam_id", errors.New("ownership changed while locking"))
	}
	return auth, nil
}

func loadReviewForMutation(ctx context.Context, tx *sqlxTxWrapper, submissionID model.SubmissionID) (*model.SubmissionReview, error) {
	var row examSubmissionReviewRow
	if err := tx.Get(ctx, &row, examSubmissionReviewSelect+` WHERE submission_id=? FOR UPDATE`, submissionID.String()); err != nil {
		return nil, translateError("submission_review", submissionID.String(), err)
	}
	return row.value()
}

func insertReview(ctx context.Context, tx *sqlxTxWrapper, review *model.SubmissionReview, attemptID model.ExamAttemptID) error {
	_, err := tx.Exec(ctx, `INSERT INTO submission_reviews(id,submission_id,exam_attempt_id,state,release_state,revision,created_by_user_id,manager_notes,student_remarks_markdown,flag_count,evidence_count,discrepancy_count,evidence_inventory_digest,created_at,updated_at,finalized_at,finalized_by_user_id,released_at,released_by_user_id) VALUES (?,?,?,?,?,?,?,?,?,?,?, ?,NULL,?,?,NULL,NULL,NULL,NULL)`, review.ID.String(), review.SubmissionID.String(), attemptID.String(), string(review.State), string(review.ReleaseState), review.Revision, review.CreatedByUserID.String(), review.ManagerNotes, review.StudentRemarksMarkdown, review.FlagCount, review.EvidenceCount, review.DiscrepancyCount, review.CreatedAt, review.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create Submission Review: %w", translateError("submission_review", review.ID.String(), err))
	}
	return nil
}

func persistReview(ctx context.Context, tx *sqlxTxWrapper, review *model.SubmissionReview, expected int64) error {
	var finalizedAt, releasedAt any
	var finalizedBy, releasedBy any
	if review.FinalizedAt.Valid {
		finalizedAt = review.FinalizedAt.Time
		finalizedBy = review.FinalizedByUserID.String()
	}
	if review.ReleasedAt.Valid {
		releasedAt = review.ReleasedAt.Time
		releasedBy = review.ReleasedByUserID.String()
	}
	result, err := tx.Exec(ctx, `UPDATE submission_reviews SET state=?,release_state=?,revision=?,manager_notes=?,student_remarks_markdown=?,flag_count=?,evidence_count=?,discrepancy_count=?,evidence_inventory_digest=?,updated_at=?,finalized_at=?,finalized_by_user_id=?,released_at=?,released_by_user_id=? WHERE id=? AND revision=?`, string(review.State), string(review.ReleaseState), review.Revision, review.ManagerNotes, review.StudentRemarksMarkdown, review.FlagCount, review.EvidenceCount, review.DiscrepancyCount, nullString(review.EvidenceInventoryDigest), review.UpdatedAt, finalizedAt, finalizedBy, releasedAt, releasedBy, review.ID.String(), expected)
	if err != nil {
		return fmt.Errorf("persist Submission Review: %w", err)
	}
	affected, e := result.RowsAffected()
	if e != nil || affected != 1 {
		return store.NewErrConflict("submission_review", "integrity_review_revision", e)
	}
	return nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func saveReviewDecision(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamIntegrityReviewDecisionMutation) (examIntegrityReviewOutcome, error) {
	auth, err := lockReviewScope(ctx, tx, input.SubmissionID, input.ActorUserID, input.ManagerOverride)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	var review *model.SubmissionReview
	if input.ExpectedReviewRevision == 0 {
		review, err = model.NewSubmissionReview(input.ReviewID, input.SubmissionID, input.ActorUserID, input.ChangedAt)
		if err == nil {
			err = insertReview(ctx, tx, review, auth.AttemptID)
		}
	} else {
		review, err = loadReviewForMutation(ctx, tx, input.SubmissionID)
		if err == nil && (review.ID != input.ReviewID || review.Revision != input.ExpectedReviewRevision) {
			err = store.NewErrConflict("submission_review", "integrity_review_revision", nil)
		}
	}
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	var flagExists bool
	if err = tx.Get(ctx, &flagExists, `SELECT EXISTS(SELECT 1 FROM integrity_flags WHERE id=? AND exam_attempt_id=?)`, input.FlagID.String(), auth.AttemptID.String()); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if !flagExists {
		return examIntegrityReviewOutcome{}, store.NewErrNotFound("integrity_flag", input.FlagID.String())
	}
	var decision *model.IntegrityReviewDecision
	if input.ExpectedDecisionRevision == 0 {
		var exists bool
		if err = tx.Get(ctx, &exists, `SELECT EXISTS(SELECT 1 FROM integrity_review_decisions WHERE submission_review_id=? AND integrity_flag_id=?)`, review.ID.String(), input.FlagID.String()); err != nil {
			return examIntegrityReviewOutcome{}, err
		}
		if exists {
			return examIntegrityReviewOutcome{}, store.NewErrConflict("integrity_review_decision", "integrity_decision_revision", nil)
		}
		decision, err = model.NewIntegrityReviewDecision(input.DecisionID, review.ID, input.FlagID, input.Outcome, input.ActorUserID, input.PrivateRationale, input.ChangedAt)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO integrity_review_decisions(id,submission_review_id,exam_attempt_id,integrity_flag_id,outcome,revision,actor_user_id,private_rationale,decided_at) VALUES(?,?,?,?,?,?,?,?,?)`, decision.ID.String(), review.ID.String(), auth.AttemptID.String(), decision.FlagID.String(), string(decision.Outcome), decision.Revision, decision.ActorUserID.String(), decision.PrivateRationale, decision.DecidedAt)
		}
	} else {
		var row examIntegrityReviewDecisionRow
		err = tx.Get(ctx, &row, examIntegrityReviewDecisionSelect+` WHERE submission_review_id=? AND integrity_flag_id=? FOR UPDATE`, review.ID.String(), input.FlagID.String())
		if err == nil {
			decision, err = row.value()
		}
		if err == nil {
			err = decision.Revise(input.ExpectedDecisionRevision, input.Outcome, input.ActorUserID, input.PrivateRationale, input.ChangedAt)
		}
		if err == nil {
			var result sql.Result
			result, err = tx.Exec(ctx, `UPDATE integrity_review_decisions SET outcome=?,revision=?,actor_user_id=?,private_rationale=?,decided_at=? WHERE id=? AND revision=?`, string(decision.Outcome), decision.Revision, decision.ActorUserID.String(), decision.PrivateRationale, decision.DecidedAt, decision.ID.String(), input.ExpectedDecisionRevision)
			if err == nil {
				affected, e := result.RowsAffected()
				if e != nil || affected != 1 {
					err = store.NewErrConflict("integrity_review_decision", "integrity_decision_revision", e)
				}
			}
		}
	}
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if input.ExpectedReviewRevision > 0 {
		if err = review.TouchDraft(input.ExpectedReviewRevision, input.ChangedAt); err != nil {
			return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_revision", err)
		}
		if err = persistReview(ctx, tx, review, input.ExpectedReviewRevision); err != nil {
			return examIntegrityReviewOutcome{}, err
		}
	}
	out := examIntegrityReviewOutcome{Authorization: *auth, Review: *review, Decision: decision}
	if err = completeIntegrityReviewAudit(ctx, tx, out, input.AuditEventID, input.AuditAt, false, ""); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	return out, nil
}

func updateReviewDraft(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamIntegrityReviewDraftMutation) (examIntegrityReviewOutcome, error) {
	auth, err := lockReviewScope(ctx, tx, input.SubmissionID, input.ActorUserID, input.ManagerOverride)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	var review *model.SubmissionReview
	if input.ExpectedReviewRevision == 0 {
		review, err = model.NewSubmissionReview(input.ReviewID, input.SubmissionID, input.ActorUserID, input.ChangedAt)
		if err == nil {
			review.ManagerNotes = input.ManagerNotes
			review.StudentRemarksMarkdown = input.StudentRemarksMarkdown
			err = review.Validate()
		}
		if err == nil {
			err = insertReview(ctx, tx, review, auth.AttemptID)
		}
	} else {
		review, err = loadReviewForMutation(ctx, tx, input.SubmissionID)
		if err == nil && (review.ID != input.ReviewID || review.Revision != input.ExpectedReviewRevision) {
			err = store.NewErrConflict("submission_review", "integrity_review_revision", nil)
		}
		if err == nil {
			err = review.UpdateDraft(input.ExpectedReviewRevision, input.ManagerNotes, input.StudentRemarksMarkdown, input.ChangedAt)
		}
		if err == nil {
			err = persistReview(ctx, tx, review, input.ExpectedReviewRevision)
		}
	}
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	out := examIntegrityReviewOutcome{Authorization: *auth, Review: *review}
	if err = completeIntegrityReviewAudit(ctx, tx, out, input.AuditEventID, input.AuditAt, false, ""); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	return out, nil
}

func finalizeReview(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamIntegrityReviewFinalize) (examIntegrityReviewOutcome, error) {
	auth, err := lockReviewScope(ctx, tx, input.SubmissionID, input.ActorUserID, input.ManagerOverride)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	review, err := loadReviewForMutation(ctx, tx, input.SubmissionID)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if review.ID != input.ReviewID || review.Revision != input.ExpectedReviewRevision {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_revision", nil)
	}
	var submissionState string
	if err = tx.Get(ctx, &submissionState, `SELECT integrity_state FROM exam_submissions WHERE id=? AND sealed=true`, input.SubmissionID.String()); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if submissionState != "settled" && submissionState != "gapped" {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_incomplete", nil)
	}
	type flagInventoryRow struct {
		FlagID           string         `db:"flag_id"`
		DecisionID       sql.NullString `db:"decision_id"`
		DecisionRevision sql.NullInt64  `db:"decision_revision"`
	}
	var flags []flagInventoryRow
	if err = tx.Select(ctx, &flags, `SELECT f.id AS flag_id,d.id AS decision_id,d.revision AS decision_revision FROM integrity_flags f LEFT JOIN integrity_review_decisions d ON d.integrity_flag_id=f.id AND d.submission_review_id=? WHERE f.exam_attempt_id=? ORDER BY f.id LIMIT 201 FOR SHARE OF f`, review.ID.String(), auth.AttemptID.String()); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if len(flags) > model.SubmissionReviewMaximumFlags {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_too_large", nil)
	}
	flagItems := make([]model.IntegrityReviewInventoryFlag, 0, len(flags))
	for _, row := range flags {
		if !row.DecisionID.Valid {
			return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_incomplete", nil)
		}
		flagID, e := model.ParseIntegrityFlagID(row.FlagID)
		if e != nil {
			return examIntegrityReviewOutcome{}, e
		}
		decisionID, e := model.ParseIntegrityReviewDecisionID(row.DecisionID.String)
		if e != nil {
			return examIntegrityReviewOutcome{}, e
		}
		flagItems = append(flagItems, model.IntegrityReviewInventoryFlag{FlagID: flagID, DecisionID: decisionID, DecisionRevision: row.DecisionRevision.Int64})
	}
	type evidenceInventoryRow struct {
		EvidenceID string `db:"evidence_id"`
		FlagID     string `db:"flag_id"`
	}
	var evidenceRows []evidenceInventoryRow
	if err = tx.Select(ctx, &evidenceRows, `SELECT e.id AS evidence_id,e.integrity_flag_id AS flag_id FROM integrity_evidence e WHERE e.exam_attempt_id=? ORDER BY e.id LIMIT 20001 FOR SHARE`, auth.AttemptID.String()); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if len(evidenceRows) > model.SubmissionReviewMaximumEvidence {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_too_large", nil)
	}
	evidenceItems := make([]model.IntegrityReviewInventoryEvidence, 0, len(evidenceRows))
	for _, row := range evidenceRows {
		evidenceID, e := model.ParseIntegrityEvidenceID(row.EvidenceID)
		if e != nil {
			return examIntegrityReviewOutcome{}, e
		}
		flagID, e := model.ParseIntegrityFlagID(row.FlagID)
		if e != nil {
			return examIntegrityReviewOutcome{}, e
		}
		evidenceItems = append(evidenceItems, model.IntegrityReviewInventoryEvidence{EvidenceID: evidenceID, FlagID: flagID})
	}
	var discrepancyRows []struct {
		DiscrepancyID string `db:"discrepancy_id"`
	}
	if err = tx.Select(ctx, &discrepancyRows, `SELECT id AS discrepancy_id FROM integrity_discrepancies
		WHERE submission_id=? ORDER BY id LIMIT 201 FOR SHARE`, input.SubmissionID.String()); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if len(discrepancyRows) > model.SubmissionReviewMaximumDiscrepancies {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_too_large", nil)
	}
	discrepancyItems := make([]model.IntegrityReviewInventoryDiscrepancy, 0, len(discrepancyRows))
	for _, row := range discrepancyRows {
		id, e := model.ParseIntegrityDiscrepancyID(row.DiscrepancyID)
		if e != nil {
			return examIntegrityReviewOutcome{}, e
		}
		discrepancyItems = append(discrepancyItems, model.IntegrityReviewInventoryDiscrepancy{DiscrepancyID: id})
	}
	digest, err := model.IntegrityReviewInventoryDigest(flagItems, evidenceItems, discrepancyItems)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	for _, item := range flagItems {
		if _, err = tx.Exec(ctx, `INSERT INTO submission_review_inventory_flags(submission_review_id,exam_attempt_id,integrity_flag_id,decision_id,decision_revision) VALUES(?,?,?,?,?)`, review.ID.String(), auth.AttemptID.String(), item.FlagID.String(), item.DecisionID.String(), item.DecisionRevision); err != nil {
			return examIntegrityReviewOutcome{}, err
		}
	}
	for _, item := range evidenceItems {
		if _, err = tx.Exec(ctx, `INSERT INTO submission_review_inventory_evidence(submission_review_id,exam_attempt_id,integrity_flag_id,integrity_evidence_id) VALUES(?,?,?,?)`, review.ID.String(), auth.AttemptID.String(), item.FlagID.String(), item.EvidenceID.String()); err != nil {
			return examIntegrityReviewOutcome{}, err
		}
	}
	for _, item := range discrepancyItems {
		if _, err = tx.Exec(ctx, `INSERT INTO submission_review_inventory_discrepancies
			(submission_review_id,submission_id,exam_attempt_id,integrity_discrepancy_id) VALUES(?,?,?,?)`,
			review.ID.String(), review.SubmissionID.String(), auth.AttemptID.String(), item.DiscrepancyID.String()); err != nil {
			return examIntegrityReviewOutcome{}, err
		}
	}
	if err = review.Finalize(input.ExpectedReviewRevision, input.ActorUserID, len(flagItems), len(evidenceItems), len(discrepancyItems), digest, input.ChangedAt); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if err = persistReview(ctx, tx, review, input.ExpectedReviewRevision); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	out := examIntegrityReviewOutcome{Authorization: *auth, Review: *review}
	if err = completeIntegrityReviewAudit(ctx, tx, out, input.AuditEventID, input.AuditAt, false, ""); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	return out, nil
}

func releaseReview(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamIntegrityReviewRelease) (examIntegrityReviewOutcome, error) {
	recipient, err := lockMailRecipientUser(ctx, tx, input.CandidateUserID)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	auth, err := lockReviewScope(ctx, tx, input.SubmissionID, input.ActorUserID, input.ManagerOverride)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	review, err := loadReviewForMutation(ctx, tx, input.SubmissionID)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if review.ID != input.ReviewID || review.Revision != input.ExpectedReviewRevision {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_revision", nil)
	}
	preparation, err := lockResultReleasePreparation(ctx, tx, input.ReviewID)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	var databaseNow time.Time
	if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
		return examIntegrityReviewOutcome{}, fmt.Errorf("read terminal Student Result release time: %w", err)
	}
	releaseAt := model.TimeUTC(preparation.ReleaseAt)
	if preparation.SubmissionID != input.SubmissionID.String() ||
		preparation.ExpectedReviewRevision != input.ExpectedReviewRevision ||
		!releaseAt.Equal(model.TimeFromMillis(input.AuditAt)) || !releaseAt.Equal(input.ChangedAt) ||
		releaseAt.After(model.TimeUTC(databaseNow)) {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "result_release_time", nil)
	}
	if auth.CandidateUserID != input.CandidateUserID || recipient.Revision != input.ExpectedRecipientRevision {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "result_release_recipient_changed", nil)
	}
	payloadKeyID, err := validateResultReleaseMail(input.Notice, recipient, input.ReviewID, releaseAt)
	if err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if payloadKeyID != "" {
		if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
			return examIntegrityReviewOutcome{}, err
		}
	}
	if err = review.Release(input.ExpectedReviewRevision, input.ActorUserID, releaseAt); err != nil {
		return examIntegrityReviewOutcome{}, store.NewErrConflict("submission_review", "integrity_review_state", err)
	}
	if err = persistReview(ctx, tx, review, input.ExpectedReviewRevision); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	out := examIntegrityReviewOutcome{Authorization: *auth, Review: *review}
	if err = completeIntegrityReviewAudit(ctx, tx, out, input.AuditEventID, input.AuditAt, false, ""); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if err = insertResultReleaseMail(ctx, tx, input.Notice, payloadKeyID); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	if err = deleteResultReleasePreparation(ctx, tx, input.ReviewID); err != nil {
		return examIntegrityReviewOutcome{}, err
	}
	return out, nil
}

type resultReleasePreparationRow struct {
	SubmissionID           string    `db:"submission_id"`
	ExpectedReviewRevision int64     `db:"expected_review_revision"`
	ReleaseAt              time.Time `db:"release_at"`
}

func lockResultReleasePreparation(ctx context.Context, tx *sqlxTxWrapper,
	reviewID model.SubmissionReviewID,
) (*resultReleasePreparationRow, error) {
	var row resultReleasePreparationRow
	if err := tx.Get(ctx, &row, `SELECT submission_id,expected_review_revision,release_at
		FROM submission_review_release_preparations WHERE submission_review_id=? FOR UPDATE`, reviewID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.NewErrConflict("submission_review", "result_release_time", nil)
		}
		return nil, translateError("submission_review_release_preparation", reviewID.String(), err)
	}
	return &row, nil
}

func deleteResultReleasePreparation(ctx context.Context, tx *sqlxTxWrapper,
	reviewID model.SubmissionReviewID,
) error {
	result, err := tx.Exec(ctx, `DELETE FROM submission_review_release_preparations WHERE submission_review_id=?`, reviewID.String())
	if err != nil {
		return fmt.Errorf("consume Student Result release preparation: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return store.NewErrConflict("submission_review", "result_release_time", rowsErr)
	}
	return nil
}

func completeIntegrityReviewAudit(ctx context.Context, tx *sqlxTxWrapper, out examIntegrityReviewOutcome, auditID string, auditAt int64, replayed bool, original string) error {
	data := map[string]any{"submission_id": out.Authorization.SubmissionID.String(), "submission_review_id": out.Review.ID.String(), "state": string(out.Review.State), "release_state": string(out.Review.ReleaseState), "revision": out.Review.Revision}
	if out.Decision != nil {
		data["integrity_flag_id"] = out.Decision.FlagID.String()
		data["decision_revision"] = out.Decision.Revision
		data["outcome"] = string(out.Decision.Outcome)
	}
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = original
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	_, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt)
	return err
}

func validateReviewOutcome(out examIntegrityReviewOutcome) error {
	if !out.Authorization.SubmissionID.IsValid() || out.Review.Validate() != nil || out.Review.SubmissionID != out.Authorization.SubmissionID || (out.Decision != nil && (out.Decision.Validate() != nil || out.Decision.ReviewID != out.Review.ID)) {
		return errors.New("invalid Integrity Review outcome")
	}
	return nil
}

func (s *SQLExamIntegrityReviewStore) GetReleasedStudentResult(ctx context.Context, attemptID model.ExamAttemptID, candidateID model.UserID) (*model.StudentResult, error) {
	if !attemptID.IsValid() || !candidateID.IsValid() {
		return nil, store.NewErrInvalidInput("student_result", "identity", nil)
	}
	var row struct {
		ReviewID        string    `db:"review_id"`
		SubmissionID    string    `db:"submission_id"`
		AttemptID       string    `db:"attempt_id"`
		CandidateUserID string    `db:"candidate_user_id"`
		StudentRemarks  string    `db:"student_remarks"`
		ReleasedAt      time.Time `db:"released_at"`
	}
	if err := s.GetMaster().Get(ctx, &row, `SELECT r.id AS review_id,r.submission_id,a.id AS attempt_id,a.candidate_user_id,r.student_remarks_markdown AS student_remarks,r.released_at FROM submission_reviews r JOIN exam_submissions sub ON sub.id=r.submission_id JOIN exam_attempts a ON a.id=sub.exam_attempt_id WHERE a.id=? AND a.candidate_user_id=? AND r.state='finalized' AND r.release_state='released'`, attemptID.String(), candidateID.String()); err != nil {
		return nil, translateError("student_result", attemptID.String(), err)
	}
	reviewID, err := model.ParseSubmissionReviewID(row.ReviewID)
	if err != nil {
		return nil, err
	}
	submissionID, err := model.ParseSubmissionID(row.SubmissionID)
	if err != nil {
		return nil, err
	}
	return &model.StudentResult{ReviewID: reviewID, SubmissionID: submissionID, AttemptID: attemptID, CandidateUserID: candidateID, StudentRemarksMarkdown: row.StudentRemarks, ReleasedAt: model.TimeUTC(row.ReleasedAt)}, nil
}
