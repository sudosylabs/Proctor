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
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// SQLExamAttemptWorkspaceStore is the PostgreSQL adapter for acknowledged
// candidate Workspace state and attempt-owned staged content metadata.
type SQLExamAttemptWorkspaceStore struct {
	*SQLStore
	attempts *SQLExamAttemptStore
}

// NewSQLExamAttemptWorkspaceStore constructs the adapter for registration by
// the root SQL Store. It performs no I/O and owns no lifecycle.
func NewSQLExamAttemptWorkspaceStore(sqlStore *SQLStore) store.ExamAttemptWorkspaceStore {
	return &SQLExamAttemptWorkspaceStore{SQLStore: sqlStore, attempts: &SQLExamAttemptStore{SQLStore: sqlStore}}
}

func (s *SQLExamAttemptWorkspaceStore) List(ctx context.Context, options store.CandidateWorkspaceListOptions) (*store.CandidateAttemptWorkspacePage, error) {
	return s.attempts.listCandidateWorkspace(ctx, options)
}

func (s *SQLExamAttemptWorkspaceStore) ResolveFile(ctx context.Context, access store.CandidateAttemptAccess, entryID model.AttemptWorkspaceEntryID) (*store.CandidateWorkspaceContent, error) {
	if !entryID.IsValid() {
		return nil, store.NewErrInvalidInput("attempt_workspace_entry", "id", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve candidate Attempt Workspace file", func(ctx context.Context, tx *sqlxTxWrapper) (*store.CandidateWorkspaceContent, error) {
		if _, err := s.attempts.lockCandidateGuard(ctx, tx, access); err != nil {
			return nil, err
		}
		return resolveCandidateWorkspaceFile(ctx, tx, access, entryID)
	})
}

type attemptWorkspaceMutationTargetRow struct {
	ExamID              string       `db:"exam_id"`
	SittingID           string       `db:"exam_sitting_id"`
	ClassID             string       `db:"class_id"`
	CandidateID         string       `db:"candidate_user_id"`
	WorkspaceID         string       `db:"workspace_id"`
	AdmissionRevisionID string       `db:"admission_revision_id"`
	CurrentRevisionID   string       `db:"current_revision_id"`
	AttemptState        string       `db:"attempt_state"`
	SittingState        string       `db:"sitting_state"`
	ScheduledEndAt      time.Time    `db:"scheduled_end_at"`
	ParticipationState  string       `db:"participation_state"`
	Generation          int64        `db:"generation"`
	CredentialHash      string       `db:"continuity_credential_hash"`
	LeaseExpiresAt      time.Time    `db:"lease_expires_at"`
	ConnectionState     string       `db:"connection_state"`
	SessionArchivedAt   sql.NullTime `db:"session_archived_at"`
	SessionRevokedAt    sql.NullTime `db:"session_revoked_at"`
	SessionIdleExpiry   time.Time    `db:"session_idle_expires_at"`
	SessionExpiry       time.Time    `db:"session_expires_at"`
	UserArchivedAt      sql.NullTime `db:"user_archived_at"`
	UserDisabledAt      sql.NullTime `db:"user_disabled_at"`
	ClassArchived       sql.NullTime `db:"class_archived_at"`
	LevelArchived       sql.NullTime `db:"level_archived_at"`
	ProgrammeArchived   sql.NullTime `db:"programme_archived_at"`
	UnitArchived        sql.NullTime `db:"unit_archived_at"`
	PeriodArchived      sql.NullTime `db:"period_archived_at"`
}

func validAttemptWorkspaceMutationAccess(access store.ExamAttemptWorkspaceMutationAccess) bool {
	return access.AttemptID.IsValid() && access.ParticipationID.IsValid() && access.Generation > 0 &&
		access.CandidateUserID.IsValid() && access.SessionID.IsValid() && access.DesktopRegistrationID.IsValid() &&
		model.IsValidDPoPKeyThumbprint(access.DPoPKeyThumbprint) && access.ConnectionID.IsValid() &&
		model.IsValidTokenHash(access.ContinuityCredentialHash)
}

func lockAttemptWorkspaceMutationTarget(ctx context.Context, tx *sqlxTxWrapper, access store.ExamAttemptWorkspaceMutationAccess) (*store.ExamAttemptWorkspaceMutationTarget, time.Time, error) {
	if !validAttemptWorkspaceMutationAccess(access) {
		return nil, time.Time{}, store.NewErrInvalidInput("attempt_workspace", "access", nil)
	}
	if err := lockAttemptWorkspaceEnrollmentFence(ctx, tx, access); err != nil {
		return nil, time.Time{}, err
	}
	row, err := lockAttemptWorkspaceMutationRow(ctx, tx, access)
	if err != nil {
		return nil, time.Time{}, err
	}
	databaseNow, err := lockAttemptWorkspaceMembership(ctx, tx, access, row.ClassID)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err = checkAttemptWorkspaceMutationAccess(ctx, tx, access, row, databaseNow); err != nil {
		return nil, time.Time{}, err
	}
	if err = lockSecurityInteraction(ctx, tx, access.AttemptID); err != nil {
		return nil, time.Time{}, err
	}
	target, err := attemptWorkspaceTarget(row)
	return target, databaseNow, err
}

// lockAttemptWorkspaceEnrollmentFence resolves the Attempt's Academic Period
// and locks the Candidate and their Class enrollment in that period.
func lockAttemptWorkspaceEnrollmentFence(ctx context.Context, tx *sqlxTxWrapper, access store.ExamAttemptWorkspaceMutationAccess) error {
	var periodID string
	if err := tx.Get(ctx, &periodID, `SELECT cl.academic_period_id FROM exam_attempts a
		JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN classes cl ON cl.id=s.class_id WHERE a.id=? AND a.candidate_user_id=?`,
		access.AttemptID.String(), access.CandidateUserID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.NewErrNotFound("attempt_workspace_access", access.AttemptID.String())
		}
		return fmt.Errorf("resolve Attempt Workspace enrollment fence: %w", err)
	}
	if err := lockCandidateUser(ctx, tx, access.CandidateUserID.String()); err != nil {
		return err
	}
	return lockClassEnrollment(ctx, tx, access.CandidateUserID.String(), periodID)
}

// lockAttemptWorkspaceMutationRow locks the Attempt, its Participation,
// Connection and Workspace, and share-locks the Sitting, Session, Desktop
// registration, User and academic structure behind them.
func lockAttemptWorkspaceMutationRow(ctx context.Context, tx *sqlxTxWrapper, access store.ExamAttemptWorkspaceMutationAccess) (attemptWorkspaceMutationTargetRow, error) {
	var row attemptWorkspaceMutationTargetRow
	err := tx.Get(ctx, &row, `SELECT a.exam_id,a.exam_sitting_id,s.class_id,a.candidate_user_id,w.id AS workspace_id,
		a.admission_revision_id,s.exam_revision_id AS current_revision_id,
		a.state AS attempt_state,s.state AS sitting_state,s.scheduled_end_at,
		p.state AS participation_state,p.generation,p.continuity_credential_hash,p.lease_expires_at,
		c.state AS connection_state,se.archived_at AS session_archived_at,se.revoked_at AS session_revoked_at,
		se.idle_expires_at AS session_idle_expires_at,se.expires_at AS session_expires_at,
		u.archived_at AS user_archived_at,u.disabled_at AS user_disabled_at,
		cl.archived_at AS class_archived_at,pl.archived_at AS level_archived_at,pr.archived_at AS programme_archived_at,
		au.archived_at AS unit_archived_at,ap.archived_at AS period_archived_at
		FROM exam_attempts a
		JOIN exam_attempt_workspaces w ON w.exam_attempt_id=a.id
		JOIN exam_sittings s ON s.id=a.exam_sitting_id AND s.exam_id=a.exam_id
		JOIN exam_attempt_participations p ON p.id=? AND p.exam_attempt_id=a.id
		JOIN exam_attempt_connections c ON c.id=? AND c.exam_attempt_id=a.id AND c.participation_id=p.id
		JOIN sessions se ON se.id=? AND se.user_id=a.candidate_user_id AND c.session_id=se.id
			AND se.desktop_registration_id=? AND se.dpop_key_thumbprint=?
		JOIN desktop_registrations dr ON dr.id=se.desktop_registration_id AND dr.user_id=se.user_id
			AND dr.revoked_at IS NULL AND dr.key_thumbprint=?
		JOIN users u ON u.id=a.candidate_user_id
		JOIN classes cl ON cl.id=s.class_id
		JOIN programme_levels pl ON pl.id=cl.programme_level_id
		JOIN programmes pr ON pr.id=pl.programme_id
		JOIN academic_units au ON au.id=pr.academic_unit_id
		JOIN academic_periods ap ON ap.id=cl.academic_period_id AND ap.institution_id=au.institution_id
		WHERE a.id=? AND a.candidate_user_id=?
		FOR UPDATE OF a,p,c,w FOR SHARE OF s,se,dr,u,cl,pl,pr,au,ap`,
		access.ParticipationID.String(), access.ConnectionID.String(), access.SessionID.String(),
		access.DesktopRegistrationID.String(), access.DPoPKeyThumbprint, access.DPoPKeyThumbprint,
		access.AttemptID.String(), access.CandidateUserID.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return row, store.NewErrNotFound("attempt_workspace_access", access.AttemptID.String())
		}
		return row, fmt.Errorf("lock Attempt Workspace mutation access: %w", err)
	}
	return row, nil
}

