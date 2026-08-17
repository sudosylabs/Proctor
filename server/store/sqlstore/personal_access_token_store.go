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
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLPersonalAccessTokenStore struct {
	*SQLStore
}

type personalAccessTokenRow struct {
	ID             string         `db:"id"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
	ArchivedAt     sql.NullTime   `db:"archived_at"`
	UserID         string         `db:"user_id"`
	Description    string         `db:"description"`
	TokenHash      string         `db:"token_hash"`
	Scopes         pq.StringArray `db:"scopes"`
	AcademicUnitID sql.NullString `db:"academic_unit_id"`
	ExpiresAt      time.Time      `db:"expires_at"`
	LastUsedAt     sql.NullTime   `db:"last_used_at"`
	DisabledAt     sql.NullTime   `db:"disabled_at"`
	RevokedAt      sql.NullTime   `db:"revoked_at"`
}

type personalAccessTokenResolutionTransactionResult struct {
	token *model.PersonalAccessToken
	user  *model.User
}

func newSQLPersonalAccessTokenStore(sqlStore *SQLStore) store.PersonalAccessTokenStore {
	return &SQLPersonalAccessTokenStore{SQLStore: sqlStore}
}

func (s SQLPersonalAccessTokenStore) Save(
	ctx context.Context,
	token *model.PersonalAccessToken,
	maximumActive int,
) (*model.PersonalAccessToken, error) {
	if token == nil || maximumActive < 1 {
		return nil, store.NewErrInvalidInput("personal_access_token", "value", nil)
	}
	if !token.ID.IsZero() {
		return nil, store.NewErrInvalidInput("personal_access_token", "id", token.ID.String())
	}
	candidate := *token
	candidate.PrepareCreate(model.NewPersonalAccessTokenID(), model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	for _, scope := range candidate.Scopes {
		if !model.IsPersonalAccessTokenAction(scope) {
			return nil, store.NewErrInvalidInput(
				"personal_access_token",
				"scopes",
				nil,
			)
		}
	}

	return runSQLTransaction(ctx, s.GetMaster().Begin, "personal access token save", func(ctx context.Context, tx *sqlxTxWrapper) (*model.PersonalAccessToken, error) {
		if _, err := tx.Exec(ctx,
			"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
			"personal_access_tokens:user:"+candidate.UserID.String(),
		); err != nil {
			return nil, fmt.Errorf("lock personal access tokens: %w", err)
		}
		var active int
		if err := tx.Get(ctx, &active, `
		SELECT COUNT(*)
		  FROM personal_access_tokens
		 WHERE user_id = ?
		   AND archived_at IS NULL
		   AND revoked_at IS NULL
		   AND disabled_at IS NULL
		   AND expires_at > ?`,
			candidate.UserID.String(), candidate.CreatedAt); err != nil {
			return nil, fmt.Errorf("count active personal access tokens: %w", err)
		}
		if active >= maximumActive {
			return nil, store.NewErrConflict("personal_access_token", "personal_access_tokens_maximum_per_user", nil)
		}
		if err := insertPersonalAccessToken(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		return clonePersonalAccessToken(&candidate), nil
	})
}

func (s SQLPersonalAccessTokenStore) Get(
	ctx context.Context,
	id string,
) (*model.PersonalAccessToken, error) {
	if !model.IsValidId(id) {
		return nil, store.NewErrInvalidInput("personal_access_token", "id", id)
	}
	var row personalAccessTokenRow
	if err := s.GetMaster().Get(ctx, &row, `
		SELECT id, created_at, updated_at, archived_at, user_id, description,
		       token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		       disabled_at, revoked_at
		  FROM personal_access_tokens
		 WHERE id = ? AND archived_at IS NULL`,
		id,
	); err != nil {
		return nil, translateError("personal_access_token", id, err)
	}
	return row.model()
}

func (s SQLPersonalAccessTokenStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.PersonalAccessToken, error) {
	if !model.IsValidId(userID) {
		return nil, store.NewErrInvalidInput("personal_access_token", "user_id", userID)
	}
	var rows []personalAccessTokenRow
	if err := s.GetMaster().Select(ctx, &rows, `
		SELECT id, created_at, updated_at, archived_at, user_id, description,
		       token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		       disabled_at, revoked_at
		  FROM personal_access_tokens
		 WHERE user_id = ? AND archived_at IS NULL
		 ORDER BY created_at DESC, id DESC`,
		userID,
	); err != nil {
		return nil, fmt.Errorf("list personal access tokens: %w", err)
	}
	result := make([]*model.PersonalAccessToken, 0, len(rows))
	for i := range rows {
		token, err := rows[i].model()
		if err != nil {
			return nil, err
		}
		result = append(result, token)
	}
	return result, nil
}

func (s SQLPersonalAccessTokenStore) Resolve(
	ctx context.Context,
	tokenHash string,
	now int64,
	updateIntervalMilliseconds int64,
) (*store.PersonalAccessTokenResolution, error) {
	if !model.IsValidTokenHash(tokenHash) || now <= 0 || updateIntervalMilliseconds <= 0 {
		return nil, store.NewErrInvalidInput("personal_access_token", "resolve", nil)
	}
	at := model.TimeFromMillis(now)
	result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "personal access token resolve", func(ctx context.Context, tx *sqlxTxWrapper) (*personalAccessTokenResolutionTransactionResult, error) {
		var tokenRow personalAccessTokenRow
		if err := tx.Get(ctx, &tokenRow, `
		SELECT pat.id, pat.created_at, pat.updated_at, pat.archived_at, pat.user_id,
		       pat.description, pat.token_hash, pat.scopes, pat.academic_unit_id,
		       pat.expires_at, pat.last_used_at, pat.disabled_at, pat.revoked_at
		  FROM personal_access_tokens pat
		  JOIN users u ON u.id = pat.user_id
		  LEFT JOIN academic_units au ON au.id = pat.academic_unit_id
		 WHERE pat.token_hash = ?
		   AND pat.archived_at IS NULL
		   AND pat.revoked_at IS NULL
		   AND pat.disabled_at IS NULL
		   AND pat.expires_at > ?
		   AND u.archived_at IS NULL
		   AND u.disabled_at IS NULL
		   AND (pat.academic_unit_id IS NULL OR au.archived_at IS NULL)
		 FOR UPDATE OF pat`,
			tokenHash, at); err != nil {
			return nil, translateError("personal_access_token", "", err)
		}
		if !tokenRow.LastUsedAt.Valid || at.Sub(tokenRow.LastUsedAt.Time) >= time.Duration(updateIntervalMilliseconds)*time.Millisecond {
			if _, err := tx.Exec(ctx, `
			UPDATE personal_access_tokens
			   SET updated_at = GREATEST(updated_at, ?), last_used_at = ?
			 WHERE id = ?`,
				at, at, tokenRow.ID); err != nil {
				return nil, fmt.Errorf("update personal access token usage: %w", err)
			}
			if tokenRow.UpdatedAt.Before(at) {
				tokenRow.UpdatedAt = at
			}
			tokenRow.LastUsedAt = sql.NullTime{Time: at, Valid: true}
		}
		var userRow userRow
		if err := tx.Get(ctx, &userRow, `
		SELECT id, created_at, updated_at, archived_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at
		       , default_profile_picture_seed, default_profile_picture_file_id,
		       custom_profile_picture_file_id, profile_picture_changed_at
		  FROM users
		 WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL
		 FOR SHARE`,
			tokenRow.UserID); err != nil {
			return nil, translateError("user", tokenRow.UserID, err)
		}
		token, err := tokenRow.model()
		if err != nil {
			return nil, err
		}
		user, err := userRow.model()
		if err != nil {
			return nil, err
		}
		return &personalAccessTokenResolutionTransactionResult{token: token, user: user}, nil
	})
	if err != nil {
		return nil, err
	}
	return &store.PersonalAccessTokenResolution{
		Token: result.token,
		User:  result.user,
	}, nil
}

func (s SQLPersonalAccessTokenStore) SetDisabled(
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
	at := model.TimeFromMillis(now)
	return runSQLTransaction(ctx, s.GetMaster().Begin, "personal access token state change", func(ctx context.Context, tx *sqlxTxWrapper) (*model.PersonalAccessToken, error) {
		if _, err := tx.Exec(ctx,
			"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
			"personal_access_tokens:user:"+userID,
		); err != nil {
			return nil, fmt.Errorf("lock personal access tokens: %w", err)
		}
		var current personalAccessTokenRow
		if err := tx.Get(ctx, &current, `
		SELECT id, created_at, updated_at, archived_at, user_id, description,
		       token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		       disabled_at, revoked_at
		  FROM personal_access_tokens
		 WHERE id = ? AND user_id = ?
		   AND archived_at IS NULL AND revoked_at IS NULL AND expires_at > ?
		 FOR UPDATE`,
			id, userID, at); err != nil {
			return nil, translateError("personal_access_token", id, err)
		}
		if disabled == current.DisabledAt.Valid {
			return current.model()
		}
		if !disabled {
			var active int
			if err := tx.Get(ctx, &active, `
			SELECT COUNT(*)
			  FROM personal_access_tokens
			 WHERE user_id = ?
			   AND id <> ?
			   AND archived_at IS NULL
			   AND revoked_at IS NULL
			   AND disabled_at IS NULL
			   AND expires_at > ?`,
				userID, id, at); err != nil {
				return nil, fmt.Errorf("count active personal access tokens: %w", err)
			}
			if active >= maximumActive {
				return nil, store.NewErrConflict("personal_access_token", "personal_access_tokens_maximum_per_user", nil)
			}
		}
		disabledAt := sql.NullTime{}
		if disabled {
			disabledAt = sql.NullTime{Time: at, Valid: true}
		}
		if err := tx.Get(ctx, &current, `
		UPDATE personal_access_tokens
		   SET updated_at = GREATEST(updated_at, ?), disabled_at = ?
		 WHERE id = ? AND user_id = ?
		 RETURNING id, created_at, updated_at, archived_at, user_id, description,
		           token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		           disabled_at, revoked_at`,
			at, disabledAt, id, userID); err != nil {
			return nil, translateError("personal_access_token", id, err)
		}
		return current.model()
	})
}

func (s SQLPersonalAccessTokenStore) Revoke(
	ctx context.Context,
	id string,
	userID string,
	now int64,
) (*model.PersonalAccessToken, error) {
	if !model.IsValidId(id) || !model.IsValidId(userID) || now <= 0 {
		return nil, store.NewErrInvalidInput("personal_access_token", "revoke", nil)
	}
	var row personalAccessTokenRow
	at := model.TimeFromMillis(now)
	if err := s.GetMaster().Get(ctx, &row, `
		UPDATE personal_access_tokens
		   SET updated_at = GREATEST(updated_at, created_at, ?),
		       revoked_at = GREATEST(created_at, ?)
		 WHERE id = ?
		   AND user_id = ?
		   AND archived_at IS NULL
		   AND revoked_at IS NULL
		 RETURNING id, created_at, updated_at, archived_at, user_id, description,
		           token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		           disabled_at, revoked_at`,
		at, at, id, userID,
	); err != nil {
		return nil, translateError("personal_access_token", id, err)
	}
	return row.model()
}

func insertPersonalAccessToken(
	ctx context.Context,
	executor sqlxExecutor,
	token *model.PersonalAccessToken,
) error {
	var academicUnitID any
	if !token.AcademicUnitID.IsZero() {
		academicUnitID = token.AcademicUnitID.String()
	}
	if _, err := executor.Exec(ctx, `
		INSERT INTO personal_access_tokens (
			id, created_at, updated_at, archived_at, user_id, description,
			token_hash, scopes, academic_unit_id, expires_at, last_used_at,
			disabled_at, revoked_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		token.ID.String(),
		token.CreatedAt,
		token.UpdatedAt,
		NullTimeFromOptional(token.ArchivedAt),
		token.UserID.String(),
		token.Description,
		token.TokenHash,
		pq.Array(token.Scopes),
		academicUnitID,
		token.ExpiresAt,
		NullTimeFromOptional(token.LastUsedAt),
		NullTimeFromOptional(token.DisabledAt),
		NullTimeFromOptional(token.RevokedAt),
	); err != nil {
		return translateError("personal_access_token", token.ID.String(), err)
	}
	return nil
}

