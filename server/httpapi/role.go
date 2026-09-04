// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/api4/role.go. Proctor retains
// per-domain Init registration and thin handlers while delegating scoped
// authorization, protected built-ins, validation, and audit to application
// use cases.

package httpapi

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type createRoleRequest struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Permissions []string `json:"permissions"`
}

type updateRoleRequest struct {
	DisplayName *string   `json:"display_name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Permissions *[]string `json:"permissions,omitempty"`
}

type roleResponse struct {
	ID          string   `json:"id"`
	CreateAt    int64    `json:"create_at"`
	UpdateAt    int64    `json:"update_at"`
	DeleteAt    int64    `json:"delete_at"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	BuiltIn     bool     `json:"built_in"`
}

func roleResponseFromModel(request *http.Request, role *model.Role) roleResponse {
	permissions := role.Permissions
	if permissions == nil {
		permissions = []string{}
	}
	displayName, description := role.DisplayName, role.Description
	if role.BuiltIn && role.Name == model.SystemAdministratorRoleName {
		displayName = translatedRequestText(request, systemAdministratorRoleNameID, displayName)
		description = translatedRequestText(request, systemAdministratorRoleDescriptionID, description)
	}
	return roleResponse{
		ID: role.ID.String(), CreateAt: model.MillisFromTime(role.CreatedAt),
		UpdateAt: model.MillisFromTime(role.UpdatedAt), DeleteAt: role.ArchivedAt.Millis(),
		Name: role.Name, DisplayName: displayName, Description: description,
		Permissions: append([]string(nil), permissions...), BuiltIn: role.BuiltIn,
	}
}

func roleResponsesFromModels(request *http.Request, roles []*model.Role) []roleResponse {
	result := make([]roleResponse, 0, len(roles))
	for _, role := range roles {
		result = append(result, roleResponseFromModel(request, role))
	}
	return result
}

type roleResourceModule struct {
	roles RoleApplication
}

func roleResource(roles RoleApplication) resource {
	module := roleResourceModule{roles: roles}
	return newResource(
		"roles",
		principalRoute(http.MethodGet, apiPath(literal("roles")),
			operatorReadErrorCodes("administration.unavailable"), module.list),
		principalRoute(http.MethodPost, apiPath(literal("roles")),
			operatorMutationErrorCodes("request.invalid", "role.invalid", "role.conflict", "role.permission.unknown", "administration.unavailable"), module.create),
		principalRoute(http.MethodGet, apiPath(literal("roles"), canonicalID("role_id")),
			operatorReadErrorCodes("request.invalid", "resource.not_found", "administration.unavailable"), module.get),
		principalRoute(http.MethodPatch, apiPath(literal("roles"), canonicalID("role_id")),
			operatorMutationErrorCodes("request.invalid", "resource.not_found", "role.invalid", "role.conflict", "role.built_in.protected", "role.permission.unknown", "administration.unavailable"), module.patch),
		principalRoute(http.MethodDelete, apiPath(literal("roles"), canonicalID("role_id")),
			operatorMutationErrorCodes("request.invalid", "resource.not_found", "role.built_in.protected", "role.conflict", "administration.unavailable"), module.archive),
	)
}

func operatorReadErrorCodes(specific ...string) []string {
	codes := []string{
		"authentication.required",
		"authentication.invalid_token",
		"authentication.credential_ambiguous",
		"authorization.denied",
		"authorization.request.invalid",
		"authorization.unavailable",
	}
	return append(codes, specific...)
}

func operatorMutationErrorCodes(specific ...string) []string {
	codes := operatorReadErrorCodes("authentication.csrf.invalid", "audit.unavailable")
	return append(codes, specific...)
}

func (module roleResourceModule) list(request operationRequest) (operationResult, error) {
	roles, err := module.roles.ListRoles(
		request.context,
		request.invocation(),
		application.ListRolesQuery{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, roleResponsesFromModels(request.request, roles)), nil
}

func (module roleResourceModule) get(request operationRequest) (operationResult, error) {
	roleID, err := request.params.RequireRoleId()
	if err != nil {
		return operationResult{}, err
	}
	role, err := module.roles.GetRole(
		request.context,
		request.invocation(),
		application.GetRoleQuery{ID: roleID},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, roleResponseFromModel(request.request, role)), nil
}

func (module roleResourceModule) create(request operationRequest) (operationResult, error) {
	var input createRoleRequest
	if err := request.decodeJSON(&input, "createRole"); err != nil {
		return operationResult{}, err
	}
	saved, err := module.roles.CreateRole(
		request.context,
		request.invocation(),
		application.CreateRoleCommand{
			Name: input.Name, DisplayName: input.DisplayName,
			Description: input.Description, Permissions: input.Permissions,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, roleResponseFromModel(request.request, saved)), nil
}

func (module roleResourceModule) patch(request operationRequest) (operationResult, error) {
	roleID, err := request.params.RequireRoleId()
	if err != nil {
		return operationResult{}, err
	}
	var input updateRoleRequest
	if err := request.decodeJSON(&input, "patchRole"); err != nil {
		return operationResult{}, err
	}
	updated, err := module.roles.UpdateRole(
		request.context,
		request.invocation(),
		application.UpdateRoleCommand{
			ID: roleID, DisplayName: input.DisplayName,
			Description: input.Description, Permissions: input.Permissions,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, roleResponseFromModel(request.request, updated)), nil
}

func (module roleResourceModule) archive(request operationRequest) (operationResult, error) {
	roleID, err := request.params.RequireRoleId()
	if err != nil {
		return operationResult{}, err
	}
	if err := module.roles.ArchiveRole(
		request.context,
		request.invocation(),
		application.ArchiveRoleCommand{ID: roleID},
	); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}
