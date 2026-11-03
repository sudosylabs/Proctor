// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// refusePasswordReuse compares the new password with the User's Limit most
// recent password hashes. Callers hold the per-User Session lock and the
// credential row, so no concurrent change can add a hash between this check
// and the write.
func refusePasswordReuse(ctx context.Context, tx *sqlxTxWrapper, userID model.UserID, check store.PasswordReuseCheck) error {
	if check.Limit == 0 {
		return nil
	}
	var hashes []string
	if err := tx.Select(ctx, &hashes, `
		SELECT password_hash FROM (
			SELECT password_hash, password_changed_at FROM password_credentials WHERE user_id = ?
			UNION ALL
			SELECT password_hash, password_changed_at FROM password_history_entries WHERE user_id = ?
		) recent
		ORDER BY password_changed_at DESC
		LIMIT ?`, userID.String(), userID.String(), check.Limit); err != nil {
		return fmt.Errorf("read password history: %w", err)
	}
	for _, encoded := range hashes {
		matched, err := check.Matcher.Matches(ctx, encoded)
		if err != nil {
			return err
		}
		if matched {
			return store.ErrPasswordReused
		}
	}
	return nil
}

// retainSupersededPassword moves a password hash replaced in place into the
// User's history and prunes the history to limit entries, newest first. Limit
// zero discards the history entirely.
func retainSupersededPassword(ctx context.Context, tx *sqlxTxWrapper, userID model.UserID, supersededHash string, changedAt, at time.Time, limit int) error {
	if limit > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO password_history_entries (id, created_at, user_id, password_hash, password_changed_at) VALUES (?, GREATEST(?::timestamptz, ?::timestamptz), ?, ?, ?)`,
			model.NewID(), model.TimeUTC(at), model.TimeUTC(changedAt), userID.String(), supersededHash, model.TimeUTC(changedAt)); err != nil {
			return fmt.Errorf("retain superseded password: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM password_history_entries
		 WHERE user_id = ? AND id NOT IN (
			SELECT id FROM password_history_entries WHERE user_id = ?
			 ORDER BY password_changed_at DESC, id DESC LIMIT ?)`, userID.String(), userID.String(), limit); err != nil {
		return fmt.Errorf("prune password history: %w", err)
	}
	return nil
}
