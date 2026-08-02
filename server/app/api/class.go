// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type classResponse struct {
	ID               string `json:"id"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
	ProgrammeLevelID string `json:"programme_level_id"`
	AcademicPeriodID string `json:"academic_period_id"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
}
type createClassRequest struct {
	ID               string `json:"id"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
	ProgrammeLevelID string `json:"programme_level_id"`
	AcademicPeriodID string `json:"academic_period_id"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
}
type updateClassRequest struct {
	ProgrammeLevelID Optional[string] `json:"programme_level_id"`
	AcademicPeriodID Optional[string] `json:"academic_period_id"`
	Name             Optional[string] `json:"name"`
	DisplayName      Optional[string] `json:"display_name"`
	Description      Optional[string] `json:"description"`
}

func (a *API) registerClassRoutes() error {
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
		if err := a.Register(route.base, route.path, route.method, a.APIPrincipalRequired(route.handler)); err != nil {
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
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	classes, err := a.classes.SearchClasses(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.SearchClassesQuery{AcademicUnitID: unitID, Query: r.URL.Query().Get("q"), Limit: limit})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, classResponses(classes))
}
func (a *API) listClasses(w http.ResponseWriter, r *http.Request) {
	principal, levelID, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	classes, err := a.classes.ListClasses(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.ListClassesQuery{ProgrammeLevelID: levelID})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, classResponses(classes))
}
func (a *API) createClass(w http.ResponseWriter, r *http.Request) {
	principal, levelID, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	var body createClassRequest
	if !decodeJSON(w, r, &body, "createClass") {
		return
	}
	class, err := a.classes.CreateClass(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.CreateClassCommand{ProgrammeLevelID: levelID, AcademicPeriodID: body.AcademicPeriodID, Name: body.Name, DisplayName: body.DisplayName, Description: body.Description})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, classResponseFromModel(class))
}
func (a *API) getClass(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	class, err := a.classes.GetClass(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.GetClassQuery{ID: id})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, classResponseFromModel(class))
}
func (a *API) patchClass(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	var body updateClassRequest
	if !decodeJSON(w, r, &body, "patchClass") {
		return
	}
	class, err := a.classes.UpdateClass(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.UpdateClassCommand{ID: id, ProgrammeLevelID: body.ProgrammeLevelID.ValuePointer(), AcademicPeriodID: body.AcademicPeriodID.ValuePointer(), Name: body.Name.ValuePointer(), DisplayName: body.DisplayName.ValuePointer(), Description: body.Description.ValuePointer()})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, classResponseFromModel(class))
}
func (a *API) archiveClass(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireClassId)
	if !ok {
		return
	}
	if err := a.classes.ArchiveClass(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.ArchiveClassCommand{ID: id}); err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func classResponseFromModel(class *model.Class) classResponse {
	if class == nil {
		return classResponse{}
	}
	return classResponse{ID: class.Id, CreateAt: class.CreateAt, UpdateAt: class.UpdateAt, DeleteAt: class.DeleteAt, ProgrammeLevelID: class.ProgrammeLevelId, AcademicPeriodID: class.AcademicPeriodId, Name: class.Name, DisplayName: class.DisplayName, Description: class.Description}
}
func classResponses(classes []*model.Class) []classResponse {
	result := make([]classResponse, 0, len(classes))
	for _, class := range classes {
		result = append(result, classResponseFromModel(class))
	}
	return result
}