// lockAttemptWorkspaceMembership share-locks the Candidate's Class membership
// history, reads the decision instant, and requires exactly one current
// membership at that instant.
func lockAttemptWorkspaceMembership(ctx context.Context, tx *sqlxTxWrapper, access store.ExamAttemptWorkspaceMutationAccess, classID string) (time.Time, error) {
	var memberships []struct {
		StartAt    time.Time    `db:"start_at"`
		EndAt      sql.NullTime `db:"end_at"`
		ArchivedAt sql.NullTime `db:"archived_at"`
	}
	if err := tx.Select(ctx, &memberships, `SELECT start_at,end_at,archived_at FROM class_members
		WHERE class_id=? AND user_id=? ORDER BY start_at,id FOR SHARE`, classID, access.CandidateUserID.String()); err != nil {
		return time.Time{}, fmt.Errorf("lock Attempt Workspace membership history: %w", err)
	}
	var databaseNow time.Time
	if err := tx.Get(ctx, &databaseNow, `SELECT statement_timestamp()`); err != nil {
		return time.Time{}, fmt.Errorf("read Attempt Workspace decision time: %w", err)
	}
	databaseNow = model.TimeUTC(databaseNow)
	currentMemberships := 0
	for _, membership := range memberships {
		if !membership.ArchivedAt.Valid && !databaseNow.Before(membership.StartAt) &&
			(!membership.EndAt.Valid || databaseNow.Before(membership.EndAt.Time)) {
			currentMemberships++
		}
	}
	if currentMemberships != 1 {
		return time.Time{}, store.NewErrNotFound("attempt_workspace_access", access.AttemptID.String())
	}
	return databaseNow, nil
}

// checkAttemptWorkspaceMutationAccess requires the current Participation
// generation and credential, live Attempt and Sitting states, no pending
// Workspace correction acknowledgement, and an active Session, User and
// academic structure.
func checkAttemptWorkspaceMutationAccess(ctx context.Context, tx *sqlxTxWrapper, access store.ExamAttemptWorkspaceMutationAccess, row attemptWorkspaceMutationTargetRow, databaseNow time.Time) error {
	if row.Generation != access.Generation {
		return store.NewErrConflict("attempt_participation", "attempt_participation_generation", nil)
	}
	if subtle.ConstantTimeCompare([]byte(row.CredentialHash), []byte(access.ContinuityCredentialHash)) != 1 ||
		row.ConnectionState != string(model.AttemptConnectionOpen) {
		return store.NewErrConflict("attempt_participation", "attempt_participation_credential", nil)
	}
	if row.ParticipationState != string(model.AttemptParticipationActive) || !databaseNow.Before(row.LeaseExpiresAt) {
		return store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	}
	if row.SittingState != string(model.ExamSittingOpen) || !databaseNow.Before(row.ScheduledEndAt) {
		return store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	}
	if row.AttemptState != string(model.ExamAttemptActive) {
		return store.NewErrConflict("exam_attempt", "exam_attempt_state", nil)
	}
	pendingAcknowledgement, err := hasPendingCandidateCorrectionCapability(ctx, tx, access.AttemptID.String(),
		row.SittingID, row.AdmissionRevisionID, row.CurrentRevisionID, model.CandidateCapabilityWorkspace)
	if err != nil {
		return err
	}
	if pendingAcknowledgement {
		return store.NewErrConflict("attempt_workspace", "exam_correction_acknowledgement_required", nil)
	}
	if row.SessionArchivedAt.Valid || row.SessionRevokedAt.Valid || !databaseNow.Before(row.SessionIdleExpiry) ||
		!databaseNow.Before(row.SessionExpiry) || row.UserArchivedAt.Valid || row.UserDisabledAt.Valid ||
		row.ClassArchived.Valid || row.LevelArchived.Valid || row.ProgrammeArchived.Valid || row.UnitArchived.Valid || row.PeriodArchived.Valid {
		return store.NewErrNotFound("attempt_workspace_access", access.AttemptID.String())
	}
	return nil
}

func attemptWorkspaceTarget(row attemptWorkspaceMutationTargetRow) (*store.ExamAttemptWorkspaceMutationTarget, error) {
	examID, err := model.ParseExamID(row.ExamID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "exam_sitting_id", err)
	}
	classID, err := model.ParseClassID(row.ClassID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting", "class_id", err)
	}
	candidateID, err := model.ParseUserID(row.CandidateID)
	if err != nil {
		return nil, invalidPersistedState("exam_attempt", "candidate_user_id", err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(row.WorkspaceID)
	if err != nil {
		return nil, invalidPersistedState("attempt_workspace", "id", err)
	}
	return &store.ExamAttemptWorkspaceMutationTarget{ExamID: examID, SittingID: sittingID, ClassID: classID,
		CandidateUserID: candidateID, WorkspaceID: workspaceID}, nil
}

func (s *SQLExamAttemptWorkspaceStore) ResolveMutationTarget(ctx context.Context, access store.ExamAttemptWorkspaceMutationAccess) (*store.ExamAttemptWorkspaceMutationTarget, error) {
	return runSQLTransaction(ctx, s.GetMaster().Begin, "resolve Attempt Workspace mutation target", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamAttemptWorkspaceMutationTarget, error) {
		target, _, err := lockAttemptWorkspaceMutationTarget(ctx, tx, access)
		return target, err
	})
}

