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

type examAttemptAdmissionGuard struct {
	ExamID         string    `db:"exam_id"`
	AcademicUnitID string    `db:"academic_unit_id"`
	RevisionID     string    `db:"exam_revision_id"`
	ClassID        string    `db:"class_id"`
	PeriodID       string    `db:"academic_period_id"`
	State          string    `db:"state"`
	ScheduledEnd   time.Time `db:"scheduled_end_at"`
	DatabaseNow    time.Time `db:"database_now"`
}

type examAttemptStarterRow struct {
	EntryID        string         `db:"entry_id"`
	Kind           string         `db:"kind"`
	Path           string         `db:"path"`
	ObjectID       sql.NullString `db:"object_id"`
	ContentVersion sql.NullString `db:"content_version"`
	MediaType      sql.NullString `db:"media_type"`
	SizeBytes      sql.NullInt64  `db:"size_bytes"`
	SHA256         sql.NullString `db:"sha256"`
}

// lockExamAttemptEligibility serializes every admission for one candidate and
// locks the candidate User, then current membership, before the Exam/Sitting
// lineage. allowPaused is reserved for exact replay recovery; every fresh
// Connection still requires Open.
func (s *SQLExamAttemptStore) lockExamAttemptEligibility(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, allowPaused bool) (examAttemptAdmissionGuard, error) {
	var zero examAttemptAdmissionGuard
	initial, err := lockExamAttemptAdmissionLineage(ctx, tx, input)
	if err != nil {
		return zero, err
	}
	var guard examAttemptAdmissionGuard
	if err = tx.Get(ctx, &guard, `SELECT s.exam_id,e.academic_unit_id,s.exam_revision_id,s.class_id,c.academic_period_id,
		s.state,s.scheduled_end_at
		FROM exam_sittings s JOIN exams e ON e.id=s.exam_id JOIN classes c ON c.id=s.class_id
		WHERE s.id=? AND s.exam_id=? FOR SHARE OF s,c`, input.SittingID.String(), initial.ExamID); err != nil {
		return zero, translateError("exam_sitting", input.SittingID.String(), err)
	}
	if guard.PeriodID != initial.PeriodID {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_membership", nil)
	}
	if guard.State != string(model.ExamSittingOpen) && !(allowPaused && guard.State == string(model.ExamSittingPaused)) {
		return zero, store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	if err = lockExamAttemptSessionEligibility(ctx, tx, input, guard.ClassID); err != nil {
		return zero, err
	}
	if err = tx.Get(ctx, &guard.DatabaseNow, `SELECT statement_timestamp()`); err != nil {
		return zero, fmt.Errorf("read Exam Attempt admission decision time: %w", err)
	}
	hasManagementAuthority, err := hasEffectiveExamManagementAuthority(ctx, tx, input.CandidateUserID.String(),
		guard.ExamID, guard.AcademicUnitID, guard.DatabaseNow)
	if err != nil {
		return zero, err
	}
	if hasManagementAuthority {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_management_authority", nil)
	}
	if !guard.DatabaseNow.Before(guard.ScheduledEnd) {
		return zero, store.NewErrConflict("exam_sitting", "exam_sitting_deadline_reached", nil)
	}
	if err = requireExamAttemptEligibilityAtDecisionTime(ctx, tx, input, guard); err != nil {
		return zero, err
	}
	return guard, nil
}

// examAttemptAdmissionLineage is the Sitting's Exam and Academic Period read
// before the admission locks the rest of the lineage.
type examAttemptAdmissionLineage struct {
	ExamID   string `db:"exam_id"`
	PeriodID string `db:"academic_period_id"`
}

// lockExamAttemptAdmissionLineage locks the candidate User and admission
// advisory lock, then the candidate's Class enrollment in the Sitting's
// Academic Period, then shares the unarchived Exam.
func lockExamAttemptAdmissionLineage(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect) (examAttemptAdmissionLineage, error) {
	var initial examAttemptAdmissionLineage
	if err := lockCandidateUser(ctx, tx, input.CandidateUserID.String()); err != nil {
		return initial, err
	}
	lockKey := advisoryLockKey("exam-attempt-admission", input.CandidateUserID.String())
	if err := lockAdvisory(ctx, tx, lockKey); err != nil {
		return initial, fmt.Errorf("lock Exam Attempt admission: %w", err)
	}

	if err := tx.Get(ctx, &initial, `SELECT s.exam_id,c.academic_period_id FROM exam_sittings s
		JOIN classes c ON c.id=s.class_id WHERE s.id=?`, input.SittingID.String()); err != nil {
		return initial, translateError("exam_sitting", input.SittingID.String(), err)
	}
	if err := lockClassEnrollment(ctx, tx, input.CandidateUserID.String(), initial.PeriodID); err != nil {
		return initial, err
	}
	var lockedExamID string
	if err := tx.Get(ctx, &lockedExamID, `SELECT id FROM exams WHERE id=? AND archived_at IS NULL FOR SHARE`, initial.ExamID); err != nil {
		return initial, translateError("exam_sitting", input.SittingID.String(), err)
	}
	return initial, nil
}

// lockExamAttemptSessionEligibility shares the candidate's User, Desktop
// Session and Registration, Class membership and academic lineage, requiring
// each to be current for the input Desktop build.
func lockExamAttemptSessionEligibility(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, classID string) error {
	var eligible bool
	if err := tx.Get(ctx, &eligible, `SELECT true FROM users u
		JOIN sessions se ON se.id=? AND se.user_id=u.id
		JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=se.user_id
		JOIN class_members cm ON cm.user_id=u.id AND cm.class_id=?
		JOIN classes c ON c.id=cm.class_id AND c.academic_period_id=cm.academic_period_id
		JOIN programme_levels pl ON pl.id=c.programme_level_id
		JOIN programmes p ON p.id=pl.programme_id
		JOIN academic_units au ON au.id=p.academic_unit_id
		JOIN academic_periods ap ON ap.id=c.academic_period_id AND ap.institution_id=au.institution_id
		WHERE u.id=? AND u.archived_at IS NULL AND u.disabled_at IS NULL
		AND se.archived_at IS NULL AND se.revoked_at IS NULL AND se.client_type='desktop'
		AND se.desktop_registration_id=? AND se.dpop_key_thumbprint=?
		AND se.desktop_release=? AND se.desktop_build_id=? AND se.desktop_platform=?
		AND se.desktop_architecture=? AND se.desktop_realtime_protocol=?
		AND dr.revoked_at IS NULL AND dr.key_thumbprint=?
		AND cm.archived_at IS NULL
		AND c.archived_at IS NULL AND pl.archived_at IS NULL AND p.archived_at IS NULL
		AND au.archived_at IS NULL AND ap.archived_at IS NULL
		FOR SHARE OF u,se,dr,cm,c,pl,p,au,ap`, input.SessionID.String(), classID, input.CandidateUserID.String(),
		input.DesktopRegistrationID.String(), input.DPoPKeyThumbprint,
		input.DesktopBuild.DesktopRelease, input.DesktopBuild.DesktopBuildID, string(input.DesktopBuild.Platform),
		string(input.DesktopBuild.Architecture), input.DesktopBuild.RealtimeProtocol, input.DPoPKeyThumbprint); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrNotFound("exam_attempt_eligibility", input.SittingID.String())
		}
		return fmt.Errorf("validate Exam Attempt eligibility: %w", err)
	}
	if !eligible {
		return store.NewErrNotFound("exam_attempt_eligibility", input.SittingID.String())
	}
	return nil
}

