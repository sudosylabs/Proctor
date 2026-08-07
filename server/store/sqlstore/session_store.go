// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/session_store.go.
// Proctor retains a dedicated session store, indexed token resolution,
// transactional revocation, user-scoped session queries, and explicit store
// errors while separating bearer credentials from session metadata.

package sqlstore

import (
	"context"
	"fmt"
	"unicode/utf8"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlSessionStore struct {
	*SqlStore
	sessionsQuery sq.SelectBuilder
}

type sessionRow struct {
	ID                     string `db:"id"`
	CreateAt               int64  `db:"create_at"`
	UpdateAt               int64  `db:"update_at"`
	DeleteAt               int64  `db:"delete_at"`
	UserID                 string `db:"user_id"`
	ClientType             string `db:"client_type"`
	DeviceID               string `db:"device_id"`
	DeviceName             string `db:"device_name"`
	AuthenticationMethod   string `db:"authentication_method"`
	AuthenticationStrength string `db:"authentication_strength"`
	AuthenticatedAt        int64  `db:"authenticated_at"`
	MFACompletedAt         int64  `db:"mfa_completed_at"`
	LastActivityAt         int64  `db:"last_activity_at"`
	IdleExpiresAt          int64  `db:"idle_expires_at"`
	ExpiresAt              int64  `db:"expires_at"`
	RevokedAt              int64  `db:"revoked_at"`
	RevocationReason       string `db:"revocation_reason"`
}

func sessionSliceColumns() []string {
	return []string{
		"sessions.id",
		"sessions.create_at",
		"sessions.update_at",
		"sessions.delete_at",
		"sessions.user_id",
		"sessions.client_type",
		"sessions.device_id",
		"sessions.device_name",
		"sessions.authentication_method",
		"sessions.authentication_strength",
		"sessions.authenticated_at",
		"sessions.mfa_completed_at",
		"sessions.last_activity_at",
		"sessions.idle_expires_at",
		"sessions.expires_at",
		"sessions.revoked_at",
		"sessions.revocation_reason",
	}
}

func newSqlSessionStore(sqlStore *SqlStore) store.SessionStore {
	s := &SqlSessionStore{SqlStore: sqlStore}
	s.sessionsQuery = s.getQueryBuilder().Select(sessionSliceColumns()...).From("sessions")
	return s
}

func (s SqlSessionStore) Save(
	ctx context.Context,
	session *model.Session,
	credentials []*model.SessionCredential,
	maximumActive int,
) (*model.Session, []*model.SessionCredential, error) {
	if session == nil {
		return nil, nil, store.NewErrInvalidInput("session", "value", nil)
	}
	if session.Id != "" {
		return nil, nil, store.NewErrInvalidInput("session", "id", session.Id)
	}
	if maximumActive < 1 {
		return nil, nil, store.NewErrInvalidInput("session", "maximum_active", maximumActive)
	}

	candidate := *session
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, nil, appErr
	}
	prepared, err := prepareInitialSessionCredentials(&candidate, credentials)
	if err != nil {
		return nil, nil, err
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin session save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockUserSessions(ctx, tx, candidate.UserId); err != nil {
		return nil, nil, err
	}
	var active int
	if err := tx.Get(ctx, &active, `
		SELECT COUNT(*)
		  FROM sessions
		 WHERE user_id = ?
		   AND delete_at = 0
		   AND revoked_at = 0
		   AND idle_expires_at > ?
		   AND expires_at > ?`,
		candidate.UserId,
		candidate.CreateAt,
		candidate.CreateAt,
	); err != nil {
		return nil, nil, fmt.Errorf("count active sessions: %w", err)
	}
	if active >= maximumActive {
		return nil, nil, store.NewErrConflict("session", "sessions_maximum_per_user", nil)
	}

	if err := insertSession(ctx, tx, &candidate); err != nil {
		return nil, nil, err
	}
	for _, credential := range prepared {
		if err := insertSessionCredential(ctx, tx, credential); err != nil {
			return nil, nil, err
		}
	}
	userResult, err := tx.Exec(ctx, `
		UPDATE users
		   SET update_at = GREATEST(update_at, ?),
		       last_login_at = GREATEST(last_login_at, ?),
		       last_activity_at = GREATEST(last_activity_at, ?)
		 WHERE id = ? AND delete_at = 0 AND disabled_at = 0`,
		candidate.CreateAt,
		candidate.CreateAt,
		candidate.CreateAt,
		candidate.UserId,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("update user login time: %w", err)
	}
	if err := requireAffected(userResult, "user", candidate.UserId); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit session save: %w", err)
	}
	return &candidate, prepared, nil
}

