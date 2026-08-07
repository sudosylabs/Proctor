// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func (a *API) initUserAdministration() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.User, "/disable", http.MethodPost, a.disableUser},
		{a.BaseRoutes.User, "/enable", http.MethodPost, a.enableUser},
		{a.BaseRoutes.UserSessions, "", http.MethodGet, a.listUserSessions},
		{a.BaseRoutes.UserSessions, "/revoke-all", http.MethodPost, a.revokeUserSessions},
		{a.BaseRoutes.UserSession, "", http.MethodDelete, a.revokeUserSession},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method,
			a.APIPrincipalRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) disableUser(w http.ResponseWriter, r *http.Request) {
	a.setUserDisabled(w, r, true)
}

func (a *API) enableUser(w http.ResponseWriter, r *http.Request) {
	a.setUserDisabled(w, r, false)
}

func (a *API) setUserDisabled(w http.ResponseWriter, r *http.Request, disabled bool) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	user, err := a.accountStates.SetUserEnabled(
		r.Context(),
		application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.SetUserEnabledCommand{ID: userID, Enabled: !disabled},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, userProfileResponseFromModel(user))
}

func (a *API) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	err := a.sessionAdministrations.RevokeUserSessions(
		r.Context(),
		application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.RevokeUserSessionsCommand{UserID: userID},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listUserSessions(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	includeRevoked, err := strconv.ParseBool(
		defaultQuery(r, "include_revoked", "false"),
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, application.NewError("request.invalid").WithField("field", "include_revoked"))
		return
	}
	sessions, listErr := a.sessionAdministrations.ListUserSessions(
		r.Context(),
		application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.ListUserSessionsQuery{UserID: userID, IncludeRevoked: includeRevoked},
	)
	if listErr != nil {
		writeApplicationError(w, r, a.logger, listErr)
		return
	}
	if sessions == nil {
		sessions = []*model.Session{}
	}
	// Session models deliberately exclude bearer/refresh credentials; keep the
	// existing bare-array wire shape for administrative listing.
	writeJSON(w, http.StatusOK, sessions)
}

func (a *API) revokeUserSession(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	params, ok := RequestParams(r.Context())
	if !ok {
		writeApplicationError(w, r, a.logger, application.NewError("request.invalid").WithField("field", "route_params"))
		return
	}
	sessionID, appErr := params.RequireSessionId()
	if appErr != nil {
		WriteError(w, r, appErr)
		return
	}
	err := a.sessionAdministrations.RevokeUserSession(
		r.Context(),
		application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.RevokeUserSessionCommand{UserID: userID, SessionID: sessionID},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func defaultQuery(r *http.Request, key, fallback string) string {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	return value
}