// requireExamAttemptEligibilityAtDecisionTime rechecks the Session expiry and
// Class membership window against the guard's decision time.
func requireExamAttemptEligibilityAtDecisionTime(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect,
	guard examAttemptAdmissionGuard,
) error {
	var eligible bool
	if err := tx.Get(ctx, &eligible, `SELECT EXISTS (
		SELECT 1 FROM users u
		JOIN sessions se ON se.id=? AND se.user_id=u.id
		JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=se.user_id
		JOIN class_members cm ON cm.user_id=u.id AND cm.class_id=?
		JOIN classes c ON c.id=cm.class_id AND c.academic_period_id=cm.academic_period_id
		JOIN programme_levels pl ON pl.id=c.programme_level_id
		JOIN programmes p ON p.id=pl.programme_id
		JOIN academic_units au ON au.id=p.academic_unit_id
		JOIN academic_periods ap ON ap.id=c.academic_period_id AND ap.institution_id=au.institution_id
		WHERE u.id=? AND u.archived_at IS NULL AND u.disabled_at IS NULL
		AND se.archived_at IS NULL AND se.revoked_at IS NULL AND se.client_type='desktop'
		AND se.desktop_registration_id=? AND se.dpop_key_thumbprint=?
		AND se.desktop_release=? AND se.desktop_build_id=? AND se.desktop_platform=?
		AND se.desktop_architecture=? AND se.desktop_realtime_protocol=?
		AND dr.revoked_at IS NULL AND dr.key_thumbprint=?
		AND se.idle_expires_at>? AND se.expires_at>?
		AND cm.archived_at IS NULL AND cm.start_at<=? AND (cm.end_at IS NULL OR cm.end_at>?)
		AND c.archived_at IS NULL AND pl.archived_at IS NULL AND p.archived_at IS NULL
		AND au.archived_at IS NULL AND ap.archived_at IS NULL)`, input.SessionID.String(), guard.ClassID,
		input.CandidateUserID.String(), input.DesktopRegistrationID.String(), input.DPoPKeyThumbprint,
		input.DesktopBuild.DesktopRelease, input.DesktopBuild.DesktopBuildID, string(input.DesktopBuild.Platform),
		string(input.DesktopBuild.Architecture), input.DesktopBuild.RealtimeProtocol, input.DPoPKeyThumbprint,
		guard.DatabaseNow, guard.DatabaseNow, guard.DatabaseNow, guard.DatabaseNow); err != nil {
		return fmt.Errorf("revalidate Exam Attempt eligibility at decision time: %w", err)
	}
	if !eligible {
		return store.NewErrNotFound("exam_attempt_eligibility", input.SittingID.String())
	}
	return nil
}

