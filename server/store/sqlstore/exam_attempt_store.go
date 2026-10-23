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

	"github.com/lib/pq"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// SQLExamAttemptStore is the PostgreSQL store.ExamAttemptStore, which owns
// Attempt admission, Participation leases, delivery, Focus Loss, and suspension
// transitions.
type SQLExamAttemptStore struct{ *SQLStore }

func newSQLExamAttemptStore(sqlStore *SQLStore) store.ExamAttemptStore {
	return &SQLExamAttemptStore{SQLStore: sqlStore}
}

type examAttemptManagerRow struct {
	AttemptID                 string         `db:"attempt_id"`
	ExamID                    string         `db:"exam_id"`
	SittingID                 string         `db:"exam_sitting_id"`
	CandidateID               string         `db:"candidate_user_id"`
	AdmissionRevisionID       string         `db:"admission_revision_id"`
	AttemptState              string         `db:"attempt_state"`
	AttemptCreatedAt          time.Time      `db:"attempt_created_at"`
	AttemptUpdatedAt          time.Time      `db:"attempt_updated_at"`
	SubmittedAt               sql.NullTime   `db:"submitted_at"`
	AttemptRevision           int64          `db:"attempt_revision"`
	WorkspaceID               string         `db:"workspace_id"`
	WorkspaceCursor           int64          `db:"workspace_cursor"`
	WorkspaceCreatedAt        time.Time      `db:"workspace_created_at"`
	WorkspaceUpdatedAt        time.Time      `db:"workspace_updated_at"`
	ParticipationID           sql.NullString `db:"participation_id"`
	ParticipationState        sql.NullString `db:"participation_state"`
	Generation                sql.NullInt64  `db:"generation"`
	RenewalSequence           sql.NullInt64  `db:"renewal_sequence"`
	StartedAt                 sql.NullTime   `db:"started_at"`
	ParticipationUpdatedAt    sql.NullTime   `db:"participation_updated_at"`
	LeaseExpiresAt            sql.NullTime   `db:"lease_expires_at"`
	EndedAt                   sql.NullTime   `db:"ended_at"`
	EndReason                 sql.NullString `db:"end_reason"`
	ConnectionID              sql.NullString `db:"connection_id"`
	ConnectionState           sql.NullString `db:"connection_state"`
	OpenedAt                  sql.NullTime   `db:"opened_at"`
	ClosedAt                  sql.NullTime   `db:"closed_at"`
	CloseReason               sql.NullString `db:"close_reason"`
	SuspensionID              sql.NullString `db:"suspension_id"`
	SuspensionParticipationID sql.NullString `db:"suspension_participation_id"`
	SuspensionFlagID          sql.NullString `db:"suspension_flag_id"`
	SuspensionGeneration      sql.NullInt64  `db:"suspension_generation"`
	SuspensionState           sql.NullString `db:"suspension_state"`
	SuspensionSource          sql.NullString `db:"suspension_source"`
	SuspensionReason          sql.NullString `db:"suspension_reason"`
	SuspensionStartedAt       sql.NullTime   `db:"suspension_started_at"`
	SuspensionEndedAt         sql.NullTime   `db:"suspension_ended_at"`
	SuspensionReallowedBy     sql.NullString `db:"suspension_reallowed_by"`
	SuspensionSuspendedBy     sql.NullString `db:"suspension_suspended_by"`
}

const examAttemptManagerSelect = `SELECT a.id AS attempt_id,a.exam_id,a.exam_sitting_id,a.candidate_user_id,a.admission_revision_id,
	a.state AS attempt_state,a.created_at AS attempt_created_at,a.updated_at AS attempt_updated_at,a.submitted_at,a.revision AS attempt_revision,
	w.id AS workspace_id,w.cursor AS workspace_cursor,w.created_at AS workspace_created_at,w.updated_at AS workspace_updated_at,
	p.id AS participation_id,p.state AS participation_state,p.generation,p.renewal_sequence,p.started_at,p.updated_at AS participation_updated_at,p.lease_expires_at,p.ended_at,p.end_reason,
	c.id AS connection_id,c.state AS connection_state,c.opened_at,c.closed_at,c.close_reason,
	su.id AS suspension_id,su.participation_id AS suspension_participation_id,su.integrity_flag_id AS suspension_flag_id,
	su.generation AS suspension_generation,su.state AS suspension_state,su.source AS suspension_source,
	su.candidate_reason AS suspension_reason,su.started_at AS suspension_started_at,su.ended_at AS suspension_ended_at,
	su.reallowed_by_user_id AS suspension_reallowed_by,su.suspended_by_user_id AS suspension_suspended_by
	FROM exam_attempts a JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
	LEFT JOIN LATERAL (SELECT id,state,generation,renewal_sequence,started_at,updated_at,lease_expires_at,ended_at,end_reason
		FROM exam_attempt_participations WHERE exam_attempt_id=a.id ORDER BY generation DESC LIMIT 1) p ON true
	LEFT JOIN LATERAL (SELECT id,state,opened_at,closed_at,close_reason FROM exam_attempt_connections
		WHERE exam_attempt_id=a.id AND state='open' ORDER BY opened_at DESC,id DESC LIMIT 1) c ON true
	LEFT JOIN LATERAL (SELECT id,participation_id,integrity_flag_id,generation,state,source,candidate_reason,started_at,ended_at,reallowed_by_user_id,
		suspended_by_user_id FROM exam_attempt_suspensions WHERE exam_attempt_id=a.id AND state='active' LIMIT 1) su ON true`

