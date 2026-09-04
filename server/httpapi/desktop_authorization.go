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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type DesktopAuthorization interface {
	StartDesktopAuthorization(context.Context, application.Invocation, application.StartDesktopAuthorizationCommand) (*application.DesktopAuthorizationStart, error)
	BindDesktopAuthorization(context.Context, application.Invocation, application.BindDesktopAuthorizationCommand) (*application.DesktopAuthorizationBinding, error)
	DesktopAuthorizationContext(context.Context, application.Invocation, string) (*application.DesktopAuthorizationBrowserContext, error)
	AuthenticateDesktopAuthorizationSession(context.Context, application.Invocation, application.AuthenticateDesktopAuthorizationCommand) (*application.DesktopAuthorizationBrowserContext, error)
	AuthenticateDesktopAuthorizationLocally(context.Context, application.Invocation, application.AuthenticateDesktopAuthorizationLocallyCommand) (*application.DesktopAuthorizationBrowserContext, error)
	ResetDesktopAuthorizationAccount(context.Context, application.Invocation, string) error
	BeginDesktopExternalAuthentication(context.Context, application.Invocation, application.BeginDesktopExternalAuthenticationCommand) (*model.ExternalAuthenticationStart, error)
	ApproveDesktopAuthorization(context.Context, application.Invocation, application.ApproveDesktopAuthorizationCommand) (*application.DesktopAuthorizationApproval, error)
	CancelDesktopAuthorization(context.Context, application.Invocation, application.ApproveDesktopAuthorizationCommand) error
	ExchangeDesktopAuthorization(context.Context, application.Invocation, application.ExchangeDesktopAuthorizationCommand) (*application.DesktopAuthorizationExchangeResult, error)
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
	ExpiresAt        int64  `json:"expires_at"`
}
type desktopAuthorizationApprovalResponse struct {
	RedirectURL string `json:"redirect_url"`
	ExpiresAt   int64  `json:"expires_at"`
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
	ExpiresAt         int64                                    `json:"expires_at"`
}

type desktopAuthorizationResourceModule struct {
	application DesktopAuthorization
	cookies     browserCookies
}

func desktopAuthorizationResource(application DesktopAuthorization, cookies browserCookies) resource {
	m := desktopAuthorizationResourceModule{application: application, cookies: cookies}
	base := apiPath(literal("auth"), literal("desktop"), literal("authorizations"))
	errors := []string{"request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.desktop_authorization.account_session_locked"}
	return newResource("desktop_authorization",
		publicRoute(http.MethodPost, base, []string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.disabled", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.desktop_authorization.incompatible", "authentication.dpop.unavailable"}, m.start),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("bind")), errors, m.bind),
		publicRoute(http.MethodGet, appendRoutePath(base, literal("context")), errors, m.context),
		sessionRoute(http.MethodPost, appendRoutePath(base, literal("authenticate"), literal("session")), sessionAuthenticationMutationErrorCodes(errors...), m.authenticateSession),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("authenticate"), literal("password")), append(errors, "authentication.invalid_credentials", "authentication.mfa.required", "authentication.mfa.invalid_code", "authentication.mfa.unavailable", "authentication.rate_limited", "authentication.rate_limit_unavailable"), m.authenticateLocal),
		protocolRoute("desktop-authorization-external-login", RouteProtocolRedirect, AuthPublic, http.MethodGet,
			appendRoutePath(base, literal("authenticate"), literal("providers"), providerID("provider_id"), literal("login")),
			append(errors, "authentication.external.provider_not_found", "authentication.external.request.invalid", "authentication.external.unavailable", "authentication.rate_limited", "authentication.rate_limit_unavailable"), m.authenticateExternal),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("account"), literal("reset")), errors, m.resetAccount),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("approve")), append(errors, "audit.unavailable"), m.approve),
		publicRoute(http.MethodPost, appendRoutePath(base, literal("cancel")), errors, m.cancel),
		publicRoute(http.MethodPost, apiPath(literal("auth"), literal("desktop"), literal("token")), []string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.desktop_authorization.incompatible", "authentication.desktop_authorization.account_session_locked", "authentication.sessions.maximum_reached", "authentication.dpop.invalid", "authentication.dpop.replayed", "authentication.dpop.use_nonce", "authentication.dpop.unavailable", "audit.unavailable"}, m.exchange),
	)
}

