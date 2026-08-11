// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type academicPeriodResponse struct {
	ID            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionID string `json:"institution_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	StartAt       int64  `json:"start_at"`
	EndAt         int64  `json:"end_at"`
}

type createAcademicPeriodRequest struct {
	ID            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionID string `json:"institution_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	StartAt       int64  `json:"start_at"`
	EndAt         int64  `json:"end_at"`
}

type updateAcademicPeriodRequest struct {
	Name        Optional[string] `json:"name"`
	DisplayName Optional[string] `json:"display_name"`
	Description Optional[string] `json:"description"`
	StartAt     Optional[int64]  `json:"start_at"`
	EndAt       Optional[int64]  `json:"end_at"`
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
		principalRoute(
			http.MethodPost, collection,
			academicMutationErrorCodes(
				"request.invalid", "resource.not_found", "academic_period.invalid",
				"academic_period.conflict",
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
	if err := request.decodeJSON(&body, "createAcademicPeriod"); err != nil {
		return operationResult{}, err
	}
	period, err := module.academicPeriods.CreateAcademicPeriod(
		request.context,
		request.invocation(),
		application.CreateAcademicPeriodCommand{
			Name: body.Name, DisplayName: body.DisplayName,
			Description: body.Description, StartAt: body.StartAt, EndAt: body.EndAt,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, academicPeriodResponseFromModel(period)), nil
}

func (module academicPeriodResourceModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicPeriodId()
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
	id, err := request.params.RequireAcademicPeriodId()
	if err != nil {
		return operationResult{}, err
	}
	var body updateAcademicPeriodRequest
	if err := request.decodeJSON(&body, "patchAcademicPeriod"); err != nil {
		return operationResult{}, err
	}
	period, err := module.academicPeriods.UpdateAcademicPeriod(
		request.context,
		request.invocation(),
		application.UpdateAcademicPeriodCommand{
			ID: id, Name: body.Name.ValuePointer(),
			DisplayName: body.DisplayName.ValuePointer(),
			Description: body.Description.ValuePointer(),
			StartAt:     body.StartAt.ValuePointer(), EndAt: body.EndAt.ValuePointer(),
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicPeriodResponseFromModel(period)), nil
}

func (module academicPeriodResourceModule) archive(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicPeriodId()
	if err != nil {
		return operationResult{}, err
	}
	if err := module.academicPeriods.ArchiveAcademicPeriod(
		request.context, request.invocation(), application.ArchiveAcademicPeriodCommand{ID: id},
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
		ID:            period.ID.String(),
		CreateAt:      model.MillisFromTime(period.CreatedAt),
		UpdateAt:      model.MillisFromTime(period.UpdatedAt),
		DeleteAt:      period.ArchivedAt.Millis(),
		InstitutionID: period.InstitutionID.String(),
		Name:          period.Name,
		DisplayName:   period.DisplayName,
		Description:   period.Description,
		StartAt:       model.MillisFromTime(period.StartsAt),
		EndAt:         model.MillisFromTime(period.EndsAt),
	}
}