func hasEffectiveExamManagementAuthority(ctx context.Context, tx *sqlxTxWrapper, userID, examID, academicUnitID string,
	at time.Time,
) (bool, error) {
	var authoritative bool
	err := tx.Get(ctx, &authoritative, `WITH RECURSIVE ancestors AS (
		SELECT id,parent_id FROM academic_units WHERE id=? AND archived_at IS NULL
		UNION ALL
		SELECT parent.id,parent.parent_id FROM academic_units parent JOIN ancestors child ON parent.id=child.parent_id
		WHERE parent.archived_at IS NULL
	)
	SELECT EXISTS (
		SELECT 1 FROM role_bindings binding JOIN roles role ON role.id=binding.role_id
		WHERE binding.user_id=? AND binding.archived_at IS NULL AND binding.start_at<=?
		AND (binding.end_at IS NULL OR binding.end_at>?) AND role.archived_at IS NULL
		AND (
			(role.name=? AND ?=ANY(role.permissions) AND binding.scope_type='institution')
			OR (
				?=ANY(role.permissions)
				AND (binding.scope_type='institution' OR
					(binding.scope_type='academic_unit' AND binding.scope_id IN (SELECT id FROM ancestors)))
				AND EXISTS (SELECT 1 FROM exam_managers manager WHERE manager.exam_id=? AND manager.user_id=?)
				AND EXISTS (SELECT 1 FROM academic_unit_members member WHERE member.academic_unit_id=?
					AND member.user_id=? AND member.archived_at IS NULL AND member.start_at<=?
					AND (member.end_at IS NULL OR member.end_at>?))
			)
		)
	)`, academicUnitID, userID, at, at, model.SystemAdministratorRoleName, string(model.ActionExamManageOverride),
		string(model.ActionExamManage), examID, userID, academicUnitID, userID, at, at)
	if err != nil {
		return false, fmt.Errorf("inspect effective Exam management authority: %w", err)
	}
	return authoritative, nil
}

