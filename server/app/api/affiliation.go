// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	"github.com/gorilla/mux"
	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type affiliationResponse struct {
	ID       string                `json:"id"`
	CreateAt int64                 `json:"create_at"`
	UpdateAt int64                 `json:"update_at"`
	DeleteAt int64                 `json:"delete_at"`
	UserID   string                `json:"user_id"`
	Kind     model.AffiliationKind `json:"kind"`
	StartAt  int64                 `json:"start_at"`
	EndAt    int64                 `json:"end_at,omitempty"`
}

type createAffiliationRequest struct {
	ID       string                `json:"id"`
	CreateAt int64                 `json:"create_at"`
	UpdateAt int64                 `json:"update_at"`
	DeleteAt int64                 `json:"delete_at"`
	UserID   string                `json:"user_id"`
	Kind     model.AffiliationKind `json:"kind"`
	StartAt  int64                 `json:"start_at"`
	EndAt    int64                 `json:"end_at"`
}

func (a *API) registerAffiliationRoutes() error {
	routes := []struct {
		base         *mux.Router
		path, method string
		handler      http.HandlerFunc
	}{
		{a.BaseRoutes.User, "/affiliations", http.MethodGet, a.listAffiliations},
		{a.BaseRoutes.User, "/affiliations", http.MethodPost, a.createAffiliation},
		{a.BaseRoutes.Affiliation, "", http.MethodDelete, a.endAffiliation},
	}
	for _, route := range routes {
		if err := a.Register(route.base, route.path, route.method, a.APIPrincipalRequired(route.handler)); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listAffiliations(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	values, err := a.affiliations.ListAffiliations(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.ListAffiliationsQuery{UserID: userID})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, affiliationResponses(values))
}

func (a *API) createAffiliation(w http.ResponseWriter, r *http.Request) {
	principal, userID, ok := requiredResourceID(w, r, Params.RequireUserId)
	if !ok {
		return
	}
	var body createAffiliationRequest
	if !decodeJSON(w, r, &body, "createAffiliation") {
		return
	}
	saved, err := a.affiliations.CreateAffiliation(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.CreateAffiliationCommand{UserID: userID, Kind: body.Kind, StartAt: body.StartAt, EndAt: body.EndAt})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	writeJSON(w, http.StatusCreated, affiliationResponseFromModel(saved))
}

func (a *API) endAffiliation(w http.ResponseWriter, r *http.Request) {
	principal, id, ok := requiredResourceID(w, r, Params.RequireAffiliationId)
	if !ok {
		return
	}
	ended, err := a.affiliations.EndAffiliation(r.Context(), application.NewInvocation(principal, RequestMetadata(r.Context())), application.EndAffiliationCommand{ID: id})
	if err != nil {
		writeApplicationError(w, r, a.logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, affiliationResponseFromModel(ended))
}

func affiliationResponseFromModel(value *model.Affiliation) affiliationResponse {
	if value == nil {
		return affiliationResponse{}
	}
	return affiliationResponse{ID: value.Id, CreateAt: value.CreateAt, UpdateAt: value.UpdateAt, DeleteAt: value.DeleteAt, UserID: value.UserId, Kind: value.Kind, StartAt: value.StartAt, EndAt: value.EndAt}
}

func affiliationResponses(values []*model.Affiliation) []affiliationResponse {
	result := make([]affiliationResponse, 0, len(values))
	for _, value := range values {
		result = append(result, affiliationResponseFromModel(value))
	}
	return result
}