func prepareInitialSessionCredentials(
	session *model.Session,
	credentials []*model.SessionCredential,
) ([]*model.SessionCredential, error) {
	if len(credentials) != 2 {
		return nil, store.NewErrInvalidInput("session", "credentials", len(credentials))
	}
	prepared := make([]*model.SessionCredential, 0, 2)
	kinds := map[model.SessionCredentialKind]bool{}
	for _, credential := range credentials {
		if credential == nil {
			return nil, store.NewErrInvalidInput("session_credential", "value", nil)
		}
		if credential.Id != "" {
			return nil, store.NewErrInvalidInput("session_credential", "id", credential.Id)
		}
		candidate := *credential
		candidate.SessionId = session.Id
		candidate.PreSave()
		if appErr := candidate.IsValid(); appErr != nil {
			return nil, appErr
		}
		if candidate.ExpiresAt > session.ExpiresAt {
			return nil, store.NewErrInvalidInput(
				"session_credential",
				"expires_at",
				candidate.ExpiresAt,
			)
		}
		if kinds[candidate.Kind] {
			return nil, store.NewErrInvalidInput("session_credential", "kind", candidate.Kind)
		}
		kinds[candidate.Kind] = true
		prepared = append(prepared, &candidate)
	}
	if !kinds[model.SessionCredentialAccess] || !kinds[model.SessionCredentialRefresh] {
		return nil, store.NewErrInvalidInput("session", "credentials", "access_and_refresh_required")
	}
	return prepared, nil
}