// firstAdmission freezes the Attempt Configuration and creates the Attempt,
// its Workspace, the first Participation and Connection, revoking the
// candidate's other interactive Sessions.
func (s *SQLExamAttemptStore) firstAdmission(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, guard examAttemptAdmissionGuard) (examAttemptConnectOutcome, error) {
	var zero examAttemptConnectOutcome
	security, securityErr := s.prepareConnectSecurity(ctx, tx, input, guard, input.AttemptID, 1, true)
	if securityErr != nil {
		return zero, securityErr
	}
	if input.InitialConfiguration == nil {
		return zero, store.NewErrConflict("exam_attempt", "attempt_configuration_required", nil)
	}
	candidate := input.InitialConfiguration.Clone()
	if candidate.ValidateForBuild(input.DesktopBuild) != nil {
		return zero, store.NewErrInvalidInput("exam_attempt", "initial_configuration", nil)
	}
	var settingsRevision string
	if err := tx.Get(ctx, &settingsRevision, `SELECT revision FROM user_settings_documents WHERE user_id=? FOR SHARE`, input.CandidateUserID.String()); err != nil {
		return zero, translateError("user_settings_document", input.CandidateUserID.String(), err)
	}
	if settingsRevision != candidate.UserSettingsRevision.String() {
		return zero, store.NewErrConflict("exam_attempt", "attempt_configuration_stale", nil)
	}
	configuration, err := candidate.Freeze(model.NewID())
	if err != nil {
		return zero, store.NewErrInvalidInput("exam_attempt", "initial_configuration", nil)
	}
	canonicalConfiguration, err := configuration.CanonicalAdmission()
	if err != nil {
		return zero, store.NewErrInvalidInput("exam_attempt", "initial_configuration", nil)
	}
	var starter []examAttemptStarterRow
	if err = tx.Select(ctx, &starter, `SELECT entry_id,kind,path,object_id,content_version,media_type,size_bytes,sha256
		FROM exam_revision_starter_workspace_entries WHERE exam_revision_id=? ORDER BY entry_id`, guard.RevisionID); err != nil {
		return zero, fmt.Errorf("load frozen Starter Workspace: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_attempts
		(id,exam_id,exam_sitting_id,candidate_user_id,admission_revision_id,
		 attempt_configuration_canonical,attempt_configuration_digest,initial_desktop_release,initial_desktop_build_id,
		 initial_desktop_platform,initial_desktop_architecture,state,created_at,updated_at,revision)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,'active',?,?,1)`, input.AttemptID.String(), guard.ExamID, input.SittingID.String(),
		input.CandidateUserID.String(), guard.RevisionID, canonicalConfiguration, configuration.Digest,
		input.DesktopBuild.DesktopRelease, input.DesktopBuild.DesktopBuildID, string(input.DesktopBuild.Platform),
		string(input.DesktopBuild.Architecture), guard.DatabaseNow, guard.DatabaseNow); err != nil {
		return zero, fmt.Errorf("insert Exam Attempt: %w", translateError("exam_attempt", input.AttemptID.String(), err))
	}
	if err = insertAttemptWorkspaceFromStarter(ctx, tx, input, guard, starter); err != nil {
		return zero, err
	}

	leaseExpires := boundedParticipationLease(guard.DatabaseNow, guard.ScheduledEnd)
	if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_participations
		(id,exam_attempt_id,session_id,state,generation,renewal_sequence,continuity_credential_hash,started_at,updated_at,lease_expires_at)
		VALUES (?,?,?,'active',1,0,?,?,?,?)`, input.ParticipationID.String(), input.AttemptID.String(), input.SessionID.String(), input.ContinuityCredentialHash,
		guard.DatabaseNow, guard.DatabaseNow, leaseExpires); err != nil {
		return zero, fmt.Errorf("insert Attempt Participation: %w", translateError("attempt_participation", input.ParticipationID.String(), err))
	}
	if err = persistConnectSecurity(ctx, tx, input, security); err != nil {
		return zero, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_connections
		(id,exam_attempt_id,participation_id,session_id,state,opened_at) VALUES (?,?,?,?,'open',?)`,
		input.ConnectionID.String(), input.AttemptID.String(), input.ParticipationID.String(), input.SessionID.String(), guard.DatabaseNow); err != nil {
		return zero, fmt.Errorf("insert Attempt Connection: %w", translateError("attempt_connection", input.ConnectionID.String(), err))
	}
	revokedSessionIDs, revokedTokenHashes, err := revokeOtherInteractiveSessionsForAttempt(ctx, tx,
		input.CandidateUserID.String(), input.SessionID.String(), guard.DatabaseNow)
	if err != nil {
		return zero, err
	}

	outcome := examAttemptConnectOutcome{AttemptID: input.AttemptID.String(), WorkspaceID: input.WorkspaceID.String(),
		ParticipationID: input.ParticipationID.String(), ConnectionID: input.ConnectionID.String(), ExamID: guard.ExamID,
		SittingID: input.SittingID.String(), CandidateID: input.CandidateUserID.String(), RevisionID: guard.RevisionID,
		SessionID: input.SessionID.String(), ClassID: guard.ClassID, StartedAt: model.TimeUTC(guard.DatabaseNow), LeaseExpiresAt: model.TimeUTC(leaseExpires),
		EntryCount: len(starter), Generation: 1, FirstAdmission: true, ConnectionOpened: true,
		RevokedSessionIDs: revokedSessionIDs, RevokedTokenHashes: revokedTokenHashes}
	if err = completeExamAttemptConnectAudit(ctx, tx, outcome, false, "", input.AuditEventID, input.AuditAt); err != nil {
		return zero, err
	}
	return outcome, nil
}

// insertAttemptWorkspaceFromStarter inserts the first-admission Attempt
// Workspace and copies each frozen Starter Workspace entry, with its object
// for a file, into it.
func insertAttemptWorkspaceFromStarter(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect,
	guard examAttemptAdmissionGuard, starter []examAttemptStarterRow,
) error {
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_workspaces (id,exam_attempt_id,admission_revision_id,cursor,created_at,updated_at)
		VALUES (?,?,?,0,?,?)`, input.WorkspaceID.String(), input.AttemptID.String(), guard.RevisionID, guard.DatabaseNow, guard.DatabaseNow); err != nil {
		return fmt.Errorf("insert Exam Attempt Workspace: %w", err)
	}

	for _, source := range starter {
		entryID := model.NewAttemptWorkspaceEntryID()
		var objectID model.AttemptWorkspaceObjectID
		if source.Kind == string(model.StarterWorkspaceEntryFile) {
			objectID = model.NewAttemptWorkspaceObjectID()
			if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_objects
				(id,workspace_id,admission_revision_id,source_starter_entry_id,storage_origin,starter_object_id,state,content_version,media_type,size_bytes,sha256,created_at,updated_at)
				VALUES (?,?,?,?,'starter',?,'current',?,?,?,?,?,?)`, objectID.String(), input.WorkspaceID.String(), guard.RevisionID, source.EntryID, source.ObjectID.String,
				source.ContentVersion.String, source.MediaType.String, source.SizeBytes.Int64, source.SHA256.String, guard.DatabaseNow, guard.DatabaseNow); err != nil {
				return fmt.Errorf("insert Exam Attempt Workspace object: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_entries
			(id,workspace_id,admission_revision_id,source_starter_entry_id,kind,path,current_object_id,created_at,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`, entryID.String(), input.WorkspaceID.String(), guard.RevisionID, source.EntryID,
			source.Kind, source.Path, nullableAttemptWorkspaceObjectID(objectID), guard.DatabaseNow, guard.DatabaseNow); err != nil {
			return fmt.Errorf("insert Exam Attempt Workspace entry: %w", err)
		}
	}
	return nil
}

