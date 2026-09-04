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
	"fmt"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type authenticationMFAVerifier interface {
	VerifyLogin(
		context.Context,
		string,
		string,
		time.Time,
	) (model.AuthenticationStrength, int64, error)
}

type authenticationPATResolver interface {
	ResolveBearer(context.Context, string, time.Time) (*model.Principal, error)
}

type sessionIssuance struct {
	User                     *model.User
	ClientType               model.SessionClientType
	DeviceID                 string
	DeviceName               string
	AuthenticationMethod     string
	AuthenticationProviderID string
	ExternalIdentityID       model.ExternalIdentityID
	AuthenticationStrength   model.AuthenticationStrength
	AuthenticatedAt          int64
	MFACompletedAt           int64
}

type authenticationSessionIssuer interface {
	createSession(
		context.Context,
		sessionIssuance,
	) (*model.Session, *model.AuthenticationTokens, error)
}

type personalAccessTokenBearerResolver struct {
	tokens      store.PersonalAccessTokenStore
	policy      PersonalAccessTokenPolicy
	diagnostics authenticationDiagnostics
}

func newPersonalAccessTokenBearerResolver(
	tokens store.PersonalAccessTokenStore,
	policy PersonalAccessTokenPolicy,
	diagnostics authenticationDiagnostics,
) (*personalAccessTokenBearerResolver, error) {
	if tokens == nil {
		return nil, errors.New("personal access token store is required")
	}
	if diagnostics == nil {
		return nil, errors.New("personal access token diagnostics are required")
	}
	return &personalAccessTokenBearerResolver{
		tokens: tokens, policy: policy, diagnostics: diagnostics,
	}, nil
}

func (r *personalAccessTokenBearerResolver) ResolveBearer(
	ctx context.Context,
	rawToken string,
	at time.Time,
) (*model.Principal, error) {
	if !validRawCredential(rawToken) {
		return nil, invalidTokenAppError()
	}
	resolved, err := r.tokens.Resolve(
		ctx,
		model.HashToken(rawToken),
		at.UnixMilli(),
		r.policy.LastUsedUpdateInterval.Milliseconds(),
	)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, invalidTokenAppError()
		}
		return nil, authenticationUnavailable(err)
	}
	principal := &model.Principal{
		UserID:               resolved.User.ID,
		CredentialID:         model.PrincipalCredentialID(resolved.Token.ID),
		CredentialType:       model.CredentialPersonalAccessToken,
		AuthenticationMethod: "personal_access_token",
		ClientType:           model.SessionClientCLI,
		CredentialScopes:     append([]string(nil), resolved.Token.Scopes...),
		AcademicUnitID:       resolved.Token.AcademicUnitID,
	}
	if principal.Validate() != nil {
		r.diagnostics.WarnContext(
			ctx,
			"personal access token resolved to invalid principal",
			fmt.Errorf("personal_access_token_id=%s", resolved.Token.ID.String()),
		)
		return nil, invalidTokenAppError()
	}
	return principal, nil
}

var _ authenticationPATResolver = (*personalAccessTokenBearerResolver)(nil)
