// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type userAdministrationResourceModule struct {
	accounts AccountStateApplication
	sessions SessionAdministrationApplication
}

func userAdministrationResource(
	accounts AccountStateApplication,
	sessions SessionAdministrationApplication,
) resource {
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
		"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous",
		"authorization.denied", "authorization.request.invalid", "authorization.unavailable",
		"request.invalid", "resource.not_found",
	}
	codes = append(codes, extra...)
	return append(codes, "administration.unavailable")
}

func userAdministrationMutationCodes(extra ...string) []string {
	codes := []string{
		"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous",
		"authentication.csrf.invalid", "authorization.denied", "authorization.request.invalid",
		"authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found",
	}
	codes = append(codes, extra...)
	return append(codes, "administration.unavailable")
}

func (module userAdministrationResourceModule) disable(request operationRequest) (operationResult, error) {
	return module.setEnabled(request, false)
}

func (module userAdministrationResourceModule) enable(request operationRequest) (operationResult, error) {
	return module.setEnabled(request, true)
}

func (module userAdministrationResourceModule) setEnabled(request operationRequest, enabled bool) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	user, err := module.accounts.SetUserEnabled(
		request.context,
		request.invocation(),
		application.SetUserEnabledCommand{ID: userID, Enabled: enabled},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, userProfileResponseFromModel(user)), nil
}

func (module userAdministrationResourceModule) revokeAllSessions(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	err = module.sessions.RevokeUserSessions(
		request.context,
		request.invocation(),
		application.RevokeUserSessionsCommand{UserID: userID},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (module userAdministrationResourceModule) listSessions(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	includeRevoked, err := strconv.ParseBool(
		defaultQuery(request.request, "include_revoked", "false"),
	)
	if err != nil {
		return operationResult{}, application.NewError("request.invalid").WithField("field", "include_revoked")
	}
	sessions, listErr := module.sessions.ListUserSessions(
		request.context,
		request.invocation(),
		application.ListUserSessionsQuery{UserID: userID, IncludeRevoked: includeRevoked},
	)
	if listErr != nil {
		return operationResult{}, listErr
	}
	if sessions == nil {
		sessions = []*model.Session{}
	}
	// Transport-owned DTOs keep the historical bare-array millis wire shape
	// while domain Session values use typed IDs and UTC times.
	return jsonResult(http.StatusOK, sessionResponsesFromModels(sessions)), nil
}

func (module userAdministrationResourceModule) revokeSession(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	sessionID, err := request.params.RequireSessionId()
	if err != nil {
		return operationResult{}, err
	}
	err = module.sessions.RevokeUserSession(
		request.context,
		request.invocation(),
		application.RevokeUserSessionCommand{UserID: userID, SessionID: sessionID},
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
