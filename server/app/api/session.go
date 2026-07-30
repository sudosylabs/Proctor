// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/user.go session-management
// handlers. Proctor exposes self-service routes until scoped administrative
// authorization exists, while retaining strict session ownership and
// application-layer revocation.

package api

import (
	"net/http"
)

type revokeSessionRequest struct {
	SessionID string `json:"session_id"`
}

func (a *API) InitSessions() error {
	if err := a.Register(
		a.BaseRoutes.CurrentUser,
		"/sessions",
		http.MethodGet,
		a.APISessionRequired(http.HandlerFunc(a.getSessions)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.CurrentUser,
		"/sessions/revoke",
		http.MethodPost,
		a.APISessionRequired(http.HandlerFunc(a.revokeSession)),
	); err != nil {
		return err
	}
	return a.Register(
		a.BaseRoutes.CurrentUser,
		"/sessions/revoke-all",
		http.MethodPost,
		a.APISessionRequired(http.HandlerFunc(a.revokeAllSessions)),
	)
}

func (a *API) getSessions(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	sessions, appErr := a.application.GetSessions(request.Context(), principal)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusOK, sessions)
}

func (a *API) revokeSession(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	var input revokeSessionRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		WriteError(writer, request, invalidRequestError("revokeSession", err))
		return
	}
	if appErr := a.application.RevokeSession(
		request.Context(),
		principal,
		input.SessionID,
	); appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	if input.SessionID == principal.SessionId &&
		credentialSourceFromContext(request.Context()) == credentialSourceCookie {
		a.cookies.clear(writer)
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) revokeAllSessions(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	if appErr := a.application.RevokeAllSessions(
		request.Context(),
		principal,
	); appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	if credentialSourceFromContext(request.Context()) == credentialSourceCookie {
		a.cookies.clear(writer)
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}
