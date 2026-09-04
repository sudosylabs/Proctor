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

type programmeLevelResponse struct {
	ID          string `json:"id"`
	CreateAt    int64  `json:"create_at"`
	UpdateAt    int64  `json:"update_at"`
	DeleteAt    int64  `json:"delete_at"`
	ProgrammeID string `json:"programme_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type createProgrammeLevelRequest struct {
	ID          string `json:"id"`
	CreateAt    int64  `json:"create_at"`
	UpdateAt    int64  `json:"update_at"`
	DeleteAt    int64  `json:"delete_at"`
	ProgrammeID string `json:"programme_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type updateProgrammeLevelRequest struct {
	Name        Optional[string] `json:"name"`
	DisplayName Optional[string] `json:"display_name"`
	Description Optional[string] `json:"description"`
}

type programmeLevelResourceModule struct {
	programmeLevels ProgrammeLevelApplication
}

func programmeLevelResource(programmeLevels ProgrammeLevelApplication) resource {
	module := programmeLevelResourceModule{programmeLevels: programmeLevels}
	collection := apiPath(literal("programmes"), canonicalID("programme_id"), literal("levels"))
	member := apiPath(literal("programme-levels"), canonicalID("programme_level_id"))
	return newResource(
		"programme-levels",
		principalRoute(
			http.MethodGet, collection,
			academicReadErrorCodes("request.invalid", "resource.not_found"), module.list,
		),
		principalRoute(
			http.MethodPost, collection,
			academicMutationErrorCodes(
				"request.invalid", "resource.not_found", "programme_level.invalid",
				"programme_level.conflict",
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
				"request.invalid", "resource.not_found", "programme_level.invalid",
				"programme_level.conflict",
			),
			module.patch,
		),
		principalRoute(
			http.MethodDelete, member,
			academicMutationErrorCodes(
				"request.invalid", "resource.not_found", "programme_level.conflict",
			),
			module.archive,
		),
	)
}

func (module programmeLevelResourceModule) list(request operationRequest) (operationResult, error) {
	programmeID, err := request.params.RequireProgrammeId()
	if err != nil {
		return operationResult{}, err
	}
	limit, err := request.queryLimit()
	if err != nil {
		return operationResult{}, err
	}
	levels, err := module.programmeLevels.ListProgrammeLevels(
		request.context,
		request.invocation(),
		application.ListProgrammeLevelsQuery{
			ProgrammeID: programmeID,
			Query:       request.request.URL.Query().Get("q"),
			Limit:       limit,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	responses := make([]programmeLevelResponse, 0, len(levels))
	for _, level := range levels {
		responses = append(responses, programmeLevelResponseFromModel(level))
	}
	return jsonResult(http.StatusOK, responses), nil
}

func (module programmeLevelResourceModule) create(request operationRequest) (operationResult, error) {
	programmeID, err := request.params.RequireProgrammeId()
	if err != nil {
		return operationResult{}, err
	}
	var body createProgrammeLevelRequest
	if err := request.decodeJSON(&body, "createProgrammeLevel"); err != nil {
		return operationResult{}, err
	}
	saved, err := module.programmeLevels.CreateProgrammeLevel(
		request.context,
		request.invocation(),
		application.CreateProgrammeLevelCommand{
			ProgrammeID: programmeID,
			Name:        body.Name,
			DisplayName: body.DisplayName,
			Description: body.Description,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, programmeLevelResponseFromModel(saved)), nil
}

func (module programmeLevelResourceModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireProgrammeLevelId()
	if err != nil {
		return operationResult{}, err
	}
	level, err := module.programmeLevels.GetProgrammeLevel(
		request.context, request.invocation(), application.GetProgrammeLevelQuery{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, programmeLevelResponseFromModel(level)), nil
}

func (module programmeLevelResourceModule) patch(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireProgrammeLevelId()
	if err != nil {
		return operationResult{}, err
	}
	var body updateProgrammeLevelRequest
	if err := request.decodeJSON(&body, "patchProgrammeLevel"); err != nil {
		return operationResult{}, err
	}
	level, err := module.programmeLevels.UpdateProgrammeLevel(
		request.context,
		request.invocation(),
		application.UpdateProgrammeLevelCommand{
			ID:          id,
			Name:        body.Name.ValuePointer(),
			DisplayName: body.DisplayName.ValuePointer(),
			Description: body.Description.ValuePointer(),
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, programmeLevelResponseFromModel(level)), nil
}

func (module programmeLevelResourceModule) archive(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireProgrammeLevelId()
	if err != nil {
		return operationResult{}, err
	}
	err = module.programmeLevels.ArchiveProgrammeLevel(
		request.context, request.invocation(), application.ArchiveProgrammeLevelCommand{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func programmeLevelResponseFromModel(level *model.ProgrammeLevel) programmeLevelResponse {
	if level == nil {
		return programmeLevelResponse{}
	}
	return programmeLevelResponse{
		ID:          level.ID.String(),
		CreateAt:    model.MillisFromTime(level.CreatedAt),
		UpdateAt:    model.MillisFromTime(level.UpdatedAt),
		DeleteAt:    level.ArchivedAt.Millis(),
		ProgrammeID: level.ProgrammeID.String(),
		Name:        level.Name,
		DisplayName: level.DisplayName,
		Description: level.Description,
	}
}
