// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/user.go access-token handlers.
// The raw credential is returned exactly once and every management route
// remains interactive-session-only.

package api

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/model"
)

type createPersonalAccessTokenRequest struct {
	Description    string   `json:"description"`
	Scopes         []string `json:"scopes"`
	AcademicUnitID string   `json:"academic_unit_id,omitempty"`
	ExpiresAt      int64    `json:"expires_at"`
}

func (a *API) InitPersonalAccessTokens() error {
	if err := a.Register(
		a.BaseRoutes.PersonalAccessTokens,
		"",
		http.MethodPost,
		a.APIRecentSessionRequired(http.HandlerFunc(a.createPersonalAccessToken)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.PersonalAccessTokens,
		"",
		http.MethodGet,
		a.APISessionRequired(http.HandlerFunc(a.listPersonalAccessTokens)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.PersonalAccessToken,
		"/disable",
		http.MethodPost,
		a.APISessionRequired(http.HandlerFunc(a.disablePersonalAccessToken)),
	); err != nil {
		return err
	}
	if err := a.Register(
		a.BaseRoutes.PersonalAccessToken,
		"/enable",
		http.MethodPost,
		a.APIRecentSessionRequired(http.HandlerFunc(a.enablePersonalAccessToken)),
	); err != nil {
		return err
	}
	return a.Register(
		a.BaseRoutes.PersonalAccessToken,
		"",
		http.MethodDelete,
		a.APISessionRequired(http.HandlerFunc(a.revokePersonalAccessToken)),
	)
}

func (a *API) disablePersonalAccessToken(
	writer http.ResponseWriter,
	request *http.Request,
) {
	a.setPersonalAccessTokenDisabled(writer, request, true)
}

func (a *API) enablePersonalAccessToken(
	writer http.ResponseWriter,
	request *http.Request,
) {
	a.setPersonalAccessTokenDisabled(writer, request, false)
}

func (a *API) setPersonalAccessTokenDisabled(
	writer http.ResponseWriter,
	request *http.Request,
	disabled bool,
) {
	principal, tokenID, ok := principalAndRequiredId(
		writer,
		request,
		func(params Params) (string, *model.AppError) {
			return params.RequirePersonalAccessTokenId()
		},
	)
	if !ok {
		return
	}
	updated, appErr := a.application.SetPersonalAccessTokenDisabled(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
		tokenID,
		disabled,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, updated)
}

func (a *API) createPersonalAccessToken(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	var input createPersonalAccessTokenRequest
	if err := decodeRequestJSON(request, &input); err != nil {
		WriteError(
			writer,
			request,
			invalidRequestError("createPersonalAccessToken", err),
		)
		return
	}
	created, appErr := a.application.CreatePersonalAccessToken(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
		input.Description,
		input.Scopes,
		input.AcademicUnitID,
		input.ExpiresAt,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusCreated, created)
}

func (a *API) listPersonalAccessTokens(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := Principal(request.Context())
	if !ok {
		WriteError(writer, request, authenticationRequiredError())
		return
	}
	tokens, appErr := a.application.ListPersonalAccessTokens(
		request.Context(),
		principal,
	)
	if appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, tokens)
}

func (a *API) revokePersonalAccessToken(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, tokenID, ok := principalAndRequiredId(
		writer,
		request,
		func(params Params) (string, *model.AppError) {
			return params.RequirePersonalAccessTokenId()
		},
	)
	if !ok {
		return
	}
	if _, appErr := a.application.RevokePersonalAccessToken(
		request.Context(),
		principal,
		RequestMetadata(request.Context()),
		tokenID,
	); appErr != nil {
		writeApplicationError(writer, request, a.logger, appErr)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}
