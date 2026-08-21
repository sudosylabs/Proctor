// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type academicUnitResponse struct {
	ID            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionID string `json:"institution_id"`
	ParentID      string `json:"parent_id,omitempty"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
}

type createAcademicUnitRequest struct {
	// Server-owned fields remain accepted for v1 compatibility with the prior
	// domain-shaped request. Mapping deliberately ignores them.
	ID            string `json:"id"`
	CreateAt      int64  `json:"create_at"`
	UpdateAt      int64  `json:"update_at"`
	DeleteAt      int64  `json:"delete_at"`
	InstitutionID string `json:"institution_id"`
	ParentID      string `json:"parent_id"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
}

type updateAcademicUnitRequest struct {
	ParentID    *string `json:"parent_id"`
	Name        *string `json:"name"`
	DisplayName *string `json:"display_name"`
	Description *string `json:"description"`
}

type academicUnitResourceModule struct {
	academicUnits AcademicUnitApplication
}

func academicUnitResource(academicUnits AcademicUnitApplication) resource {
	module := academicUnitResourceModule{academicUnits: academicUnits}
	collection := apiPath(literal("academic-units"))
	member := apiPath(literal("academic-units"), canonicalID("academic_unit_id"))
	children := apiPath(literal("academic-units"), canonicalID("academic_unit_id"), literal("children"))
	return newResource(
		"academic-units",
		principalRoute(http.MethodGet, collection, academicReadErrorCodes("request.invalid"), module.listRoot),
		idempotentPrincipalRoute(IdempotencyOptional, http.MethodPost, collection, academicMutationErrorCodes("request.invalid", "academic_unit.invalid", "academic_unit.conflict", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress"), module.createRoot),
		principalRoute(http.MethodGet, member, academicReadErrorCodes("resource.not_found"), module.get),
		principalRoute(http.MethodPatch, member, academicMutationErrorCodes("request.invalid", "resource.not_found", "academic_unit.invalid", "academic_unit.conflict"), module.patch),
		principalRoute(http.MethodDelete, member, academicMutationErrorCodes("request.invalid", "resource.not_found", "academic_unit.conflict"), module.archive),
		principalRoute(http.MethodGet, children, academicReadErrorCodes("resource.not_found"), module.listChildren),
		principalRoute(http.MethodPost, children, academicMutationErrorCodes("request.invalid", "resource.not_found", "academic_unit.invalid", "academic_unit.conflict"), module.createChild),
	)
}

func (module academicUnitResourceModule) listRoot(request operationRequest) (operationResult, error) {
	var units []*model.AcademicUnit
	var err error
	if query := request.request.URL.Query().Get("q"); query != "" {
		limit, limitErr := request.queryLimit()
		if limitErr != nil {
			return operationResult{}, limitErr
		}
		units, err = module.academicUnits.SearchAcademicUnits(
			request.context,
			request.invocation(),
			application.SearchAcademicUnitsQuery{Query: query, Limit: limit},
		)
	} else {
		units, err = module.academicUnits.ListAcademicUnits(
			request.context, request.invocation(), application.ListAcademicUnitsQuery{},
		)
	}
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitResponsesFromModels(units)), nil
}

func (module academicUnitResourceModule) createRoot(request operationRequest) (operationResult, error) {
	var body createAcademicUnitRequest
	if err := request.decodeJSON(&body, "createRootAcademicUnit"); err != nil {
		return operationResult{}, err
	}
	saved, err := module.academicUnits.CreateAcademicUnit(
		request.context,
		request.invocation(),
		application.CreateAcademicUnitCommand{
			Name: body.Name, DisplayName: body.DisplayName,
			Description: body.Description, IdempotencyKey: request.idempotencyKey,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, academicUnitResponseFromModel(saved)), nil
}

func (module academicUnitResourceModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	unit, err := module.academicUnits.GetAcademicUnit(
		request.context,
		request.invocation(),
		application.GetAcademicUnitQuery{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitResponseFromModel(unit)), nil
}

func (module academicUnitResourceModule) patch(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	var body updateAcademicUnitRequest
	if err := request.decodeJSON(&body, "patchAcademicUnit"); err != nil {
		return operationResult{}, err
	}
	unit, err := module.academicUnits.UpdateAcademicUnit(
		request.context,
		request.invocation(),
		application.UpdateAcademicUnitCommand{
			ID: id, ParentID: body.ParentID, Name: body.Name,
			DisplayName: body.DisplayName, Description: body.Description,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitResponseFromModel(unit)), nil
}

func (module academicUnitResourceModule) archive(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	err = module.academicUnits.ArchiveAcademicUnit(
		request.context,
		request.invocation(),
		application.ArchiveAcademicUnitCommand{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (module academicUnitResourceModule) listChildren(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	units, err := module.academicUnits.ListAcademicUnits(
		request.context,
		request.invocation(),
		application.ListAcademicUnitsQuery{ParentID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitResponsesFromModels(units)), nil
}

func (module academicUnitResourceModule) createChild(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	var body createAcademicUnitRequest
	if err := request.decodeJSON(&body, "createAcademicUnitChild"); err != nil {
		return operationResult{}, err
	}
	saved, err := module.academicUnits.CreateAcademicUnit(
		request.context,
		request.invocation(),
		application.CreateAcademicUnitCommand{
			ParentID: id, Name: body.Name, DisplayName: body.DisplayName,
			Description: body.Description,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, academicUnitResponseFromModel(saved)), nil
}

func academicUnitResponseFromModel(unit *model.AcademicUnit) academicUnitResponse {
	if unit == nil {
		return academicUnitResponse{}
	}
	return academicUnitResponse{
		ID:            unit.ID.String(),
		CreateAt:      model.MillisFromTime(unit.CreatedAt),
		UpdateAt:      model.MillisFromTime(unit.UpdatedAt),
		DeleteAt:      unit.ArchivedAt.Millis(),
		InstitutionID: unit.InstitutionID.String(),
		ParentID:      unit.ParentID.String(),
		Name:          unit.Name,
		DisplayName:   unit.DisplayName,
		Description:   unit.Description,
	}
}

func academicUnitResponsesFromModels(units []*model.AcademicUnit) []academicUnitResponse {
	responses := make([]academicUnitResponse, len(units))
	for index, unit := range units {
		responses[index] = academicUnitResponseFromModel(unit)
	}
	return responses
}
