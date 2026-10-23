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
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

type DesktopAuthorization interface {
	StartDesktopAuthorization(context.Context, invocation.Invocation, app.StartDesktopAuthorizationCommand) (*app.DesktopAuthorizationStart, error)
	BindDesktopAuthorization(context.Context, invocation.Invocation, app.BindDesktopAuthorizationCommand) (*app.DesktopAuthorizationBinding, error)
	DesktopAuthorizationContext(context.Context, invocation.Invocation, string) (*app.DesktopAuthorizationBrowserContext, error)
	AuthenticateDesktopAuthorizationSession(context.Context, invocation.Invocation, app.AuthenticateDesktopAuthorizationCommand) (*app.DesktopAuthorizationBrowserContext, error)
	AuthenticateDesktopAuthorizationLocally(context.Context, invocation.Invocation, app.AuthenticateDesktopAuthorizationLocallyCommand) (*app.DesktopAuthorizationBrowserContext, error)
	ResetDesktopAuthorizationAccount(context.Context, invocation.Invocation, string) error
	BeginDesktopExternalAuthentication(context.Context, invocation.Invocation, app.BeginDesktopExternalAuthenticationCommand) (*model.ExternalAuthenticationStart, error)
	ApproveDesktopAuthorization(context.Context, invocation.Invocation, app.ApproveDesktopAuthorizationCommand) (*app.DesktopAuthorizationApproval, error)
	CancelDesktopAuthorization(context.Context, invocation.Invocation, app.ApproveDesktopAuthorizationCommand) error
	ExchangeDesktopAuthorization(context.Context, invocation.Invocation, app.ExchangeDesktopAuthorizationCommand) (*app.DesktopAuthorizationExchangeResult, error)
}

type desktopAuthorizationStartRequest struct {
	CallbackURL      string                    `json:"callback_url"`
	State            string                    `json:"state"`
	CodeChallenge    string                    `json:"code_challenge"`
	DeviceID         string                    `json:"device_id,omitempty"`
	DeviceName       string                    `json:"device_name,omitempty"`
	PublicJWK        model.DesktopPublicJWK    `json:"public_jwk"`
	DesktopRelease   string                    `json:"desktop_release"`
	DesktopBuildID   string                    `json:"desktop_build_id"`
	Platform         model.DesktopPlatform     `json:"platform"`
	Architecture     model.DesktopArchitecture `json:"architecture"`
	RealtimeProtocol int                       `json:"realtime_protocol"`
}
type desktopAuthorizationProofRequest struct {
	Handle       string `json:"handle"`
	BrowserProof string `json:"browser_proof"`
	State        string `json:"state"`
}
type desktopAuthorizationStateRequest struct {
	State string `json:"state"`
}
type desktopAuthorizationLocalAuthenticationRequest struct {
	LoginID  string `json:"login_id"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code,omitempty"`
}
type desktopAuthorizationExchangeRequest struct {
	Code             string                    `json:"code"`
	State            string                    `json:"state"`
	CodeVerifier     string                    `json:"code_verifier"`
	PublicJWK        model.DesktopPublicJWK    `json:"public_jwk"`
	DesktopRelease   string                    `json:"desktop_release"`
	DesktopBuildID   string                    `json:"desktop_build_id"`
	Platform         model.DesktopPlatform     `json:"platform"`
	Architecture     model.DesktopArchitecture `json:"architecture"`
	RealtimeProtocol int                       `json:"realtime_protocol"`
}
type desktopAuthorizationStartResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	ExpiresAt        string `json:"expires_at"`
}
type desktopAuthorizationApprovalResponse struct {
	RedirectURL string `json:"redirect_url"`
	ExpiresAt   string `json:"expires_at"`
}

type desktopAuthorizationAccountResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type desktopAuthorizationContextResponse struct {
	State             string                                   `json:"state"`
	Account           *desktopAuthorizationAccountResponse     `json:"account,omitempty"`
	LocalLoginEnabled bool                                     `json:"local_login_enabled"`
	ExternalProviders []externalAuthenticationProviderResponse `json:"external_providers"`
	DeviceName        string                                   `json:"device_name"`
	ExpiresAt         string                                   `json:"expires_at"`
}

