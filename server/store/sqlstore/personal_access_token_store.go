// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
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

func (s SQLPersonalAccessTokenStore) Create(
	ctx context.Context,
	input *store.PersonalAccessTokenCreationMutation,
) (*store.PersonalAccessTokenMutationResult, error) {
	if input == nil || input.Token == nil || input.MaximumActive < 1 || input.MinimumLifetime <= 0 ||
		input.MaximumLifetime < input.MinimumLifetime || !model.IsValidId(input.PreparationID) {
		return nil, store.NewErrInvalidInput("personal_access_token", "create", nil)
	}
	candidate := clonePersonalAccessToken(input.Token)
	if !candidate.ID.IsZero() {
		return nil, store.NewErrInvalidInput("personal_access_token", "id", candidate.ID.String())
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "personal access token create", func(ctx context.Context, tx *sqlxTxWrapper) (*store.PersonalAccessTokenMutationResult, error) {
		if err := lockPersonalAccessTokensForUser(ctx, tx, candidate.UserID.String()); err != nil {
			return nil, err
		}
		preparation, databaseAt, err := lockPersonalAccessTokenPreparation(ctx, tx, input.PreparationID)
		if err != nil {
			return nil, err
		}
		if err := requirePersonalAccessTokenPreparation(preparation, store.PersonalAccessTokenMutationCreate, candidate.UserID.String(), "", databaseAt); err != nil {
			return nil, err
		}
		actionAt := preparation.CreatedAt
		candidate.PrepareCreate(model.NewPersonalAccessTokenID(), actionAt)
		if err := validatePersonalAccessTokenCandidate(candidate); err != nil {
			return nil, err
		}
		if candidate.ExpiresAt.Before(actionAt.Add(input.MinimumLifetime)) || candidate.ExpiresAt.After(actionAt.Add(input.MaximumLifetime)) {
			return nil, store.NewErrInvalidInput("personal_access_token", "expires_at", nil)
		}
		notice, payloadKeyID, err := validatePersonalAccessTokenSecurityNotice(candidate.UserID, input.Notice, model.MailTemplateIdentityPersonalAccessTokenCreated, candidate.ExpiresAt, actionAt)
		if err != nil {
			return nil, err
		}
		if err := enforcePersonalAccessTokenMaximum(ctx, tx, candidate.UserID.String(), "", candidate.CreatedAt, input.MaximumActive); err != nil {
			return nil, err
		}
		if err := insertPersonalAccessToken(ctx, tx, candidate); err != nil {
			return nil, err
		}
		if err := insertSecurityNoticeMail(ctx, tx, notice.Occurrence, notice.Delivery, notice.Job, payloadKeyID); err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(candidate.Auditable())
		if err != nil {
			return nil, err
		}
		if err = terminalizePersonalAccessTokenPreparation(ctx, tx, preparation, model.AuditStatusSuccess, "", encoded); err != nil {
			return nil, fmt.Errorf("terminalize personal access token creation: %w", err)
		}
		return &store.PersonalAccessTokenMutationResult{Token: clonePersonalAccessToken(candidate), Fresh: true}, nil
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

func (s SQLPersonalAccessTokenStore) ChangeState(
	ctx context.Context,
	input *store.PersonalAccessTokenStateMutation,
) (*store.PersonalAccessTokenMutationResult, error) {
	if input == nil || !model.IsValidId(input.ID) || !model.IsValidId(input.UserID) ||
		input.MaximumActive < 1 || !model.IsValidId(input.PreparationID) {
		return nil, store.NewErrInvalidInput("personal_access_token", "change_state", nil)
	}
	key := model.MailTemplateIdentityPersonalAccessTokenEnabled
	if input.Disabled {
		key = model.MailTemplateIdentityPersonalAccessTokenDisabled
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "personal access token state change with audit", func(ctx context.Context, tx *sqlxTxWrapper) (*store.PersonalAccessTokenMutationResult, error) {
		if err := lockPersonalAccessTokensForUser(ctx, tx, input.UserID); err != nil {
			return nil, err
		}
		preparation, databaseAt, err := lockPersonalAccessTokenPreparation(ctx, tx, input.PreparationID)
		if err != nil {
			return nil, err
		}
		kind := store.PersonalAccessTokenMutationEnable
		if input.Disabled {
			kind = store.PersonalAccessTokenMutationDisable
		}
		if err := requirePersonalAccessTokenPreparation(preparation, kind, input.UserID, input.ID, databaseAt); err != nil {
			return nil, err
		}
		actionAt := preparation.CreatedAt
		var current personalAccessTokenRow
		if err := tx.Get(ctx, &current, `
		SELECT id, created_at, updated_at, archived_at, user_id, description,
		       token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		       disabled_at, revoked_at
		  FROM personal_access_tokens
		 WHERE id = ? AND user_id = ?
		   AND archived_at IS NULL AND revoked_at IS NULL AND expires_at > ?
		 FOR UPDATE`, input.ID, input.UserID, databaseAt); err != nil {
			return nil, translateError("personal_access_token", input.ID, err)
		}
		if input.Disabled == current.DisabledAt.Valid {
			currentModel, modelErr := current.model()
			if modelErr != nil {
				return nil, modelErr
			}
			if err := deletePersonalAccessTokenPreparation(ctx, tx, preparation.ID); err != nil {
				return nil, err
			}
			return &store.PersonalAccessTokenMutationResult{Token: currentModel, Fresh: false}, nil
		}
		notice, payloadKeyID, err := validatePersonalAccessTokenSecurityNotice(model.UserID(input.UserID), input.Notice, key, current.ExpiresAt, actionAt)
		if err != nil {
			return nil, err
		}
		if !input.Disabled {
			if err := enforcePersonalAccessTokenMaximum(ctx, tx, input.UserID, input.ID, databaseAt, input.MaximumActive); err != nil {
				return nil, err
			}
		}
		disabledAt := sql.NullTime{}
		if input.Disabled {
			disabledAt = sql.NullTime{Time: actionAt, Valid: true}
		}
		if err := tx.Get(ctx, &current, `
		UPDATE personal_access_tokens
			   SET updated_at = GREATEST(updated_at, ?), disabled_at = ?
		 WHERE id = ? AND user_id = ?
		 RETURNING id, created_at, updated_at, archived_at, user_id, description,
		           token_hash, scopes, academic_unit_id, expires_at, last_used_at,
		           disabled_at, revoked_at`, actionAt, disabledAt, input.ID, input.UserID); err != nil {
			return nil, translateError("personal_access_token", input.ID, err)
		}
		updated, err := current.model()
		if err != nil {
			return nil, err
		}
		if err := insertSecurityNoticeMail(ctx, tx, notice.Occurrence, notice.Delivery, notice.Job, payloadKeyID); err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(updated.Auditable())
		if err != nil {
			return nil, err
		}
		if err = terminalizePersonalAccessTokenPreparation(ctx, tx, preparation, model.AuditStatusSuccess, "", encoded); err != nil {
			return nil, fmt.Errorf("terminalize personal access token state audit: %w", err)
		}
		return &store.PersonalAccessTokenMutationResult{Token: updated, Fresh: true}, nil
	})
}

func (s SQLPersonalAccessTokenStore) RevokeWithAudit(
	ctx context.Context,
	input *store.PersonalAccessTokenRevocation,
) (*store.PersonalAccessTokenMutationResult, error) {
	if input == nil || !model.IsValidId(input.ID) || !model.IsValidId(input.UserID) || !model.IsValidId(input.PreparationID) {
		return nil, store.NewErrInvalidInput("personal_access_token", "revoke", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "personal access token revoke with audit", func(ctx context.Context, tx *sqlxTxWrapper) (*store.PersonalAccessTokenMutationResult, error) {
		if err := lockPersonalAccessTokensForUser(ctx, tx, input.UserID); err != nil {
			return nil, err
		}
		preparation, databaseAt, err := lockPersonalAccessTokenPreparation(ctx, tx, input.PreparationID)
		if err != nil {
			return nil, err
		}
		if err := requirePersonalAccessTokenPreparation(preparation, store.PersonalAccessTokenMutationRevoke, input.UserID, input.ID, databaseAt); err != nil {
			return nil, err
		}
		actionAt := preparation.CreatedAt
		var row personalAccessTokenRow
		if err := tx.Get(ctx, &row, `SELECT id, created_at, updated_at, archived_at, user_id, description,
		       token_hash, scopes, academic_unit_id, expires_at, last_used_at, disabled_at, revoked_at
		  FROM personal_access_tokens WHERE id=? AND user_id=? AND archived_at IS NULL FOR UPDATE`, input.ID, input.UserID); err != nil {
			return nil, translateError("personal_access_token", input.ID, err)
		}
		if row.RevokedAt.Valid {
			current, modelErr := row.model()
			if modelErr != nil {
				return nil, modelErr
			}
			if err := deletePersonalAccessTokenPreparation(ctx, tx, preparation.ID); err != nil {
				return nil, err
			}
			return &store.PersonalAccessTokenMutationResult{Token: current, Fresh: false}, nil
		}
		if err := tx.Get(ctx, &row, `UPDATE personal_access_tokens
		   SET updated_at = GREATEST(updated_at, created_at, ?), revoked_at = GREATEST(created_at, ?)
		 WHERE id=? AND user_id=? RETURNING id,created_at,updated_at,archived_at,user_id,description,
		 token_hash,scopes,academic_unit_id,expires_at,last_used_at,disabled_at,revoked_at`, actionAt, actionAt, input.ID, input.UserID); err != nil {
			return nil, translateError("personal_access_token", input.ID, err)
		}
		revoked, err := row.model()
		if err != nil {
			return nil, err
		}
		notice, payloadKeyID, err := validatePersonalAccessTokenSecurityNotice(model.UserID(input.UserID), input.Notice, model.MailTemplateIdentityPersonalAccessTokenRevoked, revoked.ExpiresAt, actionAt)
		if err != nil {
			return nil, err
		}
		if err := insertSecurityNoticeMail(ctx, tx, notice.Occurrence, notice.Delivery, notice.Job, payloadKeyID); err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(revoked.Auditable())
		if err != nil {
			return nil, err
		}
		if err = terminalizePersonalAccessTokenPreparation(ctx, tx, preparation, model.AuditStatusSuccess, "", encoded); err != nil {
			return nil, fmt.Errorf("terminalize personal access token revocation audit: %w", err)
		}
		return &store.PersonalAccessTokenMutationResult{Token: revoked, Fresh: true}, nil
	})
}

func validatePersonalAccessTokenSecurityNotice(userID model.UserID, notice store.PersonalAccessTokenSecurityNotice, key model.MailTemplateKey, expiresAt, actionAt time.Time) (store.PersonalAccessTokenSecurityNotice, string, error) {
	if notice.ExpiresAt.IsZero() || !model.TimeUTC(notice.ExpiresAt).Equal(model.TimeUTC(expiresAt)) {
		return store.PersonalAccessTokenSecurityNotice{}, "", store.NewErrInvalidInput("personal_access_token", "notice.expires_at", nil)
	}
	payloadKeyID, err := validateSecurityNoticeMail(userID, notice.Occurrence, notice.Delivery, notice.Job, key, actionAt.UnixMilli())
	return notice, payloadKeyID, err
}

func validatePersonalAccessTokenCandidate(candidate *model.PersonalAccessToken) error {
	if candidate == nil || candidate.Validate() != nil {
		return store.NewErrInvalidInput("personal_access_token", "value", nil)
	}
	for _, scope := range candidate.Scopes {
		if !model.IsPersonalAccessTokenAction(scope) {
			return store.NewErrInvalidInput("personal_access_token", "scopes", nil)
		}
	}
	return nil
}

func lockPersonalAccessTokensForUser(ctx context.Context, tx *sqlxTxWrapper, userID string) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "personal_access_tokens:user:"+userID); err != nil {
		return fmt.Errorf("lock personal access tokens: %w", err)
	}
	return nil
}

func personalAccessTokenDatabaseNow(ctx context.Context, tx *sqlxTxWrapper) (time.Time, error) {
	var at time.Time
	if err := tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return time.Time{}, fmt.Errorf("read personal access token database time: %w", err)
	}
	return model.TimeFromMillis(at.UnixMilli()), nil
}

func enforcePersonalAccessTokenMaximum(ctx context.Context, tx *sqlxTxWrapper, userID, excludedID string, at time.Time, maximum int) error {
	query := `SELECT COUNT(*) FROM personal_access_tokens WHERE user_id = ? AND archived_at IS NULL AND revoked_at IS NULL AND disabled_at IS NULL AND expires_at > ?`
	args := []any{userID, at}
	if excludedID != "" {
		query = `SELECT COUNT(*) FROM personal_access_tokens WHERE user_id = ? AND id <> ? AND archived_at IS NULL AND revoked_at IS NULL AND disabled_at IS NULL AND expires_at > ?`
		args = []any{userID, excludedID, at}
	}
	var active int
	if err := tx.Get(ctx, &active, query, args...); err != nil {
		return fmt.Errorf("count active personal access tokens: %w", err)
	}
	if active >= maximum {
		return store.NewErrConflict("personal_access_token", "personal_access_tokens_maximum_per_user", nil)
	}
	return nil
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
