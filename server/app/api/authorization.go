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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type PermissionChecker interface {
	PrincipalHasPermissionToSystem(
		context.Context,
		model.Principal,
		model.Action,
		model.RequestMetadata,
	) (context.Context, bool, error)
	PrincipalHasPermissionToAcademicUnitForRequest(
		context.Context,
		model.Principal,
		string,
		model.Action,
		model.RequestMetadata,
	) (context.Context, bool, error)
	PrincipalHasPermissionToClassForRequest(
		context.Context,
		model.Principal,
		string,
		model.Action,
		model.RequestMetadata,
	) (context.Context, bool, error)
	PrincipalHasPermissionToProgrammeForRequest(
		context.Context,
		model.Principal,
		string,
		model.Action,
		model.RequestMetadata,
	) (context.Context, bool, error)
	PrincipalHasPermissionToProgrammeLevelForRequest(
		context.Context,
		model.Principal,
		string,
		model.Action,
		model.RequestMetadata,
	) (context.Context, bool, error)
	PrincipalHasPermissionToClassAdministrationForRequest(
		context.Context,
		model.Principal,
		string,
		model.RequestMetadata,
	) (context.Context, bool, error)
	PrincipalHasPermissionToUserForRequest(
		context.Context,
		model.Principal,
		string,
		model.Action,
		model.RequestMetadata,
	) (context.Context, bool, error)
	PrincipalHasPermissionToAffiliationForRequest(
		context.Context,
		model.Principal,
		string,
		model.RequestMetadata,
	) (context.Context, bool, error)
	PrincipalHasPermissionToAcademicUnitMemberForRequest(
		context.Context,
		model.Principal,
		string,
		model.RequestMetadata,
	) (context.Context, bool, error)
}

func (a *API) requirePermission(
	writer http.ResponseWriter,
	request *http.Request,
	allowed bool,
	appErr error,
) bool {
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return false
	}
	if !allowed {
		WriteError(
			writer,
			request,
			application.NewError("authorization.denied"),
		)
		return false
	}
	return true
}
