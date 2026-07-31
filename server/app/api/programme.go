// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
)

func (a *API) InitProgrammes() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.AcademicUnit, "/programmes", http.MethodGet, a.listProgrammes},
		{a.BaseRoutes.AcademicUnit, "/programmes", http.MethodPost, a.createProgramme},
		{a.BaseRoutes.Programme, "", http.MethodGet, a.getProgramme},
		{a.BaseRoutes.Programme, "", http.MethodPatch, a.patchProgramme},
		{a.BaseRoutes.Programme, "", http.MethodDelete, a.archiveProgramme},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method,
			a.APISessionRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listProgrammes(w http.ResponseWriter, r *http.Request) {
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
	programmes, appErr := a.application.ListProgrammes(
		ctx, principal, RequestMetadata(ctx), unitID, r.URL.Query().Get("q"), limit,
	)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, programmes, appErr)
}

func (a *API) createProgramme(w http.ResponseWriter, r *http.Request) {
	principal, unitID, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToAcademicUnitForRequest(
		r.Context(), principal, unitID, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	r = r.WithContext(ctx)
	var programme model.Programme
	if !decodeJSON(w, r, &programme, "createProgramme") {
		return
	}
	programme.AcademicUnitId = unitID
	saved, appErr := a.application.CreateProgramme(ctx, principal, RequestMetadata(ctx), &programme)
	writeResult(w, r, a, http.StatusCreated, saved, appErr)
}

func (a *API) getProgramme(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	programme, appErr := a.application.GetProgramme(ctx, principal, RequestMetadata(ctx), id)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, programme, appErr)
}

func (a *API) patchProgramme(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	r = r.WithContext(ctx)
	var patch model.ProgrammePatch
	if !decodeJSON(w, r, &patch, "patchProgramme") {
		return
	}
	programme, appErr := a.application.PatchProgramme(ctx, principal, RequestMetadata(ctx), id, &patch)
	writeResult(w, r, a, http.StatusOK, programme, appErr)
}

func (a *API) archiveProgramme(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	appErr = a.application.ArchiveProgramme(ctx, principal, RequestMetadata(ctx), id)
	writeNoContent(w, r.WithContext(ctx), a, appErr)
}

func (a *API) InitProgrammeLevels() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.Programme, "/levels", http.MethodGet, a.listProgrammeLevels},
		{a.BaseRoutes.Programme, "/levels", http.MethodPost, a.createProgrammeLevel},
		{a.BaseRoutes.ProgrammeLevel, "", http.MethodGet, a.getProgrammeLevel},
		{a.BaseRoutes.ProgrammeLevel, "", http.MethodPatch, a.patchProgrammeLevel},
		{a.BaseRoutes.ProgrammeLevel, "", http.MethodDelete, a.archiveProgrammeLevel},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method,
			a.APISessionRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listProgrammeLevels(w http.ResponseWriter, r *http.Request) {
	principal, programmeID, ok := requiredResourceID(w, r, Params.RequireProgrammeId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeForRequest(
		r.Context(), principal, programmeID, model.ActionAcademicUnitView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	levels, appErr := a.application.ListProgrammeLevels(
		ctx, principal, RequestMetadata(ctx), programmeID, r.URL.Query().Get("q"), limit,
	)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, levels, appErr)
}

func (a *API) createProgrammeLevel(w http.ResponseWriter, r *http.Request) {
	principal, programmeID, ok := requiredResourceID(w, r, Params.RequireProgrammeId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeForRequest(
		r.Context(), principal, programmeID, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	r = r.WithContext(ctx)
	var level model.ProgrammeLevel
	if !decodeJSON(w, r, &level, "createProgrammeLevel") {
		return
	}
	level.ProgrammeId = programmeID
	saved, appErr := a.application.CreateProgrammeLevel(ctx, principal, RequestMetadata(ctx), &level)
	writeResult(w, r, a, http.StatusCreated, saved, appErr)
}

func (a *API) getProgrammeLevel(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeLevelForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitView, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	level, appErr := a.application.GetProgrammeLevel(ctx, principal, RequestMetadata(ctx), id)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, level, appErr)
}

func (a *API) patchProgrammeLevel(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeLevelForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	r = r.WithContext(ctx)
	var patch model.ProgrammeLevelPatch
	if !decodeJSON(w, r, &patch, "patchProgrammeLevel") {
		return
	}
	level, appErr := a.application.PatchProgrammeLevel(ctx, principal, RequestMetadata(ctx), id, &patch)
	writeResult(w, r, a, http.StatusOK, level, appErr)
}

func (a *API) archiveProgrammeLevel(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToProgrammeLevelForRequest(
		r.Context(), principal, id, model.ActionAcademicUnitManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	appErr = a.application.ArchiveProgrammeLevel(ctx, principal, RequestMetadata(ctx), id)
	writeNoContent(w, r.WithContext(ctx), a, appErr)
}
