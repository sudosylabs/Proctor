// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type programmeResponse struct {
	ID             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	AcademicUnitID string `json:"academic_unit_id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
}

type createProgrammeRequest struct {
	ID             string `json:"id"`
	CreateAt       int64  `json:"create_at"`
	UpdateAt       int64  `json:"update_at"`
	DeleteAt       int64  `json:"delete_at"`
	AcademicUnitID string `json:"academic_unit_id"`
	Name           string `json:"name"`
	DisplayName    string `json:"display_name"`
	Description    string `json:"description"`
}

type updateProgrammeRequest struct {
	Name        Optional[string] `json:"name"`
	DisplayName Optional[string] `json:"display_name"`
	Description Optional[string] `json:"description"`
}

type programmeResourceModule struct {
	programmes ProgrammeApplication
}

func programmeResource(programmes ProgrammeApplication) resource {
	module := programmeResourceModule{programmes: programmes}
	collection := apiPath(literal("academic-units"), canonicalID("academic_unit_id"), literal("programmes"))
	member := apiPath(literal("programmes"), canonicalID("programme_id"))
	return newResource(
		"programmes",
		principalRoute(http.MethodGet, collection, academicReadErrorCodes("request.invalid", "resource.not_found"), module.list),
		principalRoute(http.MethodPost, collection, academicMutationErrorCodes("request.invalid", "resource.not_found", "programme.invalid", "programme.conflict"), module.create),
		principalRoute(http.MethodGet, member, academicReadErrorCodes("request.invalid", "resource.not_found"), module.get),
		principalRoute(http.MethodPatch, member, academicMutationErrorCodes("request.invalid", "resource.not_found", "programme.invalid", "programme.conflict"), module.patch),
		principalRoute(http.MethodDelete, member, academicMutationErrorCodes("request.invalid", "resource.not_found", "programme.conflict"), module.archive),
	)
}

func (module programmeResourceModule) list(request operationRequest) (operationResult, error) {
	unitID, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	limit, err := request.queryLimit()
	if err != nil {
		return operationResult{}, err
	}
	programmes, err := module.programmes.ListProgrammes(
		request.context, request.invocation(),
		application.ListProgrammesQuery{AcademicUnitID: unitID, Query: request.request.URL.Query().Get("q"), Limit: limit},
	)
	if err != nil {
		return operationResult{}, err
	}
	responses := make([]programmeResponse, 0, len(programmes))
	for _, programme := range programmes {
		responses = append(responses, programmeResponseFromModel(programme))
	}
	return jsonResult(http.StatusOK, responses), nil
}

func (module programmeResourceModule) create(request operationRequest) (operationResult, error) {
	unitID, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	var body createProgrammeRequest
	if err := request.decodeJSON(&body, "createProgramme"); err != nil {
		return operationResult{}, err
	}
	saved, err := module.programmes.CreateProgramme(
		request.context, request.invocation(),
		application.CreateProgrammeCommand{AcademicUnitID: unitID, Name: body.Name, DisplayName: body.DisplayName, Description: body.Description},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, programmeResponseFromModel(saved)), nil
}

func (module programmeResourceModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireProgrammeId()
	if err != nil {
		return operationResult{}, err
	}
	programme, err := module.programmes.GetProgramme(
		request.context, request.invocation(),
		application.GetProgrammeQuery{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, programmeResponseFromModel(programme)), nil
}

func (module programmeResourceModule) patch(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireProgrammeId()
	if err != nil {
		return operationResult{}, err
	}
	var body updateProgrammeRequest
	if err := request.decodeJSON(&body, "patchProgramme"); err != nil {
		return operationResult{}, err
	}
	programme, err := module.programmes.UpdateProgramme(
		request.context, request.invocation(),
		application.UpdateProgrammeCommand{ID: id, Name: body.Name.ValuePointer(), DisplayName: body.DisplayName.ValuePointer(), Description: body.Description.ValuePointer()},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, programmeResponseFromModel(programme)), nil
}

func (module programmeResourceModule) archive(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireProgrammeId()
	if err != nil {
		return operationResult{}, err
	}
	err = module.programmes.ArchiveProgramme(
		request.context, request.invocation(),
		application.ArchiveProgrammeCommand{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func programmeResponseFromModel(programme *model.Programme) programmeResponse {
	if programme == nil {
		return programmeResponse{}
	}
	return programmeResponse{
		ID:             programme.ID.String(),
		CreateAt:       model.MillisFromTime(programme.CreatedAt),
		UpdateAt:       model.MillisFromTime(programme.UpdatedAt),
		DeleteAt:       programme.ArchivedAt.Millis(),
		AcademicUnitID: programme.AcademicUnitID.String(),
		Name:           programme.Name,
		DisplayName:    programme.DisplayName,
		Description:    programme.Description,
	}
}
