// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/api4/user.go session-management
// handlers. Proctor exposes self-service routes until scoped administrative
// authorization exists, while retaining strict session ownership and
// application-layer revocation.

package api

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/mlog"
)

type revokeSessionRequest struct {
	SessionID string `json:"session_id"`
}

func getSessionsHandler(application Authentication, logger *mlog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := Principal(request.Context())
		if !ok {
			WriteError(writer, request, authenticationRequiredError())
			return
		}
		sessions, appErr := application.GetSessions(request.Context(), principal)
		if appErr != nil {
			writeApplicationError(writer, request, logger, appErr)
			return
		}
		writeJSON(writer, http.StatusOK, sessions)
	})
}

func revokeSessionHandler(application Authentication, logger *mlog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
		if appErr := application.RevokeSession(
			request.Context(),
			principal,
			input.SessionID,
		); appErr != nil {
			writeApplicationError(writer, request, logger, appErr)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusNoContent)
	})
}

func revokeAllSessionsHandler(application Authentication, logger *mlog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := Principal(request.Context())
		if !ok {
			WriteError(writer, request, authenticationRequiredError())
			return
		}
		if appErr := application.RevokeAllSessions(
			request.Context(),
			principal,
		); appErr != nil {
			writeApplicationError(writer, request, logger, appErr)
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(http.StatusNoContent)
	})
}
