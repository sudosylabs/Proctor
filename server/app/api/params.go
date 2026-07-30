// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/web/params.go and context.go.
// Proctor keeps a deliberately small request-parameter contract and expands it
// only when a registered API resource needs another typed parameter.

package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sudosylabs/proctor/server/model"
)

type paramsContextKey struct{}

// Params contains normalized variables selected by the matched route. Handlers
// consume this object instead of reaching into mux or parsing URL paths.
type Params struct {
	RoleId        string
	RoleBindingId string
}

func ParamsFromRequest(request *http.Request) Params {
	variables := mux.Vars(request)
	return Params{
		RoleId:        strings.TrimSpace(variables["role_id"]),
		RoleBindingId: strings.TrimSpace(variables["role_binding_id"]),
	}
}

func RequestParams(ctx context.Context) (Params, bool) {
	params, ok := ctx.Value(paramsContextKey{}).(Params)
	return params, ok
}

func withRequestParams(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		params := ParamsFromRequest(request)
		ctx := context.WithValue(request.Context(), paramsContextKey{}, params)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (p Params) RequireRoleId() (string, *model.AppError) {
	return requirePathId("role_id", p.RoleId)
}

func (p Params) RequireRoleBindingId() (string, *model.AppError) {
	return requirePathId("role_binding_id", p.RoleBindingId)
}

func requirePathId(name, id string) (string, *model.AppError) {
	if !model.IsValidId(id) {
		return "", invalidRequestError(name, nil)
	}
	return id, nil
}

func principalAndRequiredId(
	writer http.ResponseWriter,
	request *http.Request,
	require func(Params) (string, *model.AppError),
) (model.Principal, string, bool) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return model.Principal{}, "", false
	}
	params, ok := RequestParams(request.Context())
	if !ok {
		WriteError(writer, request, invalidRequestError("route_params", nil))
		return model.Principal{}, "", false
	}
	id, appErr := require(params)
	if appErr != nil {
		WriteError(writer, request, appErr)
		return model.Principal{}, "", false
	}
	return principal, id, true
}