type attemptWorkspaceObjectRow struct {
	ID              string         `db:"id"`
	WorkspaceID     string         `db:"workspace_id"`
	StorageOrigin   string         `db:"storage_origin"`
	StarterObjectID sql.NullString `db:"starter_object_id"`
	State           string         `db:"state"`
	ContentVersion  sql.NullString `db:"content_version"`
	MediaType       sql.NullString `db:"media_type"`
	SizeBytes       sql.NullInt64  `db:"size_bytes"`
	SHA256          sql.NullString `db:"sha256"`
	CreatedAt       time.Time      `db:"created_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
	ExpiresAt       sql.NullTime   `db:"expires_at"`
	ReclaimAfter    sql.NullTime   `db:"reclaim_after"`
	ClaimToken      sql.NullString `db:"claim_token"`
	ClaimedAt       sql.NullTime   `db:"claimed_at"`
}

const attemptWorkspaceObjectSelect = `SELECT id,workspace_id,storage_origin,starter_object_id,state,content_version,
	media_type,size_bytes,sha256,created_at,updated_at,expires_at,reclaim_after,claim_token,claimed_at
	FROM exam_attempt_workspace_objects`

func (row attemptWorkspaceObjectRow) model() (*model.AttemptWorkspaceObject, error) {
	id, err := model.ParseAttemptWorkspaceObjectID(row.ID)
	if err != nil {
		return nil, invalidPersistedState("attempt_workspace_object", "id", err)
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(row.WorkspaceID)
	if err != nil {
		return nil, invalidPersistedState("attempt_workspace_object", "workspace_id", err)
	}
	var starterID model.StarterWorkspaceObjectID
	if row.StarterObjectID.Valid {
		starterID, err = model.ParseStarterWorkspaceObjectID(row.StarterObjectID.String)
		if err != nil {
			return nil, invalidPersistedState("attempt_workspace_object", "starter_object_id", err)
		}
	}
	var version model.WorkspaceContentVersion
	if row.ContentVersion.Valid {
		version, err = model.ParseWorkspaceContentVersion(row.ContentVersion.String)
		if err != nil {
			return nil, invalidPersistedState("attempt_workspace_object", "content_version", err)
		}
	}
	object := &model.AttemptWorkspaceObject{ID: id, WorkspaceID: workspaceID,
		StorageOrigin: model.AttemptWorkspaceObjectStorage(row.StorageOrigin), StarterObjectID: starterID,
		State: model.AttemptWorkspaceObjectState(row.State), ContentVersion: version, MediaType: row.MediaType.String,
		SizeBytes: row.SizeBytes.Int64, SHA256: row.SHA256.String, CreatedAt: model.TimeUTC(row.CreatedAt),
		UpdatedAt: model.TimeUTC(row.UpdatedAt), ExpiresAt: nullableTime(row.ExpiresAt),
		ReclaimAfter: OptionalTimeFromNullTime(row.ReclaimAfter), ClaimToken: row.ClaimToken.String,
		ClaimedAt: OptionalTimeFromNullTime(row.ClaimedAt)}
	if err = object.Validate(); err != nil {
		return nil, invalidPersistedState("attempt_workspace_object", "value", err)
	}
	return object, nil
}

func nullableTime(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return model.TimeUTC(value.Time)
}

func (s *SQLExamAttemptWorkspaceStore) ReserveObject(ctx context.Context, input *store.ExamAttemptWorkspaceObjectReservation) (*model.AttemptWorkspaceObject, error) {
	if input == nil || !validAttemptWorkspaceMutationAccess(input.Access) || !input.ObjectID.IsValid() {
		return nil, store.NewErrInvalidInput("attempt_workspace_object", "reservation", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "reserve Attempt Workspace object", func(ctx context.Context, tx *sqlxTxWrapper) (*model.AttemptWorkspaceObject, error) {
		target, databaseNow, err := lockAttemptWorkspaceMutationTarget(ctx, tx, input.Access)
		if err != nil {
			return nil, err
		}
		var row attemptWorkspaceObjectRow
		err = tx.Get(ctx, &row, attemptWorkspaceObjectSelect+` WHERE id=? FOR UPDATE`, input.ObjectID.String())
		if err == nil {
			object, decodeErr := row.model()
			if decodeErr != nil {
				return nil, decodeErr
			}
			if object.WorkspaceID != target.WorkspaceID || object.StorageOrigin != model.AttemptWorkspaceStorageAttempt ||
				object.State != model.AttemptWorkspaceObjectStaged || !databaseNow.Before(object.ExpiresAt) {
				return nil, store.NewErrConflict("attempt_workspace_object", "attempt_workspace_object_state", nil)
			}
			return object, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("find Attempt Workspace object reservation: %w", err)
		}
		object, err := model.NewStagedAttemptWorkspaceObject(input.ObjectID, target.WorkspaceID, databaseNow,
			databaseNow.Add(model.AttemptWorkspaceStageLifetime))
		if err != nil {
			return nil, store.NewErrInvalidInput("attempt_workspace_object", "reservation", nil).Wrap(err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_objects
			(id,workspace_id,admission_revision_id,source_starter_entry_id,storage_origin,starter_object_id,state,
			 content_version,media_type,size_bytes,sha256,created_at,updated_at,expires_at,reclaim_after,claim_token,claimed_at)
			SELECT ?,w.id,w.admission_revision_id,NULL,'attempt',NULL,'staged',NULL,NULL,NULL,NULL,?,?,?,NULL,NULL,NULL
			FROM exam_attempt_workspaces w WHERE w.id=?`, object.ID.String(), object.CreatedAt, object.UpdatedAt,
			object.ExpiresAt, target.WorkspaceID.String())
		if err != nil {
			return nil, fmt.Errorf("reserve Attempt Workspace object: %w", translateError("attempt_workspace_object", object.ID.String(), err))
		}
		return object, nil
	})
}