// Get returns the Exam Manager's snapshot of the Attempt within examID, or a
// not-found error.
func (s *SQLExamAttemptStore) Get(ctx context.Context, examID model.ExamID, attemptID model.ExamAttemptID) (*store.ExamAttemptManagerSnapshot, error) {
	if !examID.IsValid() || !attemptID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_attempt", "identity", nil)
	}
	var row examAttemptManagerRow
	if err := s.GetMaster().Get(ctx, &row, examAttemptManagerSelect+` WHERE a.exam_id=? AND a.id=?`, examID.String(), attemptID.String()); err != nil {
		return nil, translateError("exam_attempt", attemptID.String(), err)
	}
	return row.snapshot()
}

// List returns one page of the Sitting's Attempts for an Exam Manager, newest
// first, excluding ExcludeCandidateUserID and optionally filtered by state.
// Limit is 1..201 and the Before cursor fields must be set together.
func (s *SQLExamAttemptStore) List(ctx context.Context, options store.ExamAttemptManagerListOptions) ([]store.ExamAttemptManagerSnapshot, error) {
	if !options.ExamID.IsValid() || !options.SittingID.IsValid() || options.Limit < 1 || options.Limit > 201 ||
		!options.ExcludeCandidateUserID.IsValid() || (options.BeforeCreatedAt.IsZero() != options.BeforeAttemptID.IsZero()) {
		return nil, store.NewErrInvalidInput("exam_attempt", "manager_list", nil)
	}
	query := examAttemptManagerSelect + ` WHERE a.exam_id=? AND a.exam_sitting_id=?`
	args := []any{options.ExamID.String(), options.SittingID.String()}
	query += ` AND a.candidate_user_id<>?`
	args = append(args, options.ExcludeCandidateUserID.String())
	if len(options.States) != 0 {
		states := make([]string, len(options.States))
		for i, state := range options.States {
			if state != model.ExamAttemptReady && state != model.ExamAttemptActive && state != model.ExamAttemptSuspended && state != model.ExamAttemptSubmitted {
				return nil, store.NewErrInvalidInput("exam_attempt", "states", nil)
			}
			states[i] = string(state)
		}
		query += ` AND a.state=ANY(?)`
		args = append(args, pq.Array(states))
	}
	if !options.BeforeCreatedAt.IsZero() {
		query += ` AND (a.created_at,a.id)<(?,?)`
		args = append(args, model.TimeUTC(options.BeforeCreatedAt), options.BeforeAttemptID.String())
	}
	query += ` ORDER BY a.created_at DESC,a.id DESC LIMIT ?`
	args = append(args, options.Limit)
	var rows []examAttemptManagerRow
	if err := s.GetMaster().Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list Exam Attempts: %w", err)
	}
	result := make([]store.ExamAttemptManagerSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshot, err := row.snapshot()
		if err != nil {
			return nil, err
		}
		result = append(result, *snapshot)
	}
	return result, nil
}

