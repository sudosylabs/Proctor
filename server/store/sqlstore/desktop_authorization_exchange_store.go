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

func validateDesktopAuthorizationExchange(input *store.DesktopAuthorizationExchange) error {
	if input == nil || !model.IsValidTokenHash(input.CodeHash) || !model.IsValidTokenHash(input.StateHash) ||
		!model.IsValidCredentialToken(input.CodeChallenge) || input.Issuer == "" ||
		!model.IsValidTokenHash(input.AccessTokenHash) || !model.IsValidTokenHash(input.RefreshTokenHash) ||
		input.AccessTokenHash == input.RefreshTokenHash || input.AccessLifetime <= 0 || input.RefreshLifetime <= 0 ||
		input.IdleLifetime <= 0 || input.AbsoluteLifetime <= 0 || input.AccessLifetime > input.IdleLifetime ||
		input.IdleLifetime > input.AbsoluteLifetime || input.RefreshLifetime > input.AbsoluteLifetime ||
		input.MaximumActive < 1 || !input.AuditEventID.IsValid() ||
		input.AuditAt.IsZero() || !validAccessDeploymentCapabilities(input.Capabilities) ||
		input.ExpectedPublicJWK.Validate() != nil || !model.IsValidDPoPKeyThumbprint(input.ExpectedKeyThumbprint) ||
		!model.IsValidDesktopRelease(input.DesktopRelease) || !model.IsValidDesktopBuildID(input.DesktopBuildID) ||
		!input.DesktopPlatform.IsValid() || !input.DesktopArchitecture.IsValid() || input.DesktopRealtimeProtocol < 1 ||
		input.DesktopCompatibilityPolicyRevision < 1 {
		return store.NewErrInvalidInput("browser_authentication_transaction", "exchange", nil)
	}
	thumbprint, _ := input.ExpectedPublicJWK.Thumbprint()
	if thumbprint != input.ExpectedKeyThumbprint {
		return store.NewErrInvalidInput("browser_authentication_transaction", "key_thumbprint", nil)
	}
	return nil
}

// Exchange redeems an issued desktop authorization code for a new desktop
// Session and completes its audit, or denies it while an active Attempt locks
// the User's Sessions.
func (s SQLBrowserAuthenticationStore) Exchange(ctx context.Context, input *store.DesktopAuthorizationExchange) (*store.DesktopAuthorizationExchangeResult, error) {
	if err := validateDesktopAuthorizationExchange(input); err != nil {
		return nil, err
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "exchange desktop authorization code", func(ctx context.Context, tx *sqlxTxWrapper) (*store.DesktopAuthorizationExchangeResult, error) {
		return exchangeDesktopAuthorizationCode(ctx, tx, input)
	})
}

