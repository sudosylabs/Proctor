// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ConsumeEmailChangeUndo restores the previous address an undo credential
// names, revokes every Session of the User, and audits the undo in one
// transaction. The per-User Session lock is taken before the token and User
// rows, matching password reset.
func (s SQLUserTokenStore) ConsumeEmailChangeUndo(ctx context.Context, input *store.EmailChangeUndo) (*store.EmailChangeUndoResult, error) {
	if input == nil || !model.IsValidTokenHash(input.TokenHash) || !input.RevocationReason.IsValid() || input.AuditEvent == nil {
		return nil, store.NewErrInvalidInput("user_token", "email_change_undo", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "email change undo", func(ctx context.Context, tx *sqlxTxWrapper) (*store.EmailChangeUndoResult, error) {
		var userID string
		if err := tx.Get(ctx, &userID, `SELECT user_id FROM user_tokens WHERE token_hash=? AND purpose=?`, input.TokenHash, model.UserTokenEmailChangeUndo); err != nil {
			return nil, translateError("user_token", "", err)
		}
		if err := lockUserSessions(ctx, tx, userID); err != nil {
			return nil, err
		}
		if err := lockUserTokenPurpose(ctx, tx, userID, model.UserTokenEmailChangeUndo); err != nil {
			return nil, err
		}
		for _, purpose := range []model.UserTokenPurpose{model.UserTokenEmailVerification, model.UserTokenPasswordReset} {
			if err := lockUserTokenPurpose(ctx, tx, userID, purpose); err != nil {
				return nil, err
			}
		}
		databaseNow, err := jobDatabaseNow(ctx, tx)
		if err != nil {
			return nil, err
		}
		token, err := lockActiveUserToken(ctx, tx, input.TokenHash, model.UserTokenEmailChangeUndo, databaseNow)
		if err != nil {
			return nil, err
		}
		var user userRow
		if err := tx.Get(ctx, &user, `SELECT id, created_at, updated_at, archived_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at
		       , default_profile_picture_seed, default_profile_picture_file_id,
		       custom_profile_picture_file_id, profile_picture_changed_at
		  FROM users WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL FOR UPDATE`, token.UserID); err != nil {
			return nil, translateError("user_token", "", err)
		}
		var holder string
		err = tx.Get(ctx, &holder, `SELECT id FROM users WHERE email = ? AND id <> ? AND archived_at IS NULL`, token.Target, token.UserID)
		if err == nil {
			return nil, store.NewErrConflict("user", "email_change_undo_target", nil)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("check email change undo target: %w", err)
		}
		at := transitionInstant(databaseNow, token.CreatedAt, token.UpdatedAt, user.UpdatedAt)
		mailEligibilityRevision, err := advanceUserMailEligibilityRevision(ctx, tx)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET email=?, email_verified=true, mail_eligibility_revision=?, updated_at=GREATEST(updated_at, ?), revision=revision+1
			WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL`, token.Target, mailEligibilityRevision, at, token.UserID); err != nil {
			return nil, translateError("user", token.UserID, err)
		}
		var superseded []string
		if err := tx.Select(ctx, &superseded, `SELECT id FROM user_tokens WHERE user_id=? AND purpose IN ('email_verification', 'password_reset') AND archived_at IS NULL AND consumed_at IS NULL FOR UPDATE`, token.UserID); err != nil {
			return nil, fmt.Errorf("lock superseded account tokens: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE user_tokens SET updated_at=GREATEST(updated_at, ?), archived_at=GREATEST(updated_at, ?) WHERE user_id=? AND purpose IN ('email_verification', 'password_reset') AND archived_at IS NULL AND consumed_at IS NULL`, at, at, token.UserID); err != nil {
			return nil, fmt.Errorf("invalidate superseded account tokens: %w", err)
		}
		if err := suppressSupersededTokenMail(ctx, tx, superseded, at); err != nil {
			return nil, err
		}
		if err := consumeUserTokens(ctx, tx, token.UserID, model.UserTokenEmailChangeUndo, at); err != nil {
			return nil, err
		}
		rows, hashes, err := revokeAllUserSessionsAt(ctx, tx, token.UserID, at, input.RevocationReason)
		if err != nil {
			return nil, err
		}
		event, err := tokenAuditEvent(input.AuditEvent, token.UserID, model.ActionAuthenticationEmailChangeUndo)
		if err != nil {
			return nil, err
		}
		if _, err := insertAuditEventAt(ctx, tx, event, at); err != nil {
			return nil, fmt.Errorf("audit email change undo: %w", err)
		}
		restored, err := getUserByID(ctx, tx, token.UserID)
		if err != nil {
			return nil, err
		}
		sessions, err := revokedSessionModelsAt(rows, at, input.RevocationReason)
		if err != nil {
			return nil, err
		}
		return &store.EmailChangeUndoResult{User: restored, RevokedSessions: sessions, RevokedAccessHashes: hashes}, nil
	})
}
