// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"

	"github.com/sudosylabs/proctor/server/model"
)

func (a *API) InitInstitution() error {
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
	ctx, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		r.Context(), principal, model.ActionInstitutionManage, RequestMetadata(r.Context()),
	)
	if !a.requirePermission(w, r, allowed, appErr) {
		return
	}
	institution, appErr := a.application.GetInstitution(
		ctx, principal, RequestMetadata(ctx),
	)
	writeResult(w, r.WithContext(ctx), a, http.StatusOK, institution, appErr)
}

func (a *API) patchInstitution(w http.ResponseWriter, r *http.Request) {
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
	var patch model.InstitutionPatch
	if !decodeJSON(w, r, &patch, "patchInstitution") {
		return
	}
	institution, appErr := a.application.PatchInstitution(
		ctx, principal, RequestMetadata(ctx), &patch,
	)
	writeResult(w, r, a, http.StatusOK, institution, appErr)
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

type idRequirement func(Params) (string, *model.AppError)

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

func writeResult(w http.ResponseWriter, r *http.Request, a *API, status int, result any, appErr *model.AppError) {
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	writeJSON(w, status, result)
}

func writeNoContent(w http.ResponseWriter, r *http.Request, a *API, appErr *model.AppError) {
	if appErr != nil {
		writeApplicationError(w, r, a.logger, appErr)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
