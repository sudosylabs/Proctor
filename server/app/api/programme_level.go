// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type programmeLevelResponse struct {
	ID          string `json:"id"`
	CreateAt    int64  `json:"create_at"`
	UpdateAt    int64  `json:"update_at"`
	DeleteAt    int64  `json:"delete_at"`
	ProgrammeID string `json:"programme_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type createProgrammeLevelRequest struct {
	ID          string `json:"id"`
	CreateAt    int64  `json:"create_at"`
	UpdateAt    int64  `json:"update_at"`
	DeleteAt    int64  `json:"delete_at"`
	ProgrammeID string `json:"programme_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type updateProgrammeLevelRequest struct {
	Name        Optional[string] `json:"name"`
	DisplayName Optional[string] `json:"display_name"`
	Description Optional[string] `json:"description"`
}

func (a *API) registerProgrammeLevelRoutes() error {
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
		if err := a.Register(route.base, route.path, route.method, a.APIPrincipalRequired(route.handler)); err != nil {
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
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	levels, err := a.programmeLevels.ListProgrammeLevels(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.ListProgrammeLevelsQuery{ProgrammeID: programmeID, Query: r.URL.Query().Get("q"), Limit: limit})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	responses := make([]programmeLevelResponse, 0, len(levels))
	for _, level := range levels {
		responses = append(responses, programmeLevelResponseFromModel(level))
	}
	writeJSON(w, http.StatusOK, responses)
}

func (a *API) createProgrammeLevel(w http.ResponseWriter, r *http.Request) {
	principal, programmeID, ok := requiredResourceID(w, r, Params.RequireProgrammeId)
	if !ok {
		return
	}
	var body createProgrammeLevelRequest
	if !decodeJSON(w, r, &body, "createProgrammeLevel") {
		return
	}
	saved, err := a.programmeLevels.CreateProgrammeLevel(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.CreateProgrammeLevelCommand{ProgrammeID: programmeID, Name: body.Name, DisplayName: body.DisplayName, Description: body.Description})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, programmeLevelResponseFromModel(saved))
}

func (a *API) getProgrammeLevel(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	level, err := a.programmeLevels.GetProgrammeLevel(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.GetProgrammeLevelQuery{ID: id})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, programmeLevelResponseFromModel(level))
}

func (a *API) patchProgrammeLevel(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	var body updateProgrammeLevelRequest
	if !decodeJSON(w, r, &body, "patchProgrammeLevel") {
		return
	}
	level, err := a.programmeLevels.UpdateProgrammeLevel(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.UpdateProgrammeLevelCommand{ID: id, Name: body.Name.ValuePointer(), DisplayName: body.DisplayName.ValuePointer(), Description: body.Description.ValuePointer()})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, programmeLevelResponseFromModel(level))
}

func (a *API) archiveProgrammeLevel(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireProgrammeLevelId)
	if !ok {
		return
	}
	err := a.programmeLevels.ArchiveProgrammeLevel(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.ArchiveProgrammeLevelCommand{ID: id})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func programmeLevelResponseFromModel(level *model.ProgrammeLevel) programmeLevelResponse {
	if level == nil {
		return programmeLevelResponse{}
	}
	return programmeLevelResponse{
		ID:          level.ID.String(),
		CreateAt:    model.MillisFromTime(level.CreatedAt),
		UpdateAt:    model.MillisFromTime(level.UpdatedAt),
		DeleteAt:    level.ArchivedAt.Millis(),
		ProgrammeID: level.ProgrammeID.String(),
		Name:        level.Name,
		DisplayName: level.DisplayName,
		Description: level.Description,
	}
}
