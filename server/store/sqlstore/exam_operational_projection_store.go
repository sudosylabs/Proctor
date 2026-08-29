// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s *sqlExamAttemptStore) ListSessionRevocationInvalidationTargets(
	ctx context.Context,
	candidateID model.UserID,
	sessionIDs []model.SessionID,
) ([]store.ExamAttemptInvalidationTarget, error) {
	if !candidateID.IsValid() || len(sessionIDs) == 0 || len(sessionIDs) > 1000 {
		return nil, store.NewErrInvalidInput("exam_attempt", "session_revocation_invalidation_targets", nil)
	}
	encoded := make([]string, len(sessionIDs))
	for index, sessionID := range sessionIDs {
		if !sessionID.IsValid() {
			return nil, store.NewErrInvalidInput("exam_attempt", "session_revocation_invalidation_targets", nil)
		}
		encoded[index] = sessionID.String()
	}
	var rows []struct {
		ExamID          string `db:"exam_id"`
		SittingID       string `db:"exam_sitting_id"`
		CandidateUserID string `db:"candidate_user_id"`
	}
	if err := s.GetMaster().Select(ctx, &rows, `
		SELECT DISTINCT a.exam_id,a.exam_sitting_id,a.candidate_user_id
		  FROM exam_attempt_participations p
		  JOIN exam_attempts a ON a.id=p.exam_attempt_id
		  JOIN exam_attempt_suspensions sus
		    ON sus.exam_attempt_id=a.id AND sus.participation_id=p.id AND sus.state='active'
		 WHERE a.candidate_user_id=? AND a.state='suspended'
		   AND p.session_id=ANY(?) AND p.end_reason='policy_suspended'
		 ORDER BY a.exam_id,a.exam_sitting_id`, candidateID.String(), pq.Array(encoded)); err != nil {
		return nil, fmt.Errorf("list Session revocation Attempt invalidation targets: %w", err)
	}
	targets := make([]store.ExamAttemptInvalidationTarget, 0, len(rows))
	for _, row := range rows {
		examID, err := model.ParseExamID(row.ExamID)
		if err != nil {
			return nil, invalidPersistedState("exam_attempt", "exam_id", err)
		}
		sittingID, err := model.ParseExamSittingID(row.SittingID)
		if err != nil {
			return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", err)
		}
		rowCandidateID, err := model.ParseUserID(row.CandidateUserID)
		if err != nil || rowCandidateID != candidateID {
			return nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
		}
		targets = append(targets, store.ExamAttemptInvalidationTarget{
			ExamID: examID, SittingID: sittingID, CandidateUserID: rowCandidateID,
		})
	}
	return targets, nil
}

type candidateExamActivityRow struct {
	ServerTime              time.Time      `db:"server_time"`
	DesktopEligible         bool           `db:"desktop_eligible"`
	ExamArchivedAt          sql.NullTime   `db:"exam_archived_at"`
	ExamID                  string         `db:"exam_id"`
	SittingID               string         `db:"sitting_id"`
	Title                   string         `db:"title"`
	AcademicUnitID          string         `db:"academic_unit_id"`
	AcademicUnitDisplayName string         `db:"academic_unit_display_name"`
	ClassID                 string         `db:"class_id"`
	ClassDisplayName        string         `db:"class_display_name"`
	ScheduledStartAt        time.Time      `db:"scheduled_start_at"`
	ScheduledEndAt          time.Time      `db:"scheduled_end_at"`
	SittingState            string         `db:"sitting_state"`
	SittingReasonCode       sql.NullString `db:"sitting_reason_code"`
	CurrentMembership       bool           `db:"current_membership"`
	ManagementAuthority     bool           `db:"management_authority"`
	OtherUnresolved         bool           `db:"other_unresolved"`
	EstablishedAccess       bool           `db:"established_access"`
	AttemptID               sql.NullString `db:"attempt_id"`
	AttemptState            sql.NullString `db:"attempt_state"`
	SuspensionReason        sql.NullString `db:"suspension_reason"`
	SubmissionID            sql.NullString `db:"submission_id"`
	SubmittedAt             sql.NullTime   `db:"submitted_at"`
	SubmissionProvenance    sql.NullString `db:"submission_provenance"`
	ReleasedAt              sql.NullTime   `db:"released_at"`
}

