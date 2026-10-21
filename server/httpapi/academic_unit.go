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

type academicUnitResponse struct {
	Revision      int64  `json:"revision"`
	ID            string `json:"id"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	ArchivedAt    string `json:"archived_at,omitempty"`
	InstitutionID string `json:"institution_id"`
	ParentID      string `json:"parent_id,omitempty"`
	Name          string `json:"name"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
}

type createAcademicUnitRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type updateAcademicUnitRequest struct {
	ExpectedRevision Optional[int64] `json:"expected_revision"`
	ParentID         *string         `json:"parent_id"`
	Name             *string         `json:"name"`
	DisplayName      *string         `json:"display_name"`
	Description      *string         `json:"description"`
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
		idempotentPrincipalRoute(IdempotencyOptional, http.MethodPost, collection, academicMutationErrorCodes("request.invalid", "academic_unit.invalid", "academic_unit.conflict", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress"), module.createRoot),
		principalRoute(http.MethodGet, member, academicReadErrorCodes("resource.not_found"), module.get),
		principalRoute(http.MethodPatch, member, academicMutationErrorCodes("request.invalid", "resource.not_found", "academic_unit.invalid", "academic_unit.conflict"), module.patch),
		principalRoute(http.MethodDelete, member, academicMutationErrorCodes("request.invalid", "resource.not_found", "academic_unit.conflict"), module.archive),
		principalRoute(http.MethodGet, children, academicReadErrorCodes("resource.not_found"), module.listChildren),
		principalRoute(http.MethodPost, children, academicMutationErrorCodes("request.invalid", "resource.not_found", "academic_unit.invalid", "academic_unit.conflict"), module.createChild),
	)
}

func (m academicUnitResourceModule) listRoot(request operationRequest) (operationResult, error) {
	var units []*model.AcademicUnit
	var err error
	if query := request.request.URL.Query().Get("q"); query != "" {
		limit, limitErr := request.queryLimit()
		if limitErr != nil {
			return operationResult{}, limitErr
		}
		units, err = m.academicUnits.SearchAcademicUnits(
			request.context,
			request.invocation(),
			application.SearchAcademicUnitsQuery{Query: query, Limit: limit},
		)
	} else {
		units, err = m.academicUnits.ListAcademicUnits(
			request.context, request.invocation(), application.ListAcademicUnitsQuery{},
		)
	}
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitResponsesFromModels(units)), nil
}

func (m academicUnitResourceModule) createRoot(request operationRequest) (operationResult, error) {
	var body createAcademicUnitRequest
	if err := request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	saved, err := m.academicUnits.CreateAcademicUnit(
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

func (m academicUnitResourceModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitID()
	if err != nil {
		return operationResult{}, err
	}
	unit, err := m.academicUnits.GetAcademicUnit(
		request.context,
		request.invocation(),
		application.GetAcademicUnitQuery{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitResponseFromModel(unit)), nil
}

func (m academicUnitResourceModule) patch(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitID()
	if err != nil {
		return operationResult{}, err
	}
	var body updateAcademicUnitRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if err := validateAcademicRevision(body.ExpectedRevision); err != nil {
		return operationResult{}, err
	}
	unit, err := m.academicUnits.UpdateAcademicUnit(
		request.context,
		request.invocation(),
		application.UpdateAcademicUnitCommand{ExpectedRevision: body.ExpectedRevision.ValuePointer(),
			ID: id, ParentID: body.ParentID, Name: body.Name,
			DisplayName: body.DisplayName, Description: body.Description,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitResponseFromModel(unit)), nil
}

func (m academicUnitResourceModule) archive(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitID()
	if err != nil {
		return operationResult{}, err
	}
	expectedRevision, err := request.academicRevision()
	if err != nil {
		return operationResult{}, err
	}
	err = m.academicUnits.ArchiveAcademicUnit(
		request.context,
		request.invocation(),
		application.ArchiveAcademicUnitCommand{ExpectedRevision: expectedRevision, ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func (m academicUnitResourceModule) listChildren(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitID()
	if err != nil {
		return operationResult{}, err
	}
	units, err := m.academicUnits.ListAcademicUnits(
		request.context,
		request.invocation(),
		application.ListAcademicUnitsQuery{ParentID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, academicUnitResponsesFromModels(units)), nil
}

func (m academicUnitResourceModule) createChild(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireAcademicUnitID()
	if err != nil {
		return operationResult{}, err
	}
	var body createAcademicUnitRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	saved, err := m.academicUnits.CreateAcademicUnit(
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
		Revision: unit.Revision, ID: unit.ID.String(),
		CreatedAt:     model.FormatRFC3339(unit.CreatedAt),
		UpdatedAt:     model.FormatRFC3339(unit.UpdatedAt),
		ArchivedAt:    unit.ArchivedAt.FormatRFC3339(),
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
