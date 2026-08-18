// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/session_store.go.
// Proctor stores only SHA-256 token hashes and adds refresh-family lineage,
// row locking, atomic rotation, and replay-triggered session revocation.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLSessionCredentialStore struct {
	*SQLStore
	credentialsQuery sq.SelectBuilder
}

type sessionCredentialRow struct {
	ID           string         `db:"id"`
	CreatedAt    time.Time      `db:"created_at"`
	UpdatedAt    time.Time      `db:"updated_at"`
	ArchivedAt   sql.NullTime   `db:"archived_at"`
	SessionID    string         `db:"session_id"`
	Kind         string         `db:"kind"`
	TokenHash    string         `db:"token_hash"`
	FamilyID     sql.NullString `db:"family_id"`
	ParentID     sql.NullString `db:"parent_id"`
	ReplacedByID sql.NullString `db:"replaced_by_id"`
	ExpiresAt    time.Time      `db:"expires_at"`
	UsedAt       sql.NullTime   `db:"used_at"`
	RevokedAt    sql.NullTime   `db:"revoked_at"`
}

func sessionCredentialSliceColumns() []string {
	return []string{
		"session_credentials.id",
		"session_credentials.created_at",
		"session_credentials.updated_at",
		"session_credentials.archived_at",
		"session_credentials.session_id",
		"session_credentials.kind",
		"session_credentials.token_hash",
		"session_credentials.family_id",
		"session_credentials.parent_id",
		"session_credentials.replaced_by_id",
		"session_credentials.expires_at",
		"session_credentials.used_at",
		"session_credentials.revoked_at",
	}
}

func newSQLSessionCredentialStore(sqlStore *SQLStore) store.SessionCredentialStore {
	s := &SQLSessionCredentialStore{SQLStore: sqlStore}
	s.credentialsQuery = s.getQueryBuilder().
		Select(sessionCredentialSliceColumns()...).
		From("session_credentials")
	return s
}

func insertSessionCredential(
	ctx context.Context,
	executor sqlxExecutor,
	credential *model.SessionCredential,
) error {
	row := newSessionCredentialRow(credential)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO session_credentials (
			id, created_at, updated_at, archived_at, session_id, kind,
			token_hash, family_id, parent_id, replaced_by_id, expires_at,
			used_at, revoked_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :session_id, :kind,
			:token_hash, :family_id, :parent_id, :replaced_by_id, :expires_at,
			:used_at, :revoked_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save session credential: %w",
			translateError("session_credential", credential.ID.String(), err),
		)
	}
	return nil
}

