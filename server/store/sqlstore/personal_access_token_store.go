// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/
// user_access_token_store.go. Proctor stores only credential hashes, requires
// bounded expiry and explicit scopes, serializes per-user active-token limits,
// and resolves the account and token in one authoritative database operation.

package sqlstore

import (
	"context"
	"fmt"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlPersonalAccessTokenStore struct {
	*SqlStore
}

type personalAccessTokenRow struct {
	ID             string         `db:"id"`
	CreateAt       int64          `db:"create_at"`
	UpdateAt       int64          `db:"update_at"`
	DeleteAt       int64          `db:"delete_at"`
	UserID         string         `db:"user_id"`
	Description    string         `db:"description"`
	TokenHash      string         `db:"token_hash"`
	Scopes         pq.StringArray `db:"scopes"`
	AcademicUnitID *string        `db:"academic_unit_id"`
	ExpiresAt      int64          `db:"expires_at"`
	LastUsedAt     int64          `db:"last_used_at"`
	DisabledAt     int64          `db:"disabled_at"`
	RevokedAt      int64          `db:"revoked_at"`
}

func newSqlPersonalAccessTokenStore(sqlStore *SqlStore) store.PersonalAccessTokenStore {
	return &SqlPersonalAccessTokenStore{SqlStore: sqlStore}
}

func (s SqlPersonalAccessTokenStore) Save(
	ctx context.Context,
	token *model.PersonalAccessToken,
	maximumActive int,
) (*model.PersonalAccessToken, error) {
	if token == nil || maximumActive < 1 {
		return nil, store.NewErrInvalidInput("personal_access_token", "value", nil)
	}
	if token.Id != "" {
		return nil, store.NewErrInvalidInput("personal_access_token", "id", token.Id)
	}
	candidate := *token
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	for _, scope := range candidate.Scopes {
		if !model.IsKnownAction(scope) {
			return nil, store.NewErrInvalidInput(
				"personal_access_token",
				"scopes",
				nil,
			)
		}
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin personal access token save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
		"personal_access_tokens:user:"+candidate.UserId,
	); err != nil {
		return nil, fmt.Errorf("lock personal access tokens: %w", err)
	}
	var active int
	if err := tx.Get(ctx, &active, `
		SELECT COUNT(*)
		  FROM personal_access_tokens
		 WHERE user_id = ?
		   AND delete_at = 0
		   AND revoked_at = 0
		   AND disabled_at = 0
		   AND expires_at > ?`,
		candidate.UserId,
		candidate.CreateAt,
	); err != nil {
		return nil, fmt.Errorf("count active personal access tokens: %w", err)
	}
	if active >= maximumActive {
		return nil, store.NewErrConflict(
			"personal_access_token",
			"personal_access_tokens_maximum_per_user",
			nil,
		)
	}
	if err := insertPersonalAccessToken(ctx, tx, &candidate); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit personal access token save: %w", err)
	}
	return clonePersonalAccessToken(&candidate), nil
}

func (s SqlPersonalAccessTokenStore) Get(
	ctx context.Context,
	id string,
) (*model.PersonalAccessToken, error) {
	if !model.IsValidId(id) {
		return nil, store.NewErrInvalidInput("personal_access_token", "id", id)
	}
	var row personalAccessTokenRow
	if err := s.GetMaster().Get(ctx, &row, `
		SELECT id, create_at, update_at, delete_at, user_id, description,
		       token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		       disabled_at, revoked_at
		  FROM personal_access_tokens
		 WHERE id = ? AND delete_at = 0`,
		id,
	); err != nil {
		return nil, translateError("personal_access_token", id, err)
	}
	return row.model(), nil
}

func (s SqlPersonalAccessTokenStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.PersonalAccessToken, error) {
	if !model.IsValidId(userID) {
		return nil, store.NewErrInvalidInput("personal_access_token", "user_id", userID)
	}
	var rows []personalAccessTokenRow
	if err := s.GetMaster().Select(ctx, &rows, `
		SELECT id, create_at, update_at, delete_at, user_id, description,
		       token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		       disabled_at, revoked_at
		  FROM personal_access_tokens
		 WHERE user_id = ? AND delete_at = 0
		 ORDER BY create_at DESC, id DESC`,
		userID,
	); err != nil {
		return nil, fmt.Errorf("list personal access tokens: %w", err)
	}
	result := make([]*model.PersonalAccessToken, 0, len(rows))
	for i := range rows {
		result = append(result, rows[i].model())
	}
	return result, nil
}