type desktopAuthorizationResourceModule struct {
	application DesktopAuthorization
	cookies     browserCookies
}

func desktopAuthorizationResource(application DesktopAuthorization, cookies browserCookies) apiResource {
	m := desktopAuthorizationResourceModule{application: application, cookies: cookies}
	base := apiPath(literal("auth"), literal("desktop"), literal("authorizations"))
	return newResource("desktop_authorization",
		publicRoute(http.MethodPost, base, []string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.disabled", "authentication.desktop_authorization.unavailable", "authentication.desktop_authorization.incompatible", "authentication.dpop.unavailable"}, m.start),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("bind")), []string{"request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable"}, m.bind),
		publicRoute(http.MethodGet, appendRoutePath(base, literal("context")), []string{"authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.credential_ambiguous"}, m.context),
		sessionRoute(http.MethodPost, appendRoutePath(base, literal("authenticate"), literal("session")), []string{"authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.desktop_authorization.account_session_locked"}, m.authenticateSession),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("authenticate"), literal("password")), []string{"request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.desktop_authorization.account_session_locked", "service.busy", "authentication.credentials_invalid", "authentication.mfa.required", "authentication.mfa.code_invalid", "authentication.mfa.unavailable", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.credential_ambiguous"}, m.authenticateLocal),
		protocolRoute("desktop-authorization-external-login", RouteProtocolRedirect, AuthPublic, http.MethodGet,
			appendRoutePath(base, literal("authenticate"), literal("providers"), providerID("provider_id"), literal("login")),
			[]string{"request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.external.provider_not_found", "authentication.external.request.invalid", "authentication.external.unavailable", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.credential_ambiguous", "authentication.external.account_not_linked", "authentication.external.rejected", "authentication.token_invalid"}, m.authenticateExternal),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("account"), literal("reset")), []string{"request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.credential_ambiguous"}, m.resetAccount),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("approve")), []string{"request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "audit.unavailable", "authentication.credential_ambiguous"}, m.approve),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("cancel")), []string{"request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.credential_ambiguous"}, m.cancel),
		publicRoute(http.MethodPost, apiPath(literal("auth"), literal("desktop"), literal("token")), []string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.desktop_authorization.incompatible", "authentication.desktop_authorization.account_session_locked", "authentication.sessions.maximum_reached", "authentication.dpop.invalid", "authentication.dpop.replayed", "authentication.dpop.use_nonce", "authentication.dpop.unavailable", "audit.unavailable"}, m.exchange),
	)
}

func (m desktopAuthorizationResourceModule) authenticateExternal(request operationRequest) (protocolResult, error) {
	binding, err := m.binding(request.request)
	if err != nil {
		return protocolResult{}, err
	}
	provider, err := request.params.RequireProviderID()
	if err != nil {
		return protocolResult{}, err
	}
	state := request.request.URL.Query().Get("state")
	start, err := m.application.BeginDesktopExternalAuthentication(request.context, request.invocation(), app.BeginDesktopExternalAuthenticationCommand{
		ProviderID: provider, Binding: binding, State: state, Source: request.metadata.IPAddress,
	})
	if err != nil {
		return protocolResult{}, err
	}
	headers := captureResponseHeaders(func(writer http.ResponseWriter) {
		m.cookies.attachExternalLoginBinding(writer, start.Binding, start.ExpiresAt)
	})
	headers.Set("Cache-Control", "no-store")
	return redirectProtocolResult(start.RedirectURL).withHeaders(headers), nil
}

