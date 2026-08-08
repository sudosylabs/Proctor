// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type academicUnitResponse struct {
	ID            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionID string `json:"institution_id"`
	ParentID      string `json:"parent_id,omitempty"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
}

type createAcademicUnitRequest struct {
	// Server-owned fields remain accepted for v1 compatibility with the prior
	// domain-shaped request. Mapping deliberately ignores them.
	ID            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionID string `json:"institution_id"`
	ParentID      string `json:"parent_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
}

type updateAcademicUnitRequest struct {
	ParentID    *string `json:"parent_id"`
	Name        *string `json:"name"`
	DisplayName *string `json:"display_name"`
	Description *string `json:"description"`
}

func (a *API) registerAcademicUnitRoutes() error {
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

func (a *API) listRootAcademicUnits(writer http.ResponseWriter, request *http.Request) {
	principal, ok := requiredPrincipal(writer, request)
	if !ok {
		return
	}
	invocation := application.NewInvocation(principal, RequestMetadata(request.Context()))
	var units []*model.AcademicUnit
	var err error
	if query := request.URL.Query().Get("q"); query != "" {
		limit, valid := queryLimit(writer, request)
		if !valid {
			return
		}
		units, err = a.academicUnits.SearchAcademicUnits(
			request.Context(),
			invocation,
			application.SearchAcademicUnitsQuery{Query: query, Limit: limit},
		)
	} else {
		units, err = a.academicUnits.ListAcademicUnits(
			request.Context(), invocation, application.ListAcademicUnitsQuery{},
		)
	}
	writeAcademicUnitResult(writer, request, a, http.StatusOK, units, err)
}

func (a *API) createRootAcademicUnit(writer http.ResponseWriter, request *http.Request) {
	principal, ok := requiredPrincipal(writer, request)
	if !ok {
		return
	}
	var body createAcademicUnitRequest
	if !decodeJSON(writer, request, &body, "createRootAcademicUnit") {
		return
	}
	saved, err := a.academicUnits.CreateAcademicUnit(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.CreateAcademicUnitCommand{
			Name: body.Name, DisplayName: body.DisplayName,
			Description: body.Description,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusCreated, academicUnitResponseFromModel(saved))
}

func (a *API) getAcademicUnit(writer http.ResponseWriter, request *http.Request) {
	principal, id, ok := requiredResourceID(
		writer, request, Params.RequireAcademicUnitId,
	)
	if !ok {
		return
	}
	unit, err := a.academicUnits.GetAcademicUnit(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.GetAcademicUnitQuery{ID: id},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusOK, academicUnitResponseFromModel(unit))
}

func (a *API) patchAcademicUnit(writer http.ResponseWriter, request *http.Request) {
	principal, id, ok := requiredResourceID(
		writer, request, Params.RequireAcademicUnitId,
	)
	if !ok {
		return
	}
	var body updateAcademicUnitRequest
	if !decodeJSON(writer, request, &body, "patchAcademicUnit") {
		return
	}
	unit, err := a.academicUnits.UpdateAcademicUnit(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.UpdateAcademicUnitCommand{
			ID: id, ParentID: body.ParentID, Name: body.Name,
			DisplayName: body.DisplayName, Description: body.Description,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusOK, academicUnitResponseFromModel(unit))
}

func (a *API) archiveAcademicUnit(writer http.ResponseWriter, request *http.Request) {
	principal, id, ok := requiredResourceID(
		writer, request, Params.RequireAcademicUnitId,
	)
	if !ok {
		return
	}
	err := a.academicUnits.ArchiveAcademicUnit(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.ArchiveAcademicUnitCommand{ID: id},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) listAcademicUnitChildren(writer http.ResponseWriter, request *http.Request) {
	principal, id, ok := requiredResourceID(
		writer, request, Params.RequireAcademicUnitId,
	)
	if !ok {
		return
	}
	units, err := a.academicUnits.ListAcademicUnits(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.ListAcademicUnitsQuery{ParentID: id},
	)
	writeAcademicUnitResult(writer, request, a, http.StatusOK, units, err)
}

func (a *API) createAcademicUnitChild(writer http.ResponseWriter, request *http.Request) {
	principal, id, ok := requiredResourceID(
		writer, request, Params.RequireAcademicUnitId,
	)
	if !ok {
		return
	}
	var body createAcademicUnitRequest
	if !decodeJSON(writer, request, &body, "createAcademicUnitChild") {
		return
	}
	saved, err := a.academicUnits.CreateAcademicUnit(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.CreateAcademicUnitCommand{
			ParentID: id, Name: body.Name, DisplayName: body.DisplayName,
			Description: body.Description,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusCreated, academicUnitResponseFromModel(saved))
}

func writeAcademicUnitResult(
	writer http.ResponseWriter,
	request *http.Request,
	a *API,
	status int,
	units []*model.AcademicUnit,
	err error,
) {
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, status, academicUnitResponsesFromModels(units))
}

func academicUnitResponseFromModel(unit *model.AcademicUnit) academicUnitResponse {
	if unit == nil {
		return academicUnitResponse{}
	}
	return academicUnitResponse{
		ID:            unit.ID.String(),
		CreateAt:      model.MillisFromTime(unit.CreatedAt),
		UpdateAt:      model.MillisFromTime(unit.UpdatedAt),
		DeleteAt:      unit.ArchivedAt.Millis(),
		InstitutionID: unit.InstitutionID.String(),
		ParentID:      unit.ParentID.String(),
		Name:          unit.Name,
		DisplayName:   unit.DisplayName,
		Description:   unit.Description,
	}
}

func academicUnitResponsesFromModels(units []*model.AcademicUnit) []academicUnitResponse {
	responses := make([]academicUnitResponse, len(units))
	for index, unit := range units {
		responses[index] = academicUnitResponseFromModel(unit)
	}
	return responses
}
