// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/role.go. Proctor retains
// per-domain Init registration and thin handlers while delegating scoped
// authorization, protected built-ins, validation, and audit to application
// use cases.

package api

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

func roleResponseFromModel(role *model.Role) roleResponse {
	permissions := role.Permissions
	if permissions == nil {
		permissions = []string{}
	}
	return roleResponse{
		ID: role.Id, CreateAt: role.CreateAt, UpdateAt: role.UpdateAt, DeleteAt: role.DeleteAt,
		Name: role.Name, DisplayName: role.DisplayName, Description: role.Description,
		Permissions: append([]string(nil), permissions...), BuiltIn: role.BuiltIn,
	}
}

func roleResponsesFromModels(roles []*model.Role) []roleResponse {
	result := make([]roleResponse, 0, len(roles))
	for _, role := range roles {
		result = append(result, roleResponseFromModel(role))
	}
	return result
}

func (a *API) InitRoles() error {
	if err := a.Register(
		a.BaseRoutes.Roles,
		"",
		http.MethodGet,
		a.APIPrincipalRequired(http.HandlerFunc(a.listRoles)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.Roles,
		"",
		http.MethodPost,
		a.APIPrincipalRequired(http.HandlerFunc(a.createRole)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.Role,
		"",
		http.MethodGet,
		a.APIPrincipalRequired(http.HandlerFunc(a.getRole)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.Role,
		"",
		http.MethodPatch,
		a.APIPrincipalRequired(http.HandlerFunc(a.patchRole)),
	); err != nil {
		return err
	}
	return a.Register(
		a.BaseRoutes.Role,
		"",
		http.MethodDelete,
		a.APIPrincipalRequired(http.HandlerFunc(a.deleteRole)),
	)
}

func (a *API) listRoles(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	roles, err := a.roles.ListRoles(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.ListRolesQuery{},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusOK, roleResponsesFromModels(roles))
}

func (a *API) getRole(writer http.ResponseWriter, request *http.Request) {
	principal, roleID, ok := principalAndRoleId(writer, request)
	if !ok {
		return
	}
	role, err := a.roles.GetRole(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.GetRoleQuery{ID: roleID},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusOK, roleResponseFromModel(role))
}

func (a *API) createRole(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	var input createRoleRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		writeApplicationError(writer, request, a.logger, application.NewError("request.invalid").WithField("field", "body"))
		return
	}
	saved, err := a.roles.CreateRole(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.CreateRoleCommand{
			Name: input.Name, DisplayName: input.DisplayName,
			Description: input.Description, Permissions: input.Permissions,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusCreated, roleResponseFromModel(saved))
}

func (a *API) patchRole(writer http.ResponseWriter, request *http.Request) {
	principal, roleID, ok := principalAndRoleId(writer, request)
	if !ok {
		return
	}
	var input updateRoleRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		writeApplicationError(writer, request, a.logger, application.NewError("request.invalid").WithField("field", "body"))
		return
	}
	updated, err := a.roles.UpdateRole(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.UpdateRoleCommand{
			ID: roleID, DisplayName: input.DisplayName,
			Description: input.Description, Permissions: input.Permissions,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writeJSON(writer, http.StatusOK, roleResponseFromModel(updated))
}

func (a *API) deleteRole(writer http.ResponseWriter, request *http.Request) {
	principal, roleID, ok := principalAndRoleId(writer, request)
	if !ok {
		return
	}
	if err := a.roles.DeleteRole(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.DeleteRoleCommand{ID: roleID},
	); err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func principalAndRoleId(
	writer http.ResponseWriter,
	request *http.Request,
) (model.Principal, string, bool) {
	return principalAndRequiredId(writer, request, func(params Params) (string, *model.AppError) {
		return params.RequireRoleId()
	})
}