func (s *sqlExamAttemptStore) ListCandidateActivity(ctx context.Context,
	options store.CandidateExamActivityListOptions,
) (*store.CandidateExamActivityPage, error) {
	desktopSelector := options.DesktopSessionID.IsValid() && options.DesktopRegistrationID.IsValid() &&
		model.IsValidDPoPKeyThumbprint(options.DPoPKeyThumbprint)
	if !options.CandidateUserID.IsValid() || options.Limit < 1 || options.Limit > 201 ||
		(options.BeforeScheduledStart.IsZero() != options.BeforeSittingID.IsZero()) ||
		(!options.DesktopSessionID.IsZero() || !options.DesktopRegistrationID.IsZero() || options.DPoPKeyThumbprint != "") && !desktopSelector {
		return nil, store.NewErrInvalidInput("candidate_exam_activity", "list_options", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "list candidate Exam activity", func(ctx context.Context,
		tx *sqlxTxWrapper,
	) (*store.CandidateExamActivityPage, error) {
		var serverTime time.Time
		if err := tx.Get(ctx, &serverTime, `SELECT statement_timestamp()`); err != nil {
			return nil, fmt.Errorf("read candidate Exam activity time: %w", err)
		}
		serverTime = model.TimeUTC(serverTime)
		query := `WITH RECURSIVE unit_ancestors(root_id,id) AS (
			SELECT id,id FROM academic_units WHERE archived_at IS NULL
			UNION ALL
			SELECT child.root_id,parent.id FROM unit_ancestors child
			JOIN academic_units current ON current.id=child.id
			JOIN academic_units parent ON parent.id=current.parent_id AND parent.archived_at IS NULL
		)
		SELECT ?::timestamptz AS server_time,
			EXISTS (SELECT 1 FROM sessions session JOIN desktop_registrations registration
				ON registration.id=session.desktop_registration_id AND registration.user_id=session.user_id
				WHERE session.id=? AND session.user_id=? AND session.client_type='desktop' AND session.revoked_at IS NULL
					AND session.expires_at>? AND session.desktop_registration_id=? AND session.dpop_key_thumbprint=?
					AND registration.revoked_at IS NULL AND registration.key_thumbprint=?) AS desktop_eligible,
			e.id AS exam_id,e.archived_at AS exam_archived_at,s.id AS sitting_id,r.title,e.academic_unit_id,unit.display_name AS academic_unit_display_name,
			s.class_id,class.display_name AS class_display_name,s.scheduled_start_at,s.scheduled_end_at,
			s.state AS sitting_state,s.reason_code AS sitting_reason_code,
			EXISTS (SELECT 1 FROM class_members current_member WHERE current_member.class_id=s.class_id
				AND current_member.user_id=? AND current_member.start_at<=?
				AND (current_member.end_at IS NULL OR current_member.end_at>?)
				AND (current_member.archived_at IS NULL OR current_member.archived_at>?)) AS current_membership,
			EXISTS (SELECT 1 FROM role_bindings binding JOIN roles role ON role.id=binding.role_id
				WHERE binding.user_id=? AND binding.archived_at IS NULL AND binding.start_at<=?
				AND (binding.end_at IS NULL OR binding.end_at>?) AND role.archived_at IS NULL AND (
					(role.name=? AND ?=ANY(role.permissions) AND binding.scope_type='institution') OR (
						?=ANY(role.permissions)
						AND (binding.scope_type='institution' OR (binding.scope_type='academic_unit'
							AND binding.scope_id IN (SELECT id FROM unit_ancestors WHERE root_id=e.academic_unit_id)))
						AND EXISTS (SELECT 1 FROM exam_managers manager WHERE manager.exam_id=e.id AND manager.user_id=?)
						AND EXISTS (SELECT 1 FROM academic_unit_members member WHERE member.academic_unit_id=e.academic_unit_id
							AND member.user_id=? AND member.archived_at IS NULL AND member.start_at<=?
							AND (member.end_at IS NULL OR member.end_at>?))
					)
			)) AS management_authority,
			EXISTS (SELECT 1 FROM exam_attempts other_attempt WHERE other_attempt.candidate_user_id=?
				AND other_attempt.exam_sitting_id<>s.id AND other_attempt.state IN ('ready','active','suspended')) AS other_unresolved,
			EXISTS (SELECT 1 FROM exam_attempt_participations participation
				WHERE participation.exam_attempt_id=a.id AND participation.state='active' AND participation.lease_expires_at>?
				AND participation.session_id=?
				AND EXISTS (SELECT 1 FROM exam_attempt_connections connection WHERE connection.exam_attempt_id=a.id
					AND connection.participation_id=participation.id AND connection.state='open' AND connection.session_id=?)) AS established_access,
			a.id AS attempt_id,a.state AS attempt_state,suspension.candidate_reason AS suspension_reason,
			submission.id AS submission_id,submission.submitted_at,submission.provenance AS submission_provenance,
			review.released_at
		FROM exam_sittings s JOIN exams e ON e.id=s.exam_id JOIN exam_revisions r ON r.id=s.exam_revision_id
		JOIN academic_units unit ON unit.id=e.academic_unit_id JOIN classes class ON class.id=s.class_id
		LEFT JOIN exam_attempts a ON a.exam_sitting_id=s.id AND a.candidate_user_id=?
		LEFT JOIN exam_submissions submission ON submission.exam_attempt_id=a.id AND submission.sealed=true
		LEFT JOIN submission_reviews review ON review.submission_id=submission.id AND review.state='finalized' AND review.release_state='released'
		LEFT JOIN exam_attempt_suspensions suspension ON suspension.exam_attempt_id=a.id AND suspension.state='active'
		WHERE (`
		args := []any{serverTime, options.DesktopSessionID.String(), options.CandidateUserID.String(), serverTime,
			options.DesktopRegistrationID.String(), options.DPoPKeyThumbprint, options.DPoPKeyThumbprint,
			options.CandidateUserID.String(), serverTime, serverTime, serverTime,
			options.CandidateUserID.String(), serverTime, serverTime, model.SystemAdministratorRoleName,
			string(model.ActionExamManageOverride), string(model.ActionExamManage), options.CandidateUserID.String(),
			options.CandidateUserID.String(), serverTime, serverTime,
			options.CandidateUserID.String(), serverTime, options.DesktopSessionID.String(), options.DesktopSessionID.String(),
			options.CandidateUserID.String()}
		query += `a.id IS NOT NULL OR
			(s.state IN ('scheduled','canceled') AND EXISTS (SELECT 1 FROM class_members scheduled_member
				WHERE scheduled_member.class_id=s.class_id AND scheduled_member.user_id=?
				AND scheduled_member.start_at<=s.scheduled_start_at AND (scheduled_member.end_at IS NULL OR scheduled_member.end_at>s.scheduled_start_at)
				AND (scheduled_member.archived_at IS NULL OR scheduled_member.archived_at>s.scheduled_start_at))) OR
			(s.state IN ('open','paused') AND (EXISTS (SELECT 1 FROM class_members opened_member
				WHERE opened_member.class_id=s.class_id AND opened_member.user_id=? AND s.opened_at IS NOT NULL
				AND opened_member.start_at<=s.opened_at AND (opened_member.end_at IS NULL OR opened_member.end_at>s.opened_at)
				AND (opened_member.archived_at IS NULL OR opened_member.archived_at>s.opened_at)) OR
				EXISTS (SELECT 1 FROM class_members live_member WHERE live_member.class_id=s.class_id AND live_member.user_id=?
				AND live_member.start_at<=? AND (live_member.end_at IS NULL OR live_member.end_at>?)
				AND (live_member.archived_at IS NULL OR live_member.archived_at>?)))) OR
			(s.state IN ('closing','closed') AND EXISTS (SELECT 1 FROM class_members opened_member
				WHERE opened_member.class_id=s.class_id AND opened_member.user_id=? AND s.opened_at IS NOT NULL
				AND opened_member.start_at<=s.opened_at AND (opened_member.end_at IS NULL OR opened_member.end_at>s.opened_at)
				AND (opened_member.archived_at IS NULL OR opened_member.archived_at>s.opened_at)))`
		args = append(args, options.CandidateUserID.String(), options.CandidateUserID.String(), options.CandidateUserID.String(),
			serverTime, serverTime, serverTime, options.CandidateUserID.String())
		query += `)`
		if !options.BeforeScheduledStart.IsZero() {
			query += ` AND (s.scheduled_start_at,s.id)<(?,?)`
			args = append(args, model.TimeUTC(options.BeforeScheduledStart), options.BeforeSittingID.String())
		}
		query += ` ORDER BY s.scheduled_start_at DESC,s.id DESC LIMIT ?`
		args = append(args, options.Limit)
		var rows []candidateExamActivityRow
		if err := tx.Select(ctx, &rows, query, args...); err != nil {
			return nil, fmt.Errorf("list candidate Exam activity: %w", err)
		}
		page := &store.CandidateExamActivityPage{ServerTime: serverTime, Items: make([]store.CandidateExamActivityItem, 0, len(rows))}
		for _, row := range rows {
			item, err := row.item()
			if err != nil {
				return nil, err
			}
			page.Items = append(page.Items, item)
		}
		return page, nil
	})
}

func (row candidateExamActivityRow) item() (store.CandidateExamActivityItem, error) {
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "sitting_id", err)
	}
	unitID, err := model.ParseAcademicUnitID(row.AcademicUnitID)
	if err != nil {
		return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "academic_unit_id", err)
	}
	classID, err := model.ParseClassID(row.ClassID)
	state := model.ExamSittingState(row.SittingState)
	if err != nil || !state.IsValid() || row.Title == "" || row.AcademicUnitDisplayName == "" || row.ClassDisplayName == "" ||
		row.ServerTime.IsZero() || !row.ScheduledEndAt.After(row.ScheduledStartAt) {
		return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "value", errors.New("invalid activity row"))
	}
	item := store.CandidateExamActivityItem{ExamID: examID, SittingID: sittingID, Title: row.Title,
		AcademicUnitID: unitID, AcademicUnitDisplayName: row.AcademicUnitDisplayName, ClassID: classID,
		ClassDisplayName: row.ClassDisplayName, ScheduledStartAt: model.TimeUTC(row.ScheduledStartAt),
		ScheduledEndAt: model.TimeUTC(row.ScheduledEndAt), SittingState: state,
		SittingReasonCode: row.SittingReasonCode.String, AllowedActions: []store.CandidateExamAllowedAction{}}
	if row.AttemptID.Valid {
		attemptID, parseErr := model.ParseExamAttemptID(row.AttemptID.String)
		attemptState := model.ExamAttemptState(row.AttemptState.String)
		if parseErr != nil || !attemptState.IsUnresolved() && attemptState != model.ExamAttemptSubmitted {
			return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "attempt", errors.New("invalid Attempt projection"))
		}
		item.Attempt = &store.CandidateExamActivityAttempt{ID: attemptID, State: attemptState,
			SuspensionReasonCode: model.AttemptSuspensionCandidateReason(row.SuspensionReason.String)}
		if (attemptState == model.ExamAttemptSuspended) != row.SuspensionReason.Valid {
			return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "suspension", errors.New("inconsistent Suspension projection"))
		}
	} else if row.AttemptState.Valid || row.SuspensionReason.Valid {
		return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "attempt", errors.New("partial Attempt projection"))
	}
	if row.SubmissionID.Valid {
		submissionID, parseErr := model.ParseSubmissionID(row.SubmissionID.String)
		provenance := model.ExamSubmissionProvenance(row.SubmissionProvenance.String)
		if parseErr != nil || !row.SubmittedAt.Valid || !provenance.IsValid() || item.Attempt == nil || item.Attempt.State != model.ExamAttemptSubmitted {
			return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "submission", errors.New("invalid Submission projection"))
		}
		item.Submission = &store.CandidateExamActivitySubmission{ID: submissionID,
			SubmittedAt: model.TimeUTC(row.SubmittedAt.Time), Provenance: provenance}
	} else if row.SubmittedAt.Valid || row.SubmissionProvenance.Valid || item.Attempt != nil && item.Attempt.State == model.ExamAttemptSubmitted {
		return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "submission", errors.New("partial Submission projection"))
	}
	if row.ReleasedAt.Valid {
		if item.Submission == nil {
			return store.CandidateExamActivityItem{}, invalidPersistedState("candidate_exam_activity", "result", errors.New("released result has no Submission"))
		}
		item.Result = &store.CandidateExamActivityResult{ReleasedAt: model.TimeUTC(row.ReleasedAt.Time)}
	}
	switch {
	case item.Result != nil:
		item.ActivityState = store.CandidateExamActivityResultsAvailable
		item.AccessState = store.CandidateExamAccessResultAvailable
		item.AllowedActions = append(item.AllowedActions, store.CandidateExamActionViewResult)
	case item.Submission != nil:
		item.ActivityState = store.CandidateExamActivitySubmitted
		item.AccessState = store.CandidateExamAccessSubmitted
	case item.Attempt != nil && item.Attempt.State.IsUnresolved():
		item.ActivityState = store.CandidateExamActivityInProgress
		item.AccessState = candidateActivityAttemptAccess(item.Attempt.State, state, row.CurrentMembership,
			row.EstablishedAccess, row.ExamArchivedAt.Valid)
	case state == model.ExamSittingScheduled:
		item.ActivityState = store.CandidateExamActivityUpcoming
		item.AccessState = store.CandidateExamAccessNotOpen
	case !row.ExamArchivedAt.Valid && (state == model.ExamSittingOpen || state == model.ExamSittingPaused) && row.CurrentMembership:
		item.ActivityState = store.CandidateExamActivityAvailable
		if row.ManagementAuthority {
			item.AccessState = store.CandidateExamAccessNotEligible
		} else if row.OtherUnresolved {
			item.AccessState = store.CandidateExamAccessBlockedByOtherAttempt
		} else if state == model.ExamSittingPaused {
			item.AccessState = store.CandidateExamAccessSittingPaused
		} else {
			item.AccessState = store.CandidateExamAccessJoinable
		}
	default:
		item.ActivityState = store.CandidateExamActivityPast
		if state == model.ExamSittingCanceled || state == model.ExamSittingClosing || state == model.ExamSittingClosed {
			item.AccessState = store.CandidateExamAccessEnded
		} else {
			item.AccessState = store.CandidateExamAccessNotEligible
		}
	}
	if row.DesktopEligible && !row.ExamArchivedAt.Valid {
		switch item.AccessState {
		case store.CandidateExamAccessJoinable:
			item.AllowedActions = append(item.AllowedActions, store.CandidateExamActionEnter)
		case store.CandidateExamAccessResumable:
			item.AllowedActions = append(item.AllowedActions, store.CandidateExamActionResume)
		}
	}
	return item, nil
}