func (m desktopAuthorizationResourceModule) authenticateExternal(request operationRequest) (protocolResult, error) {
	binding, err := m.binding(request.request)
	if err != nil {
		return protocolResult{}, err
	}
	provider, err := request.params.RequireProviderId()
	if err != nil {
		return protocolResult{}, err
	}
	state := request.request.URL.Query().Get("state")
	start, err := m.application.BeginDesktopExternalAuthentication(request.context, request.invocation(), application.BeginDesktopExternalAuthenticationCommand{
		ProviderID: provider, Binding: binding, State: state, Source: request.request.RemoteAddr,
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
	if err := request.decodeJSON(&input, "desktop_authorization_start"); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.StartDesktopAuthorization(request.context, request.invocation(), application.StartDesktopAuthorizationCommand{
		CallbackURL: input.CallbackURL, State: input.State, CodeChallenge: input.CodeChallenge,
		DeviceID: input.DeviceID, DeviceName: input.DeviceName, PublicJWK: input.PublicJWK,
		DesktopRelease: input.DesktopRelease, DesktopBuildID: input.DesktopBuildID,
		Platform: input.Platform, Architecture: input.Architecture, RealtimeProtocol: input.RealtimeProtocol,
		Source: request.request.RemoteAddr})
	if err != nil {
		return operationResult{}, err
	}
	headers := http.Header{"Cache-Control": {"no-store"}, "DPoP-Nonce": {result.DPoPNonce}}
	return jsonResult(http.StatusCreated, desktopAuthorizationStartResponse{AuthorizationURL: result.AuthorizationURL, ExpiresAt: result.ExpiresAt}).withHeaders(headers), nil
}

func (m desktopAuthorizationResourceModule) bind(request operationRequest) (operationResult, error) {
	var input desktopAuthorizationProofRequest
	if err := request.decodeJSON(&input, "desktop_authorization_bind"); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.BindDesktopAuthorization(request.context, request.invocation(), application.BindDesktopAuthorizationCommand{
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
	result, err := m.application.AuthenticateDesktopAuthorizationSession(request.context, request.invocation(), application.AuthenticateDesktopAuthorizationCommand{Binding: binding})
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
	if err = request.decodeJSON(&input, "desktop_authorization_local_authentication"); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.AuthenticateDesktopAuthorizationLocally(request.context, request.invocation(), application.AuthenticateDesktopAuthorizationLocallyCommand{
		Binding: binding, LoginID: input.LoginID, Password: input.Password, MFACode: input.MFACode, Source: request.request.RemoteAddr,
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
	if err := request.decodeJSON(&input, "desktop_authorization_approve"); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.ApproveDesktopAuthorization(request.context, request.invocation(), application.ApproveDesktopAuthorizationCommand{Binding: binding, State: input.State})
	if err != nil {
		return operationResult{}, err
	}
	headers := captureResponseHeaders(m.cookies.clearDesktopAuthorizationBinding)
	headers.Set("Cache-Control", "no-store")
	return jsonResult(http.StatusOK, desktopAuthorizationApprovalResponse{RedirectURL: result.RedirectURL, ExpiresAt: result.ExpiresAt}).withHeaders(headers), nil
}

func (m desktopAuthorizationResourceModule) cancel(request operationRequest) (operationResult, error) {
	binding, err := m.binding(request.request)
	if err != nil {
		return operationResult{}, err
	}
	var input desktopAuthorizationStateRequest
	if err := request.decodeJSON(&input, "desktop_authorization_cancel"); err != nil {
		return operationResult{}, err
	}
	if err := m.application.CancelDesktopAuthorization(request.context, request.invocation(), application.ApproveDesktopAuthorizationCommand{Binding: binding, State: input.State}); err != nil {
		return operationResult{}, err
	}
	headers := captureResponseHeaders(m.cookies.clearDesktopAuthorizationBinding)
	headers.Set("Cache-Control", "no-store")
	return noContentResult().withHeaders(headers), nil
}

func (m desktopAuthorizationResourceModule) binding(request *http.Request) (string, error) {
	binding, err := singleCookieValue(request, BrowserDesktopAuthorizationCookieName)
	if err != nil || binding == "" {
		return "", application.NewError("authentication.desktop_authorization.invalid")
	}
	return binding, nil
}

func desktopAuthorizationContextResponseFromApplication(value *application.DesktopAuthorizationBrowserContext) desktopAuthorizationContextResponse {
	if value == nil {
		return desktopAuthorizationContextResponse{}
	}
	result := desktopAuthorizationContextResponse{
		State: string(value.State), LocalLoginEnabled: value.LocalLoginEnabled,
		ExternalProviders: externalAuthenticationProviderResponses(value.ExternalProviders),
		DeviceName:        value.DeviceName, ExpiresAt: value.ExpiresAt,
	}
	if value.Account != nil {
		result.Account = &desktopAuthorizationAccountResponse{ID: value.Account.ID.String(), Username: value.Account.Username, DisplayName: value.Account.DisplayName}
	}
	return result
}

func (m desktopAuthorizationResourceModule) exchange(request operationRequest) (operationResult, error) {
	var input desktopAuthorizationExchangeRequest
	if err := request.decodeJSON(&input, "desktop_authorization_exchange"); err != nil {
		return operationResult{}, err
	}
	proof, proofErr := requestDPoPProof(request.request)
	if proofErr != nil {
		return operationResult{}, proofErr
	}
	result, err := m.application.ExchangeDesktopAuthorization(request.context, request.invocation(), application.ExchangeDesktopAuthorizationCommand{
		Code: input.Code, State: input.State, CodeVerifier: input.CodeVerifier, Source: request.request.RemoteAddr,
		DPoPProof: proof, PublicJWK: input.PublicJWK, DesktopRelease: input.DesktopRelease,
		DesktopBuildID: input.DesktopBuildID, Platform: input.Platform, Architecture: input.Architecture,
		RealtimeProtocol: input.RealtimeProtocol,
	})
	if err != nil {
		headers := http.Header{}
		if nonce, ok := application.DPoPChallengeNonce(err); ok {
			headers.Set("DPoP-Nonce", nonce)
			headers.Set("WWW-Authenticate", `DPoP error="use_dpop_nonce"`)
		} else if application.Is(err, "authentication.dpop.invalid") || application.Is(err, "authentication.dpop.replayed") {
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
