// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
)

func (a *API) InitClasses() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.AcademicUnit, "/classes", http.MethodGet, a.searchClasses},
		{a.BaseRoutes.ProgrammeLevel, "/classes", http.MethodGet, a.listClasses},
		{a.BaseRoutes.ProgrammeLevel, "/classes", http.MethodPost, a.createClass},
		{a.BaseRoutes.Class, "", http.MethodGet, a.getClass},
		{a.BaseRoutes.Class, "", http.MethodPatch, a.patchClass},
		{a.BaseRoutes.Class, "", http.MethodDelete, a.archiveClass},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method,
			a.APIPrincipalRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) searchClasses(w http.ResponseWriter, r *http.Request) {
	principal, unitID, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToAcademicUnitForRequest(
		r.Context(), principal, unitID, model.ActionAcademicUnitView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	classes, appErr := a.application.SearchClasses(
		ctx, principal, RequestMetadata(ctx), unitID, r.URL.Query().Get("q"), limit,
	)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, classes, appErr)
}

func (a *API) listClasses(w http.ResponseWriter, r *http.Request) {
	principal, levelID, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeLevelForRequest(
		r.Context(), principal, levelID, model.ActionAcademicUnitView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	classes, appErr := a.application.ListClasses(ctx, principal, RequestMetadata(ctx), levelID)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, classes, appErr)
}

func (a *API) createClass(w http.ResponseWriter, r *http.Request) {
	principal, levelID, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeLevelForRequest(
		r.Context(), principal, levelID, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	r = r.WithContext(ctx)
	var class model.Class
	if !decodeJSON(w, r, &class, "createClass") {
		return
	}
	class.ProgrammeLevelId = levelID
	saved, appErr := a.application.CreateClass(ctx, principal, RequestMetadata(ctx), &class)
	writeResult(w, r, a, http.StatusCreated, saved, appErr)
}

func (a *API) getClass(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToClassForRequest(
		r.Context(), principal, id, model.ActionClassView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	class, appErr := a.application.GetClass(ctx, principal, RequestMetadata(ctx), id)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, class, appErr)
}

func (a *API) patchClass(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToClassAdministrationForRequest(
		r.Context(), principal, id, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	r = r.WithContext(ctx)
	var patch model.ClassPatch
	if !decodeJSON(w, r, &patch, "patchClass") {
		return
	}
	class, appErr := a.application.PatchClass(ctx, principal, RequestMetadata(ctx), id, &patch)
	writeResult(w, r, a, http.StatusOK, class, appErr)
}

func (a *API) archiveClass(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToClassAdministrationForRequest(
		r.Context(), principal, id, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	appErr = a.application.ArchiveClass(ctx, principal, RequestMetadata(ctx), id)
	writeNoContent(w, r.WithContext(ctx), a, appErr)
}
