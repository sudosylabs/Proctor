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

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlSessionCredentialStore struct {
	*SqlStore
	credentialsQuery sq.SelectBuilder
}

type sessionCredentialRow struct {
	ID           string         `db:"id"`
	CreateAt     int64          `db:"create_at"`
	UpdateAt     int64          `db:"update_at"`
	DeleteAt     int64          `db:"delete_at"`
	SessionID    string         `db:"session_id"`
	Kind         string         `db:"kind"`
	TokenHash    string         `db:"token_hash"`
	FamilyID     sql.NullString `db:"family_id"`
	ParentID     sql.NullString `db:"parent_id"`
	ReplacedByID sql.NullString `db:"replaced_by_id"`
	ExpiresAt    int64          `db:"expires_at"`
	UsedAt       int64          `db:"used_at"`
	RevokedAt    int64          `db:"revoked_at"`
}

func sessionCredentialSliceColumns() []string {
	return []string{
		"session_credentials.id",
		"session_credentials.create_at",
		"session_credentials.update_at",
		"session_credentials.delete_at",
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

func newSqlSessionCredentialStore(sqlStore *SqlStore) store.SessionCredentialStore {
	s := &SqlSessionCredentialStore{SqlStore: sqlStore}
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
			id, create_at, update_at, delete_at, session_id, kind,
			token_hash, family_id, parent_id, replaced_by_id, expires_at,
			used_at, revoked_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :session_id, :kind,
			:token_hash, :family_id, :parent_id, :replaced_by_id, :expires_at,
			:used_at, :revoked_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save session credential: %w",
			translateError("session_credential", credential.Id, err),
		)
	}
	return nil
}

