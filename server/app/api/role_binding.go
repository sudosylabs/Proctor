// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"errors"
	"net/http"

	"github.com/sudosylabs/proctor/server/model"
)

type createRoleBindingRequest struct {
	UserId    string              `json:"user_id"`
	RoleId    string              `json:"role_id"`
	ScopeType model.RoleScopeType `json:"scope_type"`
	ScopeId   string              `json:"scope_id"`
	StartAt   int64               `json:"start_at"`
	EndAt     int64               `json:"end_at,omitempty"`
}

func (a *API) InitRoleBindings() error {
	if err := a.Register(
		a.BaseRoutes.RoleBindings,
		"",
		http.MethodGet,
		AuthPrivileged,
		http.HandlerFunc(a.listRoleBindings),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.RoleBindings,
		"",
		http.MethodPost,
		AuthPrivileged,
		http.HandlerFunc(a.createRoleBinding),
	); err != nil {
		return err
	}
	return a.Register(
		a.BaseRoutes.RoleBinding,
		"",
		http.MethodDelete,
		AuthPrivileged,
		http.HandlerFunc(a.endRoleBinding),
	)
}

func (a *API) listRoleBindings(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	values := request.URL.Query()
	userID := values.Get("user_id")
	scopeType := model.RoleScopeType(values.Get("scope_type"))
	scopeID := values.Get("scope_id")
	var (
		bindings []*model.RoleBinding
		appErr   *model.AppError
	)
	switch {
	case userID != "" && scopeType == "" && scopeID == "" && model.IsValidId(userID):
		bindings, appErr = a.application.ListRoleBindingsForUser(
			request.Context(), principal, RequestMetadata(request.Context()), userID,
		)
	case userID == "" && scopeType.IsValid() && model.IsValidId(scopeID):
		bindings, appErr = a.application.ListRoleBindingsForScope(
			request.Context(), principal, RequestMetadata(request.Context()), scopeType, scopeID,
		)
	default:
		WriteError(
			writer,
			request,
			invalidRequestError(
				"listRoleBindings",
				errors.New("provide either user_id or scope_type and scope_id"),
			),
		)
		return
	}
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusOK, bindings)
}

func (a *API) createRoleBinding(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	var input createRoleBindingRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		WriteError(writer, request, invalidRequestError("createRoleBinding", err))
		return
	}
	saved, appErr := a.application.CreateRoleBinding(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
		&model.RoleBinding{
			UserId: input.UserId, RoleId: input.RoleId,
			ScopeType: input.ScopeType, ScopeId: input.ScopeId,
			StartAt: input.StartAt, EndAt: input.EndAt,
		},
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusCreated, saved)
}

func (a *API) endRoleBinding(writer http.ResponseWriter, request *http.Request) {
	principal, bindingID, ok := principalAndRoleBindingId(writer, request)
	if !ok {
		return
	}
	ended, appErr := a.application.EndRoleBinding(
		request.Context(), principal, RequestMetadata(request.Context()), bindingID,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusOK, ended)
}

func principalAndRoleBindingId(
	writer http.ResponseWriter,
	request *http.Request,
) (model.Principal, string, bool) {
	return principalAndRequiredId(writer, request, func(params Params) (string, *model.AppError) {
		return params.RequireRoleBindingId()
	})
}