func candidateActivityAttemptAccess(attemptState model.ExamAttemptState, sittingState model.ExamSittingState,
	currentMembership, establishedAccess, examArchived bool,
) store.CandidateExamAccessState {
	if sittingState == model.ExamSittingCanceled || sittingState == model.ExamSittingClosing || sittingState == model.ExamSittingClosed {
		return store.CandidateExamAccessEnded
	}
	if sittingState == model.ExamSittingScheduled {
		return store.CandidateExamAccessNotOpen
	}
	if examArchived {
		return store.CandidateExamAccessNotEligible
	}
	if !currentMembership && (attemptState != model.ExamAttemptActive || !establishedAccess) {
		return store.CandidateExamAccessNotEligible
	}
	if attemptState == model.ExamAttemptSuspended {
		return store.CandidateExamAccessAwaitReallow
	}
	if sittingState == model.ExamSittingPaused {
		return store.CandidateExamAccessSittingPaused
	}
	return store.CandidateExamAccessResumable
}

type sittingCandidateStatusHeaderRow struct {
	ServerTime     time.Time    `db:"server_time"`
	ExamID         string       `db:"exam_id"`
	SittingID      string       `db:"sitting_id"`
	ClassID        string       `db:"class_id"`
	State          string       `db:"state"`
	Revision       int64        `db:"revision"`
	ScheduledStart time.Time    `db:"scheduled_start_at"`
	ScheduledEnd   time.Time    `db:"scheduled_end_at"`
	OpenedAt       sql.NullTime `db:"opened_at"`
	ExamArchivedAt sql.NullTime `db:"exam_archived_at"`
}

