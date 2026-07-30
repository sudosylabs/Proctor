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

type AuthorizationPreflight interface {
	PreauthorizePrincipalToSystem(
		context.Context,
		model.Principal,
		model.Action,
		model.RequestMetadata,
	) (context.Context, *model.AppError)
}

func (a *API) preauthorizeSystemAction(
	writer http.ResponseWriter,
	request *http.Request,
	principal model.Principal,
	action model.Action,
) (*http.Request, bool) {
	authorizedContext, appErr := a.application.PreauthorizePrincipalToSystem(
		request.Context(),
		principal,
		action,
		RequestMetadata(request.Context()),
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return request, false
	}
	return request.WithContext(authorizedContext), true
}