func insertSession(ctx context.Context, executor sqlxExecutor, session *model.Session) error {
	row := newSessionRow(session)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO sessions (
			id, create_at, update_at, delete_at, user_id, client_type,
			device_id, device_name, authentication_method,
			authentication_strength, authenticated_at, mfa_completed_at,
			last_activity_at, idle_expires_at, expires_at, revoked_at,
			revocation_reason
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :user_id, :client_type,
			:device_id, :device_name, :authentication_method,
			:authentication_strength, :authenticated_at, :mfa_completed_at,
			:last_activity_at, :idle_expires_at, :expires_at, :revoked_at,
			:revocation_reason
		)`, &row); err != nil {
		return fmt.Errorf("save session: %w", translateError("session", session.Id, err))
	}
	return nil
}

func (s SqlSessionStore) Get(ctx context.Context, id string) (*model.Session, error) {
	var row sessionRow
	query := s.sessionsQuery.Where(sq.Eq{
		"sessions.id":        id,
		"sessions.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("session", id, err)
	}
	return row.model(), nil
}

func (s SqlSessionStore) ListByUser(ctx context.Context, userID string) ([]*model.Session, error) {
	query := s.sessionsQuery.
		Where(sq.Eq{
			"sessions.user_id":   userID,
			"sessions.delete_at": int64(0),
		}).
		OrderBy("sessions.create_at DESC", "sessions.id")
	rows := []sessionRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list sessions by user: %w", err)
	}
	sessions := make([]*model.Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, row.model())
	}
	return sessions, nil
}

func (s SqlSessionStore) ListActiveByUser(
	ctx context.Context,
	userID string,
	now int64,
) ([]*model.Session, error) {
	query := s.sessionsQuery.
		Where(sq.Eq{
			"sessions.user_id":    userID,
			"sessions.delete_at":  int64(0),
			"sessions.revoked_at": int64(0),
		}).
		Where(sq.Gt{"sessions.idle_expires_at": now}).
		Where(sq.Gt{"sessions.expires_at": now}).
		OrderBy(
			"sessions.last_activity_at DESC",
			"sessions.create_at DESC",
			"sessions.id",
		)
	rows := []sessionRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list active sessions by user: %w", err)
	}
	sessions := make([]*model.Session, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, row.model())
	}
	return sessions, nil
}

func (s SqlSessionStore) UpdateActivity(
	ctx context.Context,
	id string,
	lastActivityAt int64,
	idleExpiresAt int64,
) error {
	result, err := s.GetMaster().Exec(ctx, `
		UPDATE sessions
		   SET update_at = GREATEST(update_at, ?),
		       last_activity_at = ?,
		       idle_expires_at = LEAST(?, expires_at)
		 WHERE id = ?
		   AND delete_at = 0
		   AND revoked_at = 0
		   AND idle_expires_at > ?
		   AND expires_at > ?`,
		lastActivityAt,
		lastActivityAt,
		idleExpiresAt,
		id,
		lastActivityAt,
		lastActivityAt,
	)
	if err != nil {
		return fmt.Errorf("update session activity: %w", err)
	}
	return requireAffected(result, "session", id)
}

func (s SqlSessionStore) Revoke(
	ctx context.Context,
	id string,
	userID string,
	revokedAt int64,
	reason string,
) ([]string, error) {
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockUserSessions(ctx, tx, userID); err != nil {
		return nil, err
	}
	hashes, err := revokeOneUserSession(ctx, tx, id, userID, revokedAt, reason)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit session revocation: %w", err)
	}
	return hashes, nil
}

func (s SqlSessionStore) RevokeWithAudit(
	ctx context.Context,
	input *store.SessionRevocation,
) (*store.SessionRevocationResult, error) {
	if input == nil || !model.IsValidId(input.SessionID) || !model.IsValidId(input.UserID) ||
		input.RevokedAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("session", "revocation", nil)
	}
	reason := model.SanitizeUnicode(input.Reason)
	if utf8.RuneCountInString(reason) > model.SessionRevocationMaxRunes {
		return nil, store.NewErrInvalidInput("session", "revocation_reason", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockUserSessions(ctx, tx, input.UserID); err != nil {
		return nil, err
	}
	var row sessionRow
	if err := tx.Get(ctx, &row, `
		SELECT id, create_at, update_at, delete_at, user_id, client_type,
		       device_id, device_name, authentication_method,
		       authentication_strength, authenticated_at, mfa_completed_at,
		       last_activity_at, idle_expires_at, expires_at, revoked_at,
		       revocation_reason
		  FROM sessions
		 WHERE id = ? AND user_id = ? AND delete_at = 0 AND revoked_at = 0
		 FOR UPDATE`,
		input.SessionID,
		input.UserID,
	); err != nil {
		return nil, translateError("session", input.SessionID, err)
	}
	hashes, err := revokeOneUserSession(
		ctx, tx, input.SessionID, input.UserID, input.RevokedAt, reason,
	)
	if err != nil {
		return nil, err
	}
	session := row.model()
	session.UpdateAt = max(session.UpdateAt, input.RevokedAt)
	session.RevokedAt = input.RevokedAt
	session.RevocationReason = reason
	encoded, appErr := model.EncodeAuditData(session.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(
		ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete session revocation audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit audited session revocation: %w", err)
	}
	return &store.SessionRevocationResult{Session: session, TokenHashes: hashes}, nil
}

func (s SqlSessionStore) RevokeAllForUser(
	ctx context.Context,
	userID string,
	revokedAt int64,
	reason string,
) ([]*model.Session, []string, error) {
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin user session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockUserSessions(ctx, tx, userID); err != nil {
		return nil, nil, err
	}
	rows, hashes, err := revokeAllUserSessions(
		ctx, tx, userID, revokedAt, reason,
	)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit user session revocation: %w", err)
	}
	return revokedSessionModels(rows, revokedAt, reason), hashes, nil
}

func (s SqlSessionStore) RevokeAllForUserWithAudit(
	ctx context.Context,
	input *store.UserSessionsRevocation,
) (*store.UserSessionsRevocationResult, error) {
	if input == nil || !model.IsValidId(input.UserID) || input.RevokedAt <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("session", "user_revocation", nil)
	}
	reason := model.SanitizeUnicode(input.Reason)
	if utf8.RuneCountInString(reason) > model.SessionRevocationMaxRunes {
		return nil, store.NewErrInvalidInput("session", "revocation_reason", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited user session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockUserSessions(ctx, tx, input.UserID); err != nil {
		return nil, err
	}
	rows, hashes, err := revokeAllUserSessions(
		ctx, tx, input.UserID, input.RevokedAt, reason,
	)
	if err != nil {
		return nil, err
	}
	sessions := revokedSessionModels(rows, input.RevokedAt, reason)
	encoded, appErr := model.EncodeAuditData(map[string]any{
		"user_id":               input.UserID,
		"revoked_session_count": len(sessions),
	})
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(
		ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete user session revocation audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit audited user session revocation: %w", err)
	}
	return &store.UserSessionsRevocationResult{Sessions: sessions, TokenHashes: hashes}, nil
}

func revokeOneUserSession(
	ctx context.Context,
	tx *sqlxTxWrapper,
	id string,
	userID string,
	revokedAt int64,
	reason string,
) ([]string, error) {
	var matchedSessionID string
	if err := tx.Get(ctx, &matchedSessionID, `
		SELECT id
		  FROM sessions
		 WHERE id = ? AND user_id = ? AND delete_at = 0 AND revoked_at = 0`,
		id,
		userID,
	); err != nil {
		return nil, translateError("session", id, err)
	}
	hashes, err := selectActiveTokenHashes(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(ctx, `
		UPDATE sessions
		   SET update_at = GREATEST(update_at, ?),
		       revoked_at = ?,
		       revocation_reason = ?
		 WHERE id = ? AND user_id = ? AND delete_at = 0 AND revoked_at = 0`,
		revokedAt,
		revokedAt,
		model.SanitizeUnicode(reason),
		id,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("revoke session: %w", err)
	}
	if err := requireAffected(result, "session", id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE session_credentials
		   SET update_at = GREATEST(update_at, ?), revoked_at = ?
		 WHERE session_id = ? AND delete_at = 0 AND revoked_at = 0`,
		revokedAt,
		revokedAt,
		id,
	); err != nil {
		return nil, fmt.Errorf("revoke session credentials: %w", err)
	}
	return hashes, nil
}

