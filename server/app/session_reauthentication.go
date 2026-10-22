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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ReauthenticatePasswordCommand proves the password of the current Session's
// User. It intentionally contains no selectable User or login identifier.
type ReauthenticatePasswordCommand struct {
	Password string
	Source   string
}

func (s *authenticationService) ReauthenticatePassword(ctx context.Context, invocation invocation.Invocation, command ReauthenticatePasswordCommand) (*model.Session, error) {
	principal := invocation.Principal()
	if principal.ValidateMFARecovery() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return nil, invalidTokenAppError()
	}
	// A Session retains its original authentication method and provider. A
	// password cannot silently substitute for an external identity's proof.
	if principal.AuthenticationMethod != "password" || principal.AuthenticationProviderID != "" {
		return nil, apperror.New("authentication.reauthentication_method_required")
	}
	receipt, limited, err := s.deps.Attempts.account(ctx, authenticationAttemptIntent{
		purpose: authenticationAttemptPurposeReauthentication, window: s.deps.LoginRateLimit.Window,
		limits: []authenticationAttemptLimit{
			{dimension: authenticationAttemptDimensionIdentitySource, maximum: s.deps.LoginRateLimit.MaximumAttempts, identity: principal.UserID.String(), source: command.Source},
			{dimension: authenticationAttemptDimensionSource, maximum: s.deps.LoginRateLimit.MaximumSourceAttempts, source: command.Source},
		},
	})
	if err != nil {
		return nil, rateLimitUnavailableAppError(err)
	}
	if limited {
		return nil, apperror.New("authentication.rate_limited")
	}
	allowed, err := s.deps.AccessPolicy.AllowsLocalLogin(ctx)
	if err != nil {
		return nil, authenticationUnavailable(err)
	}
	if !allowed || command.Password == "" || len(command.Password) > s.deps.Hasher.maximumLength {
		return nil, invalidCredentialsAppError()
	}
	user, err := s.deps.Users.Get(ctx, principal.UserID)
	if err != nil {
		return nil, reauthenticationStoreErrors.Translate(err)
	}
	if !user.IsActive() {
		return nil, invalidCredentialsAppError()
	}
	credential, err := s.deps.Passwords.GetByUser(ctx, principal.UserID)
	if err != nil {
		return nil, reauthenticationStoreErrors.Translate(err)
	}
	if err = s.deps.Hasher.Verify(ctx, credential.PasswordHash, command.Password); err != nil {
		if errors.Is(err, ErrPasswordMismatch) {
			return nil, invalidCredentialsAppError()
		}
		return nil, passwordWorkError(err, "authentication.internal")
	}
	result, err := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{
		Invocation: invocation, Action: model.ActionAuthenticationReauthenticate,
		Resource:  model.Resource{Type: model.ResourceUser, ID: principal.UserID.String()},
		Operation: "password", Value: map[string]any{"session_id": principal.SessionID.String()},
	}, s.deps.Now, func(ctx context.Context, reference mutationAttemptReference) (*store.SessionReauthenticationResult, error) {
		auditID := reference.ID
		credentialID, err := model.ParseSessionCredentialID(principal.CredentialID.String())
		if err != nil {
			return nil, err
		}
		return s.deps.Sessions.ReauthenticatePassword(ctx, &store.SessionPasswordReauthentication{
			SessionID: principal.SessionID, UserID: principal.UserID, CredentialID: credentialID,
			PasswordProof: store.PasswordCredentialProof{ID: credential.ID, Revision: credential.Revision},
			AuditEventID:  auditID,
		})
	}, reauthenticationStoreErrors.Translate)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Session == nil {
		return nil, authenticationUnavailable(errors.New("reauthentication returned no Session"))
	}
	s.deps.SecurityEffects.AuthenticationCacheInvalidated(ctx, principal.UserID.String(), result.AccessTokenHashes)
	if err := s.deps.Attempts.reset(ctx, receipt, authenticationAttemptDimensionIdentitySource); err != nil {
		s.warn(ctx, "reauthentication rate-limit reset failed", err)
	}
	return result.Session, nil
}

var reauthenticationStoreErrors = apperror.StoreTable{
	Rules: []apperror.StoreRule{
		{Kind: apperror.StoreSentinel, Sentinel: store.ErrPasswordCredentialChanged, Code: "authentication.credentials_invalid"},
		{Kind: apperror.StoreSentinel, Sentinel: store.ErrAuthenticationMethodDisabled, Code: "authentication.credentials_invalid"},
		{Kind: apperror.StoreSentinel, Sentinel: store.ErrAuthenticationGenerationChanged, Code: "authentication.credentials_invalid"},
		{Kind: apperror.StoreSentinel, Sentinel: store.ErrMFAReenrollmentRequired, Code: "authentication.credentials_invalid"},
	},
	NotFound:    "authentication.credentials_invalid",
	Conflict:    "authentication.credentials_invalid",
	Unavailable: "authentication.internal",
}
