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

type classResponse struct {
	ID               string `json:"id"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
	ProgrammeLevelID string `json:"programme_level_id"`
	AcademicPeriodID string `json:"academic_period_id"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
}
type createClassRequest struct {
	ID               string `json:"id"`
	CreateAt         int64  `json:"create_at"`
	UpdateAt         int64  `json:"update_at"`
	DeleteAt         int64  `json:"delete_at"`
	ProgrammeLevelID string `json:"programme_level_id"`
	AcademicPeriodID string `json:"academic_period_id"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
}
type updateClassRequest struct {
	ProgrammeLevelID Optional[string] `json:"programme_level_id"`
	AcademicPeriodID Optional[string] `json:"academic_period_id"`
	Name             Optional[string] `json:"name"`
	DisplayName      Optional[string] `json:"display_name"`
	Description      Optional[string] `json:"description"`
}

type classResourceModule struct {
	classes ClassApplication
}

func classResource(classes ClassApplication) resource {
	module := classResourceModule{classes: classes}
	return newResource(
		"classes",
		principalRoute(
			http.MethodGet,
			apiPath(literal("academic-units"), canonicalID("academic_unit_id"), literal("classes")),
			classReadErrorCodes(),
			module.search,
		),
		principalRoute(
			http.MethodGet,
			apiPath(literal("programme-levels"), canonicalID("programme_level_id"), literal("classes")),
			classReadErrorCodes(),
			module.list,
		),
		principalRoute(
			http.MethodPost,
			apiPath(literal("programme-levels"), canonicalID("programme_level_id"), literal("classes")),
			classMutationErrorCodes("class.invalid", "class.conflict"),
			module.create,
		),
		principalRoute(
			http.MethodGet,
			apiPath(literal("classes"), canonicalID("class_id")),
			classReadErrorCodes(),
			module.get,
		),
		principalRoute(
			http.MethodPatch,
			apiPath(literal("classes"), canonicalID("class_id")),
			classMutationErrorCodes("class.invalid", "class.conflict"),
			module.patch,
		),
		principalRoute(
			http.MethodDelete,
			apiPath(literal("classes"), canonicalID("class_id")),
			classMutationErrorCodes("class.conflict"),
			module.archive,
		),
	)
}

func classReadErrorCodes() []string {
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

func classMutationErrorCodes(specific ...string) []string {
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

func (module classResourceModule) search(request operationRequest) (operationResult, error) {
	academicUnitID, err := request.params.RequireAcademicUnitId()
	if err != nil {
		return operationResult{}, err
	}
	limit, err := request.queryLimit()
	if err != nil {
		return operationResult{}, err
	}
	classes, err := module.classes.SearchClasses(
		request.context,
		request.invocation(),
		application.SearchClassesQuery{
			AcademicUnitID: academicUnitID,
			Query:          request.request.URL.Query().Get("q"),
			Limit:          limit,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, classResponses(classes)), nil
}

func (module classResourceModule) list(request operationRequest) (operationResult, error) {
	programmeLevelID, err := request.params.RequireProgrammeLevelId()
	if err != nil {
		return operationResult{}, err
	}
	classes, err := module.classes.ListClasses(
		request.context,
		request.invocation(),
		application.ListClassesQuery{ProgrammeLevelID: programmeLevelID},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, classResponses(classes)), nil
}

func (module classResourceModule) create(request operationRequest) (operationResult, error) {
	programmeLevelID, err := request.params.RequireProgrammeLevelId()
	if err != nil {
		return operationResult{}, err
	}
	var body createClassRequest
	if err := request.decodeJSON(&body, "createClass"); err != nil {
		return operationResult{}, err
	}
	class, err := module.classes.CreateClass(
		request.context,
		request.invocation(),
		application.CreateClassCommand{
			ProgrammeLevelID: programmeLevelID,
			AcademicPeriodID: body.AcademicPeriodID,
			Name:             body.Name,
			DisplayName:      body.DisplayName,
			Description:      body.Description,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusCreated, classResponseFromModel(class)), nil
}

func (module classResourceModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireClassId()
	if err != nil {
		return operationResult{}, err
	}
	class, err := module.classes.GetClass(
		request.context,
		request.invocation(),
		application.GetClassQuery{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, classResponseFromModel(class)), nil
}

func (module classResourceModule) patch(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireClassId()
	if err != nil {
		return operationResult{}, err
	}
	var body updateClassRequest
	if err := request.decodeJSON(&body, "patchClass"); err != nil {
		return operationResult{}, err
	}
	class, err := module.classes.UpdateClass(
		request.context,
		request.invocation(),
		application.UpdateClassCommand{
			ID:               id,
			ProgrammeLevelID: body.ProgrammeLevelID.ValuePointer(),
			AcademicPeriodID: body.AcademicPeriodID.ValuePointer(),
			Name:             body.Name.ValuePointer(),
			DisplayName:      body.DisplayName.ValuePointer(),
			Description:      body.Description.ValuePointer(),
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, classResponseFromModel(class)), nil
}

func (module classResourceModule) archive(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireClassId()
	if err != nil {
		return operationResult{}, err
	}
	if err := module.classes.ArchiveClass(
		request.context,
		request.invocation(),
		application.ArchiveClassCommand{ID: id},
	); err != nil {
		return operationResult{}, err
	}
	return noContentResult(), nil
}

func classResponseFromModel(class *model.Class) classResponse {
	if class == nil {
		return classResponse{}
	}
	return classResponse{
		ID:               class.ID.String(),
		CreateAt:         model.MillisFromTime(class.CreatedAt),
		UpdateAt:         model.MillisFromTime(class.UpdatedAt),
		DeleteAt:         class.ArchivedAt.Millis(),
		ProgrammeLevelID: class.ProgrammeLevelID.String(),
		AcademicPeriodID: class.AcademicPeriodID.String(),
		Name:             class.Name,
		DisplayName:      class.DisplayName,
		Description:      class.Description,
	}
}
func classResponses(classes []*model.Class) []classResponse {
	result := make([]classResponse, 0, len(classes))
	for _, class := range classes {
		result = append(result, classResponseFromModel(class))
	}
	return result
}
