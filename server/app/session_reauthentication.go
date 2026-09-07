// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ReauthenticatePasswordCommand proves the password of the current Session's
// User. It intentionally contains no selectable User or login identifier.
type ReauthenticatePasswordCommand struct {
	Password string
	Source   string
}

func (a *App) ReauthenticatePassword(ctx context.Context, invocation Invocation, command ReauthenticatePasswordCommand) (*model.Session, error) {
	return a.authentication.ReauthenticatePassword(ctx, invocation, command)
}

func (s *authenticationService) ReauthenticatePassword(ctx context.Context, invocation Invocation, command ReauthenticatePasswordCommand) (*model.Session, error) {
	principal := invocation.Principal()
	if principal.ValidateMFARecovery() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, invalidTokenAppError()
	}
	// A Session retains its original authentication method and provider. A
	// password cannot silently substitute for an external identity's proof.
	if principal.AuthenticationMethod != "password" || principal.AuthenticationProviderID != "" {
		return nil, NewError("authentication.reauthentication_method_required")
	}
	receipt, limited, err := s.attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeReauthentication, window: s.loginRateLimit.Window,
		limits: []authenticationAttemptLimit{
			{dimension: authenticationAttemptDimensionIdentitySource, maximum: s.loginRateLimit.MaximumAttempts, identity: principal.UserID.String(), source: command.Source},
			{dimension: authenticationAttemptDimensionSource, maximum: s.loginRateLimit.MaximumSourceAttempts, source: command.Source},
		},
	})
	if err != nil {
		return nil, rateLimitUnavailableAppError(err)
	}
	if limited {
		return nil, NewError("authentication.rate_limited")
	}
	allowed, err := s.accessPolicy.AllowsLocalLogin(ctx)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	if !allowed || command.Password == "" || len(command.Password) > s.hasher.maximumLength {
		return nil, invalidCredentialsAppError()
	}
	user, err := s.users.Get(ctx, principal.UserID.String())
	if err != nil {
		return nil, reauthenticationFailure(err)
	}
	if !user.IsActive() {
		return nil, invalidCredentialsAppError()
	}
	credential, err := s.passwords.GetByUser(ctx, principal.UserID.String())
	if err != nil {
		return nil, reauthenticationFailure(err)
	}
	if err := s.hasher.Verify(ctx, credential.PasswordHash, command.Password); err != nil {
		if errors.Is(err, ErrPasswordMismatch) {
			return nil, invalidCredentialsAppError()
		}
		return nil, passwordWorkError(err, "authentication.internal")
	}
	result, err := runAuditedMutation(ctx, s.audit, mutationAttempt{
		Invocation: invocation, Action: "authentication.reauthenticate",
		Resource:  model.Resource{Type: model.ResourceUser, ID: principal.UserID.String()},
		Operation: "password", Value: map[string]any{"session_id": principal.SessionID.String()},
	}, s.now, func(ctx context.Context, reference mutationAttemptReference) (*store.SessionReauthenticationResult, error) {
		auditID, err := model.ParseAuditEventID(reference.ID)
		if err != nil {
			return nil, err
		}
		credentialID, err := model.ParseSessionCredentialID(principal.CredentialID.String())
		if err != nil {
			return nil, err
		}
		return s.sessions.ReauthenticatePasswordWithAudit(ctx, &store.SessionPasswordReauthentication{
			SessionID: principal.SessionID, UserID: principal.UserID, CredentialID: credentialID,
			PasswordProof: store.PasswordCredentialProof{ID: credential.ID, Revision: credential.Revision},
			AuditEventID:  auditID,
		})
	}, reauthenticationFailure)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Session == nil {
		return nil, authenticationUnavailable(errors.New("reauthentication returned no Session"))
	}
	s.securityEffects.AuthenticationCacheInvalidated(ctx, principal.UserID.String(), result.AccessTokenHashes)
	if err := s.attempts.reset(ctx, receipt, authenticationAttemptDimensionIdentitySource); err != nil {
		s.warn(ctx, "reauthentication rate-limit reset failed", err)
	}
	return result.Session, nil
}

func reauthenticationFailure(err error) error {
	if store.IsNotFound(err) || store.IsConflict(err) || errors.Is(err, store.ErrPasswordCredentialChanged) || errors.Is(err, store.ErrAuthenticationMethodDisabled) || errors.Is(err, store.ErrAuthenticationGenerationChanged) || errors.Is(err, store.ErrMFAReenrollmentRequired) {
		return invalidCredentialsAppError()
	}
	return authenticationUnavailable(err)
}
