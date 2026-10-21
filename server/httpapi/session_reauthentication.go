// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"net/http"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type SessionReauthentication interface {
	ReauthenticatePassword(context.Context, app.Invocation, app.ReauthenticatePasswordCommand) (*model.Session, error)
	BeginExternalReauthentication(context.Context, app.Invocation, app.BeginExternalReauthenticationCommand) (*model.ExternalAuthenticationStart, error)
}

type sessionReauthenticationResourceModule struct {
	authentication SessionReauthentication
	cookies        browserCookies
}

type passwordReauthenticationRequest struct {
	Password string `json:"password"`
}

type externalReauthenticationRequest struct {
	Task string `json:"task"`
}

type externalReauthenticationResponse struct {
	RedirectURL string `json:"redirect_url"`
}

func sessionReauthenticationResource(authentication SessionReauthentication, cookies browserCookies) resource {
	module := sessionReauthenticationResourceModule{authentication: authentication, cookies: cookies}
	return newResource("session-reauthentication",
		mfaRecoverySessionRoute(http.MethodPost, apiPath(literal("auth"), literal("reauthenticate"), literal("external")), personalAccessTokenSessionMutationCodes("request.invalid", "authentication.reauthentication_method_required", "authentication.external.request.invalid", "authentication.external.provider_not_found", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.external.unavailable", "authentication.external.rejected", "authentication.internal", "audit.unavailable"), module.external),
		mfaRecoverySessionRoute(http.MethodPost, apiPath(literal("auth"), literal("reauthenticate"), literal("password")),
			personalAccessTokenSessionMutationCodes("service.busy", "request.invalid", "authentication.credentials_invalid", "authentication.reauthentication_method_required", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.internal", "audit.unavailable"), module.password),
	)
}

func (m sessionReauthenticationResourceModule) password(request operationRequest) (operationResult, error) {
	var input passwordReauthenticationRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	session, err := m.authentication.ReauthenticatePassword(request.context, request.invocation(), app.ReauthenticatePasswordCommand{
		Password: input.Password, Source: request.metadata.IPAddress,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, sessionResponseFromModel(request.request, session)).withHeaders(noStoreHeaders()), nil
}

func (m sessionReauthenticationResourceModule) external(request operationRequest) (operationResult, error) {
	var input externalReauthenticationRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	start, err := m.authentication.BeginExternalReauthentication(request.context, request.invocation(), app.BeginExternalReauthenticationCommand{Task: input.Task, Source: request.metadata.IPAddress})
	if err != nil {
		return operationResult{}, err
	}
	headers := captureResponseHeaders(func(writer http.ResponseWriter) {
		m.cookies.attachExternalLoginBinding(writer, start.Binding, start.ExpiresAt)
	})
	return jsonResult(http.StatusOK, externalReauthenticationResponse{RedirectURL: start.RedirectURL}).withHeaders(combineResponseHeaders(headers, noStoreHeaders())), nil
}
