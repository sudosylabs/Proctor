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

type academicPeriodResponse struct {
	Revision    int64  `json:"revision"`
	ID          string `json:"id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	ArchivedAt  string `json:"archived_at,omitempty"`
	OwnerType   string `json:"owner_type"`
	OwnerID     string `json:"owner_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	StartAt     string `json:"start_at"`
	EndAt       string `json:"end_at"`
}

type createAcademicPeriodRequest struct {
	OwnerType   string `json:"owner_type"`
	OwnerID     string `json:"owner_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	StartAt     string `json:"start_at"`
	EndAt       string `json:"end_at"`
}

type updateAcademicPeriodRequest struct {
	ExpectedRevision Optional[int64]  `json:"expected_revision"`
	Name             Optional[string] `json:"name"`
	DisplayName      Optional[string] `json:"display_name"`
	Description      Optional[string] `json:"description"`
	StartAt          Optional[string] `json:"start_at"`
	EndAt            Optional[string] `json:"end_at"`
}

type academicPeriodResourceModule struct {
	academicPeriods AcademicPeriodApplication
}

func academicPeriodResource(academicPeriods AcademicPeriodApplication) resource {
	module := academicPeriodResourceModule{academicPeriods: academicPeriods}
	collection := apiPath(literal("academic-periods"))
	member := apiPath(literal("academic-periods"), canonicalID("academic_period_id"))
	return newResource(
		"academic-periods",
		principalRoute(
			http.MethodGet, collection,
			academicReadErrorCodes("request.invalid", "resource.not_found"), module.list,
		),
		idempotentPrincipalRoute(
			IdempotencyOptional,
			http.MethodPost, collection,
			academicMutationErrorCodes(
				"request.invalid", "resource.not_found", "academic_period.invalid",
				"academic_period.conflict", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress",
			),
			module.create,
		),
		principalRoute(
			http.MethodGet, member,
			academicReadErrorCodes("request.invalid", "resource.not_found"), module.get,
		),
		principalRoute(
			http.MethodPatch, member,
			academicMutationErrorCodes(
				"request.invalid", "resource.not_found", "academic_period.invalid",
				"academic_period.conflict",
			),
			module.patch,
		),
		principalRoute(
			http.MethodDelete, member,
			academicMutationErrorCodes(
				"request.invalid", "resource.not_found", "academic_period.conflict",
			),
			module.archive,
		),
	)
}

func (module academicPeriodResourceModule) list(request operationRequest) (operationResult, error) {
	limit, err := request.queryLimit()
	if err != nil {
		return operationResult{}, err
	}
	periods, err := module.academicPeriods.ListAcademicPeriods(
		request.context,
		request.invocation(),
		application.ListAcademicPeriodsQuery{
			Query: request.request.URL.Query().Get("q"), Limit: limit,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	responses := make([]academicPeriodResponse, 0, len(periods))
	for _, period := range periods {
		responses = append(responses, academicPeriodResponseFromModel(period))
	}
	return jsonResult(http.StatusOK, responses), nil
}

func (module academicPeriodResourceModule) create(request operationRequest) (operationResult, error) {
	var body createAcademicPeriodRequest
	if err := request.decodeJSON(&body); err != nil {
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
	period, err := module.academicPeriods.CreateAcademicPeriod(
		request.context,
		request.invocation(),
		application.CreateAcademicPeriodCommand{
			OwnerType: body.OwnerType, OwnerID: body.OwnerID,
			Name: body.Name, DisplayName: body.DisplayName,
			Description: body.Description, StartAt: startAt, EndAt: endAt,
			IdempotencyKey: request.idempotencyKey,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, academicPeriodResponseFromModel(period)), nil
}

func (module academicPeriodResourceModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicPeriodID()
	if err != nil {
		return operationResult{}, err
	}
	period, err := module.academicPeriods.GetAcademicPeriod(
		request.context, request.invocation(), application.GetAcademicPeriodQuery{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicPeriodResponseFromModel(period)), nil
}

func (module academicPeriodResourceModule) patch(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicPeriodID()
	if err != nil {
		return operationResult{}, err
	}
	var body updateAcademicPeriodRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if err := validateAcademicRevision(body.ExpectedRevision); err != nil {
		return operationResult{}, err
	}
	startAt, err := optionalWireTimeMillis("start_at", body.StartAt)
	if err != nil {
		return operationResult{}, err
	}
	endAt, err := optionalWireTimeMillis("end_at", body.EndAt)
	if err != nil {
		return operationResult{}, err
	}
	period, err := module.academicPeriods.UpdateAcademicPeriod(
		request.context,
		request.invocation(),
		application.UpdateAcademicPeriodCommand{ExpectedRevision: body.ExpectedRevision.ValuePointer(),
			ID: id, Name: body.Name.ValuePointer(),
			DisplayName: body.DisplayName.ValuePointer(),
			Description: body.Description.ValuePointer(),
			StartAt:     startAt, EndAt: endAt,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicPeriodResponseFromModel(period)), nil
}

func (module academicPeriodResourceModule) archive(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicPeriodID()
	if err != nil {
		return operationResult{}, err
	}
	expectedRevision, err := request.academicRevision()
	if err != nil {
		return operationResult{}, err
	}
	if err := module.academicPeriods.ArchiveAcademicPeriod(
		request.context, request.invocation(), application.ArchiveAcademicPeriodCommand{ExpectedRevision: expectedRevision, ID: id},
	); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func academicPeriodResponseFromModel(period *model.AcademicPeriod) academicPeriodResponse {
	if period == nil {
		return academicPeriodResponse{}
	}
	return academicPeriodResponse{
		Revision: period.Revision, ID: period.ID.String(),
		CreatedAt:   model.FormatRFC3339(period.CreatedAt),
		UpdatedAt:   model.FormatRFC3339(period.UpdatedAt),
		ArchivedAt:  period.ArchivedAt.FormatRFC3339(),
		OwnerType:   string(period.Owner.Type()),
		OwnerID:     period.Owner.ID(),
		Name:        period.Name,
		DisplayName: period.DisplayName,
		Description: period.Description,
		StartAt:     model.FormatRFC3339(period.StartsAt),
		EndAt:       model.FormatRFC3339(period.EndsAt),
	}
}
