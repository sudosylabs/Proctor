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

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type createPersonalAccessTokenRequest struct {
	Description    string   `json:"description"`
	Scopes         []string `json:"scopes"`
	AcademicUnitID string   `json:"academic_unit_id,omitempty"`
	ExpiresAt      int64    `json:"expires_at"`
}

type personalAccessTokenResponse struct {
	ID             string   `json:"id"`
	CreateAt       int64    `json:"create_at"`
	UpdateAt       int64    `json:"update_at"`
	DeleteAt       int64    `json:"delete_at"`
	UserID         string   `json:"user_id"`
	Description    string   `json:"description"`
	Scopes         []string `json:"scopes"`
	AcademicUnitID string   `json:"academic_unit_id,omitempty"`
	ExpiresAt      int64    `json:"expires_at"`
	LastUsedAt     int64    `json:"last_used_at,omitempty"`
	DisabledAt     int64    `json:"disabled_at,omitempty"`
	RevokedAt      int64    `json:"revoked_at,omitempty"`
}

type personalAccessTokenCreationResponse struct {
	Token      personalAccessTokenResponse `json:"token"`
	Credential string                      `json:"credential"`
}

func personalAccessTokenResponseFromModel(token *model.PersonalAccessToken) personalAccessTokenResponse {
	if token == nil {
		return personalAccessTokenResponse{}
	}
	scopes := token.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return personalAccessTokenResponse{
		ID: token.Id, CreateAt: token.CreateAt, UpdateAt: token.UpdateAt, DeleteAt: token.DeleteAt,
		UserID: token.UserId, Description: token.Description,
		Scopes: append([]string(nil), scopes...), AcademicUnitID: token.AcademicUnitId,
		ExpiresAt: token.ExpiresAt, LastUsedAt: token.LastUsedAt,
		DisabledAt: token.DisabledAt, RevokedAt: token.RevokedAt,
	}
}

func personalAccessTokenResponsesFromModels(tokens []*model.PersonalAccessToken) []personalAccessTokenResponse {
	result := make([]personalAccessTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		result = append(result, personalAccessTokenResponseFromModel(token))
	}
	return result
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
	updated, err := a.application.SetPersonalAccessTokenDisabled(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.SetPersonalAccessTokenDisabledCommand{TokenID: tokenID, Disabled: disabled},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, personalAccessTokenResponseFromModel(updated))
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
	created, err := a.application.CreatePersonalAccessToken(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.CreatePersonalAccessTokenCommand{
			Description: input.Description, Scopes: input.Scopes,
			AcademicUnitID: input.AcademicUnitID, ExpiresAt: input.ExpiresAt,
		},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusCreated, personalAccessTokenCreationResponse{
		Token: personalAccessTokenResponseFromModel(created.Token), Credential: created.Credential,
	})
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
	tokens, err := a.application.ListPersonalAccessTokens(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.ListPersonalAccessTokensQuery{},
	)
	if err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, personalAccessTokenResponsesFromModels(tokens))
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
	if _, err := a.application.RevokePersonalAccessToken(
		request.Context(),
		application.NewInvocation(principal, RequestMetadata(request.Context())),
		application.RevokePersonalAccessTokenCommand{TokenID: tokenID},
	); err != nil {
		writeApplicationError(writer, request, a.logger, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}
