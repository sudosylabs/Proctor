// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
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
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToUserForRequest(
		r.Context(), principal, userID, model.ActionUserManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	user, appErr := a.application.SetUserDisabled(
		ctx, principal, RequestMetadata(ctx), userID, disabled,
	)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, user, appErr)
}

func (a *API) revokeUserSessions(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToUserForRequest(
		r.Context(), principal, userID, model.ActionSessionManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	appErr = a.application.RevokeUserSessions(ctx, principal, RequestMetadata(ctx), userID)
	writeNoContent(w, r.WithContext(ctx), a, appErr)
}

func (a *API) listUserSessions(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToUserForRequest(
		r.Context(),
		principal,
		userID,
		model.ActionSessionView,
		RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	includeRevoked, err := strconv.ParseBool(
		defaultQuery(r, "include_revoked", "false"),
	)
	if err != nil {
		WriteError(w, r, invalidRequestError("include_revoked", err))
		return
	}
	sessions, appErr := a.application.ListUserSessions(
		ctx,
		principal,
		RequestMetadata(ctx),
		userID,
		includeRevoked,
	)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, sessions, appErr)
}

func (a *API) revokeUserSession(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	params, ok := RequestParams(r.Context())
	if !ok {
		WriteError(w, r, invalidRequestError("route_params", nil))
		return
	}
	sessionID, appErr := params.RequireSessionId()
	if appErr != nil {
		WriteError(w, r, appErr)
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToUserForRequest(
		r.Context(),
		principal,
		userID,
		model.ActionSessionManage,
		RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	appErr = a.application.RevokeUserSession(
		ctx,
		principal,
		RequestMetadata(ctx),
		userID,
		sessionID,
	)
	writeNoContent(w, r.WithContext(ctx), a, appErr)
}

func defaultQuery(r *http.Request, key, fallback string) string {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	return value
}
