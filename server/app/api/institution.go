// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type institutionResponse struct {
	ID          string `json:"id"`
	CreateAt    int64  `json:"create_at"`
	UpdateAt    int64  `json:"update_at"`
	DeleteAt    int64  `json:"delete_at"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type updateInstitutionRequest struct {
	Name        Optional[string] `json:"name"`
	DisplayName Optional[string] `json:"display_name"`
	Description Optional[string] `json:"description"`
}

func (a *API) registerInstitutionRoutes() error {
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
	institution, err := a.institutions.GetInstitution(
		r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.GetInstitutionQuery{},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, institutionResponseFromModel(institution))
}

func (a *API) patchInstitution(w http.ResponseWriter, r *http.Request) {
	principal, ok := requiredPrincipal(w, r)
	if !ok {
		return
	}
	var body updateInstitutionRequest
	if !decodeJSON(w, r, &body, "patchInstitution") {
		return
	}
	institution, err := a.institutions.UpdateInstitution(
		r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())),
		application.UpdateInstitutionCommand{
			Name: body.Name.ValuePointer(), DisplayName: body.DisplayName.ValuePointer(),
			Description: body.Description.ValuePointer(),
		},
	)
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, institutionResponseFromModel(institution))
}

func institutionResponseFromModel(institution *model.Institution) institutionResponse {
	if institution == nil {
		return institutionResponse{}
	}
	return institutionResponse{
		ID: institution.Id, CreateAt: institution.CreateAt,
		UpdateAt: institution.UpdateAt, DeleteAt: institution.DeleteAt,
		Name: institution.Name, DisplayName: institution.DisplayName,
		Description: institution.Description,
	}
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

type idRequirement func(Params) (string, error)

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

func writeResult(w http.ResponseWriter, r *http.Request, a *API, status int, result any, appErr error) {
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	writeJSON(w, status, result)
}

func writeNoContent(w http.ResponseWriter, r *http.Request, a *API, appErr error) {
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
