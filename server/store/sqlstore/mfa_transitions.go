// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// MFA mutations take the User Session fence before the factor fence, matching
// assisted reset. Every operation then uses a post-lock database instant.
func lockMFASessionUser(ctx context.Context, tx *sqlxTxWrapper, userID string) error {
	if err := lockUserSessions(ctx, tx, userID); err != nil {
		return err
	}
	return lockMFAUser(ctx, tx, userID)
}

func currentMFASession(ctx context.Context, tx *sqlxTxWrapper, principal model.Principal, recoveryAllowed bool, recentTTL time.Duration, strong bool) (*model.Session, time.Time, error) {
	if principal.ValidateMFARecovery() != nil || principal.CredentialType != model.CredentialSessionAccess || (!recoveryAllowed && principal.MFARecoveryRequired) {
		return nil, time.Time{}, store.ErrMFAReenrollmentRequired
	}
	var at time.Time
	if err := tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, at, err
	}
	var row sessionRow
	query := sq.Select(sessionSliceColumns()...).From("sessions").PlaceholderFormat(sq.Question).
		Where(sq.Eq{"sessions.id": principal.SessionID.String(), "sessions.user_id": principal.UserID.String(), "sessions.archived_at": nil, "sessions.revoked_at": nil}).
		Where(sq.Gt{"sessions.idle_expires_at": at, "sessions.expires_at": at}).
		Where(`EXISTS(SELECT 1 FROM session_credentials WHERE id=? AND session_id=sessions.id AND kind='access' AND archived_at IS NULL AND revoked_at IS NULL AND expires_at>?)`, principal.CredentialID.String(), at).Suffix("FOR UPDATE")
	if err := tx.GetBuilder(ctx, &row, query); err != nil {
		return nil, at, translateError("session", principal.SessionID.String(), err)
	}
	session, err := row.model()
	if err != nil {
		return nil, at, err
	}
	// Other operations can hold the Session row without taking the MFA fence.
	// Its lock may have waited past any of the deadlines used by the SELECT.
	if err = tx.Get(ctx, &at, `SELECT clock_timestamp()`); err != nil {
		return nil, at, err
	}
	at = model.TimeUTC(at)
	if session.IsExpiredAt(at) {
		return nil, at, store.NewErrConflict("session", "expired", nil)
	}
	var credentialActive bool
	if err = tx.Get(ctx, &credentialActive, `SELECT EXISTS(SELECT 1 FROM session_credentials
		WHERE id=? AND session_id=? AND kind='access' AND archived_at IS NULL AND revoked_at IS NULL AND expires_at>?)`,
		principal.CredentialID.String(), session.ID.String(), at); err != nil {
		return nil, at, err
	}
	if !credentialActive {
		return nil, at, store.NewErrConflict("session", "credential", nil)
	}
	if err := requireSessionAuthenticationGeneration(ctx, tx, session); err != nil {
		return nil, at, err
	}
	if session.AuthenticationGeneration != principal.AuthenticationGeneration || session.MFARecoveryRequired != principal.MFARecoveryRequired || (!recoveryAllowed && session.MFARecoveryRequired) {
		return nil, at, store.ErrAuthenticationGenerationChanged
	}
	current := principal
	current.AuthenticatedAt = session.AuthenticatedAt
	current.ReauthenticatedAt = session.ReauthenticatedAt
	current.MFACompletedAt = session.MFACompletedAt
	current.AuthenticationStrength = session.AuthenticationStrength
	if (recentTTL > 0 && !current.IsRecentlyAuthenticated(at, recentTTL)) || (strong && !current.HasStrongAuthentication()) {
		return nil, at, store.NewErrConflict("session", "assurance", nil)
	}
	return session, at, nil
}