func (row examAttemptManagerRow) snapshot() (*store.ExamAttemptManagerSnapshot, error) {
	attemptID, err := model.ParseExamAttemptID(row.AttemptID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "id", err)
	}
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", err)
	}
	candidateID, err := model.ParseUserID(row.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	revisionID, err := model.ParseExamRevisionID(row.AdmissionRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "admission_revision_id", err)
	}
	attempt := &model.ExamAttempt{ID: attemptID, ExamID: examID, SittingID: sittingID, CandidateUserID: candidateID, AdmissionRevisionID: revisionID, State: model.ExamAttemptState(row.AttemptState), CreatedAt: model.TimeUTC(row.AttemptCreatedAt), UpdatedAt: model.TimeUTC(row.AttemptUpdatedAt), SubmittedAt: OptionalTimeFromNullTime(row.SubmittedAt), Revision: row.AttemptRevision}
	if err = attempt.Validate(); err != nil {
		return nil, invalidPersistedState("exam_attempt", "value", err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(row.WorkspaceID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt_workspace", "id", err)
	}
	workspace := &model.ExamAttemptWorkspace{ID: workspaceID, AttemptID: attemptID, Cursor: row.WorkspaceCursor, CreatedAt: model.TimeUTC(row.WorkspaceCreatedAt), UpdatedAt: model.TimeUTC(row.WorkspaceUpdatedAt)}
	if err = workspace.Validate(); err != nil {
		return nil, invalidPersistedState("exam_attempt_workspace", "value", err)
	}
	result := &store.ExamAttemptManagerSnapshot{Attempt: attempt, Workspace: workspace}
	if row.ParticipationID.Valid {
		id, e := model.ParseAttemptParticipationID(row.ParticipationID.String)
		if e != nil {
			return nil, invalidPersistedState("attempt_participation", "id", e)
		}
		result.LatestParticipation = &store.ExamAttemptParticipationView{ID: id, AttemptID: attemptID, State: model.AttemptParticipationState(row.ParticipationState.String), Generation: row.Generation.Int64, RenewalSequence: row.RenewalSequence.Int64, StartedAt: model.TimeUTC(row.StartedAt.Time), UpdatedAt: model.TimeUTC(row.ParticipationUpdatedAt.Time), LeaseExpiresAt: model.TimeUTC(row.LeaseExpiresAt.Time), EndedAt: OptionalTimeFromNullTime(row.EndedAt), EndReason: model.AttemptParticipationEndReason(row.EndReason.String)}
	}
	if row.ConnectionID.Valid {
		id, e := model.ParseAttemptConnectionID(row.ConnectionID.String)
		if e != nil {
			return nil, invalidPersistedState("attempt_connection", "id", e)
		}
		result.CurrentConnection = &store.ExamAttemptManagerConnection{ID: id, State: model.AttemptConnectionState(row.ConnectionState.String), OpenedAt: model.TimeUTC(row.OpenedAt.Time), ClosedAt: OptionalTimeFromNullTime(row.ClosedAt), CloseReason: model.AttemptConnectionCloseReason(row.CloseReason.String)}
	}
	if row.SuspensionID.Valid {
		id, e := model.ParseAttemptSuspensionID(row.SuspensionID.String)
		if e != nil {
			return nil, invalidPersistedState("attempt_suspension", "id", e)
		}
		participationID, e := model.ParseAttemptParticipationID(row.SuspensionParticipationID.String)
		if e != nil {
			return nil, invalidPersistedState("attempt_suspension", "participation_id", e)
		}
		source := model.AttemptSuspensionSource(row.SuspensionSource.String)
		reason := model.AttemptSuspensionCandidateReason(row.SuspensionReason.String)
		flagID, suspendedBy, e := suspensionProvenance(source, reason, row.SuspensionFlagID.String, row.SuspensionSuspendedBy.String)
		if e != nil {
			return nil, e
		}
		var actorID model.UserID
		if row.SuspensionReallowedBy.Valid {
			actorID, e = model.ParseUserID(row.SuspensionReallowedBy.String)
			if e != nil {
				return nil, invalidPersistedState("attempt_suspension", "reallowed_by_user_id", e)
			}
		}
		if !participationID.IsValid() || row.SuspensionGeneration.Int64 < 1 || row.SuspensionStartedAt.Time.IsZero() ||
			model.AttemptSuspensionState(row.SuspensionState.String) != model.AttemptSuspensionActive ||
			row.SuspensionEndedAt.Valid || !actorID.IsZero() {
			return nil, invalidPersistedState("attempt_suspension", "value", errors.New("invalid active Attempt Suspension projection"))
		}
		result.ActiveSuspension = &store.ExamAttemptSuspensionView{ID: id, AttemptID: attemptID,
			ParticipationID: participationID, FlagID: flagID, Generation: row.SuspensionGeneration.Int64,
			State: model.AttemptSuspensionActive, Source: source, CandidateReason: reason,
			StartedAt: model.TimeUTC(row.SuspensionStartedAt.Time), SuspendedByUserID: suspendedBy}
	}
	return result, nil
}

type candidateAttemptGuard struct {
	AttemptID           string `db:"attempt_id"`
	ExamID              string `db:"exam_id"`
	SittingID           string `db:"sitting_id"`
	ClassID             string `db:"class_id"`
	SittingState        string `db:"sitting_state"`
	AdmissionRevisionID string `db:"admission_revision_id"`
	RevisionID          string `db:"revision_id"`
}

func (s *SQLExamAttemptStore) candidateGuard(ctx context.Context, access store.CandidateAttemptAccess) (candidateAttemptGuard, error) {
	if !access.AttemptID.IsValid() || !access.CandidateUserID.IsValid() || !access.SessionID.IsValid() ||
		!access.DesktopRegistrationID.IsValid() || !model.IsValidDPoPKeyThumbprint(access.DPoPKeyThumbprint) ||
		!access.ConnectionID.IsValid() || !model.IsValidTokenHash(access.ContinuityCredentialHash) {
		return candidateAttemptGuard{}, store.NewErrInvalidInput("exam_attempt", "candidate_access", nil)
	}
	var guard candidateAttemptGuard
	err := s.GetMaster().Get(ctx, &guard, `SELECT a.id AS attempt_id,a.exam_id,a.exam_sitting_id AS sitting_id,s.class_id,s.state AS sitting_state,a.admission_revision_id,s.exam_revision_id AS revision_id
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exam_attempt_participations p ON p.exam_attempt_id=a.id AND p.state='active'
		JOIN exam_attempt_connections c ON c.participation_id=p.id AND c.exam_attempt_id=a.id AND c.state='open'
		JOIN sessions se ON se.id=p.session_id AND se.id=c.session_id AND se.user_id=a.candidate_user_id
		JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=se.user_id
		WHERE a.id=? AND a.candidate_user_id=? AND a.state='active' AND c.id=? AND c.session_id=?
		AND p.session_id=? AND se.desktop_registration_id=? AND se.dpop_key_thumbprint=?
		AND dr.revoked_at IS NULL AND dr.key_thumbprint=? AND se.revoked_at IS NULL AND se.archived_at IS NULL
		AND p.continuity_credential_hash=? AND p.lease_expires_at>statement_timestamp()
		AND s.state IN ('open','paused') AND s.scheduled_end_at>statement_timestamp()`, access.AttemptID.String(), access.CandidateUserID.String(),
		access.ConnectionID.String(), access.SessionID.String(), access.SessionID.String(), access.DesktopRegistrationID.String(),
		access.DPoPKeyThumbprint, access.DPoPKeyThumbprint, access.ContinuityCredentialHash)
	if errors.Is(err, sql.ErrNoRows) {
		return candidateAttemptGuard{}, store.NewErrNotFound("exam_attempt_access", access.AttemptID.String())
	}
	if err != nil {
		return candidateAttemptGuard{}, fmt.Errorf("authorize candidate Exam Attempt access: %w", err)
	}
	return guard, nil
}

func (s *SQLExamAttemptStore) lockCandidateGuard(ctx context.Context, tx *sqlxTxWrapper, access store.CandidateAttemptAccess) (candidateAttemptGuard, error) {
	if !access.AttemptID.IsValid() || !access.CandidateUserID.IsValid() || !access.SessionID.IsValid() ||
		!access.DesktopRegistrationID.IsValid() || !model.IsValidDPoPKeyThumbprint(access.DPoPKeyThumbprint) ||
		!access.ConnectionID.IsValid() || !model.IsValidTokenHash(access.ContinuityCredentialHash) {
		return candidateAttemptGuard{}, store.NewErrInvalidInput("exam_attempt", "candidate_access", nil)
	}
	var row struct {
		candidateAttemptGuard
		AttemptState       string       `db:"attempt_state"`
		ScheduledEndAt     time.Time    `db:"scheduled_end_at"`
		ParticipationState string       `db:"participation_state"`
		CredentialHash     string       `db:"continuity_credential_hash"`
		LeaseExpiresAt     time.Time    `db:"lease_expires_at"`
		ConnectionState    string       `db:"connection_state"`
		SessionArchivedAt  sql.NullTime `db:"session_archived_at"`
		SessionRevokedAt   sql.NullTime `db:"session_revoked_at"`
		SessionIdleExpiry  time.Time    `db:"session_idle_expires_at"`
		SessionExpiry      time.Time    `db:"session_expires_at"`
		UserArchivedAt     sql.NullTime `db:"user_archived_at"`
		UserDisabledAt     sql.NullTime `db:"user_disabled_at"`
	}
	err := tx.Get(ctx, &row, `SELECT a.id AS attempt_id,a.exam_id,a.exam_sitting_id AS sitting_id,s.class_id,s.state AS sitting_state,
		a.admission_revision_id,s.exam_revision_id AS revision_id,a.state AS attempt_state,
		s.state AS sitting_state,s.scheduled_end_at,p.state AS participation_state,
		p.continuity_credential_hash,p.lease_expires_at,c.state AS connection_state,
		se.archived_at AS session_archived_at,se.revoked_at AS session_revoked_at,
		se.idle_expires_at AS session_idle_expires_at,se.expires_at AS session_expires_at,
		u.archived_at AS user_archived_at,u.disabled_at AS user_disabled_at
		FROM exam_attempts a JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exam_attempt_participations p ON p.exam_attempt_id=a.id
		JOIN exam_attempt_connections c ON c.participation_id=p.id AND c.exam_attempt_id=a.id
		JOIN sessions se ON se.id=p.session_id AND se.id=c.session_id JOIN users u ON u.id=se.user_id
		JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=se.user_id
		WHERE a.id=? AND a.candidate_user_id=? AND c.id=? AND c.session_id=?
		AND p.session_id=? AND se.desktop_registration_id=? AND se.dpop_key_thumbprint=?
		AND dr.revoked_at IS NULL AND dr.key_thumbprint=?
		FOR SHARE OF a,s,p,c,se,u`, access.AttemptID.String(), access.CandidateUserID.String(),
		access.ConnectionID.String(), access.SessionID.String(), access.SessionID.String(), access.DesktopRegistrationID.String(),
		access.DPoPKeyThumbprint, access.DPoPKeyThumbprint)
	if errors.Is(err, sql.ErrNoRows) {
		return candidateAttemptGuard{}, store.NewErrNotFound("exam_attempt_access", access.AttemptID.String())
	}
	if err != nil {
		return candidateAttemptGuard{}, fmt.Errorf("lock candidate Exam Attempt access: %w", err)
	}
	var databaseNow time.Time
	if err = tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
		return candidateAttemptGuard{}, fmt.Errorf("read candidate Exam Attempt access time: %w", err)
	}
	databaseNow = model.TimeUTC(databaseNow)
	if row.AttemptState != string(model.ExamAttemptActive) || row.ParticipationState != string(model.AttemptParticipationActive) ||
		row.ConnectionState != string(model.AttemptConnectionOpen) ||
		subtle.ConstantTimeCompare([]byte(row.CredentialHash), []byte(access.ContinuityCredentialHash)) != 1 ||
		!databaseNow.Before(row.LeaseExpiresAt) ||
		(row.SittingState != string(model.ExamSittingOpen) && row.SittingState != string(model.ExamSittingPaused)) ||
		!databaseNow.Before(row.ScheduledEndAt) || row.SessionArchivedAt.Valid || row.SessionRevokedAt.Valid ||
		!databaseNow.Before(row.SessionIdleExpiry) || !databaseNow.Before(row.SessionExpiry) ||
		row.UserArchivedAt.Valid || row.UserDisabledAt.Valid {
		return candidateAttemptGuard{}, store.NewErrNotFound("exam_attempt_access", access.AttemptID.String())
	}
	return row.candidateAttemptGuard, nil
}

