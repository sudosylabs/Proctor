// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
)

func (a *API) InitInstitution() error {
	if err := a.Register(a.BaseRoutes.Institution, "", http.MethodGet,
		a.APIPrincipalRequired(http.HandlerFunc(a.getInstitution))); err != nil {
		return err
	}
	return a.Register(a.BaseRoutes.Institution, "", http.MethodPatch,
		a.APIPrincipalRequired(http.HandlerFunc(a.patchInstitution)))
}

func (a *API) getInstitution(w http.ResponseWriter, r *http.Request) {
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
	institution, appErr := a.application.GetInstitution(
		ctx, principal, RequestMetadata(ctx),
	)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, institution, appErr)
}

func (a *API) patchInstitution(w http.ResponseWriter, r *http.Request) {
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
	r = r.WithContext(ctx)
	var patch model.InstitutionPatch
	if !decodeJSON(w, r, &patch, "patchInstitution") {
		return
	}
	institution, appErr := a.application.PatchInstitution(
		ctx, principal, RequestMetadata(ctx), &patch,
	)
	writeResult(w, r, a, http.StatusOK, institution, appErr)
}

func (a *API) InitAcademicUnits() error {
	routes := []struct {
		base    *mux.Router
		path    string
		method  string
		handler http.HandlerFunc
	}{
		{a.BaseRoutes.AcademicUnits, "", http.MethodGet, a.listRootAcademicUnits},
		{a.BaseRoutes.AcademicUnits, "", http.MethodPost, a.createRootAcademicUnit},
		{a.BaseRoutes.AcademicUnit, "", http.MethodGet, a.getAcademicUnit},
		{a.BaseRoutes.AcademicUnit, "", http.MethodPatch, a.patchAcademicUnit},
		{a.BaseRoutes.AcademicUnit, "", http.MethodDelete, a.archiveAcademicUnit},
		{a.BaseRoutes.AcademicUnit, "/children", http.MethodGet, a.listAcademicUnitChildren},
		{a.BaseRoutes.AcademicUnit, "/children", http.MethodPost, a.createAcademicUnitChild},
	}
	for _, route := range routes {
		if err := a.Register(
			route.base, route.path, route.method,
			a.APIPrincipalRequired(route.handler),
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listRootAcademicUnits(w http.ResponseWriter, r *http.Request) {
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
	var result any
	if query := r.URL.Query().Get("q"); query != "" {
		limit, valid := queryLimit(w, r)
		if !valid {
			return
		}
		result, appErr = a.application.SearchAcademicUnits(
			ctx, principal, RequestMetadata(ctx), query, limit,
		)
	} else {
		result, appErr = a.application.ListAcademicUnits(
			ctx, principal, RequestMetadata(ctx), "",
		)
	}
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, result, appErr)
}

func (a *API) createRootAcademicUnit(w http.ResponseWriter, r *http.Request) {
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
	r = r.WithContext(ctx)
	var unit model.AcademicUnit
	if !decodeJSON(w, r, &unit, "createRootAcademicUnit") {
		return
	}
	unit.ParentId = ""
	saved, appErr := a.application.CreateAcademicUnit(ctx, principal, RequestMetadata(ctx), &unit)
	writeResult(w, r, a, http.StatusCreated, saved, appErr)
}

func (a *API) getAcademicUnit(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToAcademicUnitForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	unit, appErr := a.application.GetAcademicUnit(ctx, principal, RequestMetadata(ctx), id)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, unit, appErr)
}

func (a *API) patchAcademicUnit(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToAcademicUnitForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	r = r.WithContext(ctx)
	var patch model.AcademicUnitPatch
	if !decodeJSON(w, r, &patch, "patchAcademicUnit") {
		return
	}
	unit, appErr := a.application.PatchAcademicUnit(
		ctx, principal, RequestMetadata(ctx), id, &patch,
	)
	writeResult(w, r, a, http.StatusOK, unit, appErr)
}

func (a *API) archiveAcademicUnit(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToAcademicUnitForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	appErr = a.application.ArchiveAcademicUnit(ctx, principal, RequestMetadata(ctx), id)
	writeNoContent(w, r.WithContext(ctx), a, appErr)
}

func (a *API) listAcademicUnitChildren(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToAcademicUnitForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	units, appErr := a.application.ListAcademicUnits(ctx, principal, RequestMetadata(ctx), id)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, units, appErr)
}

func (a *API) createAcademicUnitChild(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToAcademicUnitForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	r = r.WithContext(ctx)
	var unit model.AcademicUnit
	if !decodeJSON(w, r, &unit, "createAcademicUnitChild") {
		return
	}
	unit.ParentId = id
	saved, appErr := a.application.CreateAcademicUnit(ctx, principal, RequestMetadata(ctx), &unit)
	writeResult(w, r, a, http.StatusCreated, saved, appErr)
}

func queryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return 100, true
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 200 {
		WriteError(w, r, invalidRequestError("limit", err))
		return 0, false
	}
	return limit, true
}

func requiredPrincipal(w http.ResponseWriter, r *http.Request) (model.Principal, bool) {
	principal, ok := Principal(r.Context())
	if !ok {
		WriteError(w, r, authenticationRequiredError())
	}
	return principal, ok
}

type idRequirement func(Params) (string, *model.AppError)

func requiredResourceID(w http.ResponseWriter, r *http.Request, require idRequirement) (model.Principal, string, bool) {
	return principalAndRequiredId(w, r, require)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, where string) bool {
	if err := decodeRequestJSON(r, target); err != nil {
		WriteError(w, r, invalidRequestError(where, err))
		return false
	}
	return true
}

func writeResult(w http.ResponseWriter, r *http.Request, a *API, status int, result any, appErr *model.AppError) {
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	writeJSON(w, status, result)
}

func writeNoContent(w http.ResponseWriter, r *http.Request, a *API, appErr *model.AppError) {
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
