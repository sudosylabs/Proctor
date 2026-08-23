// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
)

type BrowserInvitationApplication interface {
	StartBrowserInvitation(context.Context, application.Invocation, application.StartBrowserInvitationCommand) (*application.BrowserInvitationStart, error)
	AcceptBrowserInvitation(context.Context, application.Invocation, application.BrowserInvitationAcceptanceCommand) (*application.InvitationAcceptanceView, error)
	AcceptBrowserInvitationWithSession(context.Context, application.Invocation, application.BrowserInvitationSessionAcceptanceCommand) (*application.InvitationAcceptanceView, error)
}

type browserInvitationStartRequest struct {
	Claim string `json:"claim"`
}

type browserInvitationStartResponse struct {
	Handle      string `json:"handle"`
	Purpose     string `json:"purpose"`
	Requirement string `json:"requirement"`
	ExpiresAt   int64  `json:"expires_at"`
}

type browserInvitationAcceptanceRequest struct {
	Handle      string `json:"handle"`
	Password    string `json:"password"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
}

type browserInvitationSessionAcceptanceRequest struct {
	Handle string `json:"handle"`
}

type browserInvitationResourceModule struct {
	application BrowserInvitationApplication
	cookies     browserCookies
}

type unavailableBrowserInvitationApplication struct{}

func (unavailableBrowserInvitationApplication) StartBrowserInvitation(context.Context, application.Invocation, application.StartBrowserInvitationCommand) (*application.BrowserInvitationStart, error) {
	return nil, application.NewError("invitation.unavailable")
}

func (unavailableBrowserInvitationApplication) AcceptBrowserInvitation(context.Context, application.Invocation, application.BrowserInvitationAcceptanceCommand) (*application.InvitationAcceptanceView, error) {
	return nil, application.NewError("invitation.unavailable")
}

func (unavailableBrowserInvitationApplication) AcceptBrowserInvitationWithSession(context.Context, application.Invocation, application.BrowserInvitationSessionAcceptanceCommand) (*application.InvitationAcceptanceView, error) {
	return nil, application.NewError("invitation.unavailable")
}

func browserInvitationResource(application BrowserInvitationApplication, cookies browserCookies) resource {
	module := browserInvitationResourceModule{application: application, cookies: cookies}
	return newResource(
		"browser-invitations",
		publicRoute(
			http.MethodPost,
			apiPath(literal("auth"), literal("browser"), literal("invitations")),
			[]string{
				"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable",
				"invitation.invalid", "invitation.unavailable",
			},
			module.start,
		),
		publicRoute(
			http.MethodPost,
			apiPath(literal("auth"), literal("browser"), literal("invitations"), literal("accept")),
			[]string{
				"request.invalid", "authentication.rate_limited", "authentication.rate_limit_unavailable",
				"invitation.invalid", "invitation.user_invalid", "invitation.mail_unavailable",
				"invitation.unavailable", "authentication.password.invalid",
			},
			module.accept,
		),
		sessionRoute(
			http.MethodPost,
			apiPath(literal("auth"), literal("browser"), literal("invitations"), literal("accept-session")),
			sessionAuthenticationMutationErrorCodes(
				"authentication.rate_limited", "authentication.rate_limit_unavailable",
				"invitation.invalid", "invitation.unavailable",
			),
			module.acceptWithSession,
		),
	)
}

func (module browserInvitationResourceModule) start(request operationRequest) (operationResult, error) {
	var body browserInvitationStartRequest
	if err := request.decodeJSON(&body, "start_browser_invitation"); err != nil {
		return operationResult{}, err
	}
	started, err := module.application.StartBrowserInvitation(
		request.context,
		request.invocation(),
		application.StartBrowserInvitationCommand{Claim: body.Claim, Source: request.request.RemoteAddr},
	)
	if err != nil {
		return operationResult{}, err
	}
	headers := captureResponseHeaders(func(writer http.ResponseWriter) {
		module.cookies.attachInvitationProof(writer, started.BrowserProof, started.ExpiresAt)
	})
	headers.Set("Cache-Control", "no-store")
	return jsonResult(http.StatusCreated, browserInvitationStartResponse{
		Handle: started.Handle, Purpose: string(started.Purpose), Requirement: string(started.Requirement),
		ExpiresAt: started.ExpiresAt,
	}).withHeaders(headers), nil
}

func (module browserInvitationResourceModule) accept(request operationRequest) (operationResult, error) {
	var body browserInvitationAcceptanceRequest
	if err := request.decodeJSON(&body, "accept_browser_invitation"); err != nil {
		return operationResult{}, err
	}
	proof, err := module.proof(request.request)
	if err != nil {
		return operationResult{}, err
	}
	accepted, err := module.application.AcceptBrowserInvitation(
		request.context,
		request.invocation(),
		application.BrowserInvitationAcceptanceCommand{
			Handle: body.Handle, BrowserProof: proof, Password: body.Password, Username: body.Username,
			DisplayName: body.DisplayName, FirstName: body.FirstName, LastName: body.LastName,
			Locale: body.Locale, Timezone: body.Timezone, Source: request.request.RemoteAddr,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)).withHeaders(module.successHeaders()), nil
}

func (module browserInvitationResourceModule) acceptWithSession(request operationRequest) (operationResult, error) {
	var body browserInvitationSessionAcceptanceRequest
	if err := request.decodeJSON(&body, "accept_browser_invitation_with_session"); err != nil {
		return operationResult{}, err
	}
	proof, err := module.proof(request.request)
	if err != nil {
		return operationResult{}, err
	}
	accepted, err := module.application.AcceptBrowserInvitationWithSession(
		request.context,
		request.invocation(),
		application.BrowserInvitationSessionAcceptanceCommand{
			Handle: body.Handle, BrowserProof: proof, Source: request.request.RemoteAddr,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)).withHeaders(module.successHeaders()), nil
}

func (module browserInvitationResourceModule) proof(request *http.Request) (string, error) {
	proof, err := singleCookieValue(request, BrowserInvitationProofCookieName)
	if err != nil || proof == "" {
		return "", application.NewError("invitation.invalid")
	}
	return proof, nil
}

func (module browserInvitationResourceModule) successHeaders() http.Header {
	headers := captureResponseHeaders(module.cookies.clearInvitationProof)
	headers.Set("Cache-Control", "no-store")
	return headers
}
