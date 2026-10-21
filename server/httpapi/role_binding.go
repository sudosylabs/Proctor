// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type createRoleBindingRequest struct {
	UserID    string              `json:"user_id"`
	RoleID    string              `json:"role_id"`
	ScopeType model.RoleScopeType `json:"scope_type"`
	ScopeID   string              `json:"scope_id"`
	StartAt   string              `json:"start_at"`
	EndAt     string              `json:"end_at,omitempty"`
}

type roleBindingResponse struct {
	ID         string              `json:"id"`
	CreatedAt  string              `json:"created_at"`
	UpdatedAt  string              `json:"updated_at"`
	ArchivedAt string              `json:"archived_at,omitempty"`
	UserID     string              `json:"user_id"`
	RoleID     string              `json:"role_id"`
	ScopeType  model.RoleScopeType `json:"scope_type"`
	ScopeID    string              `json:"scope_id"`
	StartAt    string              `json:"start_at"`
	EndAt      string              `json:"end_at,omitempty"`
}

func roleBindingResponseFromModel(binding *model.RoleBinding) roleBindingResponse {
	return roleBindingResponse{
		ID: binding.ID.String(), CreatedAt: model.FormatRFC3339(binding.CreatedAt),
		UpdatedAt: model.FormatRFC3339(binding.UpdatedAt), ArchivedAt: binding.ArchivedAt.FormatRFC3339(),
		UserID: binding.UserID.String(), RoleID: binding.RoleID.String(),
		ScopeType: binding.ScopeType, ScopeID: binding.ScopeID,
		StartAt: model.FormatRFC3339(binding.StartsAt), EndAt: binding.EndsAt.FormatRFC3339(),
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
			roleBindingMutationErrorCodes("request.invalid", "resource.not_found", "role_binding.invalid", "role_binding.conflict", "role_binding.system_admin_scope_invalid", "administration.unavailable"), module.create),
		strongRecentSessionRoute(http.MethodDelete, apiPath(literal("role-bindings"), canonicalID("role_binding_id")),
			roleBindingMutationErrorCodes("request.invalid", "resource.not_found", "role_binding.conflict", "role_binding.last_system_admin", "administration.unavailable"), module.end),
	)
}

func roleBindingMutationErrorCodes(specific ...string) []string {
	codes := operatorMutationErrorCodes()
	return append(codes, specific...)
}

func (m roleBindingResourceModule) list(request operationRequest) (operationResult, error) {
	values := request.request.URL.Query()
	bindings, err := m.bindings.ListRoleBindings(
		request.context,
		request.invocation(),
		app.ListRoleBindingsQuery{
			UserID: values.Get("user_id"), ScopeType: model.RoleScopeType(values.Get("scope_type")),
			ScopeID: values.Get("scope_id"),
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, roleBindingResponsesFromModels(bindings)), nil
}

func (m roleBindingResourceModule) create(request operationRequest) (operationResult, error) {
	var input createRoleBindingRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	startAt, endAt, err := wireWindowMillis(input.StartAt, input.EndAt)
	if err != nil {
		return operationResult{}, err
	}
	saved, err := m.bindings.CreateRoleBinding(
		request.context,
		request.invocation(),
		app.CreateRoleBindingCommand{
			UserID: input.UserID, RoleID: input.RoleID, ScopeType: input.ScopeType,
			ScopeID: input.ScopeID, StartAt: startAt, EndAt: endAt,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, roleBindingResponseFromModel(saved)), nil
}

func (m roleBindingResourceModule) end(request operationRequest) (operationResult, error) {
	bindingID, err := request.params.RequireRoleBindingID()
	if err != nil {
		return operationResult{}, err
	}
	ended, err := m.bindings.EndRoleBinding(
		request.context,
		request.invocation(),
		app.EndRoleBindingCommand{ID: bindingID},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, roleBindingResponseFromModel(ended)), nil
}
