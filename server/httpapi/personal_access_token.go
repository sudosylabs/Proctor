// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/api4/user.go access-token handlers.
// The raw credential is returned exactly once and every management route
// remains interactive-session-only.

package httpapi

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
		ID:             token.ID.String(),
		CreateAt:       model.MillisFromTime(token.CreatedAt),
		UpdateAt:       model.MillisFromTime(token.UpdatedAt),
		DeleteAt:       token.ArchivedAt.Millis(),
		UserID:         token.UserID.String(),
		Description:    token.Description,
		Scopes:         append([]string(nil), scopes...),
		AcademicUnitID: token.AcademicUnitID.String(),
		ExpiresAt:      model.MillisFromTime(token.ExpiresAt),
		LastUsedAt:     token.LastUsedAt.Millis(),
		DisabledAt:     token.DisabledAt.Millis(),
		RevokedAt:      token.RevokedAt.Millis(),
	}
}

func personalAccessTokenResponsesFromModels(tokens []*model.PersonalAccessToken) []personalAccessTokenResponse {
	result := make([]personalAccessTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		result = append(result, personalAccessTokenResponseFromModel(token))
	}
	return result
}

type personalAccessTokenResourceModule struct {
	tokens PersonalAccessTokens
}

func personalAccessTokenResource(tokens PersonalAccessTokens) resource {
	module := personalAccessTokenResourceModule{tokens: tokens}
	collection := apiPath(literal("users"), literal("me"), literal("tokens"))
	item := apiPath(literal("users"), literal("me"), literal("tokens"), canonicalID("personal_access_token_id"))
	return newResource(
		"personal-access-tokens",
		recentSessionRoute(http.MethodPost, collection, personalAccessTokenRecentMutationCodes("request.invalid", "resource.not_found", "personal_access_token.invalid", "personal_access_token.maximum_reached", "personal_access_token.unavailable", "audit.unavailable"), module.create),
		sessionRoute(http.MethodGet, collection, personalAccessTokenSessionCodes("personal_access_token.unavailable"), module.list),
		sessionRoute(http.MethodPost, appendRoutePath(item, literal("disable")), personalAccessTokenSessionMutationCodes("request.invalid", "resource.not_found", "personal_access_token.unavailable", "audit.unavailable"), module.disable),
		recentSessionRoute(http.MethodPost, appendRoutePath(item, literal("enable")), personalAccessTokenRecentMutationCodes("request.invalid", "resource.not_found", "personal_access_token.maximum_reached", "personal_access_token.unavailable", "audit.unavailable"), module.enable),
		sessionRoute(http.MethodDelete, item, personalAccessTokenSessionMutationCodes("request.invalid", "resource.not_found", "personal_access_token.unavailable", "audit.unavailable"), module.revoke),
	)
}

func appendRoutePath(path routePath, parts ...pathPart) routePath {
	combined := append([]pathPart(nil), path.parts...)
	return apiPath(append(combined, parts...)...)
}

func personalAccessTokenSessionCodes(extra ...string) []string {
	return append([]string{"authentication.required", "authentication.invalid_token", "authentication.credential_ambiguous"}, extra...)
}

func personalAccessTokenSessionMutationCodes(extra ...string) []string {
	return personalAccessTokenSessionCodes(append([]string{"authentication.csrf.invalid"}, extra...)...)
}

func personalAccessTokenRecentMutationCodes(extra ...string) []string {
	return personalAccessTokenSessionMutationCodes(append([]string{"authentication.reauthentication_required"}, extra...)...)
}

func (module personalAccessTokenResourceModule) disable(request operationRequest) (operationResult, error) {
	return module.setDisabled(request, true)
}

func (module personalAccessTokenResourceModule) enable(request operationRequest) (operationResult, error) {
	return module.setDisabled(request, false)
}

func (module personalAccessTokenResourceModule) setDisabled(request operationRequest, disabled bool) (operationResult, error) {
	tokenID, err := request.params.RequirePersonalAccessTokenId()
	if err != nil {
		return operationResult{}, err
	}
	updated, err := module.tokens.SetPersonalAccessTokenDisabled(
		request.context,
		request.invocation(),
		application.SetPersonalAccessTokenDisabledCommand{TokenID: tokenID, Disabled: disabled},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, personalAccessTokenResponseFromModel(updated)).withHeaders(noStoreHeaders()), nil
}

func (module personalAccessTokenResourceModule) create(request operationRequest) (operationResult, error) {
	var input createPersonalAccessTokenRequest
	if err := request.decodeJSON(&input, "createPersonalAccessToken"); err != nil {
		return operationResult{}, err
	}
	created, err := module.tokens.CreatePersonalAccessToken(
		request.context,
		request.invocation(),
		application.CreatePersonalAccessTokenCommand{
			Description: input.Description, Scopes: input.Scopes,
			AcademicUnitID: input.AcademicUnitID, ExpiresAt: input.ExpiresAt,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, personalAccessTokenCreationResponse{
		Token: personalAccessTokenResponseFromModel(created.Token), Credential: created.Credential,
	}).withHeaders(noStoreHeaders()), nil
}

func (module personalAccessTokenResourceModule) list(request operationRequest) (operationResult, error) {
	tokens, err := module.tokens.ListPersonalAccessTokens(
		request.context,
		request.invocation(),
		application.ListPersonalAccessTokensQuery{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, personalAccessTokenResponsesFromModels(tokens)).withHeaders(noStoreHeaders()), nil
}

func (module personalAccessTokenResourceModule) revoke(request operationRequest) (operationResult, error) {
	tokenID, err := request.params.RequirePersonalAccessTokenId()
	if err != nil {
		return operationResult{}, err
	}
	if _, err := module.tokens.RevokePersonalAccessToken(
		request.context,
		request.invocation(),
		application.RevokePersonalAccessTokenCommand{TokenID: tokenID},
	); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func noStoreHeaders() http.Header {
	return http.Header{"Cache-Control": []string{"no-store"}}
}
