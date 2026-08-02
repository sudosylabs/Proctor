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