func revokeAllUserSessions(
	ctx context.Context,
	executor sqlxExecutor,
	userID string,
	revokedAt int64,
	reason string,
) ([]sessionRow, []string, error) {
	hashes := []string{}
	if err := executor.Select(ctx, &hashes, `
		SELECT credential.token_hash
		  FROM session_credentials credential
		  JOIN sessions session ON session.id = credential.session_id
		 WHERE session.user_id = ?
		   AND session.delete_at = 0
		   AND session.revoked_at = 0
		   AND credential.delete_at = 0
		   AND credential.revoked_at = 0
		 FOR UPDATE OF credential`,
		userID,
	); err != nil {
		return nil, nil, fmt.Errorf("select user session credentials: %w", err)
	}
	rows := []sessionRow{}
	if err := executor.Select(ctx, &rows, `
		SELECT id, create_at, update_at, delete_at, user_id, client_type,
		       device_id, device_name, authentication_method,
		       authentication_strength, authenticated_at, mfa_completed_at,
		       last_activity_at, idle_expires_at, expires_at, revoked_at,
		       revocation_reason
		  FROM sessions
		 WHERE user_id = ? AND delete_at = 0 AND revoked_at = 0
		 FOR UPDATE`,
		userID,
	); err != nil {
		return nil, nil, fmt.Errorf("select user sessions: %w", err)
	}
	if _, err := executor.Exec(ctx, `
		UPDATE session_credentials credential
		   SET update_at = GREATEST(credential.update_at, ?), revoked_at = ?
		  FROM sessions session
		 WHERE session.id = credential.session_id
		   AND session.user_id = ?
		   AND session.delete_at = 0
		   AND session.revoked_at = 0
		   AND credential.delete_at = 0
		   AND credential.revoked_at = 0`,
		revokedAt,
		revokedAt,
		userID,
	); err != nil {
		return nil, nil, fmt.Errorf("revoke user session credentials: %w", err)
	}
	if _, err := executor.Exec(ctx, `
		UPDATE sessions
		   SET update_at = GREATEST(update_at, ?),
		       revoked_at = ?,
		       revocation_reason = ?
		 WHERE user_id = ? AND delete_at = 0 AND revoked_at = 0`,
		revokedAt,
		revokedAt,
		model.SanitizeUnicode(reason),
		userID,
	); err != nil {
		return nil, nil, fmt.Errorf("revoke user sessions: %w", err)
	}
	return rows, hashes, nil
}

