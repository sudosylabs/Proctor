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

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// sessionRetiredAt is the instant a Session stopped being usable: revocation,
// absolute expiry, or idle expiry, whichever came first. The baseline indexes
// the same expression.
const sessionRetiredAt = `LEAST(sessions.expires_at, sessions.idle_expires_at, COALESCE(sessions.revoked_at, 'infinity'::timestamptz))`

// sessionCredentialRetiredAt is the instant a credential stopped being usable.
const sessionCredentialRetiredAt = `LEAST(session_credentials.expires_at, COALESCE(session_credentials.revoked_at, 'infinity'::timestamptz))`

// sessionUnreferenced holds while no durable record outside the Session's own
// credentials names the Session or one of its credentials. Audit, examination
// and authentication records keep their Session until their own retention
// removes them.
const sessionUnreferenced = `
	NOT EXISTS (SELECT 1 FROM audit_events r WHERE r.session_id = sessions.id)
	AND NOT EXISTS (SELECT 1 FROM exam_attempt_participations r WHERE r.session_id = sessions.id)
	AND NOT EXISTS (SELECT 1 FROM exam_attempt_connections r WHERE r.session_id = sessions.id)
	AND NOT EXISTS (SELECT 1 FROM browser_activity_sources r WHERE r.session_id = sessions.id)
	AND NOT EXISTS (SELECT 1 FROM personal_access_token_mutation_preparations r WHERE r.session_id = sessions.id)
	AND NOT EXISTS (SELECT 1 FROM external_login_states r WHERE r.session_id = sessions.id)
	AND NOT EXISTS (SELECT 1 FROM exam_security_preflights r WHERE r.session_id = sessions.id)
	AND NOT EXISTS (SELECT 1 FROM exam_attempt_security_owners r WHERE r.session_id = sessions.id)
	AND NOT EXISTS (SELECT 1 FROM session_credentials own
		JOIN external_login_states r ON r.session_credential_id = own.id
		WHERE own.session_id = sessions.id)
	AND NOT EXISTS (SELECT 1 FROM session_credentials own
		JOIN session_credentials other ON other.parent_id = own.id OR other.replaced_by_id = own.id
		WHERE own.session_id = sessions.id AND other.session_id <> sessions.id)`

