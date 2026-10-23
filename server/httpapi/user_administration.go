// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

type userAdministrationResourceModule struct {
	accounts AccountStateApplication
	sessions SessionAdministrationApplication
}

func userAdministrationResource(
	accounts AccountStateApplication,
	sessions SessionAdministrationApplication,
) apiResource {
	module := userAdministrationResourceModule{accounts: accounts, sessions: sessions}
	return newResource(
		"user-administration",
		principalRoute(http.MethodPost, apiPath(literal("users"), canonicalID("user_id"), literal("disable")), userAdministrationMutationCodes("user.invalid", "user.conflict", "user.last_system_admin"), module.disable),
		principalRoute(http.MethodPost, apiPath(literal("users"), canonicalID("user_id"), literal("enable")), userAdministrationMutationCodes("user.invalid", "user.conflict", "user.last_system_admin"), module.enable),
		principalRoute(http.MethodGet, apiPath(literal("users"), canonicalID("user_id"), literal("sessions")), userAdministrationReadCodes("session.not_found"), module.listSessions),
		principalRoute(http.MethodPost, apiPath(literal("users"), canonicalID("user_id"), literal("sessions"), literal("revoke-all")), userAdministrationMutationCodes("session.not_found"), module.revokeAllSessions),
		principalRoute(http.MethodDelete, apiPath(literal("users"), canonicalID("user_id"), literal("sessions"), canonicalID("session_id")), userAdministrationMutationCodes("session.not_found"), module.revokeSession),
	)
}

func userAdministrationReadCodes(extra ...string) []string {
	codes := []string{
		"authorization.denied", "authorization.request.invalid", "authorization.unavailable",
		"request.invalid", "resource.not_found",
	}
	codes = append(codes, extra...)
	return append(codes, "administration.unavailable")
}

func userAdministrationMutationCodes(extra ...string) []string {
	codes := []string{
		"authorization.denied", "authorization.request.invalid",
		"authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found",
	}
	codes = append(codes, extra...)
	return append(codes, "administration.unavailable")
}

func (m userAdministrationResourceModule) disable(request operationRequest) (operationResult, error) {
	return m.setEnabled(request, false)
}

func (m userAdministrationResourceModule) enable(request operationRequest) (operationResult, error) {
	return m.setEnabled(request, true)
}

func (m userAdministrationResourceModule) setEnabled(request operationRequest, enabled bool) (operationResult, error) {
	userID, err := request.params.RequireUserID()
	if err != nil {
		return operationResult{}, err
	}
	user, err := m.accounts.SetUserEnabled(
		request.context,
		request.invocation(),
		app.SetUserEnabledCommand{ID: userID, Enabled: enabled},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userProfileResponseFromModel(user)), nil
}

func (m userAdministrationResourceModule) revokeAllSessions(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserID()
	if err != nil {
		return operationResult{}, err
	}
	err = m.sessions.RevokeUserSessions(
		request.context,
		request.invocation(),
		app.RevokeUserSessionsCommand{UserID: userID},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (m userAdministrationResourceModule) listSessions(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserID()
	if err != nil {
		return operationResult{}, err
	}
	includeRevoked, err := strconv.ParseBool(
		defaultQuery(request.request, "include_revoked", "false"),
	)
	if err != nil {
		return operationResult{}, apperror.New("request.invalid").WithField("field", "include_revoked")
	}
	query := app.ListUserSessionsQuery{UserID: userID, IncludeRevoked: includeRevoked, Limit: defaultSessionPageSize}
	values := request.request.URL.Query()
	if raw := values.Get("limit"); raw != "" {
		limit, parseErr := strconv.Atoi(raw)
		if parseErr != nil || limit < 1 || limit > maximumSessionPageSize {
			return operationResult{}, apperror.Invalid("request.invalid", "limit")
		}
		query.Limit = limit
	}
	if raw := values.Get("cursor"); raw != "" {
		cursor, decodeErr := decodeSessionCursor(raw)
		if decodeErr != nil {
			return operationResult{}, apperror.Invalid("request.invalid", "cursor")
		}
		query.BeforeCreatedAt, _ = model.ParseRFC3339(cursor.CreatedAt)
		query.BeforeID = model.SessionID(cursor.ID)
	}
	page, listErr := m.sessions.ListUserSessions(request.context, request.invocation(), query)
	if listErr != nil {
		return operationResult{}, listErr
	}
	response := sessionAdministrationListResponse{Items: sessionResponsesFromModels(request.request, page.Items)}
	if page.HasMore && len(page.Items) == 0 {
		return operationResult{}, unpresentableResult(errors.New("session page made no progress"))
	}
	if page.HasMore {
		last := page.Items[len(page.Items)-1]
		var err error
		response.NextCursor, err = encodeOpaqueCursor(sessionCursor{
			CreatedAt: model.FormatRFC3339(last.CreatedAt), ID: last.ID.String(),
		}, sessionCursorSpec())
		if err != nil {
			return operationResult{}, unpresentableResult(err)
		}
	}
	return jsonResult(http.StatusOK, response), nil
}

const (
	defaultSessionPageSize = 50
	maximumSessionPageSize = 200
)

// sessionAdministrationListResponse is one page of a User's Sessions, newest
// first.
type sessionAdministrationListResponse struct {
	Items      []sessionResponse `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type sessionCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func decodeSessionCursor(raw string) (sessionCursor, error) {
	return decodeOpaqueCursor(raw, sessionCursorSpec())
}

func sessionCursorSpec() opaqueCursorSpec[sessionCursor] {
	return opaqueCursorSpec[sessionCursor]{
		label: "session", maximumEncodedLength: defaultOpaqueCursorMaximumEncodedLength,
		members: []string{"created_at", "id"},
		validate: func(cursor sessionCursor) error {
			createdAt, err := model.ParseRFC3339(cursor.CreatedAt)
			if err != nil || createdAt.IsZero() || !model.SessionID(cursor.ID).IsValid() {
				return errors.New("invalid session keyset")
			}
			return nil
		},
	}
}

func (m userAdministrationResourceModule) revokeSession(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserID()
	if err != nil {
		return operationResult{}, err
	}
	sessionID, err := request.params.RequireSessionID()
	if err != nil {
		return operationResult{}, err
	}
	err = m.sessions.RevokeUserSession(
		request.context,
		request.invocation(),
		app.RevokeUserSessionCommand{UserID: userID, SessionID: sessionID},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func defaultQuery(r *http.Request, key, fallback string) string {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	return value
}