// GetCandidatePresentation returns the sealed Exam content, policies, capacity
// limits, and frozen Attempt Configuration the Candidate sees for the live
// Attempt named by access.
func (s *SQLExamAttemptStore) GetCandidatePresentation(ctx context.Context, access store.CandidateAttemptAccess) (*store.CandidateExamPresentation, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "get candidate Exam presentation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.CandidateExamPresentation, error) {
		guard, err := s.lockCandidateGuard(ctx, tx, access)
		if err != nil {
			return nil, err
		}
		var header struct {
			examCapacityPolicyRow
			Title                string    `db:"title"`
			Instructions         string    `db:"instructions_markdown"`
			Policy               []byte    `db:"policy_canonical"`
			BrowserPolicy        []byte    `db:"browser_policy_canonical"`
			BrowserPolicyDigest  string    `db:"browser_policy_digest"`
			PolicyRevisionNumber int64     `db:"policy_revision_number"`
			BrowserUnavailable   bool      `db:"browser_unavailable"`
			Configuration        []byte    `db:"attempt_configuration_canonical"`
			ConfigurationDigest  string    `db:"attempt_configuration_digest"`
			SittingState         string    `db:"sitting_state"`
			DatabaseNow          time.Time `db:"database_now"`
		}
		if err = tx.Get(ctx, &header, `SELECT r.title,r.instructions_markdown,r.policy_canonical,
			r.browser_policy_canonical,r.browser_policy_digest,r.number AS policy_revision_number,
			admitted.exam_resource_max_count,admitted.exam_resource_max_bytes,
			admitted.exam_workspace_max_entries,admitted.exam_workspace_max_file_bytes,admitted.exam_workspace_max_total_bytes,
 EXISTS(SELECT 1 FROM exam_attempt_security_owners bo JOIN exam_attempt_participations bp ON bp.id=bo.participation_id WHERE bo.exam_attempt_id=a.id AND bp.state='active' AND bo.browser_source_unavailable) AS browser_unavailable,
			a.attempt_configuration_canonical,a.attempt_configuration_digest,s.state AS sitting_state,
			statement_timestamp() AS database_now
			FROM exam_revisions r JOIN exam_sittings s ON s.exam_revision_id=r.id
			JOIN exam_attempts a ON a.id=? AND a.exam_sitting_id=s.id
			JOIN exam_revisions admitted ON admitted.id=a.admission_revision_id AND admitted.sealed=true
			WHERE r.id=? AND r.sealed=true FOR SHARE OF r,s,a`, guard.AttemptID, guard.RevisionID); err != nil {
			return nil, translateError("exam_revision", guard.RevisionID, err)
		}
		capacity, err := header.examCapacityPolicyRow.policy()
		if err != nil {
			return nil, err
		}
		configuration, err := model.DecodeAttemptConfiguration(header.Configuration, header.ConfigurationDigest)
		if err != nil {
			return nil, invalidPersistedState("exam_attempt", "attempt_configuration", err)
		}
		attemptID, parseErr := model.ParseExamAttemptID(guard.AttemptID)
		if parseErr != nil {
			return nil, invalidPersistedState("exam_attempt", "id", parseErr)
		}
		currentRevisionID, parseErr := model.ParseExamRevisionID(guard.RevisionID)
		if parseErr != nil {
			return nil, invalidPersistedState("exam_sitting", "exam_revision_id", parseErr)
		}
		liveCorrections, pendingCapabilities, err := listCandidateLiveCorrections(ctx, tx, attemptID, currentRevisionID)
		if err != nil {
			return nil, err
		}
		retention, err := getRetentionPolicy(ctx, tx, "FOR SHARE OF p")
		if err != nil {
			return nil, err
		}
		capabilities, browserPolicy, err := candidateRuntimeCapabilities(configuration, guard.AdmissionRevisionID, guard.RevisionID,
			header.SittingState, header.DatabaseNow, header.Policy, header.BrowserPolicy, header.BrowserPolicyDigest, pendingCapabilities, header.PolicyRevisionNumber, retention, header.BrowserUnavailable)
		if err != nil {
			return nil, err
		}
		var rows []struct {
			ResourceID  string `db:"resource_id"`
			DisplayName string `db:"display_name"`
			Description string `db:"description"`
			MediaType   string `db:"media_type"`
			SHA256      string `db:"sha256"`
			Position    int    `db:"position"`
			SizeBytes   int64  `db:"size_bytes"`
		}
		if err = tx.Select(ctx, &rows, `SELECT resource_id,display_name,description_markdown AS description,position,media_type,size_bytes,sha256
			FROM exam_revision_resources WHERE exam_revision_id=? ORDER BY position`, guard.RevisionID); err != nil {
			return nil, fmt.Errorf("list candidate Exam Resources: %w", err)
		}
		sittingID, parseErr := model.ParseExamSittingID(guard.SittingID)
		if parseErr != nil {
			return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", parseErr)
		}
		classID, parseErr := model.ParseClassID(guard.ClassID)
		if parseErr != nil {
			return nil, invalidPersistedState("exam_sitting", "class_id", parseErr)
		}
		result := &store.CandidateExamPresentation{AttemptID: attemptID, SittingID: sittingID, ClassID: classID,
			Title: header.Title, InstructionsMarkdown: header.Instructions, Capacity: capacity,
			RuntimeCapabilities: capabilities, BrowserPolicy: browserPolicy, LiveCorrections: liveCorrections,
			Resources: make([]store.CandidateExamResource, 0, len(rows))}
		for _, row := range rows {
			resourceID, parseErr := model.ParseExamResourceID(row.ResourceID)
			if parseErr != nil {
				return nil, invalidPersistedState("exam_revision_resource", "resource_id", parseErr)
			}
			result.Resources = append(result.Resources, store.CandidateExamResource{ResourceID: resourceID, DisplayName: row.DisplayName,
				DescriptionMarkdown: row.Description, Position: row.Position, MediaType: model.ExamResourceMediaType(row.MediaType),
				SizeBytes: row.SizeBytes, SHA256: row.SHA256})
		}
		return result, nil
	})
}

