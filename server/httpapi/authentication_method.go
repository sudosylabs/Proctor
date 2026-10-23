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
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type authenticationMethodApplication interface {
	AuthenticationMethods(context.Context, invocation.Invocation) (app.AuthenticationMethodView, error)
	EnrollPassword(context.Context, invocation.Invocation, string) error
	RemovePassword(context.Context, invocation.Invocation) error
	BeginProviderConnection(context.Context, invocation.Invocation, app.BeginProviderConnectionCommand) (*model.ExternalAuthenticationStart, error)
	UnlinkExternalIdentity(context.Context, invocation.Invocation, model.ExternalIdentityID) error
}

type authenticationMethodResourceModule struct {
	application authenticationMethodApplication
	cookies     browserCookies
}

type enrollPasswordRequest struct {
	Password string `json:"password"`
}
type beginProviderConnectionRequest struct {
	ReturnTo string `json:"return_to,omitempty"`
}
type authenticationMethodResponse struct {
	Password  bool                                   `json:"password"`
	Providers []authenticationProviderMethodResponse `json:"providers"`
}
type authenticationProviderMethodResponse struct {
	ID          string `json:"id"`
	ProviderID  string `json:"provider_id"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}
type providerConnectionResponse struct {
	RedirectURL string `json:"redirect_url"`
	ExpiresAt   string `json:"expires_at"`
}

func authenticationMethodResource(application authenticationMethodApplication, cookies browserCookies) apiResource {
	m := authenticationMethodResourceModule{application: application, cookies: cookies}
	base := apiPath(literal("authentication-methods"))
	password := appendRoutePath(base, literal("password"))
	return newResource("authentication-method",
		sessionRoute(http.MethodGet, base, nil, m.list),
		strongRecentSessionRoute(http.MethodPut, password, strongRecentAuthenticationMethodCodes("request.invalid", "authentication.password.invalid", "service.busy", "authentication.method.disabled", "authentication.method.conflict", "authentication.method.unavailable", "audit.unavailable"), m.enrollPassword),
		strongRecentSessionRoute(http.MethodDelete, password, []string{"authentication.method.last_usable", "authentication.method.not_found", "authentication.method.conflict", "authentication.method.unavailable", "audit.unavailable"}, m.removePassword),
		strongRecentSessionRoute(http.MethodPost, appendRoutePath(base, literal("providers"), providerID("provider_id"), literal("connect")), []string{"request.invalid", "authentication.external.provider_not_found", "authentication.external.unavailable", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.internal", "audit.unavailable", "authentication.desktop_authorization.invalid", "authentication.external.account_not_linked", "authentication.external.rejected", "authentication.external.request.invalid"}, m.connect),
		strongRecentSessionRoute(http.MethodDelete, appendRoutePath(base, literal("providers"), canonicalID("external_identity_id")), authenticationMethodRemovalCodes(), m.unlink),
	)
}

func strongRecentAuthenticationMethodCodes(extra ...string) []string {
	return sessionAuthenticationMutationErrorCodes(extra...)
}

func authenticationMethodRemovalCodes() []string {
	return strongRecentAuthenticationMethodCodes("request.invalid", "authentication.method.last_usable", "authentication.method.not_found", "authentication.method.conflict", "authentication.method.unavailable", "audit.unavailable")
}

func (m authenticationMethodResourceModule) list(request operationRequest) (operationResult, error) {
	view, err := m.application.AuthenticationMethods(request.context, request.invocation())
	if err != nil {
		return operationResult{}, err
	}
	providers := make([]authenticationProviderMethodResponse, 0, len(view.Providers))
	for _, provider := range view.Providers {
		providers = append(providers, authenticationProviderMethodResponse{ID: provider.IdentityID.String(), ProviderID: provider.ProviderID, DisplayName: provider.DisplayName, Type: provider.Type})
	}
	return jsonResult(http.StatusOK, authenticationMethodResponse{Password: view.Password, Providers: providers}), nil
}

func (m authenticationMethodResourceModule) enrollPassword(request operationRequest) (operationResult, error) {
	var body enrollPasswordRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if err := m.application.EnrollPassword(request.context, request.invocation(), body.Password); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (m authenticationMethodResourceModule) removePassword(request operationRequest) (operationResult, error) {
	if err := m.application.RemovePassword(request.context, request.invocation()); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (m authenticationMethodResourceModule) connect(request operationRequest) (operationResult, error) {
	providerID, err := request.params.RequireProviderID()
	if err != nil {
		return operationResult{}, err
	}
	var body beginProviderConnectionRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	start, err := m.application.BeginProviderConnection(request.context, request.invocation(), app.BeginProviderConnectionCommand{ProviderID: providerID, ReturnTo: body.ReturnTo, Source: request.metadata.IPAddress})
	if err != nil {
		return operationResult{}, err
	}
	headers := captureResponseHeaders(func(writer http.ResponseWriter) {
		m.cookies.attachExternalLoginBinding(writer, start.Binding, start.ExpiresAt)
	})
	headers.Set("Cache-Control", "no-store")
	return jsonResult(http.StatusCreated, providerConnectionResponse{RedirectURL: start.RedirectURL, ExpiresAt: wireTimeFromMillis(start.ExpiresAt)}).withHeaders(headers), nil
}

func (m authenticationMethodResourceModule) unlink(request operationRequest) (operationResult, error) {
	parsed, err := request.params.RequireExternalIdentityID()
	if err != nil {
		return operationResult{}, err
	}
	if err = m.application.UnlinkExternalIdentity(request.context, request.invocation(), parsed); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}