func (s SqlPersonalAccessTokenStore) Resolve(
	ctx context.Context,
	tokenHash string,
	now int64,
	updateIntervalMilliseconds int64,
) (*store.PersonalAccessTokenResolution, error) {
	if !model.IsValidTokenHash(tokenHash) || now <= 0 || updateIntervalMilliseconds <= 0 {
		return nil, store.NewErrInvalidInput("personal_access_token", "resolve", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin personal access token resolve: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var tokenRow personalAccessTokenRow
	if err := tx.Get(ctx, &tokenRow, `
		SELECT pat.id, pat.create_at, pat.update_at, pat.delete_at, pat.user_id,
		       pat.description, pat.token_hash, pat.scopes, pat.academic_unit_id,
		       pat.expires_at, pat.last_used_at, pat.disabled_at, pat.revoked_at
		  FROM personal_access_tokens pat
		  JOIN users u ON u.id = pat.user_id
		  LEFT JOIN academic_units au ON au.id = pat.academic_unit_id
		 WHERE pat.token_hash = ?
		   AND pat.delete_at = 0
		   AND pat.revoked_at = 0
		   AND pat.disabled_at = 0
		   AND pat.expires_at > ?
		   AND u.delete_at = 0
		   AND u.disabled_at = 0
		   AND (pat.academic_unit_id IS NULL OR au.delete_at = 0)
		 FOR UPDATE OF pat`,
		tokenHash,
		now,
	); err != nil {
		return nil, translateError("personal_access_token", "", err)
	}
	if tokenRow.LastUsedAt == 0 || now-tokenRow.LastUsedAt >= updateIntervalMilliseconds {
		if _, err := tx.Exec(ctx, `
			UPDATE personal_access_tokens
			   SET update_at = GREATEST(update_at, ?), last_used_at = ?
			 WHERE id = ?`,
			now, now, tokenRow.ID,
		); err != nil {
			return nil, fmt.Errorf("update personal access token usage: %w", err)
		}
		tokenRow.UpdateAt = max(tokenRow.UpdateAt, now)
		tokenRow.LastUsedAt = now
	}
	var userRow userRow
	if err := tx.Get(ctx, &userRow, `
		SELECT id, create_at, update_at, delete_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at
		  FROM users
		 WHERE id = ? AND delete_at = 0 AND disabled_at = 0
		 FOR SHARE`,
		tokenRow.UserID,
	); err != nil {
		return nil, translateError("user", tokenRow.UserID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit personal access token resolve: %w", err)
	}
	return &store.PersonalAccessTokenResolution{
		Token: tokenRow.model(),
		User:  userRow.model(),
	}, nil
}

func (s SqlPersonalAccessTokenStore) SetDisabled(
	ctx context.Context,
	id string,
	userID string,
	disabled bool,
	now int64,
	maximumActive int,
) (*model.PersonalAccessToken, error) {
	if !model.IsValidId(id) || !model.IsValidId(userID) ||
		now <= 0 || maximumActive < 1 {
		return nil, store.NewErrInvalidInput(
			"personal_access_token",
			"set_disabled",
			nil,
		)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin personal access token state change: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
		"personal_access_tokens:user:"+userID,
	); err != nil {
		return nil, fmt.Errorf("lock personal access tokens: %w", err)
	}
	var current personalAccessTokenRow
	if err := tx.Get(ctx, &current, `
		SELECT id, create_at, update_at, delete_at, user_id, description,
		       token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		       disabled_at, revoked_at
		  FROM personal_access_tokens
		 WHERE id = ? AND user_id = ?
		   AND delete_at = 0 AND revoked_at = 0 AND expires_at > ?
		 FOR UPDATE`,
		id, userID, now,
	); err != nil {
		return nil, translateError("personal_access_token", id, err)
	}
	if disabled == (current.DisabledAt != 0) {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit personal access token state: %w", err)
		}
		return current.model(), nil
	}
	if !disabled {
		var active int
		if err := tx.Get(ctx, &active, `
			SELECT COUNT(*)
			  FROM personal_access_tokens
			 WHERE user_id = ?
			   AND id <> ?
			   AND delete_at = 0
			   AND revoked_at = 0
			   AND disabled_at = 0
			   AND expires_at > ?`,
			userID, id, now,
		); err != nil {
			return nil, fmt.Errorf("count active personal access tokens: %w", err)
		}
		if active >= maximumActive {
			return nil, store.NewErrConflict(
				"personal_access_token",
				"personal_access_tokens_maximum_per_user",
				nil,
			)
		}
	}
	disabledAt := int64(0)
	if disabled {
		disabledAt = now
	}
	if err := tx.Get(ctx, &current, `
		UPDATE personal_access_tokens
		   SET update_at = GREATEST(update_at, ?), disabled_at = ?
		 WHERE id = ? AND user_id = ?
		 RETURNING id, create_at, update_at, delete_at, user_id, description,
		           token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		           disabled_at, revoked_at`,
		now, disabledAt, id, userID,
	); err != nil {
		return nil, translateError("personal_access_token", id, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit personal access token state: %w", err)
	}
	return current.model(), nil
}

func (s SqlPersonalAccessTokenStore) Revoke(
	ctx context.Context,
	id string,
	userID string,
	now int64,
) (*model.PersonalAccessToken, error) {
	if !model.IsValidId(id) || !model.IsValidId(userID) || now <= 0 {
		return nil, store.NewErrInvalidInput("personal_access_token", "revoke", nil)
	}
	var row personalAccessTokenRow
	if err := s.GetMaster().Get(ctx, &row, `
		UPDATE personal_access_tokens
		   SET update_at = GREATEST(update_at, ?), revoked_at = ?
		 WHERE id = ?
		   AND user_id = ?
		   AND delete_at = 0
		   AND revoked_at = 0
		 RETURNING id, create_at, update_at, delete_at, user_id, description,
		           token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		           disabled_at, revoked_at`,
		now, now, id, userID,
	); err != nil {
		return nil, translateError("personal_access_token", id, err)
	}
	return row.model(), nil
}

func insertPersonalAccessToken(
	ctx context.Context,
	executor sqlxExecutor,
	token *model.PersonalAccessToken,
) error {
	var academicUnitID any
	if token.AcademicUnitId != "" {
		academicUnitID = token.AcademicUnitId
	}
	if _, err := executor.Exec(ctx, `
		INSERT INTO personal_access_tokens (
			id, create_at, update_at, delete_at, user_id, description,
			token_hash, scopes, academic_unit_id, expires_at, last_used_at,
			disabled_at, revoked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		token.Id, token.CreateAt, token.UpdateAt, token.DeleteAt, token.UserId,
		token.Description, token.TokenHash, pq.Array(token.Scopes), academicUnitID,
		token.ExpiresAt, token.LastUsedAt, token.DisabledAt, token.RevokedAt,
	); err != nil {
		return translateError("personal_access_token", token.Id, err)
	}
	return nil
}

func (row personalAccessTokenRow) model() *model.PersonalAccessToken {
	academicUnitID := ""
	if row.AcademicUnitID != nil {
		academicUnitID = *row.AcademicUnitID
	}
	return &model.PersonalAccessToken{
		Id: row.ID, CreateAt: row.CreateAt, UpdateAt: row.UpdateAt, DeleteAt: row.DeleteAt,
		UserId: row.UserID, Description: row.Description, TokenHash: row.TokenHash,
		Scopes: append([]string(nil), row.Scopes...), AcademicUnitId: academicUnitID,
		ExpiresAt: row.ExpiresAt, LastUsedAt: row.LastUsedAt,
		DisabledAt: row.DisabledAt, RevokedAt: row.RevokedAt,
	}
}

func clonePersonalAccessToken(token *model.PersonalAccessToken) *model.PersonalAccessToken {
	if token == nil {
		return nil
	}
	cloned := *token
	cloned.Scopes = append([]string(nil), token.Scopes...)
	return &cloned
}
