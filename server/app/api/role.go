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
	registrations := []struct {
		route   Route
		handler http.Handler
	}{
		{
			Route{Method: http.MethodGet, Path: "/api/v1/roles", Auth: AuthPrivileged},
			http.HandlerFunc(a.listRoles),
		},
		{
			Route{Method: http.MethodPost, Path: "/api/v1/roles", Auth: AuthPrivileged},
			http.HandlerFunc(a.createRole),
		},
		{
			Route{
				Method: http.MethodGet,
				Path:   "/api/v1/roles/{role_id}",
				Auth:   AuthPrivileged,
			},
			http.HandlerFunc(a.getRole),
		},
		{
			Route{
				Method: http.MethodPatch,
				Path:   "/api/v1/roles/{role_id}",
				Auth:   AuthPrivileged,
			},
			http.HandlerFunc(a.patchRole),
		},
		{
			Route{
				Method: http.MethodDelete,
				Path:   "/api/v1/roles/{role_id}",
				Auth:   AuthPrivileged,
			},
			http.HandlerFunc(a.deleteRole),
		},
	}
	for _, registration := range registrations {
		if err := a.Register(registration.route, registration.handler); err != nil {
			return err
		}
	}
	return nil
}

func (a *API) listRoles(writer http.ResponseWriter, request *http.Request) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
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
	principal, roleID, ok := principalAndPathID(writer, request, "role_id")
	if !ok {
		return
	}
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
	principal, roleID, ok := principalAndPathID(writer, request, "role_id")
	if !ok {
		return
	}
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
	principal, roleID, ok := principalAndPathID(writer, request, "role_id")
	if !ok {
		return
	}
	if appErr := a.application.DeleteRole(
		request.Context(), principal, RequestMetadata(request.Context()), roleID,
	); appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func principalAndPathID(
	writer http.ResponseWriter,
	request *http.Request,
	name string,
) (model.Principal, string, bool) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return model.Principal{}, "", false
	}
	id := request.PathValue(name)
	if !model.IsValidId(id) {
		WriteError(writer, request, invalidRequestError(name, nil))
		return model.Principal{}, "", false
	}
	return principal, id, true
}