func (m desktopAuthorizationResourceModule) start(request operationRequest) (operationResult, error) {
	var input desktopAuthorizationStartRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.StartDesktopAuthorization(request.context, request.invocation(), app.StartDesktopAuthorizationCommand{
		CallbackURL: input.CallbackURL, State: input.State, CodeChallenge: input.CodeChallenge,
		DeviceID: input.DeviceID, DeviceName: input.DeviceName, PublicJWK: input.PublicJWK,
		DesktopRelease: input.DesktopRelease, DesktopBuildID: input.DesktopBuildID,
		Platform: input.Platform, Architecture: input.Architecture, RealtimeProtocol: input.RealtimeProtocol,
		Source: request.metadata.IPAddress})
	if err != nil {
		return operationResult{}, err
	}
	headers := http.Header{"Cache-Control": {"no-store"}, "DPoP-Nonce": {result.DPoPNonce}}
	return jsonResult(http.StatusCreated, desktopAuthorizationStartResponse{AuthorizationURL: result.AuthorizationURL, ExpiresAt: wireTimeFromMillis(result.ExpiresAt)}).withHeaders(headers), nil
}

func (m desktopAuthorizationResourceModule) bind(request operationRequest) (operationResult, error) {
	var input desktopAuthorizationProofRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.BindDesktopAuthorization(request.context, request.invocation(), app.BindDesktopAuthorizationCommand{
		Handle: input.Handle, BrowserProof: input.BrowserProof, State: input.State,
	})
	if err != nil {
		return operationResult{}, err
	}
	headers := captureResponseHeaders(func(writer http.ResponseWriter) {
		m.cookies.attachDesktopAuthorizationBinding(writer, result.Binding, result.ExpiresAt)
	})
	headers.Set("Cache-Control", "no-store")
	return noContentResult().withHeaders(headers), nil
}

func (m desktopAuthorizationResourceModule) context(request operationRequest) (operationResult, error) {
	binding, err := m.binding(request.request)
	if err != nil {
		return operationResult{}, err
	}
	result, err := m.application.DesktopAuthorizationContext(request.context, request.invocation(), binding)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, desktopAuthorizationContextResponseFromApplication(result)).withHeaders(http.Header{"Cache-Control": {"no-store"}}), nil
}

func (m desktopAuthorizationResourceModule) authenticateSession(request operationRequest) (operationResult, error) {
	binding, err := m.binding(request.request)
	if err != nil {
		return operationResult{}, err
	}
	result, err := m.application.AuthenticateDesktopAuthorizationSession(request.context, request.invocation(), app.AuthenticateDesktopAuthorizationCommand{Binding: binding})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, desktopAuthorizationContextResponseFromApplication(result)).withHeaders(http.Header{"Cache-Control": {"no-store"}}), nil
}

func (m desktopAuthorizationResourceModule) authenticateLocal(request operationRequest) (operationResult, error) {
	binding, err := m.binding(request.request)
	if err != nil {
		return operationResult{}, err
	}
	var input desktopAuthorizationLocalAuthenticationRequest
	if err = request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.AuthenticateDesktopAuthorizationLocally(request.context, request.invocation(), app.AuthenticateDesktopAuthorizationLocallyCommand{
		Binding: binding, LoginID: input.LoginID, Password: input.Password, MFACode: input.MFACode, Source: request.metadata.IPAddress,
	})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, desktopAuthorizationContextResponseFromApplication(result)).withHeaders(http.Header{"Cache-Control": {"no-store"}}), nil
}

func (m desktopAuthorizationResourceModule) resetAccount(request operationRequest) (operationResult, error) {
	binding, err := m.binding(request.request)
	if err != nil {
		return operationResult{}, err
	}
	if err = m.application.ResetDesktopAuthorizationAccount(request.context, request.invocation(), binding); err != nil {
		return operationResult{}, err
	}
	return noContentResult().withHeaders(http.Header{"Cache-Control": {"no-store"}}), nil
}

