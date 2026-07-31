// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
)

func (a *API) InitAcademicPeriods() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.AcademicPeriods, "", http.MethodGet, a.listAcademicPeriods},
		{a.BaseRoutes.AcademicPeriods, "", http.MethodPost, a.createAcademicPeriod},
		{a.BaseRoutes.AcademicPeriod, "", http.MethodGet, a.getAcademicPeriod},
		{a.BaseRoutes.AcademicPeriod, "", http.MethodPatch, a.patchAcademicPeriod},
		{a.BaseRoutes.AcademicPeriod, "", http.MethodDelete, a.archiveAcademicPeriod},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method,
			a.APIPrincipalRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listAcademicPeriods(w http.ResponseWriter, r *http.Request) {
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
	periods, appErr := a.application.ListAcademicPeriods(
		ctx, principal, RequestMetadata(ctx), r.URL.Query().Get("q"), limit,
	)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, periods, appErr)
}

func (a *API) createAcademicPeriod(w http.ResponseWriter, r *http.Request) {
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
	var period model.AcademicPeriod
	if !decodeJSON(w, r, &period, "createAcademicPeriod") {
		return
	}
	saved, appErr := a.application.CreateAcademicPeriod(ctx, principal, RequestMetadata(ctx), &period)
	writeResult(w, r, a, http.StatusCreated, saved, appErr)
}

func (a *API) getAcademicPeriod(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicPeriodId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		r.Context(), principal, model.ActionInstitutionManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	period, appErr := a.application.GetAcademicPeriod(ctx, principal, RequestMetadata(ctx), id)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, period, appErr)
}

func (a *API) patchAcademicPeriod(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicPeriodId)
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
	var patch model.AcademicPeriodPatch
	if !decodeJSON(w, r, &patch, "patchAcademicPeriod") {
		return
	}
	period, appErr := a.application.PatchAcademicPeriod(ctx, principal, RequestMetadata(ctx), id, &patch)
	writeResult(w, r, a, http.StatusOK, period, appErr)
}

func (a *API) archiveAcademicPeriod(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicPeriodId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		r.Context(), principal, model.ActionInstitutionManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	appErr = a.application.ArchiveAcademicPeriod(ctx, principal, RequestMetadata(ctx), id)
	writeNoContent(w, r.WithContext(ctx), a, appErr)
}