func (s SQLSessionCredentialStore) GetSessionByTokenHash(
	ctx context.Context,
	tokenHash string,
	kind model.SessionCredentialKind,
) (*model.SessionCredential, *model.Session, error) {
	var credentialRow sessionCredentialRow
	query := s.credentialsQuery.Where(sq.Eq{
		"session_credentials.token_hash":  tokenHash,
		"session_credentials.kind":        string(kind),
		"session_credentials.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &credentialRow, query); err != nil {
		return nil, nil, translateError("session_credential", tokenHash, err)
	}
	var lockedSessionRow sessionRow
	sessionQuery := s.getQueryBuilder().
		Select(sessionSliceColumns()...).
		From("sessions").
		Where(sq.Eq{
			"sessions.id":          credentialRow.SessionID,
			"sessions.archived_at": nil,
		})
	if err := s.GetMaster().GetBuilder(ctx, &lockedSessionRow, sessionQuery); err != nil {
		return nil, nil, translateError("session", credentialRow.SessionID, err)
	}
	credential, err := credentialRow.model()
	if err != nil {
		return nil, nil, err
	}
	session, err := lockedSessionRow.model()
	if err != nil {
		return nil, nil, err
	}
	return credential, session, nil
}

func (s SQLSessionCredentialStore) RotateRefresh(
	ctx context.Context,
	tokenHash string,
	access *model.SessionCredential,
	refresh *model.SessionCredential,
	now int64,
	idleExpiresAt int64,
) (*store.SessionRotation, error) {
	if access == nil || refresh == nil {
		return nil, store.NewErrInvalidInput("session_credential", "rotation", nil)
	}
	if !access.ID.IsZero() || !refresh.ID.IsZero() {
		return nil, store.NewErrInvalidInput("session_credential", "id", "must_be_empty")
	}

	return runSQLTransaction(ctx, s.GetMaster().Begin, "refresh rotation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.SessionRotation, error) {
		var userID string
		if err := tx.Get(ctx, &userID, `
		SELECT session.user_id
		  FROM session_credentials credential
		  JOIN sessions session ON session.id = credential.session_id
		 WHERE credential.token_hash = ?
		   AND credential.kind = ?
		   AND credential.archived_at IS NULL
		   AND session.archived_at IS NULL`,
			tokenHash,
			string(model.SessionCredentialRefresh),
		); err != nil {
			return nil, translateError("session_credential", tokenHash, err)
		}
		if err := lockUserSessions(ctx, tx, userID); err != nil {
			return nil, err
		}

		var currentRow sessionCredentialRow
		if err := tx.Get(ctx, &currentRow, `
		SELECT id, created_at, updated_at, archived_at, session_id, kind,
		       token_hash, family_id, parent_id, replaced_by_id, expires_at,
		       used_at, revoked_at
		  FROM session_credentials
		 WHERE token_hash = ? AND kind = ? AND archived_at IS NULL
		 FOR UPDATE`,
			tokenHash,
			string(model.SessionCredentialRefresh),
		); err != nil {
			return nil, translateError("session_credential", tokenHash, err)
		}
		current, err := currentRow.model()
		if err != nil {
			return nil, err
		}

		var lockedSessionRow sessionRow
		if err := tx.Get(ctx, &lockedSessionRow, `
		SELECT id, created_at, updated_at, archived_at, user_id, client_type,
		       device_id, device_name, authentication_method, authentication_provider_id, external_identity_id,
		       authentication_strength, authenticated_at, mfa_completed_at,
		       last_activity_at, idle_expires_at, expires_at, revoked_at,
		       revocation_reason
		  FROM sessions
		 WHERE id = ? AND archived_at IS NULL
		 FOR UPDATE`,
			current.SessionID.String(),
		); err != nil {
			return nil, translateError("session", current.SessionID.String(), err)
		}
		session, err := lockedSessionRow.model()
		if err != nil {
			return nil, err
		}
		nowTime := model.TimeFromMillis(now)

		if current.UsedAt.Valid || !current.ReplacedByID.IsZero() {
			hashes, revokeErr := revokeReplayedSession(ctx, tx, session, nowTime)
			if revokeErr != nil {
				return nil, revokeErr
			}
			return &store.SessionRotation{
				Session:             session,
				RevokedAccessHashes: hashes,
				ReplayDetected:      true,
			}, nil
		}
		if current.IsExpiredAt(nowTime) || session.IsExpiredAt(nowTime) {
			return nil, store.NewErrConflict("session_credential", "session_credentials_inactive", nil)
		}

		newAccess := *access
		newAccess.SessionID = session.ID
		newAccess.Kind = model.SessionCredentialAccess
		if newAccess.ExpiresAt.After(session.ExpiresAt) {
			newAccess.ExpiresAt = session.ExpiresAt
		}
		newAccess.PrepareCreate(model.NewSessionCredentialID(), nowTime)
		if err := newAccess.Validate(); err != nil {
			return nil, err
		}
		newRefresh := *refresh
		newRefresh.SessionID = session.ID
		newRefresh.Kind = model.SessionCredentialRefresh
		newRefresh.FamilyID = current.FamilyID
		newRefresh.ParentID = current.ID
		if newRefresh.ExpiresAt.After(session.ExpiresAt) {
			newRefresh.ExpiresAt = session.ExpiresAt
		}
		newRefresh.PrepareCreate(model.NewSessionCredentialID(), nowTime)
		if err := newRefresh.Validate(); err != nil {
			return nil, err
		}
		revokedHashes, err := selectActiveAccessHashes(ctx, tx, session.ID.String())
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
		UPDATE session_credentials
		   SET updated_at = GREATEST(updated_at, ?), revoked_at = ?
		 WHERE session_id = ?
		   AND kind = ?
		   AND archived_at IS NULL
		   AND revoked_at IS NULL`,
			nowTime,
			nowTime,
			session.ID.String(),
			string(model.SessionCredentialAccess),
		); err != nil {
			return nil, fmt.Errorf("revoke replaced access credentials: %w", err)
		}
		if err := insertSessionCredential(ctx, tx, &newAccess); err != nil {
			return nil, err
		}
		if err := insertSessionCredential(ctx, tx, &newRefresh); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
		UPDATE session_credentials
		   SET updated_at = GREATEST(updated_at, ?),
		       used_at = ?,
		       replaced_by_id = ?
		 WHERE id = ? AND used_at IS NULL AND replaced_by_id IS NULL`,
			nowTime,
			nowTime,
			newRefresh.ID.String(),
			current.ID.String(),
		); err != nil {
			return nil, fmt.Errorf("mark refresh credential used: %w", err)
		}
		idleAt := model.TimeFromMillis(idleExpiresAt)
		if idleAt.After(session.ExpiresAt) {
			idleAt = session.ExpiresAt
		}
		session.LastActivityAt = nowTime
		session.IdleExpiresAt = idleAt
		if session.UpdatedAt.Before(nowTime) {
			session.UpdatedAt = nowTime
		}
		if _, err := tx.Exec(ctx, `
		UPDATE sessions
		   SET updated_at = ?, last_activity_at = ?, idle_expires_at = ?
		 WHERE id = ? AND revoked_at IS NULL`,
			session.UpdatedAt,
			session.LastActivityAt,
			session.IdleExpiresAt,
			session.ID.String(),
		); err != nil {
			return nil, fmt.Errorf("update rotated session: %w", err)
		}
		return &store.SessionRotation{
			Session:             session,
			AccessCredential:    &newAccess,
			RefreshCredential:   &newRefresh,
			RevokedAccessHashes: revokedHashes,
		}, nil
	})
}