func (s *SQLExamAttemptWorkspaceStore) MarkObjectReady(ctx context.Context, input *store.ExamAttemptWorkspaceObjectReady) (*model.AttemptWorkspaceObject, error) {
	if input == nil || !validAttemptWorkspaceMutationAccess(input.Access) || !input.ObjectID.IsValid() ||
		!input.ContentVersion.IsValid() || input.Content.Validate() != nil {
		return nil, store.NewErrInvalidInput("attempt_workspace_object", "ready", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "mark Attempt Workspace object ready", func(ctx context.Context, tx *sqlxTxWrapper) (*model.AttemptWorkspaceObject, error) {
		target, databaseNow, err := lockAttemptWorkspaceMutationTarget(ctx, tx, input.Access)
		if err != nil {
			return nil, err
		}
		capacity, err := attemptWorkspaceCapacityPolicy(ctx, tx, target.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if input.Content.SizeBytes > capacity.WorkspaceMaximumFileBytes {
			return nil, store.NewErrConflict("attempt_workspace", "attempt_workspace_size_limit", nil)
		}
		var row attemptWorkspaceObjectRow
		if err = tx.Get(ctx, &row, attemptWorkspaceObjectSelect+` WHERE id=? AND workspace_id=? FOR UPDATE`,
			input.ObjectID.String(), target.WorkspaceID.String()); err != nil {
			return nil, translateError("attempt_workspace_object", input.ObjectID.String(), err)
		}
		object, err := row.model()
		if err != nil {
			return nil, err
		}
		if object.State != model.AttemptWorkspaceObjectStaged || !databaseNow.Before(object.ExpiresAt) {
			return nil, store.NewErrConflict("attempt_workspace_object", "attempt_workspace_object_state", nil)
		}
		if object.HasContent() {
			if object.ContentVersion != input.ContentVersion || object.MediaType != input.Content.MediaType ||
				object.SizeBytes != input.Content.SizeBytes || subtle.ConstantTimeCompare([]byte(object.SHA256), []byte(strings.ToLower(input.Content.SHA256))) != 1 {
				return nil, store.NewErrConflict("attempt_workspace_object", "attempt_workspace_object_state", nil)
			}
			return object, nil
		}
		if err = object.MarkContentReady(input.ContentVersion, input.Content.MediaType, input.Content.SizeBytes, input.Content.SHA256, databaseNow); err != nil {
			return nil, store.NewErrInvalidInput("attempt_workspace_object", "content", nil).Wrap(err)
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_attempt_workspace_objects
			SET content_version=?,media_type=?,size_bytes=?,sha256=?,updated_at=? WHERE id=? AND state='staged' AND content_version IS NULL`,
			object.ContentVersion.String(), object.MediaType, object.SizeBytes, object.SHA256, object.UpdatedAt, object.ID.String()); err != nil {
			return nil, fmt.Errorf("mark Attempt Workspace object ready: %w", err)
		}
		return object, nil
	})
}

func (s *SQLExamAttemptWorkspaceStore) ListJournal(ctx context.Context, options store.CandidateWorkspaceJournalOptions) (*store.CandidateWorkspaceJournalPage, error) {
	if options.AfterCursor < 0 || options.Limit < 1 || options.Limit > model.AttemptWorkspaceJournalReadMaximum {
		return nil, store.NewErrInvalidInput("attempt_workspace", "journal_options", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "list candidate Attempt Workspace journal", func(ctx context.Context, tx *sqlxTxWrapper) (*store.CandidateWorkspaceJournalPage, error) {
		if _, err := s.attempts.lockCandidateGuard(ctx, tx, options.Access); err != nil {
			return nil, err
		}
		return listAttemptWorkspaceJournalSnapshot(ctx, tx, options)
	})
}

func listAttemptWorkspaceJournalSnapshot(ctx context.Context, tx *sqlxTxWrapper, options store.CandidateWorkspaceJournalOptions) (*store.CandidateWorkspaceJournalPage, error) {
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
	page := &store.CandidateWorkspaceJournalPage{WorkspaceID: workspaceID, CurrentCursor: workspace.Cursor,
		Entries: make([]model.AttemptWorkspaceJournalEntry, 0)}
	if options.AfterCursor > workspace.Cursor {
		page.RefreshRequired = true
		return page, nil
	}
	var first sql.NullInt64
	if err = tx.Get(ctx, &first, `SELECT MIN(cursor) FROM exam_attempt_workspace_journal WHERE workspace_id=?`, workspace.ID); err != nil {
		return nil, fmt.Errorf("read Attempt Workspace journal window: %w", err)
	}
	if first.Valid && options.AfterCursor < first.Int64-1 {
		page.RefreshRequired = true
		return page, nil
	}
	var rows []struct {
		Cursor         int64          `db:"cursor"`
		EntryID        string         `db:"entry_id"`
		EntryKind      string         `db:"entry_kind"`
		Operation      string         `db:"operation"`
		OldPath        sql.NullString `db:"old_path"`
		NewPath        sql.NullString `db:"new_path"`
		ContentVersion sql.NullString `db:"content_version"`
		ChangedAt      time.Time      `db:"changed_at"`
		Recursive      bool           `db:"recursive"`
	}
	if err = tx.Select(ctx, &rows, `SELECT cursor,entry_id,entry_kind,operation,old_path,new_path,content_version,changed_at,recursive
		FROM exam_attempt_workspace_journal WHERE workspace_id=? AND cursor>? AND cursor<=? ORDER BY cursor LIMIT ?`,
		workspace.ID, options.AfterCursor, workspace.Cursor, options.Limit+1); err != nil {
		return nil, fmt.Errorf("list Attempt Workspace journal: %w", err)
	}
	page.HasMore = len(rows) > options.Limit
	if page.HasMore {
		rows = rows[:options.Limit]
	}
	for _, row := range rows {
		entryID, parseErr := model.ParseAttemptWorkspaceEntryID(row.EntryID)
		if parseErr != nil {
			return nil, invalidPersistedState("attempt_workspace_journal", "entry_id", parseErr)
		}
		var version model.WorkspaceContentVersion
		if row.ContentVersion.Valid {
			version, parseErr = model.ParseWorkspaceContentVersion(row.ContentVersion.String)
			if parseErr != nil {
				return nil, invalidPersistedState("attempt_workspace_journal", "content_version", parseErr)
			}
		}
		entry := model.AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: row.Cursor, EntryID: entryID,
			EntryKind: model.StarterWorkspaceEntryKind(row.EntryKind), Operation: model.AttemptWorkspaceMutationKind(row.Operation),
			OldPath: row.OldPath.String, NewPath: row.NewPath.String, ContentVersion: version, ChangedAt: model.TimeUTC(row.ChangedAt), Recursive: row.Recursive}
		if parseErr = entry.Validate(); parseErr != nil {
			return nil, invalidPersistedState("attempt_workspace_journal", "value", parseErr)
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}

// attemptWorkspaceJournalOutcome is the persisted spelling of the
// acknowledged journal change inside a mutation outcome. Field names
// are the JSON keys; Recursive is omitted when false.
type attemptWorkspaceJournalOutcome struct {
	WorkspaceID    model.ExamAttemptWorkspaceID       `json:"workspace_id"`
	Cursor         int64                              `json:"cursor"`
	EntryID        model.AttemptWorkspaceEntryID      `json:"entry_id"`
	EntryKind      model.StarterWorkspaceEntryKind    `json:"entry_kind"`
	Operation      model.AttemptWorkspaceMutationKind `json:"operation"`
	OldPath        string                             `json:"old_path"`
	NewPath        string                             `json:"new_path"`
	ContentVersion model.WorkspaceContentVersion      `json:"content_version"`
	ChangedAt      time.Time                          `json:"changed_at"`
	Recursive      bool                               `json:"recursive,omitempty"`
}

func attemptWorkspaceJournalOutcomeFrom(entry model.AttemptWorkspaceJournalEntry) attemptWorkspaceJournalOutcome {
	return attemptWorkspaceJournalOutcome(entry)
}

func (o attemptWorkspaceJournalOutcome) entry() model.AttemptWorkspaceJournalEntry {
	return model.AttemptWorkspaceJournalEntry(o)
}

type attemptWorkspaceMutationOutcome struct {
	SittingID          string                         `json:"s"`
	ClassID            string                         `json:"c"`
	CandidateID        string                         `json:"u"`
	WorkspaceID        string                         `json:"w"`
	Entry              *attemptWorkspaceItemDocument  `json:"e,omitempty"`
	Change             attemptWorkspaceJournalOutcome `json:"j"`
	ProtectedObjectIDs []string                       `json:"o,omitempty"`
}

func (attemptWorkspaceMutationOutcome) outcomeDocument() {}

// attemptWorkspaceItemDocument spells store.CandidateAttemptWorkspaceItem
// inside a persisted outcome; the two convert directly.
type attemptWorkspaceItemDocument struct {
	EntryID        model.AttemptWorkspaceEntryID   `json:"entry_id"`
	Kind           model.StarterWorkspaceEntryKind `json:"kind"`
	Path           string                          `json:"path"`
	ContentVersion model.WorkspaceContentVersion   `json:"content_version"`
	MediaType      string                          `json:"media_type"`
	SizeBytes      int64                           `json:"size_bytes"`
	SHA256         string                          `json:"sha256"`
}

func (s *SQLExamAttemptWorkspaceStore) ApplyMutation(ctx context.Context, input *store.ExamAttemptWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamAttemptWorkspaceMutationResult, error) {
	prepared, err := prepareAttemptWorkspaceMutation(input, command)
	if err != nil {
		return nil, err
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "apply Attempt Workspace mutation", idempotentMutation[attemptWorkspaceMutationOutcome]{
		command: command, auditEventID: prepared.AuditEventID,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (attemptWorkspaceMutationOutcome, error) {
			return applyAttemptWorkspaceMutation(ctx, tx, prepared, command)
		},
		encode: func(outcome attemptWorkspaceMutationOutcome) ([]byte, error) {
			return encodeOutcome(outcome)
		},
		decode: func(encoded []byte) (attemptWorkspaceMutationOutcome, error) {
			var outcome attemptWorkspaceMutationOutcome
			if decodeErr := decodeOutcome(encoded, &outcome); decodeErr != nil {
				return outcome, decodeErr
			}
			if err := validateAttemptWorkspaceMutationOutcome(outcome); err != nil {
				return outcome, err
			}
			return outcome, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, outcome attemptWorkspaceMutationOutcome, originalAuditID string) error {
			target, _, lockErr := lockAttemptWorkspaceMutationTarget(ctx, tx, prepared.Access)
			if lockErr != nil {
				return lockErr
			}
			if target.SittingID.String() != outcome.SittingID || target.ClassID.String() != outcome.ClassID ||
				target.CandidateUserID.String() != outcome.CandidateID || target.WorkspaceID.String() != outcome.WorkspaceID {
				return store.NewErrNotFound("attempt_workspace_access", prepared.Access.AttemptID.String())
			}
			return completeAttemptWorkspaceMutationAudit(ctx, tx, outcome, prepared.AuditEventID, prepared.AuditAt, true, originalAuditID)
		},
	})
	if err != nil {
		return nil, err
	}
	response, err := attemptWorkspaceMutationResult(result.Value)
	if err != nil {
		return nil, err
	}
	response.Replayed = result.Replayed
	return response, nil
}

func prepareAttemptWorkspaceMutation(input *store.ExamAttemptWorkspaceMutation, command *store.CommandIdempotency) (*store.ExamAttemptWorkspaceMutation, error) {
	if input == nil || command == nil || command.Operation != store.ExamAttemptWorkspaceMutationOperation ||
		command.UserID != input.Access.CandidateUserID || !validAttemptWorkspaceMutationAccess(input.Access) ||
		!input.EntryID.IsValid() || !input.AuditEventID.IsValid() || input.AuditAt.IsZero() {
		return nil, store.NewErrInvalidInput("attempt_workspace", "mutation", nil)
	}
	prepared := *input
	if (prepared.Recursive && (prepared.Operation != model.AttemptWorkspaceMutationDeleteEntry || prepared.ExpectedWorkspaceCursor == nil ||
		*prepared.ExpectedWorkspaceCursor < 0 || !prepared.ExpectedContentVersion.IsZero())) ||
		(!prepared.Recursive && prepared.ExpectedWorkspaceCursor != nil) {
		return nil, store.NewErrInvalidInput("attempt_workspace", "recursive_delete", nil)
	}
	if prepared.ExpectedWorkspaceCursor != nil {
		cursor := *prepared.ExpectedWorkspaceCursor
		prepared.ExpectedWorkspaceCursor = &cursor
	}
	normalize := func(value string) (string, bool) {
		path, normalizeErr := model.NormalizeAttemptWorkspacePath(value)
		return path, normalizeErr == nil && path == value
	}
	switch prepared.Operation {
	case model.AttemptWorkspaceMutationCreateFile:
		path, valid := normalize(prepared.DestinationPath)
		if !valid || prepared.ExpectedPath != "" || !prepared.ExpectedContentVersion.IsZero() || !prepared.ObjectID.IsValid() {
			return nil, store.NewErrInvalidInput("attempt_workspace", "create_file", nil)
		}
		prepared.DestinationPath = path
	case model.AttemptWorkspaceMutationCreateDirectory:
		path, valid := normalize(prepared.DestinationPath)
		if !valid || prepared.ExpectedPath != "" || !prepared.ExpectedContentVersion.IsZero() || !prepared.ObjectID.IsZero() {
			return nil, store.NewErrInvalidInput("attempt_workspace", "create_directory", nil)
		}
		prepared.DestinationPath = path
	case model.AttemptWorkspaceMutationReplaceFile:
		path, valid := normalize(prepared.ExpectedPath)
		if !valid || prepared.DestinationPath != "" || !prepared.ExpectedContentVersion.IsValid() || !prepared.ObjectID.IsValid() {
			return nil, store.NewErrInvalidInput("attempt_workspace", "replace_file", nil)
		}
		prepared.ExpectedPath = path
	case model.AttemptWorkspaceMutationMoveEntry:
		oldPath, oldValid := normalize(prepared.ExpectedPath)
		newPath, newValid := normalize(prepared.DestinationPath)
		if !oldValid || !newValid || oldPath == newPath || !prepared.ExpectedContentVersion.IsZero() || !prepared.ObjectID.IsZero() {
			return nil, store.NewErrInvalidInput("attempt_workspace", "move_entry", nil)
		}
		prepared.ExpectedPath, prepared.DestinationPath = oldPath, newPath
	case model.AttemptWorkspaceMutationDeleteEntry:
		path, valid := normalize(prepared.ExpectedPath)
		if !valid || prepared.DestinationPath != "" || !prepared.ObjectID.IsZero() ||
			(!prepared.ExpectedContentVersion.IsZero() && !prepared.ExpectedContentVersion.IsValid()) {
			return nil, store.NewErrInvalidInput("attempt_workspace", "delete_entry", nil)
		}
		prepared.ExpectedPath = path
	default:
		return nil, store.NewErrInvalidInput("attempt_workspace", "operation", nil)
	}
	return &prepared, nil
}

type attemptWorkspaceEntryMutationRow struct {
	ID                   string         `db:"id"`
	WorkspaceID          string         `db:"workspace_id"`
	AdmissionRevisionID  string         `db:"admission_revision_id"`
	SourceStarterEntryID sql.NullString `db:"source_starter_entry_id"`
	Kind                 string         `db:"kind"`
	Path                 string         `db:"path"`
	CurrentObjectID      sql.NullString `db:"current_object_id"`
	CreatedAt            time.Time      `db:"created_at"`
	UpdatedAt            time.Time      `db:"updated_at"`
	ContentVersion       sql.NullString `db:"content_version"`
	MediaType            sql.NullString `db:"media_type"`
	SizeBytes            sql.NullInt64  `db:"size_bytes"`
	SHA256               sql.NullString `db:"sha256"`
	ObjectOrigin         sql.NullString `db:"object_origin"`
}

const attemptWorkspaceEntryMutationSelect = `SELECT e.id,e.workspace_id,e.admission_revision_id,e.source_starter_entry_id,
	e.kind,e.path,e.current_object_id,e.created_at,e.updated_at,o.content_version,o.media_type,o.size_bytes,o.sha256,
	o.storage_origin AS object_origin FROM exam_attempt_workspace_entries e
	LEFT JOIN exam_attempt_workspace_objects o ON o.id=e.current_object_id AND o.workspace_id=e.workspace_id`

// attemptWorkspaceMutationEffect carries what one Workspace operation changed
// into its journal entry and outcome.
type attemptWorkspaceMutationEffect struct {
	entry     *store.CandidateAttemptWorkspaceItem
	oldPath   string
	newPath   string
	entryKind model.StarterWorkspaceEntryKind
	version   model.WorkspaceContentVersion
	protected []string
}

// attemptWorkspaceMutationScope is the locked Workspace one operation acts on.
type attemptWorkspaceMutationScope struct {
	workspaceID model.ExamAttemptWorkspaceID
	revisionID  model.ExamRevisionID
	databaseNow time.Time
}

func applyAttemptWorkspaceMutation(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptWorkspaceMutation, command *store.CommandIdempotency) (attemptWorkspaceMutationOutcome, error) {
	var zero attemptWorkspaceMutationOutcome
	target, databaseNow, err := lockAttemptWorkspaceMutationTarget(ctx, tx, input.Access)
	if err != nil {
		return zero, err
	}
	var workspace struct {
		AdmissionRevisionID string `db:"admission_revision_id"`
		Cursor              int64  `db:"cursor"`
	}
	if err = tx.Get(ctx, &workspace, `SELECT admission_revision_id,cursor FROM exam_attempt_workspaces WHERE id=? FOR UPDATE`, target.WorkspaceID.String()); err != nil {
		return zero, fmt.Errorf("lock Attempt Workspace: %w", err)
	}
	if input.Recursive && workspace.Cursor != *input.ExpectedWorkspaceCursor {
		return zero, store.NewErrConflict("attempt_workspace", "attempt_workspace_cursor", nil)
	}
	revisionID, err := model.ParseExamRevisionID(workspace.AdmissionRevisionID)
	if err != nil {
		return zero, invalidPersistedState("attempt_workspace", "admission_revision_id", err)
	}
	scope := attemptWorkspaceMutationScope{workspaceID: target.WorkspaceID, revisionID: revisionID, databaseNow: databaseNow}
	effect := attemptWorkspaceMutationEffect{protected: make([]string, 0, 2)}
	switch input.Operation {
	case model.AttemptWorkspaceMutationCreateFile:
		err = createAttemptWorkspaceFile(ctx, tx, input, scope, &effect)
	case model.AttemptWorkspaceMutationCreateDirectory:
		err = createAttemptWorkspaceDirectory(ctx, tx, input, scope, &effect)
	case model.AttemptWorkspaceMutationReplaceFile:
		err = replaceAttemptWorkspaceFile(ctx, tx, input, scope, &effect)
	case model.AttemptWorkspaceMutationMoveEntry:
		err = moveAttemptWorkspaceMutationEntry(ctx, tx, input, scope, &effect)
	case model.AttemptWorkspaceMutationDeleteEntry:
		err = deleteAttemptWorkspaceMutationEntry(ctx, tx, input, scope, &effect)
	}
	if err != nil {
		return zero, err
	}
	change, err := appendAttemptWorkspaceJournal(ctx, tx, target.WorkspaceID, input.EntryID, effect.entryKind, input.Operation,
		effect.oldPath, effect.newPath, effect.version, input.Recursive, command.KeyDigest[:], databaseNow)
	if err != nil {
		return zero, err
	}
	outcome := attemptWorkspaceMutationOutcome{SittingID: target.SittingID.String(), ClassID: target.ClassID.String(),
		CandidateID: target.CandidateUserID.String(), WorkspaceID: target.WorkspaceID.String(), Entry: (*attemptWorkspaceItemDocument)(effect.entry),
		Change: attemptWorkspaceJournalOutcomeFrom(*change), ProtectedObjectIDs: effect.protected}
	if err = completeAttemptWorkspaceMutationAudit(ctx, tx, outcome, input.AuditEventID, input.AuditAt, false, ""); err != nil {
		return zero, err
	}
	return outcome, nil
}

// createAttemptWorkspaceFile inserts a file entry backed by a newly consumed
// ready object.
func createAttemptWorkspaceFile(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptWorkspaceMutation, scope attemptWorkspaceMutationScope, effect *attemptWorkspaceMutationEffect) error {
	if err := ensureAttemptWorkspaceParent(ctx, tx, scope.workspaceID, input.DestinationPath); err != nil {
		return err
	}
	if err := ensureAttemptWorkspaceCapacity(ctx, tx, scope.workspaceID, 1, 0); err != nil {
		return err
	}
	object, err := consumeAttemptWorkspaceObject(ctx, tx, scope.workspaceID, input.ObjectID, scope.databaseNow)
	if err != nil {
		return err
	}
	if err = ensureAttemptWorkspaceCapacity(ctx, tx, scope.workspaceID, 0, object.SizeBytes); err != nil {
		return err
	}
	if _, err = model.NewCandidateAttemptWorkspaceFile(input.EntryID, scope.workspaceID, scope.revisionID,
		input.DestinationPath, object.ID, scope.databaseNow); err != nil {
		return store.NewErrInvalidInput("attempt_workspace_entry", "value", nil).Wrap(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_entries
		(id,workspace_id,admission_revision_id,source_starter_entry_id,kind,path,current_object_id,created_at,updated_at)
		VALUES (?,?,?,NULL,'file',?,?,?,?)`, input.EntryID.String(), scope.workspaceID.String(), scope.revisionID.String(),
		input.DestinationPath, object.ID.String(), scope.databaseNow, scope.databaseNow); err != nil {
		return translateAttemptWorkspaceMutationError("create Attempt Workspace file", err)
	}
	effect.entry = candidateAttemptWorkspaceItem(input.EntryID, model.StarterWorkspaceEntryFile, input.DestinationPath, object)
	effect.entryKind, effect.newPath, effect.version = model.StarterWorkspaceEntryFile, input.DestinationPath, object.ContentVersion
	effect.protected = append(effect.protected, object.ID.String())
	return nil
}

// createAttemptWorkspaceDirectory inserts an empty directory entry.
func createAttemptWorkspaceDirectory(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptWorkspaceMutation, scope attemptWorkspaceMutationScope, effect *attemptWorkspaceMutationEffect) error {
	if err := ensureAttemptWorkspaceParent(ctx, tx, scope.workspaceID, input.DestinationPath); err != nil {
		return err
	}
	if err := ensureAttemptWorkspaceCapacity(ctx, tx, scope.workspaceID, 1, 0); err != nil {
		return err
	}
	if _, err := model.NewCandidateAttemptWorkspaceDirectory(input.EntryID, scope.workspaceID, scope.revisionID, input.DestinationPath, scope.databaseNow); err != nil {
		return store.NewErrInvalidInput("attempt_workspace_entry", "value", nil).Wrap(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_entries
		(id,workspace_id,admission_revision_id,source_starter_entry_id,kind,path,current_object_id,created_at,updated_at)
		VALUES (?,?,?,NULL,'directory',?,NULL,?,?)`, input.EntryID.String(), scope.workspaceID.String(), scope.revisionID.String(),
		input.DestinationPath, scope.databaseNow, scope.databaseNow); err != nil {
		return translateAttemptWorkspaceMutationError("create Attempt Workspace directory", err)
	}
	effect.entry = &store.CandidateAttemptWorkspaceItem{EntryID: input.EntryID, Kind: model.StarterWorkspaceEntryDirectory, Path: input.DestinationPath}
	effect.entryKind, effect.newPath = model.StarterWorkspaceEntryDirectory, input.DestinationPath
	return nil
}

// replaceAttemptWorkspaceFile points a file entry at the expected version's
// replacement object and retires the Attempt-owned object it replaced.
func replaceAttemptWorkspaceFile(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptWorkspaceMutation, scope attemptWorkspaceMutationScope, effect *attemptWorkspaceMutationEffect) error {
	current, err := loadAttemptWorkspaceEntryForMutation(ctx, tx, scope.workspaceID, input.EntryID)
	if err != nil {
		return err
	}
	currentVersion, versionErr := current.workspaceContentVersion()
	if current.Kind != string(model.StarterWorkspaceEntryFile) || versionErr != nil || current.Path != input.ExpectedPath ||
		currentVersion != input.ExpectedContentVersion {
		return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_content_version", versionErr)
	}
	object, err := consumeAttemptWorkspaceObject(ctx, tx, scope.workspaceID, input.ObjectID, scope.databaseNow)
	if err != nil {
		return err
	}
	if err = ensureAttemptWorkspaceCapacity(ctx, tx, scope.workspaceID, 0, object.SizeBytes-current.SizeBytes.Int64); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_attempt_workspace_entries SET current_object_id=?,updated_at=? WHERE workspace_id=? AND id=?`,
		object.ID.String(), scope.databaseNow, scope.workspaceID.String(), input.EntryID.String()); err != nil {
		return translateAttemptWorkspaceMutationError("replace Attempt Workspace file", err)
	}
	if current.ObjectOrigin.String == string(model.AttemptWorkspaceStorageAttempt) {
		if err = retireAttemptWorkspaceObject(ctx, tx, current.CurrentObjectID.String, scope.databaseNow); err != nil {
			return err
		}
		effect.protected = append(effect.protected, current.CurrentObjectID.String)
	}
	effect.entry = candidateAttemptWorkspaceItem(input.EntryID, model.StarterWorkspaceEntryFile, current.Path, object)
	effect.entryKind, effect.oldPath, effect.newPath, effect.version = model.StarterWorkspaceEntryFile, current.Path, current.Path, object.ContentVersion
	effect.protected = append(effect.protected, object.ID.String())
	return nil
}

// moveAttemptWorkspaceMutationEntry moves the entry at its expected path, with
// any descendants, to the destination path.
func moveAttemptWorkspaceMutationEntry(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptWorkspaceMutation, scope attemptWorkspaceMutationScope, effect *attemptWorkspaceMutationEffect) error {
	current, err := loadAttemptWorkspaceEntryForMutation(ctx, tx, scope.workspaceID, input.EntryID)
	if err != nil {
		return err
	}
	if current.Path != input.ExpectedPath {
		return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_entry", nil)
	}
	if err = moveAttemptWorkspaceEntry(ctx, tx, scope.workspaceID, current, input.DestinationPath, scope.databaseNow); err != nil {
		return err
	}
	effect.entryKind, effect.oldPath, effect.newPath = model.StarterWorkspaceEntryKind(current.Kind), current.Path, input.DestinationPath
	if effect.entryKind == model.StarterWorkspaceEntryFile {
		effect.version, err = current.workspaceContentVersion()
		if err != nil {
			return err
		}
		effect.entry = &store.CandidateAttemptWorkspaceItem{EntryID: input.EntryID, Kind: effect.entryKind, Path: effect.newPath,
			ContentVersion: effect.version, MediaType: current.MediaType.String, SizeBytes: current.SizeBytes.Int64, SHA256: current.SHA256.String}
	} else {
		effect.entry = &store.CandidateAttemptWorkspaceItem{EntryID: input.EntryID, Kind: effect.entryKind, Path: effect.newPath}
	}
	return nil
}

// deleteAttemptWorkspaceMutationEntry deletes the entry at its expected path
// and version, or its whole subtree when the deletion is recursive.
func deleteAttemptWorkspaceMutationEntry(ctx context.Context, tx *sqlxTxWrapper, input *store.ExamAttemptWorkspaceMutation, scope attemptWorkspaceMutationScope, effect *attemptWorkspaceMutationEffect) error {
	current, err := loadAttemptWorkspaceEntryForMutation(ctx, tx, scope.workspaceID, input.EntryID)
	if err != nil {
		return err
	}
	if current.Path != input.ExpectedPath {
		return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_entry", nil)
	}
	effect.entryKind, effect.oldPath = model.StarterWorkspaceEntryKind(current.Kind), current.Path
	if effect.entryKind == model.StarterWorkspaceEntryDirectory {
		if !input.ExpectedContentVersion.IsZero() {
			return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_content_version", nil)
		}
		var descendants int
		if err = tx.Get(ctx, &descendants, `SELECT count(*) FROM exam_attempt_workspace_entries WHERE workspace_id=? AND path LIKE ? ESCAPE '!'`,
			scope.workspaceID.String(), escapeLike(current.Path)+"/%"); err != nil {
			return fmt.Errorf("count Attempt Workspace descendants: %w", err)
		}
		if descendants != 0 && !input.Recursive {
			return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_not_empty", nil)
		}
	} else {
		if input.Recursive {
			return store.NewErrInvalidInput("attempt_workspace", "recursive_delete", nil)
		}
		currentVersion, versionErr := current.workspaceContentVersion()
		if versionErr != nil || !input.ExpectedContentVersion.IsValid() || currentVersion != input.ExpectedContentVersion {
			return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_content_version", versionErr)
		}
		if current.ObjectOrigin.String == string(model.AttemptWorkspaceStorageAttempt) {
			if err = retireAttemptWorkspaceObject(ctx, tx, current.CurrentObjectID.String, scope.databaseNow); err != nil {
				return err
			}
			effect.protected = append(effect.protected, current.CurrentObjectID.String)
		}
	}
	if input.Recursive {
		return deleteAttemptWorkspaceSubtree(ctx, tx, scope.workspaceID, current.Path, input.AuditEventID, scope.databaseNow)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM exam_attempt_workspace_entries WHERE workspace_id=? AND id=?`,
		scope.workspaceID.String(), input.EntryID.String()); err != nil {
		return fmt.Errorf("delete Attempt Workspace entry: %w", err)
	}
	return nil
}

func deleteAttemptWorkspaceSubtree(ctx context.Context, tx *sqlxTxWrapper, workspaceID model.ExamAttemptWorkspaceID,
	path string, auditID model.AuditEventID, at time.Time,
) error {
	// A retirement group keeps outcome protection bounded even for the maximum
	// hierarchy. Starter-origin objects remain pinned to their published source.
	if _, err := tx.Exec(ctx, `UPDATE exam_attempt_workspace_objects objects
		SET state='reclaimable',updated_at=?,reclaim_after=?,retired_by_audit_event_id=?
		WHERE objects.workspace_id=? AND objects.storage_origin='attempt' AND objects.state='current'
			AND EXISTS (SELECT 1 FROM exam_attempt_workspace_entries entries
				WHERE entries.workspace_id=objects.workspace_id AND entries.current_object_id=objects.id
				AND (entries.path=? OR entries.path LIKE ? ESCAPE '!'))`,
		at, at.Add(model.AttemptWorkspaceReclaimSafetyWindow), auditID, workspaceID.String(), path, escapeLike(path)+"/%"); err != nil {
		return fmt.Errorf("retire Attempt Workspace subtree objects: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM exam_attempt_workspace_entries
		WHERE workspace_id=? AND (path=? OR path LIKE ? ESCAPE '!')`, workspaceID.String(), path, escapeLike(path)+"/%"); err != nil {
		return fmt.Errorf("delete Attempt Workspace subtree: %w", err)
	}
	return nil
}

func loadAttemptWorkspaceEntryForMutation(ctx context.Context, tx *sqlxTxWrapper, workspaceID model.ExamAttemptWorkspaceID, entryID model.AttemptWorkspaceEntryID) (*attemptWorkspaceEntryMutationRow, error) {
	var row attemptWorkspaceEntryMutationRow
	if err := tx.Get(ctx, &row, attemptWorkspaceEntryMutationSelect+` WHERE e.workspace_id=? AND e.id=? FOR UPDATE OF e`,
		workspaceID.String(), entryID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_entry", nil)
		}
		return nil, fmt.Errorf("lock Attempt Workspace entry: %w", err)
	}
	return &row, nil
}

func (row *attemptWorkspaceEntryMutationRow) workspaceContentVersion() (model.WorkspaceContentVersion, error) {
	if row == nil || !row.ContentVersion.Valid {
		return "", invalidPersistedState("attempt_workspace_entry", "content_version", errors.New("missing current content"))
	}
	version, err := model.ParseWorkspaceContentVersion(row.ContentVersion.String)
	if err != nil {
		return "", invalidPersistedState("attempt_workspace_entry", "content_version", err)
	}
	return version, nil
}

func ensureAttemptWorkspaceParent(ctx context.Context, tx *sqlxTxWrapper, workspaceID model.ExamAttemptWorkspaceID, path string) error {
	index := strings.LastIndexByte(path, '/')
	if index < 0 {
		return nil
	}
	var exists bool
	if err := tx.Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM exam_attempt_workspace_entries WHERE workspace_id=? AND path=? AND kind='directory')`,
		workspaceID.String(), path[:index]); err != nil {
		return fmt.Errorf("find Attempt Workspace parent: %w", err)
	}
	if !exists {
		return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_path", nil)
	}
	return nil
}

func ensureAttemptWorkspaceCapacity(ctx context.Context, tx *sqlxTxWrapper, workspaceID model.ExamAttemptWorkspaceID, entryDelta int, byteDelta int64) error {
	capacity, err := attemptWorkspaceCapacityPolicy(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	var usage struct {
		Entries int   `db:"entries"`
		Bytes   int64 `db:"bytes"`
	}
	if err := tx.Get(ctx, &usage, `SELECT count(*) AS entries,COALESCE(sum(o.size_bytes),0) AS bytes
		FROM exam_attempt_workspace_entries e LEFT JOIN exam_attempt_workspace_objects o ON o.id=e.current_object_id
		WHERE e.workspace_id=?`, workspaceID.String()); err != nil {
		return fmt.Errorf("read Attempt Workspace capacity: %w", err)
	}
	if usage.Entries+entryDelta > capacity.WorkspaceMaximumEntries {
		return store.NewErrConflict("attempt_workspace", "attempt_workspace_entry_limit", nil)
	}
	if usage.Bytes+byteDelta > capacity.WorkspaceMaximumTotalBytes {
		return store.NewErrConflict("attempt_workspace", "attempt_workspace_size_limit", nil)
	}
	return nil
}

func consumeAttemptWorkspaceObject(ctx context.Context, tx *sqlxTxWrapper, workspaceID model.ExamAttemptWorkspaceID, objectID model.AttemptWorkspaceObjectID, databaseNow time.Time) (*model.AttemptWorkspaceObject, error) {
	var row attemptWorkspaceObjectRow
	if err := tx.Get(ctx, &row, attemptWorkspaceObjectSelect+` WHERE id=? AND workspace_id=? FOR UPDATE`, objectID.String(), workspaceID.String()); err != nil {
		return nil, translateError("attempt_workspace_object", objectID.String(), err)
	}
	object, err := row.model()
	if err != nil {
		return nil, err
	}
	if object.StorageOrigin != model.AttemptWorkspaceStorageAttempt || object.State != model.AttemptWorkspaceObjectStaged ||
		!object.HasContent() || !databaseNow.Before(object.ExpiresAt) {
		return nil, store.NewErrConflict("attempt_workspace_object", "attempt_workspace_object_state", nil)
	}
	if err = object.MarkCurrent(databaseNow); err != nil {
		return nil, store.NewErrConflict("attempt_workspace_object", "attempt_workspace_object_state", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE exam_attempt_workspace_objects SET state='current',updated_at=?,expires_at=NULL
		WHERE id=? AND state='staged'`, object.UpdatedAt, object.ID.String()); err != nil {
		return nil, fmt.Errorf("consume Attempt Workspace object: %w", err)
	}
	return object, nil
}

func retireAttemptWorkspaceObject(ctx context.Context, tx *sqlxTxWrapper, objectID string, databaseNow time.Time) error {
	reclaimAfter := databaseNow.Add(model.AttemptWorkspaceReclaimSafetyWindow)
	result, err := tx.Exec(ctx, `UPDATE exam_attempt_workspace_objects
		SET state='reclaimable',updated_at=?,reclaim_after=? WHERE id=? AND storage_origin='attempt' AND state='current'`,
		databaseNow, reclaimAfter, objectID)
	if err != nil {
		return fmt.Errorf("retire Attempt Workspace object: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return store.NewErrConflict("attempt_workspace_object", "attempt_workspace_object_state", err)
	}
	return nil
}

func moveAttemptWorkspaceEntry(ctx context.Context, tx *sqlxTxWrapper, workspaceID model.ExamAttemptWorkspaceID, root *attemptWorkspaceEntryMutationRow, destination string, databaseNow time.Time) error {
	if err := ensureAttemptWorkspaceParent(ctx, tx, workspaceID, destination); err != nil {
		return err
	}
	var rows []struct {
		ID   string `db:"id"`
		Path string `db:"path"`
	}
	if err := tx.Select(ctx, &rows, `SELECT id,path FROM exam_attempt_workspace_entries WHERE workspace_id=? ORDER BY path,id FOR UPDATE`,
		workspaceID.String()); err != nil {
		return fmt.Errorf("lock Attempt Workspace hierarchy: %w", err)
	}
	moving := make(map[string]string)
	occupied := make(map[string]string, len(rows))
	for _, row := range rows {
		occupied[row.Path] = row.ID
		if row.Path == root.Path || strings.HasPrefix(row.Path, root.Path+"/") {
			moving[row.ID] = destination + strings.TrimPrefix(row.Path, root.Path)
		}
	}
	if len(moving) == 0 || strings.HasPrefix(destination, root.Path+"/") {
		return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_path", nil)
	}
	for id, path := range moving {
		if normalized, normalizeErr := model.NormalizeAttemptWorkspacePath(path); normalizeErr != nil || normalized != path {
			return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_path", normalizeErr)
		}
		if occupiedID, exists := occupied[path]; exists && occupiedID != id {
			if _, partOfMove := moving[occupiedID]; !partOfMove {
				return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_path", nil)
			}
		}
	}
	for id, path := range moving {
		if _, err := tx.Exec(ctx, `UPDATE exam_attempt_workspace_entries SET path=?,updated_at=? WHERE workspace_id=? AND id=?`,
			path, databaseNow, workspaceID.String(), id); err != nil {
			return translateAttemptWorkspaceMutationError("move Attempt Workspace entry", err)
		}
	}
	return nil
}

func candidateAttemptWorkspaceItem(id model.AttemptWorkspaceEntryID, kind model.StarterWorkspaceEntryKind, path string, object *model.AttemptWorkspaceObject) *store.CandidateAttemptWorkspaceItem {
	item := &store.CandidateAttemptWorkspaceItem{EntryID: id, Kind: kind, Path: path}
	if object != nil {
		item.ContentVersion, item.MediaType, item.SizeBytes, item.SHA256 = object.ContentVersion, object.MediaType, object.SizeBytes, object.SHA256
	}
	return item
}

func appendAttemptWorkspaceJournal(ctx context.Context, tx *sqlxTxWrapper, workspaceID model.ExamAttemptWorkspaceID,
	entryID model.AttemptWorkspaceEntryID, entryKind model.StarterWorkspaceEntryKind, operation model.AttemptWorkspaceMutationKind,
	oldPath, newPath string, version model.WorkspaceContentVersion, recursive bool, keyDigest []byte, databaseNow time.Time,
) (*model.AttemptWorkspaceJournalEntry, error) {
	var cursor int64
	if err := tx.Get(ctx, &cursor, `UPDATE exam_attempt_workspaces SET cursor=cursor+1,updated_at=? WHERE id=? RETURNING cursor`,
		databaseNow, workspaceID.String()); err != nil {
		return nil, fmt.Errorf("advance Attempt Workspace cursor: %w", err)
	}
	change := &model.AttemptWorkspaceJournalEntry{WorkspaceID: workspaceID, Cursor: cursor, EntryID: entryID,
		EntryKind: entryKind, Operation: operation, OldPath: oldPath, NewPath: newPath,
		ContentVersion: version, ChangedAt: databaseNow, Recursive: recursive}
	if err := change.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("attempt_workspace_journal", "change", nil).Wrap(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO exam_attempt_workspace_journal
		(workspace_id,cursor,entry_id,entry_kind,operation,old_path,new_path,content_version,mutation_key_digest,changed_at,recursive)
		VALUES (?,?,?,?,?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?,?,?)`, workspaceID.String(), cursor, entryID.String(),
		string(entryKind), string(operation), oldPath, newPath, version.String(), keyDigest, databaseNow, recursive); err != nil {
		return nil, fmt.Errorf("append Attempt Workspace journal: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM exam_attempt_workspace_journal WHERE workspace_id=? AND cursor<=?`,
		workspaceID.String(), cursor-model.AttemptWorkspaceJournalRetention); err != nil {
		return nil, fmt.Errorf("trim Attempt Workspace journal: %w", err)
	}
	return change, nil
}

func completeAttemptWorkspaceMutationAudit(ctx context.Context, tx *sqlxTxWrapper, outcome attemptWorkspaceMutationOutcome,
	auditID model.AuditEventID, auditAt time.Time, replayed bool, originalAuditID string,
) error {
	data := map[string]any{"exam_sitting_id": outcome.SittingID, "candidate_user_id": outcome.CandidateID,
		"workspace_id": outcome.WorkspaceID, "entry_id": outcome.Change.EntryID.String(),
		"operation": string(outcome.Change.Operation), "workspace_cursor": outcome.Change.Cursor}
	if replayed {
		data["idempotency_replayed"] = true
		data["original_audit_event_id"] = originalAuditID
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, auditAt); err != nil {
		return fmt.Errorf("complete Attempt Workspace mutation audit: %w", err)
	}
	return nil
}

func validateAttemptWorkspaceMutationOutcome(outcome attemptWorkspaceMutationOutcome) error {
	if _, err := attemptWorkspaceMutationResult(outcome); err != nil {
		return fmt.Errorf("invalid Attempt Workspace mutation outcome: %w", err)
	}
	for _, value := range outcome.ProtectedObjectIDs {
		if _, err := model.ParseAttemptWorkspaceObjectID(value); err != nil {
			return fmt.Errorf("invalid Attempt Workspace mutation outcome object: %w", err)
		}
	}
	return nil
}

func attemptWorkspaceMutationResult(outcome attemptWorkspaceMutationOutcome) (*store.ExamAttemptWorkspaceMutationResult, error) {
	sittingID, err := model.ParseExamSittingID(outcome.SittingID)
	if err != nil {
		return nil, err
	}
	classID, err := model.ParseClassID(outcome.ClassID)
	if err != nil {
		return nil, err
	}
	candidateID, err := model.ParseUserID(outcome.CandidateID)
	if err != nil {
		return nil, err
	}
	workspaceID, err := model.ParseExamAttemptWorkspaceID(outcome.WorkspaceID)
	change := outcome.Change.entry()
	if err != nil || change.Validate() != nil || change.WorkspaceID != workspaceID ||
		(outcome.Entry == nil) != (change.Operation == model.AttemptWorkspaceMutationDeleteEntry) {
		return nil, fmt.Errorf("invalid Attempt Workspace mutation result")
	}
	if outcome.Entry != nil {
		if !outcome.Entry.EntryID.IsValid() || outcome.Entry.EntryID != change.EntryID || outcome.Entry.Kind != change.EntryKind ||
			outcome.Entry.Path != change.NewPath || outcome.Entry.Kind == model.StarterWorkspaceEntryFile &&
			(!outcome.Entry.ContentVersion.IsValid() || outcome.Entry.ContentVersion != change.ContentVersion) {
			return nil, fmt.Errorf("invalid Attempt Workspace mutation entry")
		}
	}
	return &store.ExamAttemptWorkspaceMutationResult{SittingID: sittingID, ClassID: classID, CandidateUserID: candidateID,
		WorkspaceID: workspaceID, Entry: (*store.CandidateAttemptWorkspaceItem)(outcome.Entry), Change: change}, nil
}

func translateAttemptWorkspaceMutationError(operation string, err error) error {
	translated := translateError("attempt_workspace_entry", "", err)
	var conflict *store.ErrConflict
	if errors.As(translated, &conflict) && conflict.Constraint == "exam_attempt_workspace_entries_path_key" {
		return store.NewErrConflict("attempt_workspace_entry", "attempt_workspace_path", err)
	}
	return fmt.Errorf("%s: %w", operation, translated)
}
