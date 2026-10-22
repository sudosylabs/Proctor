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

type classResponse struct {
	Revision         int64  `json:"revision"`
	ID               string `json:"id"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	ArchivedAt       string `json:"archived_at,omitempty"`
	ProgrammeLevelID string `json:"programme_level_id"`
	AcademicPeriodID string `json:"academic_period_id"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
}
type createClassRequest struct {
	AcademicPeriodID string `json:"academic_period_id"`
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	Description      string `json:"description"`
}
type updateClassRequest struct {
	ExpectedRevision Optional[int64]  `json:"expected_revision"`
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

func (m classResourceModule) search(request operationRequest) (operationResult, error) {
	academicUnitID, err := request.params.RequireAcademicUnitID()
	if err != nil {
		return operationResult{}, err
	}
	pageQuery, err := request.academicNamePageQuery()
	if err != nil {
		return operationResult{}, err
	}
	page, err := m.classes.SearchClasses(
		request.context,
		request.invocation(),
		app.SearchClassesQuery{
			AcademicUnitID: academicUnitID,
			Query:          request.request.URL.Query().Get("q"),
			NamePageQuery:  pageQuery,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return academicNameListResult(page, classResponseFromModel, func(class *model.Class) (string, string) {
		return class.Name, class.ID.String()
	})
}

func (m classResourceModule) list(request operationRequest) (operationResult, error) {
	programmeLevelID, err := request.params.RequireProgrammeLevelID()
	if err != nil {
		return operationResult{}, err
	}
	pageQuery, err := request.academicNamePageQuery()
	if err != nil {
		return operationResult{}, err
	}
	page, err := m.classes.ListClasses(
		request.context,
		request.invocation(),
		app.ListClassesQuery{ProgrammeLevelID: programmeLevelID, NamePageQuery: pageQuery},
	)
	if err != nil {
		return operationResult{}, err
	}
	return academicNameListResult(page, classResponseFromModel, func(class *model.Class) (string, string) {
		return class.Name, class.ID.String()
	})
}

func (m classResourceModule) create(request operationRequest) (operationResult, error) {
	programmeLevelID, err := request.params.RequireProgrammeLevelID()
	if err != nil {
		return operationResult{}, err
	}
	var body createClassRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	class, err := m.classes.CreateClass(
		request.context,
		request.invocation(),
		app.CreateClassCommand{
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

func (m classResourceModule) get(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireClassID()
	if err != nil {
		return operationResult{}, err
	}
	class, err := m.classes.GetClass(
		request.context,
		request.invocation(),
		app.GetClassQuery{ID: id},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, classResponseFromModel(class)), nil
}

func (m classResourceModule) patch(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireClassID()
	if err != nil {
		return operationResult{}, err
	}
	var body updateClassRequest
	if err = request.decodeJSON(&body); err != nil {
		return operationResult{}, err
	}
	if err := validateAcademicRevision(body.ExpectedRevision); err != nil {
		return operationResult{}, err
	}
	class, err := m.classes.UpdateClass(
		request.context,
		request.invocation(),
		app.UpdateClassCommand{ExpectedRevision: body.ExpectedRevision.ValuePointer(),
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

func (m classResourceModule) archive(request operationRequest) (operationResult, error) {
	id, err := request.params.RequireClassID()
	if err != nil {
		return operationResult{}, err
	}
	expectedRevision, err := request.academicRevision()
	if err != nil {
		return operationResult{}, err
	}
	if err := m.classes.ArchiveClass(
		request.context,
		request.invocation(),
		app.ArchiveClassCommand{ExpectedRevision: expectedRevision, ID: id},
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
		Revision: class.Revision, ID: class.ID.String(),
		CreatedAt:        model.FormatRFC3339(class.CreatedAt),
		UpdatedAt:        model.FormatRFC3339(class.UpdatedAt),
		ArchivedAt:       class.ArchivedAt.FormatRFC3339(),
		ProgrammeLevelID: class.ProgrammeLevelID.String(),
		AcademicPeriodID: class.AcademicPeriodID.String(),
		Name:             class.Name,
		DisplayName:      class.DisplayName,
		Description:      class.Description,
	}
}