func revokeReplayedSession(
	ctx context.Context,
	executor sqlxExecutor,
	session *model.Session,
	now time.Time,
) ([]string, error) {
	hashes, err := selectActiveAccessHashes(ctx, executor, session.ID.String())
	if err != nil {
		return nil, err
	}
	if _, err := executor.Exec(ctx, `
		UPDATE session_credentials
		   SET updated_at = GREATEST(updated_at, ?), revoked_at = ?
		 WHERE session_id = ? AND archived_at IS NULL AND revoked_at IS NULL`,
		now,
		now,
		session.ID.String(),
	); err != nil {
		return nil, fmt.Errorf("revoke replayed credential family: %w", err)
	}
	if _, err := executor.Exec(ctx, `
		UPDATE sessions
		   SET updated_at = GREATEST(updated_at, ?),
		       revoked_at = ?,
		       revocation_reason = ?
		 WHERE id = ? AND archived_at IS NULL AND revoked_at IS NULL`,
		now,
		now,
		"refresh credential replay detected",
		session.ID.String(),
	); err != nil {
		return nil, fmt.Errorf("revoke replayed session: %w", err)
	}
	if session.UpdatedAt.Before(now) {
		session.UpdatedAt = now
	}
	session.RevokedAt = model.OptionalTimeFrom(now)
	session.RevocationReason = "refresh credential replay detected"
	return hashes, nil
}

func selectActiveAccessHashes(
	ctx context.Context,
	executor sqlxExecutor,
	sessionID string,
) ([]string, error) {
	hashes := []string{}
	if err := executor.Select(ctx, &hashes, `
		SELECT token_hash
		  FROM session_credentials
		 WHERE session_id = ?
		   AND kind = ?
		   AND archived_at IS NULL
		   AND revoked_at IS NULL
		 FOR UPDATE`,
		sessionID,
		string(model.SessionCredentialAccess),
	); err != nil {
		return nil, fmt.Errorf("select access credential hashes: %w", err)
	}
	return hashes, nil
}

func newSessionCredentialRow(credential *model.SessionCredential) sessionCredentialRow {
	return sessionCredentialRow{
		ID:           credential.ID.String(),
		CreatedAt:    UTCTime(credential.CreatedAt),
		UpdatedAt:    UTCTime(credential.UpdatedAt),
		ArchivedAt:   NullTimeFromOptional(credential.ArchivedAt),
		SessionID:    credential.SessionID.String(),
		Kind:         string(credential.Kind),
		TokenHash:    credential.TokenHash,
		FamilyID:     nullableString(credential.FamilyID),
		ParentID:     nullableString(credential.ParentID.String()),
		ReplacedByID: nullableString(credential.ReplacedByID.String()),
		ExpiresAt:    UTCTime(credential.ExpiresAt),
		UsedAt:       NullTimeFromOptional(credential.UsedAt),
		RevokedAt:    NullTimeFromOptional(credential.RevokedAt),
	}
}

func (row sessionCredentialRow) model() (*model.SessionCredential, error) {
	id, err := parsePersistedID("session_credential", "id", row.ID, model.ParseSessionCredentialID)
	if err != nil {
		return nil, err
	}
	sessionID, err := parsePersistedID("session_credential", "session_id", row.SessionID, model.ParseSessionID)
	if err != nil {
		return nil, err
	}
	parentID, err := parseNullablePersistedID("session_credential", "parent_id", row.ParentID, model.ParseSessionCredentialID)
	if err != nil {
		return nil, err
	}
	replacedByID, err := parseNullablePersistedID("session_credential", "replaced_by_id", row.ReplacedByID, model.ParseSessionCredentialID)
	if err != nil {
		return nil, err
	}
	credential := &model.SessionCredential{
		ID:           id,
		CreatedAt:    row.CreatedAt.UTC(),
		UpdatedAt:    row.UpdatedAt.UTC(),
		ArchivedAt:   OptionalTimeFromNullTime(row.ArchivedAt),
		SessionID:    sessionID,
		Kind:         model.SessionCredentialKind(row.Kind),
		TokenHash:    row.TokenHash,
		FamilyID:     row.FamilyID.String,
		ParentID:     parentID,
		ReplacedByID: replacedByID,
		ExpiresAt:    row.ExpiresAt.UTC(),
		UsedAt:       OptionalTimeFromNullTime(row.UsedAt),
		RevokedAt:    OptionalTimeFromNullTime(row.RevokedAt),
	}
	if err := validatePersistedModel("session_credential", credential); err != nil {
		return nil, err
	}
	return credential, nil
}

var _ store.SessionCredentialStore = (*SQLSessionCredentialStore)(nil)
