// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/token_store.go and
// user_store.go. Proctor binds every purpose-specific token to its target
// email and consumes the token, account mutation, session revocation, and
// terminal security audit in one PostgreSQL transaction.

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

type SqlUserTokenStore struct {
	*SqlStore
	tokensQuery sq.SelectBuilder
}

type userTokenRow struct {
	ID         string                 `db:"id"`
	CreatedAt  time.Time              `db:"created_at"`
	UpdatedAt  time.Time              `db:"updated_at"`
	ArchivedAt sql.NullTime           `db:"archived_at"`
	UserID     string                 `db:"user_id"`
	Purpose    model.UserTokenPurpose `db:"purpose"`
	TokenHash  string                 `db:"token_hash"`
	Target     string                 `db:"target"`
	ExpiresAt  time.Time              `db:"expires_at"`
	ConsumedAt sql.NullTime           `db:"consumed_at"`
}

func userTokenSliceColumns() []string {
	return []string{
		"user_tokens.id",
		"user_tokens.created_at",
		"user_tokens.updated_at",
		"user_tokens.archived_at",
		"user_tokens.user_id",
		"user_tokens.purpose",
		"user_tokens.token_hash",
		"user_tokens.target",
		"user_tokens.expires_at",
		"user_tokens.consumed_at",
	}
}

func newSqlUserTokenStore(sqlStore *SqlStore) store.UserTokenStore {
	s := &SqlUserTokenStore{SqlStore: sqlStore}
	s.tokensQuery = s.getQueryBuilder().
		Select(userTokenSliceColumns()...).
		From("user_tokens")
	return s
}

func (s SqlUserTokenStore) Issue(
	ctx context.Context,
	token *model.UserToken,
	auditEvent *model.AuditEvent,
) (*model.UserToken, error) {
	if token == nil || auditEvent == nil {
		return nil, store.NewErrInvalidInput("user_token", "issue", nil)
	}
	if !token.ID.IsZero() {
		return nil, store.NewErrInvalidInput("user_token", "id", token.ID.String())
	}
	candidate := *token
	// Preserve caller-supplied CreatedAt when set so expiry windows stay exact.
	at := model.NowUTC()
	if !candidate.CreatedAt.IsZero() {
		at = model.TimeUTC(candidate.CreatedAt)
	}
	candidate.PrepareCreate(model.NewUserTokenID(), at)
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	if auditEvent.Resource.Type != model.ResourceUser ||
		auditEvent.Resource.ID != candidate.UserID.String() ||
		auditEvent.Status != model.AuditStatusSuccess {
		return nil, store.NewErrInvalidInput("user_token", "audit_event", nil)
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin user token issue: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockUserTokenPurpose(
		ctx, tx, candidate.UserID.String(), candidate.Purpose,
	); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE user_tokens
		   SET updated_at = ?, archived_at = ?
		 WHERE user_id = ? AND purpose = ?
		   AND archived_at IS NULL AND consumed_at IS NULL`,
		candidate.CreatedAt,
		candidate.CreatedAt,
		candidate.UserID.String(),
		candidate.Purpose,
	); err != nil {
		return nil, fmt.Errorf("invalidate prior user tokens: %w", err)
	}
	if err := insertUserToken(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	if _, err := insertAuditEvent(ctx, tx, auditEvent); err != nil {
		return nil, fmt.Errorf("audit user token issue: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit user token issue: %w", err)
	}
	return &candidate, nil
}

func (s SqlUserTokenStore) GetByHash(
	ctx context.Context,
	tokenHash string,
	purpose model.UserTokenPurpose,
) (*model.UserToken, error) {
	if !model.IsValidTokenHash(tokenHash) || !purpose.IsValid() {
		return nil, store.NewErrInvalidInput("user_token", "lookup", nil)
	}
	var row userTokenRow
	query := s.tokensQuery.Where(sq.Eq{
		"user_tokens.token_hash": tokenHash,
		"user_tokens.purpose":    purpose,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("user_token", "", err)
	}
	return row.model(), nil
}

func (s SqlUserTokenStore) ConsumeEmailVerification(
	ctx context.Context,
	tokenHash string,
	now int64,
	auditEvent *model.AuditEvent,
) (*store.EmailVerificationResult, error) {
	if !model.IsValidTokenHash(tokenHash) || now <= 0 || auditEvent == nil {
		return nil, store.NewErrInvalidInput("user_token", "consume", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin email verification: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	at := model.TimeFromMillis(now)
	token, err := lockActiveUserToken(
		ctx, tx, tokenHash, model.UserTokenEmailVerification, now,
	)
	if err != nil {
		return nil, err
	}
	user, err := lockTokenUser(ctx, tx, token)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users
		   SET updated_at = ?, email_verified = true, revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL`,
		at, user.ID,
	); err != nil {
		return nil, fmt.Errorf("verify user email: %w", err)
	}
	if err := consumeUserTokens(
		ctx, tx, token.UserID, token.Purpose, now,
	); err != nil {
		return nil, err
	}
	event, err := tokenAuditEvent(auditEvent, token.UserID)
	if err != nil {
		return nil, err
	}
	if _, err := insertAuditEvent(ctx, tx, event); err != nil {
		return nil, fmt.Errorf("audit email verification: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit email verification: %w", err)
	}
	token.ConsumedAt = sql.NullTime{Time: at, Valid: true}
	token.UpdatedAt = at
	verified := user.model()
	verified.UpdatedAt = model.TimeFromMillis(now)
	verified.EmailVerified = true
	verified.Revision++
	return &store.EmailVerificationResult{
		Token: token.model(),
		User:  verified,
	}, nil
}

