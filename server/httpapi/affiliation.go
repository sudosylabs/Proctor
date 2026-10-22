// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type affiliationResponse struct {
	ID         string                `json:"id"`
	CreatedAt  string                `json:"created_at"`
	UpdatedAt  string                `json:"updated_at"`
	ArchivedAt string                `json:"archived_at,omitempty"`
	UserID     string                `json:"user_id"`
	Kind       model.AffiliationKind `json:"kind"`
	StartAt    string                `json:"start_at"`
	EndAt      string                `json:"end_at,omitempty"`
}

type createAffiliationRequest struct {
	Kind    model.AffiliationKind `json:"kind"`
	StartAt string                `json:"start_at"`
	EndAt   string                `json:"end_at"`
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

func (m affiliationResourceModule) list(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserID()
	if err != nil {
		return operationResult{}, err
	}
	values, err := m.affiliations.ListAffiliations(request.context, request.invocation(), app.ListAffiliationsQuery{UserID: userID})
	if err != nil {
		return operationResult{}, err
	}
	response, err := boundedListResult(request, "Affiliation", values,
		func(value *model.Affiliation) boundedPageKey {
			return boundedPageKey{At: value.CreatedAt, ID: value.ID.String()}
		},
		affiliationResponseFromModel)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m affiliationResourceModule) create(request operationRequest) (operationResult, error) {
	userID, err := request.params.RequireUserID()
	if err != nil {
		return operationResult{}, err
	}
	var body createAffiliationRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	startAt, err := wireTimeMillis("start_at", body.StartAt)
	if err != nil {
		return operationResult{}, err
	}
	endAt, err := wireTimeMillis("end_at", body.EndAt)
	if err != nil {
		return operationResult{}, err
	}
	saved, err := m.affiliations.CreateAffiliation(request.context, request.invocation(), app.CreateAffiliationCommand{UserID: userID, Kind: body.Kind, StartAt: startAt, EndAt: endAt})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, affiliationResponseFromModel(saved)), nil
}

func (m affiliationResourceModule) end(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAffiliationID()
	if err != nil {
		return operationResult{}, err
	}
	ended, err := m.affiliations.EndAffiliation(request.context, request.invocation(), app.EndAffiliationCommand{ID: id})
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
		ID:         value.ID.String(),
		CreatedAt:  model.FormatRFC3339(value.CreatedAt),
		UpdatedAt:  model.FormatRFC3339(value.UpdatedAt),
		ArchivedAt: value.ArchivedAt.FormatRFC3339(),
		UserID:     value.UserID.String(),
		Kind:       value.Kind,
		StartAt:    model.FormatRFC3339(value.StartsAt),
		EndAt:      value.EndsAt.FormatRFC3339(),
	}
}