func (s *SQLExamAttemptStore) listCandidateWorkspace(ctx context.Context, options store.CandidateWorkspaceListOptions) (*store.CandidateAttemptWorkspacePage, error) {
	firstPage := options.ExpectedCursor == -1 && options.AfterEntryID.IsZero()
	continuation := options.ExpectedCursor >= 0 && options.AfterEntryID.IsValid()
	if options.Limit < 1 || options.Limit > model.AttemptWorkspaceJournalReadMaximum || (!firstPage && !continuation) {
		return nil, store.NewErrInvalidInput("attempt_workspace", "list_options", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "list candidate Attempt Workspace", func(ctx context.Context, tx *sqlxTxWrapper) (*store.CandidateAttemptWorkspacePage, error) {
		if _, err := s.lockCandidateGuard(ctx, tx, options.Access); err != nil {
			return nil, err
		}
		return listCandidateWorkspaceSnapshot(ctx, tx, options, continuation)
	})
}

func listCandidateWorkspaceSnapshot(ctx context.Context, tx *sqlxTxWrapper, options store.CandidateWorkspaceListOptions, continuation bool) (*store.CandidateAttemptWorkspacePage, error) {
	var workspace struct {
		ID     string `db:"id"`
		Cursor int64  `db:"cursor"`
	}
	if err := tx.Get(ctx, &workspace, `SELECT id,cursor FROM exam_attempt_workspaces WHERE exam_attempt_id=? FOR SHARE`,
		options.Access.AttemptID.String()); err != nil {
		return nil, translateError("attempt_workspace", options.Access.AttemptID.String(), err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(workspace.ID)
	if err != nil {
		return nil, invalidPersistedState("attempt_workspace", "id", err)
	}
	if continuation && options.ExpectedCursor != workspace.Cursor {
		return &store.CandidateAttemptWorkspacePage{WorkspaceID: workspaceID, Cursor: workspace.Cursor, RefreshRequired: true}, nil
	}
	var afterPath string
	if continuation {
		if err = tx.Get(ctx, &afterPath, `SELECT path FROM exam_attempt_workspace_entries WHERE workspace_id=? AND id=?`,
			workspace.ID, options.AfterEntryID.String()); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &store.CandidateAttemptWorkspacePage{WorkspaceID: workspaceID, Cursor: workspace.Cursor, RefreshRequired: true}, nil
			}
			return nil, fmt.Errorf("resolve Attempt Workspace manifest boundary: %w", err)
		}
	}
	var rows []struct {
		EntryID        string         `db:"entry_id"`
		Kind           string         `db:"kind"`
		Path           string         `db:"path"`
		ContentVersion sql.NullString `db:"content_version"`
		MediaType      sql.NullString `db:"media_type"`
		SizeBytes      sql.NullInt64  `db:"size_bytes"`
		SHA256         sql.NullString `db:"sha256"`
	}
	query := `SELECT e.id AS entry_id,e.kind,e.path,o.content_version,o.media_type,o.size_bytes,o.sha256
		FROM exam_attempt_workspaces w JOIN exam_attempt_workspace_entries e ON e.workspace_id=w.id
		LEFT JOIN exam_attempt_workspace_objects o ON o.id=e.current_object_id AND o.workspace_id=e.workspace_id
		WHERE w.exam_attempt_id=?`
	args := []any{options.Access.AttemptID.String()}
	if continuation {
		query += ` AND (e.path,e.id)>(?,?)`
		args = append(args, afterPath, options.AfterEntryID.String())
	}
	query += ` ORDER BY e.path,e.id LIMIT ?`
	args = append(args, options.Limit+1)
	if err := tx.Select(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list candidate Attempt Workspace: %w", err)
	}
	hasMore := len(rows) > options.Limit
	if hasMore {
		rows = rows[:options.Limit]
	}
	result := make([]store.CandidateAttemptWorkspaceItem, 0, len(rows))
	for _, row := range rows {
		id, err := model.ParseAttemptWorkspaceEntryID(row.EntryID)
		if err != nil {
			return nil, invalidPersistedState("attempt_workspace_entry", "id", err)
		}
		var version model.WorkspaceContentVersion
		if row.ContentVersion.Valid {
			version, err = model.ParseWorkspaceContentVersion(row.ContentVersion.String)
			if err != nil {
				return nil, invalidPersistedState("attempt_workspace_object", "content_version", err)
			}
		}
		result = append(result, store.CandidateAttemptWorkspaceItem{EntryID: id, Kind: model.StarterWorkspaceEntryKind(row.Kind), Path: row.Path, ContentVersion: version, MediaType: row.MediaType.String, SizeBytes: row.SizeBytes.Int64, SHA256: row.SHA256.String})
	}
	return &store.CandidateAttemptWorkspacePage{WorkspaceID: workspaceID, Cursor: workspace.Cursor, Items: result, HasMore: hasMore}, nil
}

// ResolveCandidateResource returns the metadata and stored file rendition of an
// Exam Resource in the Candidate's admitted Exam Revision, or a not-found
// error.
func (s *SQLExamAttemptStore) ResolveCandidateResource(ctx context.Context, access store.CandidateAttemptAccess, resourceID model.ExamResourceID) (*store.CandidateResourceContent, error) {
	guard, err := s.candidateGuard(ctx, access)
	if err != nil {
		return nil, err
	}
	if !resourceID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_resource", "id", nil)
	}
	var row struct {
		ResourceID     string `db:"resource_id"`
		DisplayName    string `db:"display_name"`
		Description    string `db:"description"`
		MediaType      string `db:"media_type"`
		SHA256         string `db:"sha256"`
		FileRevisionID string `db:"file_revision_id"`
		RenditionID    string `db:"rendition_id"`
		Position       int    `db:"position"`
		SizeBytes      int64  `db:"size_bytes"`
	}
	err = s.GetMaster().Get(ctx, &row, `SELECT resource_id,display_name,description_markdown AS description,position,media_type,size_bytes,sha256,file_revision_id,rendition_id
		FROM exam_revision_resources WHERE exam_revision_id=? AND resource_id=?`, guard.RevisionID, resourceID.String())
	if err != nil {
		return nil, translateError("exam_resource", resourceID.String(), err)
	}
	fileRevisionID, err := model.ParseFileRevisionID(row.FileRevisionID)
	if err != nil {
		return nil, invalidPersistedState("exam_resource", "file_revision_id", err)
	}
	renditionID, err := model.ParseFileRenditionID(row.RenditionID)
	if err != nil {
		return nil, invalidPersistedState("exam_resource", "rendition_id", err)
	}
	return &store.CandidateResourceContent{Resource: store.CandidateExamResource{ResourceID: resourceID, DisplayName: row.DisplayName,
		DescriptionMarkdown: row.Description, Position: row.Position, MediaType: model.ExamResourceMediaType(row.MediaType), SizeBytes: row.SizeBytes, SHA256: row.SHA256}, FileRevisionID: fileRevisionID, RenditionID: renditionID}, nil
}

