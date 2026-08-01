// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type programmeResponse struct {
	ID             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	AcademicUnitID string `json:"academic_unit_id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
}

type createProgrammeRequest struct {
	ID             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	AcademicUnitID string `json:"academic_unit_id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
}

type updateProgrammeRequest struct {
	Name        Optional[string] `json:"name"`
	DisplayName Optional[string] `json:"display_name"`
	Description Optional[string] `json:"description"`
}

func (a *API) registerProgrammeRoutes() error {
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
			a.APIPrincipalRequired(route.handler)); err != nil {
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
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	programmes, err := a.programmes.ListProgrammes(
		r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.ListProgrammesQuery{AcademicUnitID: unitID, Query: r.URL.Query().Get("q"), Limit: limit},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	responses := make([]programmeResponse, 0, len(programmes))
	for _, programme := range programmes {
		responses = append(responses, programmeResponseFromModel(programme))
	}
	writeJSON(w, http.StatusOK, responses)
}

func (a *API) createProgramme(w http.ResponseWriter, r *http.Request) {
	principal, unitID, ok := requiredResourceID(w, r, Params.RequireAcademicUnitId)
	if !ok {
		return
	}
	var body createProgrammeRequest
	if !decodeJSON(w, r, &body, "createProgramme") {
		return
	}
	saved, err := a.programmes.CreateProgramme(
		r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.CreateProgrammeCommand{AcademicUnitID: unitID, Name: body.Name, DisplayName: body.DisplayName, Description: body.Description},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, programmeResponseFromModel(saved))
}

func (a *API) getProgramme(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeId)
	if !ok {
		return
	}
	programme, err := a.programmes.GetProgramme(
		r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.GetProgrammeQuery{ID: id},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, programmeResponseFromModel(programme))
}

func (a *API) patchProgramme(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeId)
	if !ok {
		return
	}
	var body updateProgrammeRequest
	if !decodeJSON(w, r, &body, "patchProgramme") {
		return
	}
	programme, err := a.programmes.UpdateProgramme(
		r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.UpdateProgrammeCommand{ID: id, Name: body.Name.ValuePointer(), DisplayName: body.DisplayName.ValuePointer(), Description: body.Description.ValuePointer()},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, programmeResponseFromModel(programme))
}

func (a *API) archiveProgramme(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeId)
	if !ok {
		return
	}
	err := a.programmes.ArchiveProgramme(
		r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.ArchiveProgrammeCommand{ID: id},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func programmeResponseFromModel(programme *model.Programme) programmeResponse {
	if programme == nil {
		return programmeResponse{}
	}
	return programmeResponse{
		ID: programme.Id, CreateAt: programme.CreateAt, UpdateAt: programme.UpdateAt,
		DeleteAt: programme.DeleteAt, AcademicUnitID: programme.AcademicUnitId,
		Name: programme.Name, DisplayName: programme.DisplayName, Description: programme.Description,
	}
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
			a.APIPrincipalRequired(route.handler)); err != nil {
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
