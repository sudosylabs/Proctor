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
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type BeginExternalReauthenticationCommand struct {
	Task   string
	Source string
}

func (a *App) BeginExternalReauthentication(ctx context.Context, invocation Invocation, command BeginExternalReauthenticationCommand) (*model.ExternalAuthenticationStart, error) {
	return a.externalAuthentication.beginReauthentication(ctx, invocation, command)
}

func (s *externalAuthenticationService) beginReauthentication(ctx context.Context, invocation Invocation, command BeginExternalReauthenticationCommand) (*model.ExternalAuthenticationStart, error) {
	principal := invocation.Principal()
	if principal.ValidateMFARecovery() != nil || principal.CredentialType != model.CredentialSessionAccess || principal.ClientType != model.SessionClientWeb {
		return nil, invalidTokenAppError()
	}
	if principal.AuthenticationMethod == "password" || !principal.ExternalIdentityID.IsValid() {
		return nil, NewError("authentication.reauthentication_method_required")
	}
	returnTo := ""
	switch command.Task {
	case "security":
		returnTo = "/account/security"
	case "connect-provider":
		if principal.MFARecoveryRequired {
			return nil, NewError("authentication.external.request.invalid")
		}
		returnTo = "/account/connect-provider"
	default:
		return nil, NewError("authentication.external.request.invalid")
	}
	auditID, err := s.mutationAudit.Begin(ctx, invocation, "authentication.reauthenticate", model.Resource{Type: model.ResourceUser, ID: principal.UserID.String()}, "external", map[string]any{"session_id": principal.SessionID.String(), "provider": principal.AuthenticationProviderID, "task": command.Task}, nil)
	if err != nil {
		return nil, err
	}
	start, err := s.beginForPurpose(ctx, principal.AuthenticationProviderID, returnTo, model.SessionClientWeb, "", "", command.Source, model.ExternalAuthenticationPurposeReauthenticate, principal.UserID, auditID, "", "", &principal)
	if err != nil {
		if auditErr := s.mutationAudit.Fail(ctx, auditID, appErrorCode(err)); auditErr != nil {
			return nil, auditErr
		}
	}
	return start, err
}

func (s *externalAuthenticationService) completeReauthentication(ctx context.Context, state *model.ExternalLoginState, assertion *model.ExternalAuthenticationAssertion) (*model.ExternalAuthenticationCompletion, error) {
	if state == nil || state.Purpose != model.ExternalAuthenticationPurposeReauthenticate || state.Validate() != nil || !state.ConsumedAt.Valid || assertion == nil || assertion.ProviderId != state.Provider || assertion.AuthenticatedAt < state.CreatedAt.Truncate(time.Second).UnixMilli() || assertion.AuthenticatedAt > s.now().UnixMilli() {
		return nil, s.failConsumedProviderConnection(ctx, state, NewError("authentication.external.rejected"))
	}
	result, err := s.sessions.ReauthenticateExternalWithAudit(ctx, &store.SessionExternalReauthentication{StateID: state.ID, ProviderID: state.Provider, Subject: assertion.Subject, AuthenticatedAt: model.TimeFromMillis(assertion.AuthenticatedAt)})
	if err != nil {
		return nil, s.failConsumedProviderConnection(ctx, state, reauthenticationFailure(err))
	}
	if result == nil || result.Session == nil {
		return nil, s.failConsumedProviderConnection(ctx, state, authenticationUnavailable(errors.New("external reauthentication returned no Session")))
	}
	s.invalidator.InvalidateAccessCredentials(ctx, result.AccessTokenHashes)
	return &model.ExternalAuthenticationCompletion{Session: result.Session, ReturnTo: state.ReturnTo}, nil
}
