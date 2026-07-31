// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (a *API) initUserAdministration() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.Users, "", http.MethodGet, a.listUsers},
		{a.BaseRoutes.User, "", http.MethodGet, a.getUser},
		{a.BaseRoutes.User, "", http.MethodPatch, a.patchUser},
		{a.BaseRoutes.User, "/disable", http.MethodPost, a.disableUser},
		{a.BaseRoutes.User, "/enable", http.MethodPost, a.enableUser},
		{a.BaseRoutes.User, "/sessions/revoke-all", http.MethodPost, a.revokeUserSessions},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method,
			a.APISessionRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	principal, ok := requiredPrincipal(w, r)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		r.Context(), principal, model.ActionInstitutionManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	includeDisabled, err := strconv.ParseBool(defaultQuery(r, "include_disabled", "false"))
	if err != nil {
		WriteError(w, r, invalidRequestError("include_disabled", err))
		return
	}
	users, appErr := a.application.ListUsers(ctx, principal, RequestMetadata(ctx), store.UserListOptions{
		Query:           r.URL.Query().Get("q"),
		AfterUsername:   r.URL.Query().Get("after_username"),
		AfterId:         r.URL.Query().Get("after_id"),
		Limit:           limit,
		IncludeDisabled: includeDisabled,
	})
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, users, appErr)
}

func (a *API) getUser(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToUserForRequest(
		r.Context(), principal, userID, model.ActionUserView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	user, appErr := a.application.GetUserForPrincipal(ctx, principal, RequestMetadata(ctx), userID)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, user, appErr)
}

func (a *API) patchUser(w http.ResponseWriter, r *http.Request) {
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
	r = r.WithContext(ctx)
	var patch model.UserPatch
	if !decodeJSON(w, r, &patch, "patchUser") {
		return
	}
	user, appErr := a.application.PatchUser(ctx, principal, RequestMetadata(ctx), userID, &patch)
	writeResult(w, r, a, http.StatusOK, user, appErr)
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
		r.Context(), principal, userID, model.ActionUserManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	appErr = a.application.RevokeUserSessions(ctx, principal, RequestMetadata(ctx), userID)
	writeNoContent(w, r.WithContext(ctx), a, appErr)
}

func defaultQuery(r *http.Request, key, fallback string) string {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	return value
}
