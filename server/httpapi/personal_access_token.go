// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/api4/user.go access-token handlers.
// The raw credential is returned exactly once and every management route
// remains interactive-session-only.

package httpapi

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type createPersonalAccessTokenRequest struct {
	Description    string   `json:"description"`
	Scopes         []string `json:"scopes"`
	AcademicUnitID string   `json:"academic_unit_id,omitempty"`
	ExpiresAt      string   `json:"expires_at"`
}

type personalAccessTokenResponse struct {
	ID             string   `json:"id"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
	ArchivedAt     string   `json:"archived_at,omitempty"`
	UserID         string   `json:"user_id"`
	Description    string   `json:"description"`
	Scopes         []string `json:"scopes"`
	AcademicUnitID string   `json:"academic_unit_id,omitempty"`
	ExpiresAt      string   `json:"expires_at"`
	LastUsedAt     string   `json:"last_used_at,omitempty"`
	DisabledAt     string   `json:"disabled_at,omitempty"`
	RevokedAt      string   `json:"revoked_at,omitempty"`
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
		CreatedAt:      model.FormatRFC3339(token.CreatedAt),
		UpdatedAt:      model.FormatRFC3339(token.UpdatedAt),
		ArchivedAt:     token.ArchivedAt.FormatRFC3339(),
		UserID:         token.UserID.String(),
		Description:    token.Description,
		Scopes:         append([]string(nil), scopes...),
		AcademicUnitID: token.AcademicUnitID.String(),
		ExpiresAt:      model.FormatRFC3339(token.ExpiresAt),
		LastUsedAt:     token.LastUsedAt.FormatRFC3339(),
		DisabledAt:     token.DisabledAt.FormatRFC3339(),
		RevokedAt:      token.RevokedAt.FormatRFC3339(),
	}
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
	return append([]string(nil), extra...)
}

func personalAccessTokenSessionMutationCodes(extra ...string) []string {
	return personalAccessTokenSessionCodes(extra...)
}

func personalAccessTokenRecentMutationCodes(extra ...string) []string {
	return personalAccessTokenSessionMutationCodes(extra...)
}

func (m personalAccessTokenResourceModule) disable(request operationRequest) (operationResult, error) {
	return m.setDisabled(request, true)
}

func (m personalAccessTokenResourceModule) enable(request operationRequest) (operationResult, error) {
	return m.setDisabled(request, false)
}

func (m personalAccessTokenResourceModule) setDisabled(request operationRequest, disabled bool) (operationResult, error) {
	tokenID, err := request.params.RequirePersonalAccessTokenID()
	if err != nil {
		return operationResult{}, err
	}
	updated, err := m.tokens.SetPersonalAccessTokenDisabled(
		request.context,
		request.invocation(),
		app.SetPersonalAccessTokenDisabledCommand{TokenID: tokenID, Disabled: disabled},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, personalAccessTokenResponseFromModel(updated)).withHeaders(noStoreHeaders()), nil
}

func (m personalAccessTokenResourceModule) create(request operationRequest) (operationResult, error) {
	var input createPersonalAccessTokenRequest
	if err := request.decodeJSON(&input); err != nil {
		return operationResult{}, err
	}
	expiresAt, err := wireTimeMillis("expires_at", input.ExpiresAt)
	if err != nil {
		return operationResult{}, err
	}
	created, err := m.tokens.CreatePersonalAccessToken(
		request.context,
		request.invocation(),
		app.CreatePersonalAccessTokenCommand{
			Description: input.Description, Scopes: input.Scopes,
			AcademicUnitID: input.AcademicUnitID, ExpiresAt: expiresAt,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, personalAccessTokenCreationResponse{
		Token: personalAccessTokenResponseFromModel(created.Token), Credential: created.Credential,
	}).withHeaders(noStoreHeaders()), nil
}

func (m personalAccessTokenResourceModule) list(request operationRequest) (operationResult, error) {
	tokens, err := m.tokens.ListPersonalAccessTokens(
		request.context,
		request.invocation(),
		app.ListPersonalAccessTokensQuery{},
	)
	if err != nil {
		return operationResult{}, err
	}
	response, err := boundedListResult(request, "personal access token", tokens,
		func(token *model.PersonalAccessToken) boundedPageKey {
			return boundedPageKey{At: token.CreatedAt, ID: token.ID.String()}
		}, personalAccessTokenResponseFromModel)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func (m personalAccessTokenResourceModule) revoke(request operationRequest) (operationResult, error) {
	tokenID, err := request.params.RequirePersonalAccessTokenID()
	if err != nil {
		return operationResult{}, err
	}
	if _, err := m.tokens.RevokePersonalAccessToken(
		request.context,
		request.invocation(),
		app.RevokePersonalAccessTokenCommand{TokenID: tokenID},
	); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func noStoreHeaders() http.Header {
	return http.Header{"Cache-Control": []string{"no-store"}}
}