type sittingCandidateStatusRow struct {
	CandidateUserID          string         `db:"candidate_user_id"`
	Username                 string         `db:"username"`
	DisplayName              string         `db:"display_name"`
	CurrentMembership        bool           `db:"current_membership"`
	AttemptID                sql.NullString `db:"attempt_id"`
	AttemptState             sql.NullString `db:"attempt_state"`
	AttemptRevision          sql.NullInt64  `db:"attempt_revision"`
	AttemptCreatedAt         sql.NullTime   `db:"attempt_created_at"`
	AttemptUpdatedAt         sql.NullTime   `db:"attempt_updated_at"`
	SubmissionID             sql.NullString `db:"submission_id"`
	SubmittedAt              sql.NullTime   `db:"submitted_at"`
	SubmissionProvenance     sql.NullString `db:"submission_provenance"`
	ParticipationID          sql.NullString `db:"participation_id"`
	ParticipationState       sql.NullString `db:"participation_state"`
	ParticipationUpdated     sql.NullTime   `db:"participation_updated_at"`
	LeaseExpiresAt           sql.NullTime   `db:"lease_expires_at"`
	ActiveParticipationCount int64          `db:"active_participation_count"`
	OpenConnectionCount      int64          `db:"open_connection_count"`
	SuspensionID             sql.NullString `db:"suspension_id"`
	SuspensionReason         sql.NullString `db:"suspension_reason"`
	ActiveSuspensionCount    int64          `db:"active_suspension_count"`
	IntegrityAttention       int64          `db:"integrity_attention_count"`
}

