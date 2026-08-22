// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
)

type DesktopAuthorization interface {
	StartDesktopAuthorization(context.Context, application.Invocation, application.StartDesktopAuthorizationCommand) (*application.DesktopAuthorizationStart, error)
	ApproveDesktopAuthorization(context.Context, application.Invocation, application.ApproveDesktopAuthorizationCommand) (*application.DesktopAuthorizationApproval, error)
	CancelDesktopAuthorization(context.Context, application.Invocation, application.ApproveDesktopAuthorizationCommand) error
	ExchangeDesktopAuthorization(context.Context, application.Invocation, application.ExchangeDesktopAuthorizationCommand) (*application.DesktopAuthorizationExchangeResult, error)
}

type desktopAuthorizationStartRequest struct {
	CallbackURL          string `json:"callback_url"`
	State                string `json:"state"`
	CodeChallenge        string `json:"code_challenge"`
	AuthenticationMethod string `json:"authentication_method"`
	ProviderID           string `json:"provider_id,omitempty"`
	DeviceID             string `json:"device_id,omitempty"`
	DeviceName           string `json:"device_name,omitempty"`
}
type desktopAuthorizationProofRequest struct {
	Handle       string `json:"handle"`
	BrowserProof string `json:"browser_proof"`
	State        string `json:"state"`
}
type desktopAuthorizationExchangeRequest struct {
	Code         string `json:"code"`
	State        string `json:"state"`
	CodeVerifier string `json:"code_verifier"`
}
type desktopAuthorizationStartResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	ExpiresAt        int64  `json:"expires_at"`
}
type desktopAuthorizationApprovalResponse struct {
	RedirectURL string `json:"redirect_url"`
	ExpiresAt   int64  `json:"expires_at"`
}

type desktopAuthorizationResourceModule struct{ application DesktopAuthorization }

func desktopAuthorizationResource(application DesktopAuthorization) resource {
	m := desktopAuthorizationResourceModule{application: application}
	return newResource("desktop_authorization",
		publicRoute(http.MethodPost, apiPath(literal("auth"), literal("desktop"), literal("authorizations")), []string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.disabled", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable"}, m.start),
		sessionRoute(http.MethodPost, apiPath(literal("auth"), literal("desktop"), literal("authorizations"), literal("approve")), sessionAuthenticationMutationErrorCodes("request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "audit.unavailable"), m.approve),
		publicRoute(http.MethodPost, apiPath(literal("auth"), literal("desktop"), literal("authorizations"), literal("cancel")), []string{"request.invalid", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable"}, m.cancel),
		publicRoute(http.MethodPost, apiPath(literal("auth"), literal("desktop"), literal("token")), []string{"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable", "authentication.desktop_authorization.invalid", "authentication.desktop_authorization.rejected", "authentication.desktop_authorization.unavailable", "authentication.sessions.maximum_reached", "audit.unavailable"}, m.exchange),
	)
}

func (m desktopAuthorizationResourceModule) start(request operationRequest) (operationResult, error) {
	var input desktopAuthorizationStartRequest
	if err := request.decodeJSON(&input, "desktop_authorization_start"); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.StartDesktopAuthorization(request.context, request.invocation(), application.StartDesktopAuthorizationCommand{
		CallbackURL: input.CallbackURL, State: input.State, CodeChallenge: input.CodeChallenge,
		AuthenticationMethod: input.AuthenticationMethod, ProviderID: input.ProviderID,
		DeviceID: input.DeviceID, DeviceName: input.DeviceName, Source: request.request.RemoteAddr})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, desktopAuthorizationStartResponse{AuthorizationURL: result.AuthorizationURL, ExpiresAt: result.ExpiresAt}).withHeaders(http.Header{"Cache-Control": {"no-store"}}), nil
}

func (m desktopAuthorizationResourceModule) approve(request operationRequest) (operationResult, error) {
	var input desktopAuthorizationProofRequest
	if err := request.decodeJSON(&input, "desktop_authorization_approve"); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.ApproveDesktopAuthorization(request.context, request.invocation(), application.ApproveDesktopAuthorizationCommand{Handle: input.Handle, BrowserProof: input.BrowserProof, State: input.State})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, desktopAuthorizationApprovalResponse{RedirectURL: result.RedirectURL, ExpiresAt: result.ExpiresAt}).withHeaders(http.Header{"Cache-Control": {"no-store"}}), nil
}

func (m desktopAuthorizationResourceModule) cancel(request operationRequest) (operationResult, error) {
	var input desktopAuthorizationProofRequest
	if err := request.decodeJSON(&input, "desktop_authorization_cancel"); err != nil {
		return operationResult{}, err
	}
	if err := m.application.CancelDesktopAuthorization(request.context, request.invocation(), application.ApproveDesktopAuthorizationCommand{Handle: input.Handle, BrowserProof: input.BrowserProof, State: input.State}); err != nil {
		return operationResult{}, err
	}
	return noContentResult().withHeaders(http.Header{"Cache-Control": {"no-store"}}), nil
}

func (m desktopAuthorizationResourceModule) exchange(request operationRequest) (operationResult, error) {
	var input desktopAuthorizationExchangeRequest
	if err := request.decodeJSON(&input, "desktop_authorization_exchange"); err != nil {
		return operationResult{}, err
	}
	result, err := m.application.ExchangeDesktopAuthorization(request.context, request.invocation(), application.ExchangeDesktopAuthorizationCommand{Code: input.Code, State: input.State, CodeVerifier: input.CodeVerifier, Source: request.request.RemoteAddr})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, authenticationResponse{Session: sessionResponseFromModel(request.request, result.Session), Tokens: authenticationTokensResponseFromModel(result.Tokens)}).withHeaders(http.Header{"Cache-Control": {"no-store"}}), nil
}