func revokedSessionModels(
	rows []sessionRow,
	revokedAt int64,
	reason string,
) []*model.Session {
	sessions := make([]*model.Session, 0, len(rows))
	for _, row := range rows {
		session := row.model()
		session.UpdateAt = max(session.UpdateAt, revokedAt)
		session.RevokedAt = revokedAt
		session.RevocationReason = model.SanitizeUnicode(reason)
		sessions = append(sessions, session)
	}
	return sessions
}

func lockUserSessions(
	ctx context.Context,
	executor sqlxExecutor,
	userID string,
) error {
	lockKey := "proctor:session-user:" + userID
	if _, err := executor.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
		lockKey,
	); err != nil {
		return fmt.Errorf("lock user sessions: %w", err)
	}
	return nil
}

func selectActiveTokenHashes(
	ctx context.Context,
	executor sqlxExecutor,
	sessionID string,
) ([]string, error) {
	hashes := []string{}
	if err := executor.Select(ctx, &hashes, `
		SELECT token_hash
		  FROM session_credentials
		 WHERE session_id = ? AND delete_at = 0 AND revoked_at = 0
		 FOR UPDATE`,
		sessionID,
	); err != nil {
		return nil, fmt.Errorf("select session credential hashes: %w", err)
	}
	return hashes, nil
}

func newSessionRow(session *model.Session) sessionRow {
	return sessionRow{
		ID:                     session.Id,
		CreateAt:               session.CreateAt,
		UpdateAt:               session.UpdateAt,
		DeleteAt:               session.DeleteAt,
		UserID:                 session.UserId,
		ClientType:             string(session.ClientType),
		DeviceID:               session.DeviceId,
		DeviceName:             session.DeviceName,
		AuthenticationMethod:   session.AuthenticationMethod,
		AuthenticationStrength: string(session.AuthenticationStrength),
		AuthenticatedAt:        session.AuthenticatedAt,
		MFACompletedAt:         session.MFACompletedAt,
		LastActivityAt:         session.LastActivityAt,
		IdleExpiresAt:          session.IdleExpiresAt,
		ExpiresAt:              session.ExpiresAt,
		RevokedAt:              session.RevokedAt,
		RevocationReason:       session.RevocationReason,
	}
}

func (row sessionRow) model() *model.Session {
	return &model.Session{
		Id:                     row.ID,
		CreateAt:               row.CreateAt,
		UpdateAt:               row.UpdateAt,
		DeleteAt:               row.DeleteAt,
		UserId:                 row.UserID,
		ClientType:             model.SessionClientType(row.ClientType),
		DeviceId:               row.DeviceID,
		DeviceName:             row.DeviceName,
		AuthenticationMethod:   row.AuthenticationMethod,
		AuthenticationStrength: model.AuthenticationStrength(row.AuthenticationStrength),
		AuthenticatedAt:        row.AuthenticatedAt,
		MFACompletedAt:         row.MFACompletedAt,
		LastActivityAt:         row.LastActivityAt,
		IdleExpiresAt:          row.IdleExpiresAt,
		ExpiresAt:              row.ExpiresAt,
		RevokedAt:              row.RevokedAt,
		RevocationReason:       row.RevocationReason,
	}
}

var _ store.SessionStore = (*SqlSessionStore)(nil)
