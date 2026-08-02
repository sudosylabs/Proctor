// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type academicPeriodResponse struct {
	ID            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionID string `json:"institution_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	StartAt       int64  `json:"start_at"`
	EndAt         int64  `json:"end_at"`
}

type createAcademicPeriodRequest struct {
	ID            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionID string `json:"institution_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	StartAt       int64  `json:"start_at"`
	EndAt         int64  `json:"end_at"`
}

type updateAcademicPeriodRequest struct {
	Name        Optional[string] `json:"name"`
	DisplayName Optional[string] `json:"display_name"`
	Description Optional[string] `json:"description"`
	StartAt     Optional[int64]  `json:"start_at"`
	EndAt       Optional[int64]  `json:"end_at"`
}

func (a *API) registerAcademicPeriodRoutes() error {
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
		if err := a.Register(route.base, route.path, route.method, a.APIPrincipalRequired(route.handler)); err != nil {
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
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	periods, err := a.academicPeriods.ListAcademicPeriods(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.ListAcademicPeriodsQuery{Query: r.URL.Query().Get("q"), Limit: limit})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	responses := make([]academicPeriodResponse, 0, len(periods))
	for _, period := range periods {
		responses = append(responses, academicPeriodResponseFromModel(period))
	}
	writeJSON(w, http.StatusOK, responses)
}

func (a *API) createAcademicPeriod(w http.ResponseWriter, r *http.Request) {
	principal, ok := requiredPrincipal(w, r)
	if !ok {
		return
	}
	var body createAcademicPeriodRequest
	if !decodeJSON(w, r, &body, "createAcademicPeriod") {
		return
	}
	period, err := a.academicPeriods.CreateAcademicPeriod(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.CreateAcademicPeriodCommand{Name: body.Name, DisplayName: body.DisplayName, Description: body.Description, StartAt: body.StartAt, EndAt: body.EndAt})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, academicPeriodResponseFromModel(period))
}

func (a *API) getAcademicPeriod(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicPeriodId)
	if !ok {
		return
	}
	period, err := a.academicPeriods.GetAcademicPeriod(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.GetAcademicPeriodQuery{ID: id})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, academicPeriodResponseFromModel(period))
}

func (a *API) patchAcademicPeriod(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicPeriodId)
	if !ok {
		return
	}
	var body updateAcademicPeriodRequest
	if !decodeJSON(w, r, &body, "patchAcademicPeriod") {
		return
	}
	period, err := a.academicPeriods.UpdateAcademicPeriod(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.UpdateAcademicPeriodCommand{ID: id, Name: body.Name.ValuePointer(), DisplayName: body.DisplayName.ValuePointer(), Description: body.Description.ValuePointer(), StartAt: body.StartAt.ValuePointer(), EndAt: body.EndAt.ValuePointer()})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, academicPeriodResponseFromModel(period))
}

func (a *API) archiveAcademicPeriod(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAcademicPeriodId)
	if !ok {
		return
	}
	if err := a.academicPeriods.ArchiveAcademicPeriod(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.ArchiveAcademicPeriodCommand{ID: id}); err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func academicPeriodResponseFromModel(period *model.AcademicPeriod) academicPeriodResponse {
	if period == nil {
		return academicPeriodResponse{}
	}
	return academicPeriodResponse{ID: period.Id, CreateAt: period.CreateAt, UpdateAt: period.UpdateAt, DeleteAt: period.DeleteAt, InstitutionID: period.InstitutionId, Name: period.Name, DisplayName: period.DisplayName, Description: period.Description, StartAt: period.StartAt, EndAt: period.EndAt}
}