func (s *sqlExamAttemptStore) ListSittingCandidateStatuses(ctx context.Context,
	options store.SittingCandidateStatusListOptions,
) (*store.SittingCandidateStatusPage, error) {
	if !options.ExamID.IsValid() || !options.SittingID.IsValid() || !options.ExcludeCandidateUserID.IsValid() ||
		!options.AfterCandidateUserID.IsZero() && !options.AfterCandidateUserID.IsValid() || options.Limit < 1 || options.Limit > 201 {
		return nil, store.NewErrInvalidInput("sitting_candidate_status", "list_options", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "list Sitting candidate statuses", func(ctx context.Context,
		tx *sqlxTxWrapper,
	) (*store.SittingCandidateStatusPage, error) {
		var header sittingCandidateStatusHeaderRow
		if err := tx.Get(ctx, &header, `SELECT statement_timestamp() AS server_time,s.exam_id,s.id AS sitting_id,s.class_id,
			s.state,s.revision,s.scheduled_start_at,s.scheduled_end_at,s.opened_at,e.archived_at AS exam_archived_at
			FROM exam_sittings s JOIN exams e ON e.id=s.exam_id WHERE s.id=? AND s.exam_id=?`,
			options.SittingID.String(), options.ExamID.String()); err != nil {
			return nil, translateError("exam_sitting", options.SittingID.String(), err)
		}
		serverTime := model.TimeUTC(header.ServerTime)
		query := `WITH population AS (
			SELECT a.candidate_user_id FROM exam_attempts a WHERE a.exam_sitting_id=?
			UNION
			SELECT member.user_id FROM class_members member WHERE member.class_id=? AND (
				(? IN ('scheduled','canceled') AND member.start_at<=? AND (member.end_at IS NULL OR member.end_at>?)
					AND (member.archived_at IS NULL OR member.archived_at>?)) OR
				(? NOT IN ('scheduled','canceled') AND ?::timestamptz IS NOT NULL AND member.start_at<=?
					AND (member.end_at IS NULL OR member.end_at>?) AND (member.archived_at IS NULL OR member.archived_at>?)) OR
				(? IN ('open','paused') AND member.start_at<=? AND (member.end_at IS NULL OR member.end_at>?)
					AND (member.archived_at IS NULL OR member.archived_at>?))
			)
		), finalized_review AS (
			SELECT review.id,review.exam_attempt_id FROM submission_reviews review WHERE review.state='finalized'
		)
		SELECT candidate.id AS candidate_user_id,candidate.username,candidate.display_name,
			EXISTS (SELECT 1 FROM class_members current_member WHERE current_member.class_id=? AND current_member.user_id=candidate.id
				AND current_member.start_at<=? AND (current_member.end_at IS NULL OR current_member.end_at>?)
				AND (current_member.archived_at IS NULL OR current_member.archived_at>?)) AS current_membership,
			a.id AS attempt_id,a.state AS attempt_state,a.revision AS attempt_revision,
			a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,
			submission.id AS submission_id,submission.submitted_at,submission.provenance AS submission_provenance,
			participation.id AS participation_id,participation.state AS participation_state,
			participation.updated_at AS participation_updated_at,participation.lease_expires_at,
			(SELECT COUNT(*) FROM exam_attempt_participations active_participation WHERE active_participation.exam_attempt_id=a.id
				AND active_participation.state='active') AS active_participation_count,
			(SELECT COUNT(*) FROM exam_attempt_connections open_connection WHERE open_connection.exam_attempt_id=a.id
				AND open_connection.state='open') AS open_connection_count,
			suspension.id AS suspension_id,suspension.candidate_reason AS suspension_reason,
			(SELECT COUNT(*) FROM exam_attempt_suspensions active_suspension WHERE active_suspension.exam_attempt_id=a.id
				AND active_suspension.state='active') AS active_suspension_count,
			COALESCE((SELECT COUNT(*) FROM integrity_flags flag WHERE flag.exam_attempt_id=a.id AND NOT EXISTS (
				SELECT 1 FROM integrity_review_decisions decision JOIN submission_reviews decision_review
					ON decision_review.id=decision.submission_review_id
				WHERE decision.integrity_flag_id=flag.id AND decision_review.exam_attempt_id=a.id)),0)
			+ CASE WHEN (a.state<>'submitted' AND (EXISTS (SELECT 1 FROM exam_attempt_focus_loss_evaluations evaluation
				WHERE evaluation.exam_attempt_id=a.id AND evaluation.unresolved_missing_count>0)
				OR EXISTS (SELECT 1 FROM browser_activity_sources source WHERE source.exam_attempt_id=a.id
					AND (source.state='gapped' OR (source.state='current' AND source.highest_contiguous<source.highest_seen)))))
				OR (a.state='submitted' AND submission.integrity_state='gapped' AND NOT EXISTS (
					SELECT 1 FROM finalized_review review WHERE review.exam_attempt_id=a.id)) THEN 1 ELSE 0 END
			+ COALESCE((SELECT COUNT(*) FROM integrity_discrepancies discrepancy WHERE discrepancy.exam_attempt_id=a.id
				AND NOT EXISTS (SELECT 1 FROM submission_review_inventory_discrepancies inventory
					JOIN finalized_review review ON review.id=inventory.submission_review_id
					WHERE inventory.integrity_discrepancy_id=discrepancy.id)),0) AS integrity_attention_count
		FROM population JOIN users candidate ON candidate.id=population.candidate_user_id
		LEFT JOIN exam_attempts a ON a.exam_sitting_id=? AND a.candidate_user_id=candidate.id
		LEFT JOIN exam_submissions submission ON submission.exam_attempt_id=a.id AND submission.sealed=true
		LEFT JOIN LATERAL (SELECT p.id,p.state,p.updated_at,p.lease_expires_at FROM exam_attempt_participations p
			WHERE p.exam_attempt_id=a.id ORDER BY p.generation DESC LIMIT 1) participation ON TRUE
		LEFT JOIN exam_attempt_suspensions suspension ON suspension.exam_attempt_id=a.id AND suspension.state='active'
		WHERE candidate.id>? AND candidate.id<>? ORDER BY candidate.id LIMIT ?`
		openedAt := any(nil)
		if header.OpenedAt.Valid {
			openedAt = model.TimeUTC(header.OpenedAt.Time)
		}
		args := []any{options.SittingID.String(), header.ClassID, header.State, header.ScheduledStart, header.ScheduledStart,
			header.ScheduledStart, header.State, openedAt, openedAt, openedAt, openedAt, header.State, serverTime, serverTime,
			serverTime, header.ClassID, serverTime, serverTime, serverTime, options.SittingID.String(),
			options.AfterCandidateUserID.String(), options.ExcludeCandidateUserID.String(), options.Limit}
		var rows []sittingCandidateStatusRow
		if err := tx.Select(ctx, &rows, query, args...); err != nil {
			return nil, fmt.Errorf("list Sitting candidate statuses: %w", err)
		}
		page, err := candidateStatusPageHeader(header)
		if err != nil {
			return nil, err
		}
		page.Items = make([]store.SittingCandidateStatusItem, 0, len(rows))
		for _, row := range rows {
			item, itemErr := row.item(serverTime, page.SittingState, model.TimeUTC(header.ScheduledEnd),
				header.ExamArchivedAt.Valid, options.ReallowAuthorized)
			if itemErr != nil {
				return nil, itemErr
			}
			page.Items = append(page.Items, item)
		}
		return page, nil
	})
}

func candidateStatusPageHeader(row sittingCandidateStatusHeaderRow) (*store.SittingCandidateStatusPage, error) {
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("sitting_candidate_status", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	state := model.ExamSittingState(row.State)
	if err != nil || !state.IsValid() || row.ServerTime.IsZero() || row.Revision < 1 || !row.ScheduledEnd.After(row.ScheduledStart) {
		return nil, invalidPersistedState("sitting_candidate_status", "header", errors.New("invalid Sitting status header"))
	}
	return &store.SittingCandidateStatusPage{ServerTime: model.TimeUTC(row.ServerTime), ExamID: examID, SittingID: sittingID,
		SittingState: state, SittingRevision: row.Revision, Items: []store.SittingCandidateStatusItem{}}, nil
}

func (row sittingCandidateStatusRow) item(serverTime time.Time, sittingState model.ExamSittingState, scheduledEnd time.Time,
	examArchived, reallowAuthorized bool,
) (store.SittingCandidateStatusItem, error) {
	candidateID, err := model.ParseUserID(row.CandidateUserID)
	if err != nil || row.Username == "" || row.DisplayName == "" || row.IntegrityAttention < 0 {
		return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "candidate", errors.New("invalid candidate status row"))
	}
	item := store.SittingCandidateStatusItem{Candidate: store.SittingCandidateIdentity{UserID: candidateID,
		Username: row.Username, DisplayName: row.DisplayName}, CurrentClassMembership: row.CurrentMembership,
		Presence:                store.SittingCandidatePresence{State: store.SittingCandidateNotStarted},
		IntegrityAttentionCount: row.IntegrityAttention}
	if !row.AttemptID.Valid {
		if row.AttemptState.Valid || row.AttemptRevision.Valid || row.AttemptCreatedAt.Valid || row.AttemptUpdatedAt.Valid ||
			row.SubmissionID.Valid || row.ParticipationID.Valid || row.SuspensionID.Valid || row.ActiveParticipationCount != 0 ||
			row.OpenConnectionCount != 0 || row.ActiveSuspensionCount != 0 || row.IntegrityAttention != 0 {
			return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "attempt", errors.New("orphan Attempt fields"))
		}
		return item, nil
	}
	attemptID, err := model.ParseExamAttemptID(row.AttemptID.String)
	attemptState := model.ExamAttemptState(row.AttemptState.String)
	if err != nil || !attemptState.IsUnresolved() && attemptState != model.ExamAttemptSubmitted || !row.AttemptRevision.Valid ||
		row.AttemptRevision.Int64 < 1 || !row.AttemptCreatedAt.Valid || !row.AttemptUpdatedAt.Valid {
		return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "attempt", errors.New("invalid Attempt fields"))
	}
	item.Attempt = &store.SittingCandidateStatusAttempt{ID: attemptID, State: attemptState, Revision: row.AttemptRevision.Int64,
		CreatedAt: model.TimeUTC(row.AttemptCreatedAt.Time), UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt.Time)}
	if row.SubmissionID.Valid {
		submissionID, parseErr := model.ParseSubmissionID(row.SubmissionID.String)
		provenance := model.ExamSubmissionProvenance(row.SubmissionProvenance.String)
		if parseErr != nil || !row.SubmittedAt.Valid || !provenance.IsValid() || attemptState != model.ExamAttemptSubmitted {
			return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "submission", errors.New("invalid Submission fields"))
		}
		item.Attempt.Submission = &store.SittingCandidateStatusSubmission{ID: submissionID,
			SubmittedAt: model.TimeUTC(row.SubmittedAt.Time), Provenance: provenance}
	} else if row.SubmittedAt.Valid || row.SubmissionProvenance.Valid || attemptState == model.ExamAttemptSubmitted {
		return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "submission", errors.New("partial Submission fields"))
	}
	if row.ParticipationID.Valid {
		if _, parseErr := model.ParseAttemptParticipationID(row.ParticipationID.String); parseErr != nil || !row.ParticipationUpdated.Valid || !row.LeaseExpiresAt.Valid {
			return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "participation", errors.New("invalid Participation fields"))
		}
		item.Presence.LastLeaseRenewedAt = model.OptionalTimeFrom(row.ParticipationUpdated.Time)
		item.Presence.LeaseExpiresAt = model.OptionalTimeFrom(row.LeaseExpiresAt.Time)
	} else if row.ParticipationState.Valid || row.ParticipationUpdated.Valid || row.LeaseExpiresAt.Valid {
		return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "participation", errors.New("partial Participation fields"))
	}
	if row.SuspensionID.Valid {
		suspensionID, parseErr := model.ParseAttemptSuspensionID(row.SuspensionID.String)
		reason := model.AttemptSuspensionCandidateReason(row.SuspensionReason.String)
		if parseErr != nil || !reason.IsValid() || attemptState != model.ExamAttemptSuspended || row.ActiveSuspensionCount != 1 {
			return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "suspension", errors.New("invalid Suspension fields"))
		}
		item.Suspension = &store.SittingCandidateSuspension{ID: suspensionID, CandidateReason: reason,
			ReallowAvailable: reallowAuthorized && !examArchived && serverTime.Before(scheduledEnd) &&
				(sittingState == model.ExamSittingOpen || sittingState == model.ExamSittingPaused)}
	} else if row.SuspensionReason.Valid || row.ActiveSuspensionCount != 0 || attemptState == model.ExamAttemptSuspended {
		return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "suspension", errors.New("partial Suspension fields"))
	}
	switch attemptState {
	case model.ExamAttemptSubmitted:
		item.Presence.State = store.SittingCandidateSubmitted
	case model.ExamAttemptSuspended:
		item.Presence.State = store.SittingCandidateSuspended
	case model.ExamAttemptReady:
		item.Presence.State = store.SittingCandidateReady
	case model.ExamAttemptActive:
		if row.ActiveParticipationCount != 1 || !row.ParticipationID.Valid || row.ParticipationState.String != string(model.AttemptParticipationActive) {
			return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "active_participation", errors.New("active Attempt lacks one current Participation"))
		}
		if !row.LeaseExpiresAt.Time.After(serverTime) {
			item.Presence.State = store.SittingCandidateLeaseExpired
		} else if row.OpenConnectionCount == 1 {
			item.Presence.State = store.SittingCandidateConnected
		} else if row.OpenConnectionCount == 0 {
			item.Presence.State = store.SittingCandidateReconnecting
		} else {
			return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "open_connections", errors.New("multiple open Connections"))
		}
	}
	if attemptState != model.ExamAttemptActive && (row.ActiveParticipationCount != 0 || row.OpenConnectionCount != 0) {
		return store.SittingCandidateStatusItem{}, invalidPersistedState("sitting_candidate_status", "terminal_fence", errors.New("non-active Attempt retains active transport"))
	}
	return item, nil
}