func (s SqlUserTokenStore) ConsumePasswordReset(
	ctx context.Context,
	tokenHash string,
	passwordHash string,
	now int64,
	reason string,
	auditEvent *model.AuditEvent,
) (*store.PasswordResetResult, error) {
	if !model.IsValidTokenHash(tokenHash) ||
		!model.IsValidPasswordHash(passwordHash) ||
		now <= 0 ||
		auditEvent == nil {
		return nil, store.NewErrInvalidInput("user_token", "password_reset", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin password reset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	at := model.TimeFromMillis(now)
	token, err := lockActiveUserToken(
		ctx, tx, tokenHash, model.UserTokenPasswordReset, now,
	)
	if err != nil {
		return nil, err
	}
	user, err := lockTokenUser(ctx, tx, token)
	if err != nil {
		return nil, err
	}
	var credential passwordCredentialRow
	if err := tx.Get(ctx, &credential, `
		SELECT id, created_at, updated_at, archived_at, user_id,
		       password_hash, password_changed_at
		  FROM password_credentials
		 WHERE user_id = ? AND archived_at IS NULL
		 FOR UPDATE`,
		token.UserID,
	); err != nil {
		return nil, translateError("password_credential", token.UserID, err)
	}
	credential.PasswordHash = passwordHash
	credential.PasswordChangedAt = at
	credential.UpdatedAt = at
	if _, err := tx.Exec(ctx, `
		UPDATE password_credentials
		   SET updated_at = ?, password_hash = ?, password_changed_at = ?
		 WHERE id = ? AND user_id = ? AND archived_at IS NULL`,
		at, passwordHash, at, credential.ID, token.UserID,
	); err != nil {
		return nil, fmt.Errorf("update reset password: %w", err)
	}
	if err := lockUserSessions(ctx, tx, token.UserID); err != nil {
		return nil, err
	}
	sessionRows, hashes, err := revokeAllUserSessions(
		ctx, tx, token.UserID, now, reason,
	)
	if err != nil {
		return nil, err
	}
	if err := consumeUserTokens(
		ctx, tx, token.UserID, token.Purpose, now,
	); err != nil {
		return nil, err
	}
	event, err := tokenAuditEvent(auditEvent, token.UserID)
	if err != nil {
		return nil, err
	}
	if _, err := insertAuditEvent(ctx, tx, event); err != nil {
		return nil, fmt.Errorf("audit password reset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit password reset: %w", err)
	}
	token.ConsumedAt = sql.NullTime{Time: at, Valid: true}
	token.UpdatedAt = at
	return &store.PasswordResetResult{
		Token:               token.model(),
		User:                user.model(),
		PasswordCredential:  credential.model(),
		RevokedSessions:     revokedSessionModels(sessionRows, now, reason),
		RevokedAccessHashes: hashes,
	}, nil
}

func insertUserToken(
	ctx context.Context,
	executor sqlxExecutor,
	token *model.UserToken,
) error {
	row := newUserTokenRow(token)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO user_tokens (
			id, created_at, updated_at, archived_at, user_id, purpose,
			token_hash, target, expires_at, consumed_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :user_id, :purpose,
			:token_hash, :target, :expires_at, :consumed_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save user token: %w",
			translateError("user_token", token.ID.String(), err),
		)
	}
	return nil
}

func lockUserTokenPurpose(
	ctx context.Context,
	executor sqlxExecutor,
	userID string,
	purpose model.UserTokenPurpose,
) error {
	if _, err := executor.Exec(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"proctor:user-token:"+userID+":"+string(purpose),
	); err != nil {
		return fmt.Errorf("lock user token purpose: %w", err)
	}
	return nil
}

func lockActiveUserToken(
	ctx context.Context,
	executor sqlxExecutor,
	tokenHash string,
	purpose model.UserTokenPurpose,
	now int64,
) (*userTokenRow, error) {
	var row userTokenRow
	if err := executor.Get(ctx, &row, `
		SELECT id, created_at, updated_at, archived_at, user_id, purpose,
		       token_hash, target, expires_at, consumed_at
		  FROM user_tokens
		 WHERE token_hash = ? AND purpose = ?
		   AND archived_at IS NULL AND consumed_at IS NULL AND expires_at > ?
		 FOR UPDATE`,
		tokenHash, purpose, model.TimeFromMillis(now),
	); err != nil {
		return nil, translateError("user_token", "", err)
	}
	return &row, nil
}

func lockTokenUser(
	ctx context.Context,
	executor sqlxExecutor,
	token *userTokenRow,
) (*userRow, error) {
	var user userRow
	if err := executor.Get(ctx, &user, `
		SELECT id, created_at, updated_at, archived_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at
		  FROM users
		 WHERE id = ? AND email = ? AND archived_at IS NULL AND disabled_at IS NULL
		 FOR UPDATE`,
		token.UserID, token.Target,
	); err != nil {
		return nil, translateError("user_token", "", err)
	}
	return &user, nil
}

func consumeUserTokens(
	ctx context.Context,
	executor sqlxExecutor,
	userID string,
	purpose model.UserTokenPurpose,
	now int64,
) error {
	at := model.TimeFromMillis(now)
	result, err := executor.Exec(ctx, `
		UPDATE user_tokens
		   SET updated_at = ?, consumed_at = ?
		 WHERE user_id = ? AND purpose = ?
		   AND archived_at IS NULL AND consumed_at IS NULL`,
		at, at, userID, purpose,
	)
	if err != nil {
		return fmt.Errorf("consume user tokens: %w", err)
	}
	if err := requireAffected(result, "user_token", userID); err != nil {
		return err
	}
	return nil
}

func tokenAuditEvent(
	event *model.AuditEvent,
	userID string,
) (*model.AuditEvent, error) {
	if event == nil || event.Status != model.AuditStatusSuccess {
		return nil, store.NewErrInvalidInput("user_token", "audit_event", nil)
	}
	candidate := event.Clone()
	if candidate.Resource.Type == "" {
		candidate.Resource.Type = model.ResourceUser
	}
	if candidate.Resource.Type != model.ResourceUser {
		return nil, store.NewErrInvalidInput("user_token", "audit_resource", nil)
	}
	if candidate.Resource.ID != "" && candidate.Resource.ID != userID {
		return nil, store.NewErrInvalidInput("user_token", "audit_resource_id", nil)
	}
	candidate.Resource.ID = userID
	return candidate, nil
}

func newUserTokenRow(token *model.UserToken) userTokenRow {
	return userTokenRow{
		ID:         token.ID.String(),
		CreatedAt:  UTCTime(token.CreatedAt),
		UpdatedAt:  UTCTime(token.UpdatedAt),
		ArchivedAt: NullTimeFromOptional(token.ArchivedAt),
		UserID:     token.UserID.String(),
		Purpose:    token.Purpose,
		TokenHash:  token.TokenHash,
		Target:     token.Target,
		ExpiresAt:  UTCTime(token.ExpiresAt),
		ConsumedAt: NullTimeFromOptional(token.ConsumedAt),
	}
}

func (row userTokenRow) model() *model.UserToken {
	return &model.UserToken{
		ID:         model.UserTokenID(row.ID),
		CreatedAt:  row.CreatedAt.UTC(),
		UpdatedAt:  row.UpdatedAt.UTC(),
		ArchivedAt: OptionalTimeFromNullTime(row.ArchivedAt),
		UserID:     model.UserID(row.UserID),
		Purpose:    row.Purpose,
		TokenHash:  row.TokenHash,
		Target:     row.Target,
		ExpiresAt:  row.ExpiresAt.UTC(),
		ConsumedAt: OptionalTimeFromNullTime(row.ConsumedAt),
	}
}

var _ store.UserTokenStore = (*SqlUserTokenStore)(nil)
