// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type createRoleBindingRequest struct {
	UserID    string              `json:"user_id"`
	RoleId    string              `json:"role_id"`
	ScopeType model.RoleScopeType `json:"scope_type"`
	ScopeId   string              `json:"scope_id"`
	StartAt   int64               `json:"start_at"`
	EndAt     int64               `json:"end_at,omitempty"`
}

type roleBindingResponse struct {
	ID        string              `json:"id"`
	CreateAt  int64               `json:"create_at"`
	UpdateAt  int64               `json:"update_at"`
	DeleteAt  int64               `json:"delete_at"`
	UserID    string              `json:"user_id"`
	RoleID    string              `json:"role_id"`
	ScopeType model.RoleScopeType `json:"scope_type"`
	ScopeID   string              `json:"scope_id"`
	StartAt   int64               `json:"start_at"`
	EndAt     int64               `json:"end_at,omitempty"`
}

func roleBindingResponseFromModel(binding *model.RoleBinding) roleBindingResponse {
	return roleBindingResponse{
		ID: binding.ID.String(), CreateAt: model.MillisFromTime(binding.CreatedAt),
		UpdateAt: model.MillisFromTime(binding.UpdatedAt), DeleteAt: binding.ArchivedAt.Millis(),
		UserID: binding.UserID.String(), RoleID: binding.RoleID.String(),
		ScopeType: binding.ScopeType, ScopeID: binding.ScopeID,
		StartAt: model.MillisFromTime(binding.StartsAt), EndAt: binding.EndsAt.Millis(),
	}
}

func roleBindingResponsesFromModels(bindings []*model.RoleBinding) []roleBindingResponse {
	result := make([]roleBindingResponse, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, roleBindingResponseFromModel(binding))
	}
	return result
}

func (a *API) InitRoleBindings() error {
	if err := a.Register(
		a.BaseRoutes.RoleBindings,
		"",
		http.MethodGet,
		a.APIPrincipalRequired(http.HandlerFunc(a.listRoleBindings)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.RoleBindings,
		"",
		http.MethodPost,
		a.APIPrincipalRequired(http.HandlerFunc(a.createRoleBinding)),
	); err != nil {
		return err
	}
	return a.Register(
		a.BaseRoutes.RoleBinding,
		"",
		http.MethodDelete,
		a.APIPrincipalRequired(http.HandlerFunc(a.endRoleBinding)),
	)
}

func (a *API) listRoleBindings(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	values := request.URL.Query()
	bindings, err := a.roleBindings.ListRoleBindings(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.ListRoleBindingsQuery{
			UserID: values.Get("user_id"), ScopeType: model.RoleScopeType(values.Get("scope_type")),
			ScopeID: values.Get("scope_id"),
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusOK, roleBindingResponsesFromModels(bindings))
}

func (a *API) createRoleBinding(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	var input createRoleBindingRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		writeApplicationError(writer, request, a.logger, application.NewError("request.invalid").WithField("field", "body"))
		return
	}
	saved, err := a.roleBindings.CreateRoleBinding(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.CreateRoleBindingCommand{
			UserID: input.UserID, RoleID: input.RoleId, ScopeType: input.ScopeType,
			ScopeID: input.ScopeId, StartAt: input.StartAt, EndAt: input.EndAt,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusCreated, roleBindingResponseFromModel(saved))
}

func (a *API) endRoleBinding(writer http.ResponseWriter, request *http.Request) {
	principal, bindingID, ok := principalAndRoleBindingId(writer, request)
	if !ok {
		return
	}
	ended, err := a.roleBindings.EndRoleBinding(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.EndRoleBindingCommand{ID: bindingID},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusOK, roleBindingResponseFromModel(ended))
}

func principalAndRoleBindingId(
	writer http.ResponseWriter,
	request *http.Request,
) (model.Principal, string, bool) {
	return principalAndRequiredId(writer, request, func(params Params) (string, error) {
		return params.RequireRoleBindingId()
	})
}