// reconnectParticipation is the active Participation generation a reconnect
// resumes, or the next generation it starts for a ready Attempt.
type reconnectParticipation struct {
	ID             string    `db:"id"`
	SessionID      string    `db:"session_id"`
	CredentialHash string    `db:"continuity_credential_hash"`
	Generation     int64     `db:"generation"`
	LeaseExpiresAt time.Time `db:"lease_expires_at"`
}

// reconnect resumes the existing Attempt's active Participation, or starts
// its next generation when a re-allowed Attempt is ready, and opens or reuses
// the Attempt Connection.
func (s *SQLExamAttemptStore) reconnect(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, guard examAttemptAdmissionGuard,
	attemptID, workspaceID, admissionRevisionID, attemptState string, attemptCreatedAt time.Time, workspaceEntryCount int,
) (examAttemptConnectOutcome, error) {
	var zero examAttemptConnectOutcome
	if attemptState != string(model.ExamAttemptActive) && attemptState != string(model.ExamAttemptReady) {
		return zero, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	reactivating := attemptState == string(model.ExamAttemptReady)
	participation, err := s.lockReconnectParticipation(ctx, tx, input, guard, attemptID, reactivating)
	if err != nil {
		return zero, err
	}
	if participation.SessionID != input.SessionID.String() {
		return zero, store.NewErrConflict("attempt_participation", "attempt_participation_session", nil)
	}
	if subtle.ConstantTimeCompare([]byte(participation.CredentialHash), []byte(input.ContinuityCredentialHash)) != 1 {
		return zero, store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	if !guard.DatabaseNow.Before(participation.LeaseExpiresAt) {
		return zero, store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}

	if !reactivating {
		if err = validateSecurityResume(ctx, tx, input, participation.ID, participation.Generation); err != nil {
			return zero, err
		}
	}
	connectionID, connectionOpened, err := openReconnectConnection(ctx, tx, input, guard, attemptID, participation.ID)
	if err != nil {
		return zero, err
	}
	var revokedSessionIDs, revokedTokenHashes []string
	if reactivating {
		revokedSessionIDs, revokedTokenHashes, err = activateReadyExamAttempt(ctx, tx, input, guard, attemptID)
		if err != nil {
			return zero, err
		}
	}

	outcome := examAttemptConnectOutcome{AttemptID: attemptID, WorkspaceID: workspaceID, ParticipationID: participation.ID,
		ConnectionID: connectionID, ExamID: guard.ExamID, SittingID: input.SittingID.String(), CandidateID: input.CandidateUserID.String(),
		RevisionID: admissionRevisionID, SessionID: input.SessionID.String(), ClassID: guard.ClassID, StartedAt: model.TimeUTC(attemptCreatedAt),
		LeaseExpiresAt: model.TimeUTC(participation.LeaseExpiresAt), EntryCount: workspaceEntryCount,
		Generation: participation.Generation, FirstAdmission: false, ConnectionOpened: connectionOpened,
		RevokedSessionIDs: revokedSessionIDs, RevokedTokenHashes: revokedTokenHashes}
	if err = completeExamAttemptConnectAudit(ctx, tx, outcome, false, "", input.AuditEventID, input.AuditAt); err != nil {
		return zero, err
	}
	return outcome, nil
}

// lockReconnectParticipation locks the Attempt's active Participation; for a
// ready Attempt, which must have none, it starts the next generation instead.
func (s *SQLExamAttemptStore) lockReconnectParticipation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect,
	guard examAttemptAdmissionGuard, attemptID string, reactivating bool,
) (reconnectParticipation, error) {
	var participation reconnectParticipation
	err := tx.Get(ctx, &participation, `SELECT id,session_id,continuity_credential_hash,generation,lease_expires_at
		FROM exam_attempt_participations WHERE exam_attempt_id=? AND state='active' FOR UPDATE`, attemptID)
	switch {
	case err == nil && reactivating:
		return participation, invalidPersistedState("exam_attempt", "ready_participation", errors.New("ready Attempt has an active Participation"))
	case err == nil:
		return participation, nil
	case errors.Is(err, sql.ErrNoRows) && reactivating:
		return s.startNextAttemptParticipation(ctx, tx, input, guard, attemptID)
	case errors.Is(err, sql.ErrNoRows):
		return participation, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	default:
		return participation, fmt.Errorf("lock active Attempt Participation: %w", err)
	}
}

// startNextAttemptParticipation prepares connect security for the next
// Participation generation of a ready Attempt and inserts that generation.
func (s *SQLExamAttemptStore) startNextAttemptParticipation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect,
	guard examAttemptAdmissionGuard, attemptID string,
) (reconnectParticipation, error) {
	var participation reconnectParticipation
	if err := tx.Get(ctx, &participation.Generation, `SELECT COALESCE(MAX(generation),0)+1
				FROM exam_attempt_participations WHERE exam_attempt_id=?`, attemptID); err != nil {
		return participation, fmt.Errorf("select next Attempt Participation generation: %w", err)
	}
	parsedAttemptID, parseErr := model.ParseExamAttemptID(attemptID)
	if parseErr != nil {
		return participation, parseErr
	}
	security, securityErr := s.prepareConnectSecurity(ctx, tx, input, guard, parsedAttemptID, participation.Generation, false)
	if securityErr != nil {
		return participation, securityErr
	}
	participation.ID = input.ParticipationID.String()
	participation.SessionID = input.SessionID.String()
	participation.CredentialHash = input.ContinuityCredentialHash
	participation.LeaseExpiresAt = boundedParticipationLease(guard.DatabaseNow, guard.ScheduledEnd)
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_participations
				(id,exam_attempt_id,session_id,state,generation,renewal_sequence,continuity_credential_hash,started_at,updated_at,lease_expires_at)
				VALUES (?,?,?,'active',?,0,?,?,?,?)`, participation.ID, attemptID, participation.SessionID, participation.Generation,
		participation.CredentialHash, guard.DatabaseNow, guard.DatabaseNow, participation.LeaseExpiresAt); err != nil {
		return participation, fmt.Errorf("insert next Attempt Participation: %w", translateError("attempt_participation", participation.ID, err))
	}
	if err := persistConnectSecurity(ctx, tx, input, security); err != nil {
		return participation, err
	}
	return participation, nil
}

// openReconnectConnection reuses the Attempt's open Connection when it belongs
// to this Participation and Session, or opens the input Connection when none
// is open; it returns the Connection ID and whether it opened one.
func openReconnectConnection(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, guard examAttemptAdmissionGuard,
	attemptID, participationID string,
) (string, bool, error) {
	connectionID := input.ConnectionID.String()
	var open struct {
		ID              string `db:"id"`
		ParticipationID string `db:"participation_id"`
		SessionID       string `db:"session_id"`
	}
	err := tx.Get(ctx, &open, `SELECT id,participation_id,session_id FROM exam_attempt_connections
		WHERE exam_attempt_id=? AND state='open' FOR UPDATE`, attemptID)
	switch {
	case err == nil && open.ParticipationID == participationID && open.SessionID == input.SessionID.String():
		return open.ID, false, nil
	case err == nil:
		return "", false, store.NewErrConflict("attempt_connection", "attempt_connection_open", nil)
	case errors.Is(err, sql.ErrNoRows):
		if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_connections
			(id,exam_attempt_id,participation_id,session_id,state,opened_at) VALUES (?,?,?,?,'open',?)`,
			connectionID, attemptID, participationID, input.SessionID.String(), guard.DatabaseNow); err != nil {
			return "", false, fmt.Errorf("insert reconnect Attempt Connection: %w", translateError("attempt_connection", connectionID, err))
		}
		return connectionID, true, nil
	default:
		return "", false, fmt.Errorf("lock open Attempt Connection: %w", err)
	}
}

