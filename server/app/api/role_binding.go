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

type roleBindingResourceModule struct {
	bindings RoleBindingApplication
}

func roleBindingResource(bindings RoleBindingApplication) resource {
	module := roleBindingResourceModule{bindings: bindings}
	return newResource(
		"role-bindings",
		principalRoute(http.MethodGet, apiPath(literal("role-bindings")),
			operatorReadErrorCodes("request.invalid", "administration.unavailable"), module.list),
		strongRecentSessionRoute(http.MethodPost, apiPath(literal("role-bindings")),
			roleBindingMutationErrorCodes("request.invalid", "resource.not_found", "role_binding.invalid", "role_binding.conflict", "role_binding.system_admin_requires_institution_scope", "administration.unavailable"), module.create),
		strongRecentSessionRoute(http.MethodDelete, apiPath(literal("role-bindings"), canonicalID("role_binding_id")),
			roleBindingMutationErrorCodes("request.invalid", "resource.not_found", "role_binding.conflict", "role_binding.last_system_admin", "administration.unavailable"), module.end),
	)
}

func roleBindingMutationErrorCodes(specific ...string) []string {
	codes := operatorMutationErrorCodes(
		"authentication.strong_required",
		"authentication.reauthentication_required",
	)
	return append(codes, specific...)
}

func (module roleBindingResourceModule) list(request operationRequest) (operationResult, error) {
	values := request.request.URL.Query()
	bindings, err := module.bindings.ListRoleBindings(
		request.context,
		request.invocation(),
		application.ListRoleBindingsQuery{
			UserID: values.Get("user_id"), ScopeType: model.RoleScopeType(values.Get("scope_type")),
			ScopeID: values.Get("scope_id"),
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, roleBindingResponsesFromModels(bindings)), nil
}

func (module roleBindingResourceModule) create(request operationRequest) (operationResult, error) {
	var input createRoleBindingRequest
	if err := request.decodeJSON(&input, "createRoleBinding"); err != nil {
		return operationResult{}, err
	}
	saved, err := module.bindings.CreateRoleBinding(
		request.context,
		request.invocation(),
		application.CreateRoleBindingCommand{
			UserID: input.UserID, RoleID: input.RoleId, ScopeType: input.ScopeType,
			ScopeID: input.ScopeId, StartAt: input.StartAt, EndAt: input.EndAt,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, roleBindingResponseFromModel(saved)), nil
}

func (module roleBindingResourceModule) end(request operationRequest) (operationResult, error) {
	bindingID, err := request.params.RequireRoleBindingId()
	if err != nil {
		return operationResult{}, err
	}
	ended, err := module.bindings.EndRoleBinding(
		request.context,
		request.invocation(),
		application.EndRoleBindingCommand{ID: bindingID},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, roleBindingResponseFromModel(ended)), nil
}
