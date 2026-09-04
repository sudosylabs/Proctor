// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"
	"strconv"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type institutionResponse struct {
	ID           string                     `json:"id"`
	CreateAt     int64                      `json:"create_at"`
	UpdateAt     int64                      `json:"update_at"`
	DeleteAt     int64                      `json:"delete_at"`
	Name         string                     `json:"name"`
	DisplayName  string                     `json:"display_name"`
	Description  string                     `json:"description"`
	ExamCapacity examCapacityPolicyResponse `json:"exam_capacity"`
}

type examCapacityPolicyResponse struct {
	ResourceMaximumCount       int   `json:"resource_maximum_count"`
	ResourceMaximumBytes       int64 `json:"resource_maximum_bytes"`
	WorkspaceMaximumEntries    int   `json:"workspace_maximum_entries"`
	WorkspaceMaximumFileBytes  int64 `json:"workspace_maximum_file_bytes"`
	WorkspaceMaximumTotalBytes int64 `json:"workspace_maximum_total_bytes"`
}

func examCapacityPolicyResponseFromModel(policy model.ExamCapacityPolicy) examCapacityPolicyResponse {
	if policy.IsZero() {
		policy = model.DefaultExamCapacityPolicy()
	}
	return examCapacityPolicyResponse{
		ResourceMaximumCount:       policy.ResourceMaximumCount,
		ResourceMaximumBytes:       policy.ResourceMaximumBytes,
		WorkspaceMaximumEntries:    policy.WorkspaceMaximumEntries,
		WorkspaceMaximumFileBytes:  policy.WorkspaceMaximumFileBytes,
		WorkspaceMaximumTotalBytes: policy.WorkspaceMaximumTotalBytes,
	}
}

type examCapacityPolicyRequest struct {
	ResourceMaximumCount       int   `json:"resource_maximum_count"`
	ResourceMaximumBytes       int64 `json:"resource_maximum_bytes"`
	WorkspaceMaximumEntries    int   `json:"workspace_maximum_entries"`
	WorkspaceMaximumFileBytes  int64 `json:"workspace_maximum_file_bytes"`
	WorkspaceMaximumTotalBytes int64 `json:"workspace_maximum_total_bytes"`
}

func (request examCapacityPolicyRequest) model() model.ExamCapacityPolicy {
	return model.ExamCapacityPolicy{
		ResourceMaximumCount:       request.ResourceMaximumCount,
		ResourceMaximumBytes:       request.ResourceMaximumBytes,
		WorkspaceMaximumEntries:    request.WorkspaceMaximumEntries,
		WorkspaceMaximumFileBytes:  request.WorkspaceMaximumFileBytes,
		WorkspaceMaximumTotalBytes: request.WorkspaceMaximumTotalBytes,
	}
}

type updateInstitutionRequest struct {
	Name         Optional[string]                    `json:"name"`
	DisplayName  Optional[string]                    `json:"display_name"`
	Description  Optional[string]                    `json:"description"`
	ExamCapacity Optional[examCapacityPolicyRequest] `json:"exam_capacity"`
}

type institutionResourceModule struct {
	institutions InstitutionApplication
}

func institutionResource(institutions InstitutionApplication) resource {
	module := institutionResourceModule{institutions: institutions}
	return newResource(
		"institution",
		principalRoute(
			http.MethodGet,
			apiPath(literal("institution")),
			academicReadErrorCodes("resource.not_found"),
			module.get,
		),
		principalRoute(
			http.MethodPatch,
			apiPath(literal("institution")),
			academicMutationErrorCodes(
				"request.invalid", "resource.not_found", "institution.invalid",
				"institution.conflict",
			),
			module.patch,
		),
	)
}

func academicReadErrorCodes(specific ...string) []string {
	codes := []string{
		"authentication.required",
		"authentication.invalid_token",
		"authentication.credential_ambiguous",
		"authorization.denied",
		"authorization.request.invalid",
		"authorization.unavailable",
	}
	codes = append(codes, specific...)
	return append(codes, "administration.unavailable")
}

func academicMutationErrorCodes(specific ...string) []string {
	codes := []string{
		"authentication.required",
		"authentication.invalid_token",
		"authentication.credential_ambiguous",
		"authentication.csrf.invalid",
		"authorization.denied",
		"authorization.request.invalid",
		"authorization.unavailable",
		"audit.unavailable",
	}
	codes = append(codes, specific...)
	return append(codes, "administration.unavailable")
}

func (module institutionResourceModule) get(request operationRequest) (operationResult, error) {
	institution, err := module.institutions.GetInstitution(
		request.context,
		request.invocation(),
		application.GetInstitutionQuery{},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, institutionResponseFromModel(institution)), nil
}

func (module institutionResourceModule) patch(request operationRequest) (operationResult, error) {
	var body updateInstitutionRequest
	if err := request.decodeJSON(&body, "patchInstitution"); err != nil {
		return operationResult{}, err
	}
	var capacity *model.ExamCapacityPolicy
	if requested := body.ExamCapacity.ValuePointer(); requested != nil {
		value := requested.model()
		capacity = &value
	}
	institution, err := module.institutions.UpdateInstitution(
		request.context,
		request.invocation(),
		application.UpdateInstitutionCommand{
			Name: body.Name.ValuePointer(), DisplayName: body.DisplayName.ValuePointer(),
			Description:  body.Description.ValuePointer(),
			ExamCapacity: capacity,
		},
	)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, institutionResponseFromModel(institution)), nil
}

func institutionResponseFromModel(institution *model.Institution) institutionResponse {
	if institution == nil {
		return institutionResponse{}
	}
	return institutionResponse{
		ID:           institution.ID.String(),
		CreateAt:     model.MillisFromTime(institution.CreatedAt),
		UpdateAt:     model.MillisFromTime(institution.UpdatedAt),
		DeleteAt:     institution.ArchivedAt.Millis(),
		Name:         institution.Name,
		DisplayName:  institution.DisplayName,
		Description:  institution.Description,
		ExamCapacity: examCapacityPolicyResponseFromModel(institution.ExamCapacity),
	}
}

// Legacy handlers use these helpers until their resource modules migrate to
// operationRequest. Academic structure resources do not depend on them.
func queryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return 100, true
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 200 {
		WriteError(w, r, invalidRequestError("limit", err))
		return 0, false
	}
	return limit, true
}

func requiredPrincipal(w http.ResponseWriter, r *http.Request) (model.Principal, bool) {
	principal, ok := Principal(r.Context())
	if !ok {
		WriteError(w, r, authenticationRequiredError())
	}
	return principal, ok
}

type idRequirement func(Params) (string, error)

func requiredResourceID(w http.ResponseWriter, r *http.Request, require idRequirement) (model.Principal, string, bool) {
	return principalAndRequiredId(w, r, require)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, where string) bool {
	if err := decodeRequestJSON(r, target); err != nil {
		WriteError(w, r, invalidRequestError(where, err))
		return false
	}
	return true
}