// PurgeRetired deletes Sessions retired longer than Grace ago, with their
// credentials, examining at most Limit retired Sessions after the cursor.
func (s SQLSessionStore) PurgeRetired(ctx context.Context, input *store.SessionPurge) (*store.SessionPurgeResult, error) {
	if input == nil || input.Grace <= 0 || input.Limit < 1 || input.Limit > store.MaximumSessionPurgeBatch ||
		(input.After.RetiredAt.IsZero() != input.After.SessionID.IsZero()) ||
		(!input.After.SessionID.IsZero() && !input.After.SessionID.IsValid()) {
		return nil, store.NewErrInvalidInput("session", "purge", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "session purge", func(ctx context.Context, tx *sqlxTxWrapper) (*store.SessionPurgeResult, error) {
		after := input.After.RetiredAt
		if after.IsZero() {
			after = time.Unix(0, 0).UTC()
		}
		var examined []struct {
			ID        string    `db:"id"`
			RetiredAt time.Time `db:"retired_at"`
		}
		if err := tx.Select(ctx, &examined, `
			SELECT sessions.id, `+sessionRetiredAt+` AS retired_at
			  FROM sessions
			 WHERE `+sessionRetiredAt+` < CURRENT_TIMESTAMP - (? * interval '1 millisecond')
			   AND (`+sessionRetiredAt+`, sessions.id) > (?, ?)
			 ORDER BY `+sessionRetiredAt+`, sessions.id
			 LIMIT ?`,
			input.Grace.Milliseconds(), after, input.After.SessionID.String(), input.Limit); err != nil {
			return nil, fmt.Errorf("examine retired sessions: %w", translateError("session", "", err))
		}
		result := &store.SessionPurgeResult{Examined: len(examined)}
		if len(examined) == 0 {
			return result, nil
		}
		last := examined[len(examined)-1]
		result.Next = store.SessionPurgePosition{RetiredAt: model.TimeUTC(last.RetiredAt), SessionID: model.SessionID(last.ID)}
		ids := make([]string, 0, len(examined))
		for _, row := range examined {
			ids = append(ids, row.ID)
		}
		var purgeable []string
		if err := tx.Select(ctx, &purgeable, `
			SELECT sessions.id FROM sessions
			 WHERE sessions.id = ANY(?) AND `+sessionUnreferenced+`
			 ORDER BY sessions.id
			 FOR UPDATE OF sessions SKIP LOCKED`, pq.Array(ids)); err != nil {
			return nil, fmt.Errorf("select purgeable sessions: %w", translateError("session", "", err))
		}
		if len(purgeable) == 0 {
			return result, nil
		}
		credentials, err := tx.Exec(ctx, `DELETE FROM session_credentials WHERE session_id = ANY(?)`, pq.Array(purgeable))
		if err != nil {
			return nil, fmt.Errorf("delete retired session credentials: %w", translateError("session_credential", "", err))
		}
		if result.Credentials, err = credentials.RowsAffected(); err != nil {
			return nil, fmt.Errorf("count retired session credentials: %w", err)
		}
		sessions, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = ANY(?)`, pq.Array(purgeable))
		if err != nil {
			return nil, fmt.Errorf("delete retired sessions: %w", translateError("session", "", err))
		}
		if result.Sessions, err = sessions.RowsAffected(); err != nil {
			return nil, fmt.Errorf("count retired sessions: %w", err)
		}
		return result, nil
	})
}

// PurgeRetired deletes up to Limit credentials of any Session retired longer
// than Grace ago. A credential's lineage links from surviving credentials are
// cleared first; replay detection still sees a surviving parent's use.
func (s SQLSessionCredentialStore) PurgeRetired(ctx context.Context, input *store.SessionCredentialPurge) (int64, error) {
	if input == nil || input.Grace <= 0 || input.Limit < 1 || input.Limit > store.MaximumSessionPurgeBatch {
		return 0, store.NewErrInvalidInput("session_credential", "purge", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "session credential purge", func(ctx context.Context, tx *sqlxTxWrapper) (int64, error) {
		var ids []string
		if err := tx.Select(ctx, &ids, `
			SELECT session_credentials.id FROM session_credentials
			 WHERE `+sessionCredentialRetiredAt+` < CURRENT_TIMESTAMP - (? * interval '1 millisecond')
			   AND NOT EXISTS (SELECT 1 FROM external_login_states r WHERE r.session_credential_id = session_credentials.id)
			 ORDER BY `+sessionCredentialRetiredAt+`, session_credentials.id
			 LIMIT ? FOR UPDATE SKIP LOCKED`, input.Grace.Milliseconds(), input.Limit); err != nil {
			return 0, fmt.Errorf("select retired session credentials: %w", translateError("session_credential", "", err))
		}
		if len(ids) == 0 {
			return 0, nil
		}
		array := pq.Array(ids)
		if _, err := tx.Exec(ctx, `
			UPDATE session_credentials
			   SET parent_id = CASE WHEN parent_id = ANY(?) THEN NULL ELSE parent_id END,
			       replaced_by_id = CASE WHEN replaced_by_id = ANY(?) THEN NULL ELSE replaced_by_id END,
			       updated_at = GREATEST(updated_at, CURRENT_TIMESTAMP)
			 WHERE (parent_id = ANY(?) OR replaced_by_id = ANY(?)) AND NOT (id = ANY(?))`,
			array, array, array, array, array); err != nil {
			return 0, fmt.Errorf("detach retired session credential lineage: %w", translateError("session_credential", "", err))
		}
		deleted, err := tx.Exec(ctx, `DELETE FROM session_credentials WHERE id = ANY(?)`, array)
		if err != nil {
			return 0, fmt.Errorf("delete retired session credentials: %w", translateError("session_credential", "", err))
		}
		return deleted.RowsAffected()
	})
}