// activateReadyExamAttempt returns a ready Attempt to active and revokes the
// candidate's other interactive Sessions.
func activateReadyExamAttempt(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptConnect, guard examAttemptAdmissionGuard,
	attemptID string,
) ([]string, []string, error) {
	// guard.DatabaseNow is read before this Attempt is locked, so a re-allow
	// committed while Connect waited may already carry a later instant.
	result, updateErr := tx.Exec(ctx, `UPDATE exam_attempts SET state='active',updated_at=GREATEST(updated_at,?),revision=revision+1
			WHERE id=? AND state='ready'`, guard.DatabaseNow, attemptID)
	if updateErr != nil {
		return nil, nil, fmt.Errorf("activate ready Exam Attempt: %w", updateErr)
	}
	if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
		if affectedErr != nil {
			return nil, nil, fmt.Errorf("inspect ready Exam Attempt activation: %w", affectedErr)
		}
		return nil, nil, store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	return revokeOtherInteractiveSessionsForAttempt(ctx, tx,
		input.CandidateUserID.String(), input.SessionID.String(), guard.DatabaseNow)
}

func revokeOtherInteractiveSessionsForAttempt(ctx context.Context, tx *sqlxTxWrapper, userID, keepSessionID string,
	at time.Time,
) ([]string, []string, error) {
	at = model.TimeUTC(at)
	sessionIDs := []string{}
	if err := tx.Select(ctx, &sessionIDs, `SELECT id FROM sessions
		WHERE user_id=? AND id<>? AND client_type IN ('web','desktop')
		AND archived_at IS NULL AND revoked_at IS NULL AND idle_expires_at>? AND expires_at>?
		ORDER BY id FOR UPDATE`, userID, keepSessionID, at, at); err != nil {
		return nil, nil, fmt.Errorf("lock other interactive Sessions for Exam Attempt: %w", err)
	}
	if len(sessionIDs) == 0 {
		return []string{}, []string{}, nil
	}
	hashes := []string{}
	if err := tx.Select(ctx, &hashes, `SELECT token_hash FROM session_credentials
		WHERE session_id=ANY(?) AND archived_at IS NULL AND revoked_at IS NULL ORDER BY id FOR UPDATE`, pq.Array(sessionIDs)); err != nil {
		return nil, nil, fmt.Errorf("lock other interactive Session credentials for Exam Attempt: %w", err)
	}
	// at is PostgreSQL time while Sessions carry the creating node's clock; a
	// revocation never precedes the revoked row's creation.
	if _, err := tx.Exec(ctx, `UPDATE session_credentials SET updated_at=GREATEST(updated_at,?),revoked_at=GREATEST(created_at,?)
		WHERE session_id=ANY(?) AND archived_at IS NULL AND revoked_at IS NULL`, at, at, pq.Array(sessionIDs)); err != nil {
		return nil, nil, fmt.Errorf("revoke other interactive Session credentials for Exam Attempt: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET updated_at=GREATEST(updated_at,?),revoked_at=GREATEST(created_at,?),revocation_reason=?
		WHERE id=ANY(?) AND archived_at IS NULL AND revoked_at IS NULL`, at, at,
		string(model.SessionRevocationAttemptSessionLock), pq.Array(sessionIDs)); err != nil {
		return nil, nil, fmt.Errorf("revoke other interactive Sessions for Exam Attempt: %w", err)
	}
	return sessionIDs, hashes, nil
}

func nullableAttemptWorkspaceObjectID(id model.AttemptWorkspaceObjectID) any {
	if id.IsZero() {
		return nil
	}
	return id.String()
}
