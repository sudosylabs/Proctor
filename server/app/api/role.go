// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/role.go. Proctor retains
// per-domain Init registration and thin handlers while delegating scoped
// authorization, protected built-ins, validation, and audit to app.App.

package api

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/model"
)

type createRoleRequest struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Permissions []string `json:"permissions"`
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
	authorizedContext, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		request.Context(),
		principal,
		model.ActionRoleManage,
		RequestMetadata(request.Context()),
	)
	if !a.requirePermission(writer, request, allowed, appErr) {
		return
	}
	request = request.WithContext(authorizedContext)
	roles, appErr := a.application.ListRoles(
		request.Context(), principal, RequestMetadata(request.Context()),
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusOK, roles)
}

func (a *API) getRole(writer http.ResponseWriter, request *http.Request) {
	principal, roleID, ok := principalAndRoleId(writer, request)
	if !ok {
		return
	}
	authorizedContext, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		request.Context(),
		principal,
		model.ActionRoleManage,
		RequestMetadata(request.Context()),
	)
	if !a.requirePermission(writer, request, allowed, appErr) {
		return
	}
	request = request.WithContext(authorizedContext)
	role, appErr := a.application.GetRole(
		request.Context(), principal, RequestMetadata(request.Context()), roleID,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusOK, role)
}

func (a *API) createRole(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	authorizedContext, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		request.Context(),
		principal,
		model.ActionRoleManage,
		RequestMetadata(request.Context()),
	)
	if !a.requirePermission(writer, request, allowed, appErr) {
		return
	}
	request = request.WithContext(authorizedContext)
	var input createRoleRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		WriteError(writer, request, invalidRequestError("createRole", err))
		return
	}
	saved, appErr := a.application.CreateRole(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
		&model.Role{
			Name: input.Name, DisplayName: input.DisplayName,
			Description: input.Description, Permissions: input.Permissions,
		},
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusCreated, saved)
}

func (a *API) patchRole(writer http.ResponseWriter, request *http.Request) {
	principal, roleID, ok := principalAndRoleId(writer, request)
	if !ok {
		return
	}
	authorizedContext, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		request.Context(),
		principal,
		model.ActionRoleManage,
		RequestMetadata(request.Context()),
	)
	if !a.requirePermission(writer, request, allowed, appErr) {
		return
	}
	request = request.WithContext(authorizedContext)
	var patch model.RolePatch
	if err := decodeRequestJSON(request, &patch); err != nil {
		WriteError(writer, request, invalidRequestError("patchRole", err))
		return
	}
	updated, appErr := a.application.PatchRole(
		request.Context(), principal, RequestMetadata(request.Context()), roleID, &patch,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writeJSON(writer, http.StatusOK, updated)
}

func (a *API) deleteRole(writer http.ResponseWriter, request *http.Request) {
	principal, roleID, ok := principalAndRoleId(writer, request)
	if !ok {
		return
	}
	authorizedContext, allowed, appErr := a.application.PrincipalHasPermissionToSystem(
		request.Context(),
		principal,
		model.ActionRoleManage,
		RequestMetadata(request.Context()),
	)
	if !a.requirePermission(writer, request, allowed, appErr) {
		return
	}
	request = request.WithContext(authorizedContext)
	if appErr := a.application.DeleteRole(
		request.Context(), principal, RequestMetadata(request.Context()), roleID,
	); appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
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