func (s SQLMFAStore) SavePendingWithAudit(ctx context.Context, input *store.MFAPendingEnrollment) (*model.MFACredential, error) {
	if input == nil || input.Credential == nil || !input.Credential.ID.IsZero() || input.Credential.UserID != input.Principal.UserID || input.Lifetime <= 0 || input.Lifetime > time.Hour || input.RecentAuthenticationTTL <= 0 || !input.AuditEventID.IsValid() {
		return nil, store.NewErrInvalidInput("mfa_credential", "enrollment", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "MFA enrollment", func(ctx context.Context, tx *sqlxTxWrapper) (*model.MFACredential, error) {
		if err := lockMFASessionUser(ctx, tx, input.Principal.UserID.String()); err != nil {
			return nil, err
		}
		_, at, err := currentMFASession(ctx, tx, input.Principal, true, input.RecentAuthenticationTTL, false)
		if err != nil {
			return nil, err
		}
		var active bool
		if err := tx.Get(ctx, &active, `SELECT EXISTS(SELECT 1 FROM mfa_credentials WHERE user_id=? AND archived_at IS NULL AND state='active')`, input.Principal.UserID.String()); err != nil {
			return nil, err
		}
		if active {
			return nil, store.NewErrConflict("mfa_credential", "mfa_already_enabled", nil)
		}
		candidate := *input.Credential
		candidate.PendingExpiresAt = model.OptionalTimeFrom(at.Add(input.Lifetime))
		candidate.PrepareCreate(model.NewMFACredentialID(), at)
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE mfa_credentials SET updated_at=GREATEST(updated_at,?),archived_at=? WHERE user_id=? AND archived_at IS NULL`, at, at, candidate.UserID.String()); err != nil {
			return nil, err
		}
		if err := insertMFACredential(ctx, tx, &candidate); err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(candidate.Auditable())
		if err != nil {
			return nil, err
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID.String(), model.AuditStatusSuccess, "", encoded, at.UnixMilli()); err != nil {
			return nil, err
		}
		return &candidate, nil
	})
}

func (s SQLMFAStore) ChallengeWithAudit(ctx context.Context, input *store.MFAChallenge) (*store.SessionReauthenticationResult, error) {
	if input == nil || !input.CredentialID.IsValid() || !input.AuditEventID.IsValid() || input.VerifiedAt.IsZero() || ((input.TimeStep <= 0) == (input.RecoveryCodeHash == "")) || (input.RecoveryCodeHash != "" && !model.IsValidTokenHash(input.RecoveryCodeHash)) {
		return nil, store.NewErrInvalidInput("mfa_credential", "challenge", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "MFA challenge", func(ctx context.Context, tx *sqlxTxWrapper) (*store.SessionReauthenticationResult, error) {
		if err := lockMFASessionUser(ctx, tx, input.Principal.UserID.String()); err != nil {
			return nil, err
		}
		_, at, err := currentMFASession(ctx, tx, input.Principal, false, 0, false)
		if err != nil {
			return nil, err
		}
		if input.VerifiedAt.After(at.Add(time.Minute)) || input.VerifiedAt.Before(at.Add(-time.Minute)) {
			return nil, store.NewErrConflict("mfa_credential", "verification_expired", nil)
		}
		if err := consumeExactMFAFactor(ctx, tx, input.Principal.UserID, input.CredentialID, input.TimeStep, input.RecoveryCodeHash, at); err != nil {
			return nil, err
		}
		session, err := upgradeSessionAuthentication(ctx, tx, input.Principal.SessionID.String(), input.Principal.UserID.String(), at)
		if err != nil {
			return nil, err
		}
		hashes, err := selectActiveAccessTokenHashes(ctx, tx, session.ID.String())
		if err != nil {
			return nil, err
		}
		encoded, err := model.EncodeAuditData(session.Auditable())
		if err != nil {
			return nil, err
		}
		if _, err := completeAuditEvent(ctx, tx, input.AuditEventID.String(), model.AuditStatusSuccess, "", encoded, at.UnixMilli()); err != nil {
			return nil, err
		}
		return &store.SessionReauthenticationResult{Session: session, AccessTokenHashes: hashes}, nil
	})
}

func consumeExactMFAFactor(ctx context.Context, tx *sqlxTxWrapper, userID model.UserID, credentialID model.MFACredentialID, step int64, recoveryHash string, at time.Time) error {
	if step > 0 {
		currentStep := at.Unix() / 30
		if step < currentStep-1 || step > currentStep+1 {
			return store.NewErrConflict("mfa_credential", "verification_expired", nil)
		}
		result, err := tx.Exec(ctx, `UPDATE mfa_credentials SET updated_at=GREATEST(updated_at,?),last_used_time_step=? WHERE id=? AND user_id=? AND archived_at IS NULL AND state='active' AND last_used_time_step<?`, at, step, credentialID.String(), userID.String(), step)
		if err != nil {
			return err
		}
		return requireAffected(result, "mfa_credential", credentialID.String())
	}
	var active bool
	if err := tx.Get(ctx, &active, `SELECT true FROM mfa_credentials WHERE id=? AND user_id=? AND archived_at IS NULL AND state='active' FOR UPDATE`, credentialID.String(), userID.String()); err != nil {
		return translateError("mfa_credential", credentialID.String(), err)
	}
	result, err := tx.Exec(ctx, `UPDATE mfa_recovery_codes SET updated_at=GREATEST(updated_at,?),consumed_at=? WHERE user_id=? AND code_hash=? AND archived_at IS NULL AND consumed_at IS NULL`, at, at, userID.String(), recoveryHash)
	if err != nil {
		return err
	}
	return requireAffected(result, "mfa_recovery_code", "")
}
