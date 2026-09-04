// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type affiliationResponse struct {
	ID       string                `json:"id"`
	CreateAt int64                 `json:"create_at"`
	UpdateAt int64                 `json:"update_at"`
	DeleteAt int64                 `json:"delete_at"`
	UserID   string                `json:"user_id"`
	Kind     model.AffiliationKind `json:"kind"`
	StartAt  int64                 `json:"start_at"`
	EndAt    int64                 `json:"end_at,omitempty"`
}

type createAffiliationRequest struct {
	ID       string                `json:"id"`
	CreateAt int64                 `json:"create_at"`
	UpdateAt int64                 `json:"update_at"`
	DeleteAt int64                 `json:"delete_at"`
	UserID   string                `json:"user_id"`
	Kind     model.AffiliationKind `json:"kind"`
	StartAt  int64                 `json:"start_at"`
	EndAt    int64                 `json:"end_at"`
}

type affiliationResourceModule struct {
	affiliations AffiliationApplication
}

func affiliationResource(affiliations AffiliationApplication) resource {
	module := affiliationResourceModule{affiliations: affiliations}
	return newResource(
		"affiliations",
		principalRoute(
			http.MethodGet,
			apiPath(literal("users"), canonicalID("user_id"), literal("affiliations")),
			academicRelationshipReadErrorCodes(),
			module.list,
		),
		principalRoute(
			http.MethodPost,
			apiPath(literal("users"), canonicalID("user_id"), literal("affiliations")),
			academicRelationshipMutationErrorCodes("affiliation.invalid", "affiliation.conflict"),
			module.create,
		),
		principalRoute(
			http.MethodDelete,
			apiPath(literal("affiliations"), canonicalID("affiliation_id")),
			academicRelationshipMutationErrorCodes("affiliation.student_has_active_enrollment", "affiliation.conflict"),
			module.end,
		),
	)
}

func academicRelationshipReadErrorCodes() []string {
	return []string{
		"authentication.required",
		"authentication.invalid_token",
		"authentication.credential_ambiguous",
		"authorization.denied",
		"authorization.request.invalid",
		"authorization.unavailable",
		"request.invalid",
		"resource.not_found",
		"administration.unavailable",
	}
}

func academicRelationshipMutationErrorCodes(specific ...string) []string {
	codes := []string{
		"authentication.required",
		"authentication.invalid_token",
		"authentication.credential_ambiguous",
		"authentication.csrf.invalid",
		"authorization.denied",
		"authorization.request.invalid",
		"authorization.unavailable",
		"audit.unavailable",
		"request.invalid",
		"resource.not_found",
	}
	codes = append(codes, specific...)
	return append(codes, "administration.unavailable")
}

func (module affiliationResourceModule) list(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	values, err := module.affiliations.ListAffiliations(request.context, request.invocation(), application.ListAffiliationsQuery{UserID: userID})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, affiliationResponses(values)), nil
}

func (module affiliationResourceModule) create(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserId()
	if err != nil {
		return operationResult{}, err
	}
	var body createAffiliationRequest
	if err := request.decodeJSON(&body, "createAffiliation"); err != nil {
		return operationResult{}, err
	}
	saved, err := module.affiliations.CreateAffiliation(request.context, request.invocation(), application.CreateAffiliationCommand{UserID: userID, Kind: body.Kind, StartAt: body.StartAt, EndAt: body.EndAt})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, affiliationResponseFromModel(saved)), nil
}

func (module affiliationResourceModule) end(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAffiliationId()
	if err != nil {
		return operationResult{}, err
	}
	ended, err := module.affiliations.EndAffiliation(request.context, request.invocation(), application.EndAffiliationCommand{ID: id})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, affiliationResponseFromModel(ended)), nil
}

func affiliationResponseFromModel(value *model.Affiliation) affiliationResponse {
	if value == nil {
		return affiliationResponse{}
	}
	return affiliationResponse{
		ID:       value.ID.String(),
		CreateAt: model.MillisFromTime(value.CreatedAt),
		UpdateAt: model.MillisFromTime(value.UpdatedAt),
		DeleteAt: value.ArchivedAt.Millis(),
		UserID:   value.UserID.String(),
		Kind:     value.Kind,
		StartAt:  model.MillisFromTime(value.StartsAt),
		EndAt:    value.EndsAt.Millis(),
	}
}

func affiliationResponses(values []*model.Affiliation) []affiliationResponse {
	result := make([]affiliationResponse, 0, len(values))
	for _, value := range values {
		result = append(result, affiliationResponseFromModel(value))
	}
	return result
}
