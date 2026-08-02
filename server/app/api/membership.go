// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
)

func (a *API) InitMemberships() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.Class, "/members", http.MethodGet, a.listClassMembers},
		{a.BaseRoutes.Class, "/members", http.MethodPost, a.enrollClassMember},
		{a.BaseRoutes.ClassMember, "", http.MethodDelete, a.endClassMember},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method,
			a.APIPrincipalRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listClassMembers(w http.ResponseWriter, r *http.Request) {
	principal, classID, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToClassForRequest(
		r.Context(), principal, classID, model.ActionClassMembersView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	activeAt, ok := queryActiveAt(w, r)
	if !ok {
		return
	}
	members, appErr := a.application.ListClassMembers(
		ctx, principal, RequestMetadata(ctx), classID, activeAt,
	)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, members, appErr)
}

func (a *API) enrollClassMember(w http.ResponseWriter, r *http.Request) {
	principal, classID, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToClassForRequest(
		r.Context(), principal, classID, model.ActionClassMembersManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	r = r.WithContext(ctx)
	var member model.ClassMember
	if !decodeJSON(w, r, &member, "enrollClassMember") {
		return
	}
	member.ClassId = classID
	enrollment, appErr := a.application.EnrollClassMember(ctx, principal, RequestMetadata(ctx), &member)
	writeResult(w, r, a, http.StatusCreated, enrollment, appErr)
}

func (a *API) endClassMember(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireClassMemberId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToClassMemberForRequest(
		r.Context(), principal, id, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	ended, appErr := a.application.EndClassMember(ctx, principal, RequestMetadata(ctx), id)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, ended, appErr)
}

func queryActiveAt(w http.ResponseWriter, r *http.Request) (int64, bool) {
	history := r.URL.Query().Get("history")
	if history != "" {
		if history != "true" && history != "false" {
			WriteError(w, r, invalidRequestError("history", nil))
			return 0, false
		}
		if history == "true" {
			return 0, true
		}
	}
	value := r.URL.Query().Get("active_at")
	if value == "" {
		return model.GetMillis(), true
	}
	activeAt, err := strconv.ParseInt(value, 10, 64)
	if err != nil || activeAt <= 0 {
		WriteError(w, r, invalidRequestError("active_at", err))
		return 0, false
	}
	return activeAt, true
}