func resolveCandidateWorkspaceFile(ctx context.Context, executor sqlxExecutor, access store.CandidateAttemptAccess, entryID model.AttemptWorkspaceEntryID) (*store.CandidateWorkspaceContent, error) {
	var row struct {
		EntryID   string         `db:"entry_id"`
		Kind      string         `db:"kind"`
		Path      string         `db:"path"`
		ObjectID  string         `db:"object_id"`
		Origin    string         `db:"origin"`
		Version   string         `db:"version"`
		MediaType string         `db:"media_type"`
		SHA256    string         `db:"sha256"`
		StarterID sql.NullString `db:"starter_id"`
		SizeBytes int64          `db:"size_bytes"`
	}
	err := executor.Get(ctx, &row, `SELECT e.id AS entry_id,e.kind,e.path,o.id AS object_id,o.storage_origin AS origin,
		o.starter_object_id AS starter_id,o.content_version AS version,o.media_type,o.size_bytes,o.sha256
		FROM exam_attempt_workspaces w JOIN exam_attempt_workspace_entries e ON e.workspace_id=w.id
		JOIN exam_attempt_workspace_objects o ON o.id=e.current_object_id AND o.workspace_id=e.workspace_id
		WHERE w.exam_attempt_id=? AND e.id=? AND e.kind='file'`, access.AttemptID.String(), entryID.String())
	if err != nil {
		return nil, translateError("attempt_workspace_entry", entryID.String(), err)
	}
	objectID, err := model.ParseAttemptWorkspaceObjectID(row.ObjectID)
	if err != nil {
		return nil, invalidPersistedState("attempt_workspace_object", "id", err)
	}
	version, err := model.ParseWorkspaceContentVersion(row.Version)
	if err != nil {
		return nil, invalidPersistedState("attempt_workspace_object", "content_version", err)
	}
	var starterID model.StarterWorkspaceObjectID
	if row.StarterID.Valid {
		starterID, err = model.ParseStarterWorkspaceObjectID(row.StarterID.String)
		if err != nil {
			return nil, invalidPersistedState("attempt_workspace_object", "starter_object_id", err)
		}
	}
	return &store.CandidateWorkspaceContent{Entry: store.CandidateAttemptWorkspaceItem{EntryID: entryID, Kind: model.StarterWorkspaceEntryKind(row.Kind), Path: row.Path,
		ContentVersion: version, MediaType: row.MediaType, SizeBytes: row.SizeBytes, SHA256: row.SHA256}, StorageOrigin: model.AttemptWorkspaceObjectStorage(row.Origin),
		StarterObjectID: starterID, AttemptObjectID: objectID, ContentVersion: version}, nil
}