func (row personalAccessTokenRow) model() (*model.PersonalAccessToken, error) {
	id, err := parsePersistedID("personal_access_token", "id", row.ID, model.ParsePersonalAccessTokenID)
	if err != nil {
		return nil, err
	}
	userID, err := parsePersistedID("personal_access_token", "user_id", row.UserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	academicUnitID, err := parseNullablePersistedID("personal_access_token", "academic_unit_id", row.AcademicUnitID, model.ParseAcademicUnitID)
	if err != nil {
		return nil, err
	}
	token := &model.PersonalAccessToken{
		ID:             id,
		CreatedAt:      row.CreatedAt.UTC(),
		UpdatedAt:      row.UpdatedAt.UTC(),
		ArchivedAt:     OptionalTimeFromNullTime(row.ArchivedAt),
		UserID:         userID,
		Description:    row.Description,
		TokenHash:      row.TokenHash,
		Scopes:         append([]string(nil), row.Scopes...),
		AcademicUnitID: academicUnitID,
		ExpiresAt:      row.ExpiresAt.UTC(),
		LastUsedAt:     OptionalTimeFromNullTime(row.LastUsedAt),
		DisabledAt:     OptionalTimeFromNullTime(row.DisabledAt),
		RevokedAt:      OptionalTimeFromNullTime(row.RevokedAt),
	}
	if err := validatePersistedModel("personal_access_token", token); err != nil {
		return nil, err
	}
	return token, nil
}

func clonePersonalAccessToken(token *model.PersonalAccessToken) *model.PersonalAccessToken {
	if token == nil {
		return nil
	}
	cloned := *token
	cloned.Scopes = append([]string(nil), token.Scopes...)
	return &cloned
}
