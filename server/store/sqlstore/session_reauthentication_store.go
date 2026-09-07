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
	"unicode/utf8"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s SQLSessionStore) ReauthenticatePasswordWithAudit(
	ctx context.Context,
	input *store.SessionPasswordReauthentication,
) (*store.SessionReauthenticationResult, error) {
	if input == nil || !input.SessionID.IsValid() || !input.CredentialID.IsValid() ||
		!input.UserID.IsValid() || !input.AuditEventID.IsValid() || !input.PasswordProof.IsValid() {
		return nil, store.NewErrInvalidInput("session", "reauthentication", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "password reauthentication", func(ctx context.Context, tx *sqlxTxWrapper) (*store.SessionReauthenticationResult, error) {
		if err := requireCurrentLocalLogin(ctx, tx); err != nil {
			return nil, err
		}
		if err := lockUserSessions(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		if err := requireCurrentPasswordProof(ctx, tx, input.UserID, input.PasswordProof); err != nil {
			return nil, err
		}
		var at time.Time
		if err := tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read password reauthentication time: %w", err)
		}
		var row sessionRow
		query := s.sessionsQuery.Where(sq.Eq{
			"sessions.id": input.SessionID.String(), "sessions.user_id": input.UserID.String(),
			"sessions.authentication_method": "password", "sessions.authentication_provider_id": "",
			"sessions.archived_at": nil, "sessions.revoked_at": nil,
		}).Where(sq.Gt{"sessions.idle_expires_at": at, "sessions.expires_at": at}).
			Where(`EXISTS (SELECT 1 FROM users WHERE id=sessions.user_id AND archived_at IS NULL AND disabled_at IS NULL)`).
			Where(`EXISTS (SELECT 1 FROM session_credentials WHERE id=? AND session_id=sessions.id AND kind='access' AND archived_at IS NULL AND revoked_at IS NULL AND expires_at>?)`, input.CredentialID.String(), at).
			Suffix("FOR UPDATE")
		if err := tx.GetBuilder(ctx, &row, query); err != nil {
			return nil, translateError("session", input.SessionID.String(), err)
		}
		session, err := row.model()
		if err != nil {
			return nil, err
		}
		return commitSessionReauthentication(ctx, tx, session, at, input.AuditEventID.String())
	})
}

func commitSessionReauthentication(ctx context.Context, tx *sqlxTxWrapper, session *model.Session, at time.Time, auditID string) (*store.SessionReauthenticationResult, error) {
	if err := requireSessionAuthenticationGeneration(ctx, tx, session); err != nil {
		return nil, err
	}
	if at.Before(session.AuthenticatedAt) || (session.ReauthenticatedAt.Valid && at.Before(session.ReauthenticatedAt.Time)) {
		return nil, store.NewErrConflict("session", "reauthentication_time", nil)
	}
	session.ReauthenticatedAt = model.OptionalTimeFrom(at)
	if at.After(session.UpdatedAt) {
		session.UpdatedAt = at
	}
	if err := session.Validate(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET reauthenticated_at=?, updated_at=? WHERE id=?`, at, session.UpdatedAt, session.ID.String()); err != nil {
		return nil, fmt.Errorf("refresh password authentication: %w", err)
	}
	hashes, err := selectActiveAccessTokenHashes(ctx, tx, session.ID.String())
	if err != nil {
		return nil, err
	}
	encoded, err := model.EncodeAuditData(session.Auditable())
	if err != nil {
		return nil, err
	}
	if _, err := completeAuditEvent(ctx, tx, auditID, model.AuditStatusSuccess, "", encoded, at.UnixMilli()); err != nil {
		return nil, fmt.Errorf("complete password reauthentication audit: %w", err)
	}
	return &store.SessionReauthenticationResult{Session: session, AccessTokenHashes: hashes}, nil
}

func (s SQLSessionStore) ReauthenticateExternalWithAudit(ctx context.Context, input *store.SessionExternalReauthentication) (*store.SessionReauthenticationResult, error) {
	if input == nil || !input.StateID.IsValid() || !model.IsValidIdentityProviderID(input.ProviderID) || input.Subject == "" || utf8.RuneCountInString(input.Subject) > model.IdentitySubjectMaxRunes || input.AuthenticatedAt.IsZero() {
		return nil, store.NewErrInvalidInput("session", "reauthentication", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "external reauthentication", func(ctx context.Context, tx *sqlxTxWrapper) (*store.SessionReauthenticationResult, error) {
		if err := requireCurrentExternalProvider(ctx, tx, input.ProviderID); err != nil {
			return nil, err
		}
		var userID string
		if err := tx.Get(ctx, &userID, `SELECT target_user_id FROM external_login_states WHERE id=? AND purpose='reauthenticate'`, input.StateID.String()); err != nil {
			return nil, translateError("external_login_state", input.StateID.String(), err)
		}
		if err := lockUserSessions(ctx, tx, userID); err != nil {
			return nil, err
		}
		var stateRow externalLoginStateRow
		query := s.getQueryBuilder().Select(externalLoginStateSliceColumns()...).From("external_login_states").Where(sq.Eq{"external_login_states.id": input.StateID.String(), "external_login_states.provider": input.ProviderID, "external_login_states.purpose": "reauthenticate"}).Suffix("FOR UPDATE")
		if err := tx.GetBuilder(ctx, &stateRow, query); err != nil {
			return nil, translateError("external_login_state", input.StateID.String(), err)
		}
		state, err := stateRow.model()
		if err != nil {
			return nil, err
		}
		if !state.ConsumedAt.Valid {
			return nil, store.NewErrConflict("external_login_state", "not_consumed", nil)
		}
		var at time.Time
		if err := tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
			return nil, err
		}
		recovery, err := getMFARecoveryState(ctx, tx, state.TargetUserID)
		if err != nil {
			return nil, err
		}
		if recovery.ResetAt.Valid && state.CreatedAt.Before(recovery.ResetAt.Time) {
			return nil, store.ErrAuthenticationGenerationChanged
		}
		proofAt := model.TimeUTC(input.AuthenticatedAt)
		if !at.Before(state.ExpiresAt) || proofAt.Before(state.CreatedAt.Truncate(time.Second)) || proofAt.After(at) {
			return nil, store.NewErrConflict("session", "reauthentication_time", nil)
		}
		var exact bool
		if err := tx.Get(ctx, &exact, `SELECT true FROM external_identities WHERE id=? AND user_id=? AND provider=? AND subject=? AND archived_at IS NULL FOR SHARE`, state.ExternalIdentityID.String(), state.TargetUserID.String(), input.ProviderID, input.Subject); err != nil {
			return nil, translateError("external_identity", "", err)
		}
		var row sessionRow
		sessionQuery := s.sessionsQuery.Where(sq.Eq{"sessions.id": state.SessionID.String(), "sessions.user_id": state.TargetUserID.String(), "sessions.authentication_provider_id": input.ProviderID, "sessions.external_identity_id": state.ExternalIdentityID.String(), "sessions.archived_at": nil, "sessions.revoked_at": nil}).Where(sq.Gt{"sessions.idle_expires_at": at, "sessions.expires_at": at}).Where(`EXISTS(SELECT 1 FROM users WHERE id=sessions.user_id AND archived_at IS NULL AND disabled_at IS NULL)`).Where(`EXISTS(SELECT 1 FROM session_credentials WHERE id=? AND session_id=sessions.id AND kind='access' AND archived_at IS NULL AND revoked_at IS NULL AND expires_at>?)`, state.SessionCredentialID.String(), at).Suffix("FOR UPDATE")
		if err := tx.GetBuilder(ctx, &row, sessionQuery); err != nil {
			return nil, translateError("session", state.SessionID.String(), err)
		}
		session, err := row.model()
		if err != nil {
			return nil, err
		}
		return commitSessionReauthentication(ctx, tx, session, proofAt, state.AuditEventID)
	})
}