func (m desktopAuthorizationResourceModule) approve(request operationRequest) (operationResult, error) {
	binding, err := m.binding(request.request)
	if err != nil {
		return operationResult{}, err
	}
	var input desktopAuthorizationStateRequest
	if err = request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.ApproveDesktopAuthorization(request.context, request.invocation(), app.ApproveDesktopAuthorizationCommand{Binding: binding, State: input.State})
	if err != nil {
		return operationResult{}, err
	}
	headers := captureResponseHeaders(m.cookies.clearDesktopAuthorizationBinding)
	headers.Set("Cache-Control", "no-store")
	return jsonResult(http.StatusOK, desktopAuthorizationApprovalResponse{RedirectURL: result.RedirectURL, ExpiresAt: wireTimeFromMillis(result.ExpiresAt)}).withHeaders(headers), nil
}

func (m desktopAuthorizationResourceModule) cancel(request operationRequest) (operationResult, error) {
	binding, err := m.binding(request.request)
	if err != nil {
		return operationResult{}, err
	}
	var input desktopAuthorizationStateRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	if err := m.application.CancelDesktopAuthorization(request.context, request.invocation(), app.ApproveDesktopAuthorizationCommand{Binding: binding, State: input.State}); err != nil {
		return operationResult{}, err
	}
	headers := captureResponseHeaders(m.cookies.clearDesktopAuthorizationBinding)
	headers.Set("Cache-Control", "no-store")
	return noContentResult().withHeaders(headers), nil
}

func (m desktopAuthorizationResourceModule) binding(request *http.Request) (string, error) {
	binding, err := singleCookieValue(request, BrowserDesktopAuthorizationCookieName)
	if err != nil || binding == "" {
		return "", apperror.New("authentication.desktop_authorization.invalid")
	}
	return binding, nil
}

func desktopAuthorizationContextResponseFromApplication(value *app.DesktopAuthorizationBrowserContext) desktopAuthorizationContextResponse {
	if value == nil {
		return desktopAuthorizationContextResponse{}
	}
	result := desktopAuthorizationContextResponse{
		State: string(value.State), LocalLoginEnabled: value.LocalLoginEnabled,
		ExternalProviders: externalAuthenticationProviderResponses(value.ExternalProviders),
		DeviceName:        value.DeviceName, ExpiresAt: wireTimeFromMillis(value.ExpiresAt),
	}
	if value.Account != nil {
		result.Account = &desktopAuthorizationAccountResponse{ID: value.Account.ID.String(), Username: value.Account.Username, DisplayName: value.Account.DisplayName}
	}
	return result
}

func (m desktopAuthorizationResourceModule) exchange(request operationRequest) (operationResult, error) {
	var input desktopAuthorizationExchangeRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	proof, proofErr := requestDPoPProof(request.request)
	if proofErr != nil {
		return operationResult{}, proofErr
	}
	result, err := m.application.ExchangeDesktopAuthorization(request.context, request.invocation(), app.ExchangeDesktopAuthorizationCommand{
		Code: input.Code, State: input.State, CodeVerifier: input.CodeVerifier, Source: request.metadata.IPAddress,
		DPoPProof: proof, PublicJWK: input.PublicJWK, DesktopRelease: input.DesktopRelease,
		DesktopBuildID: input.DesktopBuildID, Platform: input.Platform, Architecture: input.Architecture,
		RealtimeProtocol: input.RealtimeProtocol,
	})
	if err != nil {
		headers := http.Header{}
		if nonce, ok := app.DPoPChallengeNonce(err); ok {
			headers.Set("DPoP-Nonce", nonce)
			headers.Set("WWW-Authenticate", `DPoP error="use_dpop_nonce"`)
		} else if apperror.Is(err, "authentication.dpop.invalid") || apperror.Is(err, "authentication.dpop.replayed") {
			headers.Set("WWW-Authenticate", `DPoP error="invalid_dpop_proof"`)
		}
		return operationResult{}, errorWithHeaders(err, headers)
	}
	headers := http.Header{"Cache-Control": {"no-store"}}
	if result.DPoPNonce != "" {
		headers.Set("DPoP-Nonce", result.DPoPNonce)
	}
	return jsonResult(http.StatusOK, authenticationResponse{Session: sessionResponseFromModel(request.request, result.Session), Tokens: authenticationTokensResponseFromModel(result.Tokens)}).withHeaders(headers), nil
}
