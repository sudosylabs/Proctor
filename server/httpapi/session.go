// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/api4/user.go session-management
// handlers. Proctor exposes self-service routes until scoped administrative
// authorization exists, while retaining strict session ownership and
// application-layer revocation.

package httpapi

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type revokeSessionRequest struct {
	SessionID string `json:"session_id"`
}

func sessionResponsesFromModels(request *http.Request, sessions []*model.Session) []sessionResponse {
	// Historical self-service listing returns a non-null array body. Prefer
	// stability over introducing an envelope mid-migration.
	items := make([]sessionResponse, 0, len(sessions))
	for _, session := range sessions {
		if mapped := sessionResponseFromModel(request, session); mapped != nil {
			items = append(items, *mapped)
		}
	}
	return items
}

type sessionResourceModule struct {
	sessions Sessions
	cookies  browserCookies
}

func sessionResource(sessions Sessions, cookies browserCookies) resource {
	module := sessionResourceModule{sessions: sessions, cookies: cookies}
	base := apiPath(literal("users"), literal("me"), literal("sessions"))
	return newResource(
		"sessions",
		sessionRoute(http.MethodGet, base, personalAccessTokenSessionCodes("authentication.internal"), module.list),
		sessionRoute(http.MethodPost, appendRoutePath(base, literal("revoke")), personalAccessTokenSessionMutationCodes("request.invalid", "session.id.invalid", "session.not_found", "authentication.internal"), module.revoke),
		sessionRoute(http.MethodPost, appendRoutePath(base, literal("revoke-all")), personalAccessTokenSessionMutationCodes("authentication.internal"), module.revokeAll),
	)
}

func (module sessionResourceModule) list(request operationRequest) (operationResult, error) {
	sessions, err := module.sessions.ListSessions(
		request.context,
		request.invocation(),
		application.ListSessionsQuery{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, sessionResponsesFromModels(request.request, sessions)), nil
}

func (module sessionResourceModule) revoke(request operationRequest) (operationResult, error) {
	var input revokeSessionRequest
	if err := request.decodeJSON(&input, "revokeSession"); err != nil {
		return operationResult{}, err
	}
	if err := module.sessions.RevokeSession(
		request.context,
		request.invocation(),
		application.RevokeSessionCommand{SessionID: input.SessionID},
	); err != nil {
		return operationResult{}, err
	}
	result := noContentResult()
	if input.SessionID == request.principal.SessionID.String() &&
		credentialSourceFromContext(request.context) == credentialSourceCookie {
		result = result.withHeaders(captureResponseHeaders(module.cookies.clear))
	}
	return result, nil
}

func (module sessionResourceModule) revokeAll(request operationRequest) (operationResult, error) {
	if err := module.sessions.RevokeAllSessions(
		request.context,
		request.invocation(),
		application.RevokeAllSessionsCommand{},
	); err != nil {
		return operationResult{}, err
	}
	result := noContentResult()
	if credentialSourceFromContext(request.context) == credentialSourceCookie {
		result = result.withHeaders(captureResponseHeaders(module.cookies.clear))
	}
	return result, nil
}
