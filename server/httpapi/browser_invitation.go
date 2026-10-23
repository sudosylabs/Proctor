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
)

type BrowserInvitationApplication interface {
	StartBrowserInvitation(context.Context, invocation.Invocation, app.StartBrowserInvitationCommand) (*app.BrowserInvitationStart, error)
	AcceptBrowserInvitation(context.Context, invocation.Invocation, app.BrowserInvitationAcceptanceCommand) (*app.InvitationAcceptanceView, error)
	AcceptBrowserInvitationWithSession(context.Context, invocation.Invocation, app.BrowserInvitationSessionAcceptanceCommand) (*app.InvitationAcceptanceView, error)
}

type browserInvitationStartRequest struct {
	Claim string `json:"claim"`
}

type browserInvitationStartResponse struct {
	Handle      string `json:"handle"`
	Purpose     string `json:"purpose"`
	Requirement string `json:"requirement"`
	ExpiresAt   string `json:"expires_at"`
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

func (unavailableBrowserInvitationApplication) StartBrowserInvitation(context.Context, invocation.Invocation, app.StartBrowserInvitationCommand) (*app.BrowserInvitationStart, error) {
	return nil, errUnpresentableResult
}

func (unavailableBrowserInvitationApplication) AcceptBrowserInvitation(context.Context, invocation.Invocation, app.BrowserInvitationAcceptanceCommand) (*app.InvitationAcceptanceView, error) {
	return nil, errUnpresentableResult
}

func (unavailableBrowserInvitationApplication) AcceptBrowserInvitationWithSession(context.Context, invocation.Invocation, app.BrowserInvitationSessionAcceptanceCommand) (*app.InvitationAcceptanceView, error) {
	return nil, errUnpresentableResult
}

func browserInvitationResource(application BrowserInvitationApplication, cookies browserCookies) apiResource {
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
				"invitation.unavailable", "authentication.password.invalid", "service.busy",
			},
			module.accept,
		),
		sessionRoute(
			http.MethodPost,
			apiPath(literal("auth"), literal("browser"), literal("invitations"), literal("accept-session")),
			[]string{"authentication.rate_limited", "authentication.rate_limit_unavailable", "invitation.invalid", "invitation.unavailable", "audit.unavailable", "request.invalid"},
			module.acceptWithSession,
		),
	)
}

func (m browserInvitationResourceModule) start(request operationRequest) (operationResult, error) {
	var body browserInvitationStartRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	started, err := m.application.StartBrowserInvitation(
		request.context,
		request.invocation(),
		app.StartBrowserInvitationCommand{Claim: body.Claim, Source: request.metadata.IPAddress},
	)
	if err != nil {
		return operationResult{}, err
	}
	headers := captureResponseHeaders(func(writer http.ResponseWriter) {
		m.cookies.attachInvitationProof(writer, started.BrowserProof, started.ExpiresAt)
	})
	headers.Set("Cache-Control", "no-store")
	return jsonResult(http.StatusCreated, browserInvitationStartResponse{
		Handle: started.Handle, Purpose: string(started.Purpose), Requirement: string(started.Requirement),
		ExpiresAt: wireTimeFromMillis(started.ExpiresAt),
	}).withHeaders(headers), nil
}

func (m browserInvitationResourceModule) accept(request operationRequest) (operationResult, error) {
	var body browserInvitationAcceptanceRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	proof := m.proof(request.request)
	accepted, err := m.application.AcceptBrowserInvitation(
		request.context,
		request.invocation(),
		app.BrowserInvitationAcceptanceCommand{
			Handle: body.Handle, BrowserProof: proof, Password: body.Password, Username: body.Username,
			DisplayName: body.DisplayName, FirstName: body.FirstName, LastName: body.LastName,
			Locale: body.Locale, Timezone: body.Timezone, Source: request.metadata.IPAddress,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)).withHeaders(m.successHeaders()), nil
}

func (m browserInvitationResourceModule) acceptWithSession(request operationRequest) (operationResult, error) {
	var body browserInvitationSessionAcceptanceRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	proof := m.proof(request.request)
	accepted, err := m.application.AcceptBrowserInvitationWithSession(
		request.context,
		request.invocation(),
		app.BrowserInvitationSessionAcceptanceCommand{
			Handle: body.Handle, BrowserProof: proof, Source: request.metadata.IPAddress,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, invitationAcceptanceResponseFromView(accepted)).withHeaders(m.successHeaders()), nil
}

// proof returns the browser proof cookie, or "" when it is absent or
// ambiguous. The application owns the decision and rejects a missing proof as
// invitation.invalid.
func (m browserInvitationResourceModule) proof(request *http.Request) string {
	proof, err := singleCookieValue(request, BrowserInvitationProofCookieName)
	if err != nil {
		return ""
	}
	return proof
}

func (m browserInvitationResourceModule) successHeaders() http.Header {
	headers := captureResponseHeaders(m.cookies.clearInvitationProof)
	headers.Set("Cache-Control", "no-store")
	return headers
}