func (s SqlSessionCredentialStore) GetSessionByTokenHash(
	ctx context.Context,
	tokenHash string,
	kind model.SessionCredentialKind,
) (*model.SessionCredential, *model.Session, error) {
	var credentialRow sessionCredentialRow
	query := s.credentialsQuery.Where(sq.Eq{
		"session_credentials.token_hash": tokenHash,
		"session_credentials.kind":       string(kind),
		"session_credentials.delete_at":  int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &credentialRow, query); err != nil {
		return nil, nil, translateError("session_credential", tokenHash, err)
	}
	var sessionRow sessionRow
	sessionQuery := s.getQueryBuilder().
		Select(sessionSliceColumns()...).
		From("sessions").
		Where(sq.Eq{
			"sessions.id":        credentialRow.SessionID,
			"sessions.delete_at": int64(0),
		})
	if err := s.GetMaster().GetBuilder(ctx, &sessionRow, sessionQuery); err != nil {
		return nil, nil, translateError("session", credentialRow.SessionID, err)
	}
	return credentialRow.model(), sessionRow.model(), nil
}

func (s SqlSessionCredentialStore) RotateRefresh(
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
	if access.Id != "" || refresh.Id != "" {
		return nil, store.NewErrInvalidInput("session_credential", "id", "must_be_empty")
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refresh rotation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentRow sessionCredentialRow
	if err := tx.Get(ctx, &currentRow, `
		SELECT id, create_at, update_at, delete_at, session_id, kind,
		       token_hash, family_id, parent_id, replaced_by_id, expires_at,
		       used_at, revoked_at
		  FROM session_credentials
		 WHERE token_hash = ? AND kind = ? AND delete_at = 0
		 FOR UPDATE`,
		tokenHash,
		string(model.SessionCredentialRefresh),
	); err != nil {
		return nil, translateError("session_credential", tokenHash, err)
	}
	current := currentRow.model()

	var lockedSessionRow sessionRow
	if err := tx.Get(ctx, &lockedSessionRow, `
		SELECT id, create_at, update_at, delete_at, user_id, client_type,
		       device_id, device_name, authentication_method,
		       authentication_strength, authenticated_at, mfa_completed_at,
		       last_activity_at, idle_expires_at, expires_at, revoked_at,
		       revocation_reason
		  FROM sessions
		 WHERE id = ? AND delete_at = 0
		 FOR UPDATE`,
		current.SessionId,
	); err != nil {
		return nil, translateError("session", current.SessionId, err)
	}
	session := lockedSessionRow.model()

	if current.UsedAt != 0 || current.ReplacedById != "" {
		hashes, revokeErr := revokeReplayedSession(ctx, tx, session, now)
		if revokeErr != nil {
			return nil, revokeErr
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit refresh replay revocation: %w", err)
		}
		return &store.SessionRotation{
			Session:             session,
			RevokedAccessHashes: hashes,
			ReplayDetected:      true,
		}, nil
	}
	if current.IsExpiredAt(now) || session.IsExpiredAt(now) {
		return nil, store.NewErrConflict("session_credential", "session_credentials_inactive", nil)
	}

	newAccess := *access
	newAccess.SessionId = session.Id
	newAccess.Kind = model.SessionCredentialAccess
	newAccess.ExpiresAt = min(newAccess.ExpiresAt, session.ExpiresAt)
	newAccess.PreSave()
	if appErr := newAccess.IsValid(); appErr != nil {
		return nil, appErr
	}
	newRefresh := *refresh
	newRefresh.SessionId = session.Id
	newRefresh.Kind = model.SessionCredentialRefresh
	newRefresh.FamilyId = current.FamilyId
	newRefresh.ParentId = current.Id
	newRefresh.ExpiresAt = min(newRefresh.ExpiresAt, session.ExpiresAt)
	newRefresh.PreSave()
	if appErr := newRefresh.IsValid(); appErr != nil {
		return nil, appErr
	}
	revokedHashes, err := selectActiveAccessHashes(ctx, tx, session.Id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE session_credentials
		   SET update_at = GREATEST(update_at, ?), revoked_at = ?
		 WHERE session_id = ?
		   AND kind = ?
		   AND delete_at = 0
		   AND revoked_at = 0`,
		now,
		now,
		session.Id,
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
		   SET update_at = GREATEST(update_at, ?),
		       used_at = ?,
		       replaced_by_id = ?
		 WHERE id = ? AND used_at = 0 AND replaced_by_id IS NULL`,
		now,
		now,
		newRefresh.Id,
		current.Id,
	); err != nil {
		return nil, fmt.Errorf("mark refresh credential used: %w", err)
	}
	session.LastActivityAt = now
	session.IdleExpiresAt = min(idleExpiresAt, session.ExpiresAt)
	session.UpdateAt = max(session.UpdateAt, now)
	if _, err := tx.Exec(ctx, `
		UPDATE sessions
		   SET update_at = ?, last_activity_at = ?, idle_expires_at = ?
		 WHERE id = ? AND revoked_at = 0`,
		session.UpdateAt,
		session.LastActivityAt,
		session.IdleExpiresAt,
		session.Id,
	); err != nil {
		return nil, fmt.Errorf("update rotated session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit refresh rotation: %w", err)
	}
	return &store.SessionRotation{
		Session:             session,
		AccessCredential:    &newAccess,
		RefreshCredential:   &newRefresh,
		RevokedAccessHashes: revokedHashes,
	}, nil
}

func revokeReplayedSession(
	ctx context.Context,
	executor sqlxExecutor,
	session *model.Session,
	now int64,
) ([]string, error) {
	hashes, err := selectActiveAccessHashes(ctx, executor, session.Id)
	if err != nil {
		return nil, err
	}
	if _, err := executor.Exec(ctx, `
		UPDATE session_credentials
		   SET update_at = GREATEST(update_at, ?), revoked_at = ?
		 WHERE session_id = ? AND delete_at = 0 AND revoked_at = 0`,
		now,
		now,
		session.Id,
	); err != nil {
		return nil, fmt.Errorf("revoke replayed credential family: %w", err)
	}
	if _, err := executor.Exec(ctx, `
		UPDATE sessions
		   SET update_at = GREATEST(update_at, ?),
		       revoked_at = ?,
		       revocation_reason = ?
		 WHERE id = ? AND delete_at = 0 AND revoked_at = 0`,
		now,
		now,
		"refresh credential replay detected",
		session.Id,
	); err != nil {
		return nil, fmt.Errorf("revoke replayed session: %w", err)
	}
	session.UpdateAt = max(session.UpdateAt, now)
	session.RevokedAt = now
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
		   AND delete_at = 0
		   AND revoked_at = 0
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
		ID:           credential.Id,
		CreateAt:     credential.CreateAt,
		UpdateAt:     credential.UpdateAt,
		DeleteAt:     credential.DeleteAt,
		SessionID:    credential.SessionId,
		Kind:         string(credential.Kind),
		TokenHash:    credential.TokenHash,
		FamilyID:     nullableString(credential.FamilyId),
		ParentID:     nullableString(credential.ParentId),
		ReplacedByID: nullableString(credential.ReplacedById),
		ExpiresAt:    credential.ExpiresAt,
		UsedAt:       credential.UsedAt,
		RevokedAt:    credential.RevokedAt,
	}
}

func (row sessionCredentialRow) model() *model.SessionCredential {
	return &model.SessionCredential{
		Id:           row.ID,
		CreateAt:     row.CreateAt,
		UpdateAt:     row.UpdateAt,
		DeleteAt:     row.DeleteAt,
		SessionId:    row.SessionID,
		Kind:         model.SessionCredentialKind(row.Kind),
		TokenHash:    row.TokenHash,
		FamilyId:     row.FamilyID.String,
		ParentId:     row.ParentID.String,
		ReplacedById: row.ReplacedByID.String,
		ExpiresAt:    row.ExpiresAt,
		UsedAt:       row.UsedAt,
		RevokedAt:    row.RevokedAt,
	}
}

var _ store.SessionCredentialStore = (*SqlSessionCredentialStore)(nil)
