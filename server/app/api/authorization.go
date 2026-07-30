// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4 permission preflights. Proctor
// performs the visible fail-fast check at the HTTP boundary while attaching a
// sealed receipt that the application use case verifies and consumes.

package api

import (
	"context"
	"net/http"

	"github.com/sudosylabs/proctor/server/model"
)

type PermissionChecker interface {
	PrincipalHasPermissionToSystem(
		context.Context,
		model.Principal,
		model.Action,
		model.RequestMetadata,
	) (context.Context, bool, *model.AppError)
}

func (a *API) requirePermission(
	writer http.ResponseWriter,
	request *http.Request,
	allowed bool,
	appErr *model.AppError,
) bool {
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return false
	}
	if !allowed {
		WriteError(
			writer,
			request,
			model.NewAppError(
				"API.requirePermission",
				"authorization.denied",
				nil,
				"",
				http.StatusForbidden,
			),
		)
		return false
	}
	return true
}
