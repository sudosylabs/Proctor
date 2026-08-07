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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type revokeSessionRequest struct {
	SessionID string `json:"session_id"`
}

func sessionResponsesFromModels(sessions []*model.Session) []sessionResponse {
	// Historical self-service listing returns a non-null array body. Prefer
	// stability over introducing an envelope mid-migration.
	items := make([]sessionResponse, 0, len(sessions))
	for _, session := range sessions {
		if mapped := sessionResponseFromModel(session); mapped != nil {
			items = append(items, *mapped)
		}
	}
	return items
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
	sessions, err := a.application.ListSessions(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.ListSessionsQuery{},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusOK, sessionResponsesFromModels(sessions))
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
	if err := a.application.RevokeSession(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.RevokeSessionCommand{SessionID: input.SessionID},
	); err != nil {
		writeApplicationError(writer, request, a.logger, err)
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
	if err := a.application.RevokeAllSessions(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.RevokeAllSessionsCommand{},
	); err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	if credentialSourceFromContext(request.Context()) == credentialSourceCookie {
		a.cookies.clear(writer)
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}