// exchangeDesktopAuthorizationCode redeems an issued desktop authorization code for a desktop Session inside its transaction.
func exchangeDesktopAuthorizationCode(ctx context.Context, tx *sqlxTxWrapper, input *store.DesktopAuthorizationExchange) (*store.DesktopAuthorizationExchangeResult, error) {
	if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
		return nil, err
	}
	transaction, err := lockIssuedDesktopAuthorization(ctx, tx, input)
	if err != nil {
		return nil, err
	}
	recovery, err := requireDesktopExchangeAuthentication(ctx, tx, input, transaction)
	if err != nil {
		return nil, err
	}
	activeAttempt, err := hasActiveAttemptSessionLock(ctx, tx, transaction.UserID)
	if err != nil {
		return nil, err
	}
	if activeAttempt {
		return denyDesktopExchangeForActiveAttempt(ctx, tx, input, transaction)
	}
	var now time.Time
	if err = tx.Get(ctx, &now, "SELECT clock_timestamp()"); err != nil {
		return nil, fmt.Errorf("read desktop exchange time: %w", err)
	}
	now = model.TimeUTC(now)
	registration, err := getOrCreateDesktopRegistration(ctx, tx, transaction, now)
	if err != nil {
		return nil, err
	}
	candidate, credentials, err := prepareDesktopExchangeSession(input, transaction, registration, recovery.Generation, now)
	if err != nil {
		return nil, err
	}
	if err = insertDesktopExchangeSession(ctx, tx, input, candidate, credentials, now); err != nil {
		return nil, err
	}
	if err = completeDesktopExchange(ctx, tx, input, transaction, now); err != nil {
		return nil, err
	}
	accessExpiresAt, refreshExpiresAt, err := desktopExchangeCredentialExpiries(credentials)
	if err != nil {
		return nil, err
	}
	return &store.DesktopAuthorizationExchangeResult{
		Session: candidate, Registration: registration,
		AccessExpiresAt: accessExpiresAt, RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

// lockIssuedDesktopAuthorization locks the live code-issued transaction and requires it to match the exchanging desktop.
func lockIssuedDesktopAuthorization(ctx context.Context, tx *sqlxTxWrapper, input *store.DesktopAuthorizationExchange) (*model.BrowserAuthenticationTransaction, error) {
	var row browserAuthenticationRow
	if err := tx.Get(ctx, &row, `SELECT `+browserAuthenticationColumns+` FROM browser_authentication_transactions
		 WHERE code_hash = ? AND state_hash = ? AND code_challenge = ? AND issuer = ?
		   AND state = 'code_issued' AND created_at <= clock_timestamp() AND expires_at > clock_timestamp()
		   AND code_expires_at > clock_timestamp() FOR UPDATE`, input.CodeHash, input.StateHash,
		input.CodeChallenge, input.Issuer); err != nil {
		return nil, translateError("browser_authentication_transaction", "", err)
	}
	transaction, err := row.model()
	if err != nil {
		return nil, err
	}
	if transaction.ProposedPublicJWK != input.ExpectedPublicJWK ||
		transaction.ProposedKeyThumbprint != input.ExpectedKeyThumbprint ||
		transaction.DesktopRelease != input.DesktopRelease || transaction.DesktopBuildID != input.DesktopBuildID ||
		transaction.DesktopPlatform != input.DesktopPlatform || transaction.DesktopArchitecture != input.DesktopArchitecture ||
		transaction.DesktopRealtimeProtocol != input.DesktopRealtimeProtocol {
		return nil, store.NewErrNotFound("browser_authentication_transaction", "desktop_key_binding")
	}
	return transaction, nil
}

// requireDesktopExchangeAuthentication rechecks the policy, authentication path, MFA recovery and proof behind the code and locks the active User.
func requireDesktopExchangeAuthentication(ctx context.Context, tx *sqlxTxWrapper, input *store.DesktopAuthorizationExchange,
	transaction *model.BrowserAuthenticationTransaction,
) (*model.UserMFARecovery, error) {
	if err := requireDesktopCompatibilityPolicyRevision(ctx, tx, input.DesktopCompatibilityPolicyRevision); err != nil {
		return nil, err
	}
	if err := requireDesktopAuthenticationPath(ctx, tx, transaction.AuthenticationMethod, transaction.AuthenticationProviderID, input.Capabilities); err != nil {
		return nil, err
	}
	// Match every user-session mutation's lock order. If disable commits
	// first the active-user lock below rejects exchange; if exchange commits
	// first disable observes and revokes the new Session.
	if err := lockUserSessions(ctx, tx, transaction.UserID.String()); err != nil {
		return nil, err
	}
	recovery, recoveryErr := getMFARecoveryState(ctx, tx, transaction.UserID)
	if recoveryErr != nil {
		return nil, recoveryErr
	}
	if recovery.ReenrollmentRequired {
		return nil, store.ErrMFAReenrollmentRequired
	}
	if recovery.ResetAt.Valid && transaction.CreatedAt.Before(recovery.ResetAt.Time) {
		return nil, store.ErrAuthenticationGenerationChanged
	}
	if transaction.AuthenticationMethod == "password" {
		if err := requireCurrentPasswordProof(ctx, tx, transaction.UserID, store.PasswordCredentialProof{ID: transaction.PasswordCredentialID, Revision: transaction.PasswordCredentialRevision}); err != nil {
			return nil, err
		}
	}
	if err := requireExactExternalIdentity(ctx, tx, transaction.UserID, transaction.AuthenticationProviderID, transaction.ExternalIdentityID); err != nil {
		return nil, err
	}
	var activeUserID string
	if err := tx.Get(ctx, &activeUserID, `SELECT id FROM users
			WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL FOR UPDATE`, transaction.UserID.String()); err != nil {
		return nil, translateError("user", transaction.UserID.String(), err)
	}
	return recovery, nil
}

// hasActiveAttemptSessionLock reports whether the User has an active Attempt that locks new Sessions.
func hasActiveAttemptSessionLock(ctx context.Context, tx *sqlxTxWrapper, userID model.UserID) (bool, error) {
	var activeAttempt bool
	if err := tx.Get(ctx, &activeAttempt, `SELECT EXISTS(SELECT 1 FROM exam_attempts WHERE candidate_user_id=? AND state='active')`, userID.String()); err != nil {
		return false, fmt.Errorf("inspect active Attempt Session lock: %w", err)
	}
	return activeAttempt, nil
}

// denyDesktopExchangeForActiveAttempt denies the code-issued transaction and completes its audit as a Session-lock failure.
func denyDesktopExchangeForActiveAttempt(ctx context.Context, tx *sqlxTxWrapper, input *store.DesktopAuthorizationExchange,
	transaction *model.BrowserAuthenticationTransaction,
) (*store.DesktopAuthorizationExchangeResult, error) {
	var denied browserAuthenticationRow
	if err := tx.Get(ctx, &denied, `UPDATE browser_authentication_transactions
			   SET updated_at=terminal.at,state='denied',handle_hash=NULL,browser_proof_hash=NULL,state_hash=NULL,
			       callback_url=NULL,code_challenge=NULL,user_id=NULL,authentication_method=NULL,
			       proposed_public_jwk=NULL,proposed_key_thumbprint=NULL,desktop_release=NULL,desktop_build_id=NULL,
			       desktop_platform=NULL,desktop_architecture=NULL,desktop_realtime_protocol=NULL,
			       authentication_provider_id=NULL,external_identity_id=NULL,password_credential_id=NULL,password_credential_revision=NULL,authentication_strength=NULL,
			       authenticated_at=NULL,mfa_completed_at=NULL,code_hash=NULL,code_expires_at=NULL,
			       denied_at=terminal.at,denial_reason='active_attempt_session_lock'
			  FROM (SELECT clock_timestamp() AS at) AS terminal
			 WHERE id=? AND state='code_issued' RETURNING `+browserAuthenticationColumns, transaction.ID.String()); err != nil {
		return nil, translateError("browser_authentication_transaction", transaction.ID.String(), err)
	}
	value, modelErr := denied.model()
	if modelErr != nil {
		return nil, modelErr
	}
	encoded, encodeErr := model.EncodeAuditData(value.Auditable())
	if encodeErr != nil {
		return nil, encodeErr
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusFail,
		"authentication.desktop_authorization.account_session_locked", encoded, input.AuditAt); err != nil {
		return nil, err
	}
	return &store.DesktopAuthorizationExchangeResult{Denied: true}, nil
}

// prepareDesktopExchangeSession builds the desktop Session and its initial credentials from the authenticated transaction.
func prepareDesktopExchangeSession(input *store.DesktopAuthorizationExchange, transaction *model.BrowserAuthenticationTransaction,
	registration *model.DesktopRegistration, generation int64, now time.Time,
) (*model.Session, []*model.SessionCredential, error) {
	candidate := model.Session{
		UserID: transaction.UserID, ClientType: model.SessionClientDesktop, AuthenticationGeneration: generation,
		DesktopRegistrationID: registration.ID, DPoPKeyThumbprint: registration.KeyThumbprint,
		DesktopRelease: transaction.DesktopRelease, DesktopBuildID: transaction.DesktopBuildID,
		DesktopPlatform: transaction.DesktopPlatform, DesktopArchitecture: transaction.DesktopArchitecture,
		DesktopRealtimeProtocol: transaction.DesktopRealtimeProtocol,
		DeviceID:                transaction.DeviceID, DeviceName: transaction.DeviceName,
		AuthenticationMethod: transaction.AuthenticationMethod, AuthenticationProviderID: transaction.AuthenticationProviderID,
		ExternalIdentityID:     transaction.ExternalIdentityID,
		AuthenticationStrength: transaction.AuthenticationStrength, AuthenticatedAt: transaction.AuthenticatedAt.Time,
		MFACompletedAt: transaction.MFACompletedAt, LastActivityAt: now,
		IdleExpiresAt: now.Add(input.IdleLifetime), ExpiresAt: now.Add(input.AbsoluteLifetime),
	}
	candidate.PrepareCreate(model.NewSessionID(), now)
	if err := candidate.Validate(); err != nil {
		return nil, nil, err
	}
	credentials, err := prepareInitialSessionCredentials(&candidate, []*model.SessionCredential{
		{Kind: model.SessionCredentialAccess, TokenHash: input.AccessTokenHash, ExpiresAt: now.Add(input.AccessLifetime)},
		{Kind: model.SessionCredentialRefresh, TokenHash: input.RefreshTokenHash, ExpiresAt: now.Add(input.RefreshLifetime)},
	}, now)
	if err != nil {
		return nil, nil, err
	}
	return &candidate, credentials, nil
}

// insertDesktopExchangeSession enforces the per-User Session limit, inserts the Session and credentials and records the login.
func insertDesktopExchangeSession(ctx context.Context, tx *sqlxTxWrapper, input *store.DesktopAuthorizationExchange,
	candidate *model.Session, credentials []*model.SessionCredential, now time.Time,
) error {
	var active int
	if err := tx.Get(ctx, &active, `SELECT COUNT(*) FROM sessions WHERE user_id = ? AND archived_at IS NULL
			AND revoked_at IS NULL AND idle_expires_at > clock_timestamp() AND expires_at > clock_timestamp()`, candidate.UserID.String()); err != nil {
		return fmt.Errorf("count desktop sessions: %w", err)
	}
	if active >= input.MaximumActive {
		return store.NewErrConflict("session", "sessions_maximum_per_user", nil)
	}
	if err := insertSession(ctx, tx, candidate); err != nil {
		return err
	}
	for _, credential := range credentials {
		if err := insertSessionCredential(ctx, tx, credential); err != nil {
			return err
		}
	}
	userUpdate, err := tx.Exec(ctx, `UPDATE users SET updated_at = GREATEST(updated_at, ?),
			last_login_at = GREATEST(last_login_at, created_at, ?), last_activity_at = GREATEST(last_activity_at, created_at, ?)
			WHERE id = ? AND archived_at IS NULL AND disabled_at IS NULL`, now, now, now, candidate.UserID.String())
	if err != nil {
		return fmt.Errorf("update desktop user login time: %w", err)
	}
	if affected, affectedErr := userUpdate.RowsAffected(); affectedErr != nil || affected != 1 {
		if affectedErr != nil {
			return fmt.Errorf("read desktop user login update: %w", affectedErr)
		}
		return store.NewErrNotFound("user", candidate.UserID.String())
	}
	return nil
}

// completeDesktopExchange marks the transaction exchanged and completes its audit event.
func completeDesktopExchange(ctx context.Context, tx *sqlxTxWrapper, input *store.DesktopAuthorizationExchange,
	transaction *model.BrowserAuthenticationTransaction, now time.Time,
) error {
	var terminal browserAuthenticationRow
	if err := tx.Get(ctx, &terminal, `UPDATE browser_authentication_transactions
			SET updated_at = ?, state = 'exchanged', state_hash = NULL, callback_url = NULL,
			    code_challenge = NULL, code_hash = NULL, code_expires_at = NULL,
			    proposed_public_jwk=NULL,proposed_key_thumbprint=NULL,desktop_release=NULL,desktop_build_id=NULL,
			    desktop_platform=NULL,desktop_architecture=NULL,desktop_realtime_protocol=NULL,exchanged_at = ?
			WHERE id = ? AND state = 'code_issued' RETURNING `+browserAuthenticationColumns,
		now, now, transaction.ID.String()); err != nil {
		return translateError("browser_authentication_transaction", transaction.ID.String(), err)
	}
	terminalTransaction, err := terminal.model()
	if err != nil {
		return err
	}
	encoded, err := model.EncodeAuditData(terminalTransaction.Auditable())
	if err != nil {
		return err
	}
	if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return err
	}
	return nil
}

// desktopExchangeCredentialExpiries returns the access and refresh expiry of the exchanged credentials.
func desktopExchangeCredentialExpiries(credentials []*model.SessionCredential) (time.Time, time.Time, error) {
	var accessExpiresAt, refreshExpiresAt time.Time
	for _, credential := range credentials {
		switch credential.Kind {
		case model.SessionCredentialAccess:
			accessExpiresAt = credential.ExpiresAt
		case model.SessionCredentialRefresh:
			refreshExpiresAt = credential.ExpiresAt
		}
	}
	if accessExpiresAt.IsZero() || refreshExpiresAt.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("project desktop authorization exchange: missing credential expiry")
	}
	return accessExpiresAt, refreshExpiresAt, nil
}