var _ store.ExamAttemptStore = (*SQLExamAttemptStore)(nil)

func boundedParticipationLease(now, end time.Time) time.Time {
	deadline := now.Add(model.AttemptParticipationInitialLease)
	if end.Before(deadline) {
		deadline = end
	}
	return deadline
}

// suspensionProvenance validates the source-specific links of one persisted
// Suspension episode without loading any private reason: a policy episode
// links exactly one Integrity Flag, and a manual episode links its suspending
// Exam Manager instead.
func suspensionProvenance(source model.AttemptSuspensionSource, reason model.AttemptSuspensionCandidateReason,
	flag, suspendedBy string,
) (model.IntegrityFlagID, model.UserID, error) {
	switch {
	case source == model.AttemptSuspensionSourcePolicy && reason.IsPolicy() && suspendedBy == "":
		flagID, err := model.ParseIntegrityFlagID(flag)
		if err != nil {
			return "", "", invalidPersistedState("attempt_suspension", "integrity_flag_id", err)
		}
		return flagID, "", nil
	case source == model.AttemptSuspensionSourceManual && reason == model.AttemptSuspensionCandidateReasonManagerSuspended && flag == "":
		actorID, err := model.ParseUserID(suspendedBy)
		if err != nil {
			return "", "", invalidPersistedState("attempt_suspension", "suspended_by_user_id", err)
		}
		return "", actorID, nil
	default:
		return "", "", invalidPersistedState("attempt_suspension", "source", errors.New("inconsistent Attempt Suspension provenance"))
	}
}
