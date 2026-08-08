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
			translateError("session_credential", credential.ID.String(), err),
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
	var lockedSessionRow sessionRow
	sessionQuery := s.getQueryBuilder().
		Select(sessionSliceColumns()...).
		From("sessions").
		Where(sq.Eq{
			"sessions.id":        credentialRow.SessionID,
			"sessions.delete_at": int64(0),
		})
	if err := s.GetMaster().GetBuilder(ctx, &lockedSessionRow, sessionQuery); err != nil {
		return nil, nil, translateError("session", credentialRow.SessionID, err)
	}
	return credentialRow.model(), lockedSessionRow.model(), nil
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
	if !access.ID.IsZero() || !refresh.ID.IsZero() {
		return nil, store.NewErrInvalidInput("session_credential", "id", "must_be_empty")
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refresh rotation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var userID string
	if err := tx.Get(ctx, &userID, `
		SELECT session.user_id
		  FROM session_credentials credential
		  JOIN sessions session ON session.id = credential.session_id
		 WHERE credential.token_hash = ?
		   AND credential.kind = ?
		   AND credential.delete_at = 0
		   AND session.delete_at = 0`,
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
		current.SessionID.String(),
	); err != nil {
		return nil, translateError("session", current.SessionID.String(), err)
	}
	session := lockedSessionRow.model()
	nowTime := model.TimeFromMillis(now)

	if current.UsedAt.Valid || !current.ReplacedByID.IsZero() {
		hashes, revokeErr := revokeReplayedSession(ctx, tx, session, nowTime)
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
		   SET update_at = GREATEST(update_at, ?), revoked_at = ?
		 WHERE session_id = ?
		   AND kind = ?
		   AND delete_at = 0
		   AND revoked_at = 0`,
		now,
		now,
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
		   SET update_at = GREATEST(update_at, ?),
		       used_at = ?,
		       replaced_by_id = ?
		 WHERE id = ? AND used_at = 0 AND replaced_by_id IS NULL`,
		now,
		now,
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
		   SET update_at = ?, last_activity_at = ?, idle_expires_at = ?
		 WHERE id = ? AND revoked_at = 0`,
		model.MillisFromTime(session.UpdatedAt),
		model.MillisFromTime(session.LastActivityAt),
		model.MillisFromTime(session.IdleExpiresAt),
		session.ID.String(),
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
	now time.Time,
) ([]string, error) {
	nowMillis := model.MillisFromTime(now)
	hashes, err := selectActiveAccessHashes(ctx, executor, session.ID.String())
	if err != nil {
		return nil, err
	}
	if _, err := executor.Exec(ctx, `
		UPDATE session_credentials
		   SET update_at = GREATEST(update_at, ?), revoked_at = ?
		 WHERE session_id = ? AND delete_at = 0 AND revoked_at = 0`,
		nowMillis,
		nowMillis,
		session.ID.String(),
	); err != nil {
		return nil, fmt.Errorf("revoke replayed credential family: %w", err)
	}
	if _, err := executor.Exec(ctx, `
		UPDATE sessions
		   SET update_at = GREATEST(update_at, ?),
		       revoked_at = ?,
		       revocation_reason = ?
		 WHERE id = ? AND delete_at = 0 AND revoked_at = 0`,
		nowMillis,
		nowMillis,
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
		ID:           credential.ID.String(),
		CreateAt:     model.MillisFromTime(credential.CreatedAt),
		UpdateAt:     model.MillisFromTime(credential.UpdatedAt),
		DeleteAt:     credential.ArchivedAt.Millis(),
		SessionID:    credential.SessionID.String(),
		Kind:         string(credential.Kind),
		TokenHash:    credential.TokenHash,
		FamilyID:     nullableString(credential.FamilyID),
		ParentID:     nullableString(credential.ParentID.String()),
		ReplacedByID: nullableString(credential.ReplacedByID.String()),
		ExpiresAt:    model.MillisFromTime(credential.ExpiresAt),
		UsedAt:       credential.UsedAt.Millis(),
		RevokedAt:    credential.RevokedAt.Millis(),
	}
}

func (row sessionCredentialRow) model() *model.SessionCredential {
	return &model.SessionCredential{
		ID:           model.SessionCredentialID(row.ID),
		CreatedAt:    model.TimeFromMillis(row.CreateAt),
		UpdatedAt:    model.TimeFromMillis(row.UpdateAt),
		ArchivedAt:   model.OptionalTimeFromMillis(row.DeleteAt),
		SessionID:    model.SessionID(row.SessionID),
		Kind:         model.SessionCredentialKind(row.Kind),
		TokenHash:    row.TokenHash,
		FamilyID:     row.FamilyID.String,
		ParentID:     model.SessionCredentialID(row.ParentID.String),
		ReplacedByID: model.SessionCredentialID(row.ReplacedByID.String),
		ExpiresAt:    model.TimeFromMillis(row.ExpiresAt),
		UsedAt:       model.OptionalTimeFromMillis(row.UsedAt),
		RevokedAt:    model.OptionalTimeFromMillis(row.RevokedAt),
	}
}

var _ store.SessionCredentialStore = (*SqlSessionCredentialStore)(nil)
